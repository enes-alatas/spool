package egress

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// relayFixture is a relay wired to a stand-in Langfuse, with two loop
// networks: the proxy is 10.1.0.2 on loop-a's and 10.2.0.2 on loop-b's.
type relayFixture struct {
	relay    *Relay
	path     string
	mu       sync.Mutex
	received []*receivedTrace
	answer   func(http.ResponseWriter)
}

type receivedTrace struct {
	path   string
	header http.Header
	body   map[string]any
}

const (
	syntheticAuthorization = "Basic c3ludGhldGljLXB1YmxpYzpzeW50aGV0aWMtc2VjcmV0"
	loopAAddress           = "10.1.0.2"
	loopBAddress           = "10.2.0.2"
)

func newRelayFixture(t *testing.T) *relayFixture {
	t.Helper()
	fixture := &relayFixture{path: filepath.Join(t.TempDir(), "relay.json")}
	langfuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(data, &body)
		fixture.mu.Lock()
		fixture.received = append(fixture.received, &receivedTrace{path: r.URL.Path, header: r.Header.Clone(), body: body})
		answer := fixture.answer
		fixture.mu.Unlock()
		if answer != nil {
			answer(w)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(langfuse.Close)

	fixture.write(t, RelayFile{
		Upstream:      "http://langfuse-web",
		Authorization: syntheticAuthorization,
		Langfuse:      []string{"langfuse.example"},
		Loops: []RelayLoop{
			{Address: loopAAddress, ID: "loop-a", Name: "alpha"},
			{Address: loopBAddress, ID: "loop-b", Name: "beta"},
		},
	})
	relay := NewRelay(NewRelayConfig(fixture.path, nil), nil)
	target := langfuse.Listener.Addr().String()
	relay.client.Transport = &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, target)
		},
	}
	relay.subnet = func(local netip.Addr) (netip.Prefix, bool) {
		return netip.PrefixFrom(local, 24).Masked(), true
	}
	fixture.relay = relay
	return fixture
}

func (fixture *relayFixture) write(t *testing.T, file RelayFile) {
	t.Helper()
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (fixture *relayFixture) last(t *testing.T) *receivedTrace {
	t.Helper()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.received) == 0 {
		t.Fatal("nothing reached the upstream")
	}
	return fixture.received[len(fixture.received)-1]
}

func (fixture *relayFixture) count() int {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return len(fixture.received)
}

// traceRequest is a request as a loop at from sends it to the proxy at
// local.
func traceRequest(local, from, body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, RelayPath, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = net.JoinHostPort(from, "40000")
	localAddr := &net.TCPAddr{IP: net.ParseIP(local), Port: 4318}
	return request.WithContext(context.WithValue(request.Context(), http.LocalAddrContextKey, localAddr))
}

func (fixture *relayFixture) serve(request *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	fixture.relay.ServeHTTP(recorder, request)
	return recorder
}

// forgedTrace names another loop on its resource, its scope, its span, and
// the span's event and link.
const forgedTrace = `{"resourceSpans":[{
	"resource":{"attributes":[
		{"key":"service.name","value":{"stringValue":"claude-code"}},
		{"key":"spool.loop.id","value":{"stringValue":"loop-b"}},
		{"key":"spool.loop.name","value":{"stringValue":"beta"}}]},
	"scopeSpans":[{
		"scope":{"name":"com.anthropic.claude_code","attributes":[{"key":"spool.loop.id","value":{"stringValue":"loop-b"}}]},
		"spans":[{"traceId":"5b8efff798038103d269b633813fc60c","spanId":"eee19b7ec3c1b174","name":"interaction",
			"startTimeUnixNano":"1730800000000000000",
			"attributes":[
				{"key":"interaction.duration_ms","value":{"intValue":"1234"}},
				{"key":"spool.loop.name","value":{"stringValue":"beta"}}],
			"events":[{"name":"tool","attributes":[{"key":"spool.loop.id","value":{"stringValue":"loop-b"}}]}],
			"links":[{"traceId":"5b8efff798038103d269b633813fc60c","spanId":"eee19b7ec3c1b175","attributes":[{"key":"spool.loop.name","value":{"stringValue":"beta"}}]}]}]}]}]}`

func attributes(t *testing.T, holder any) map[string]string {
	t.Helper()
	found := map[string]string{}
	object, _ := holder.(map[string]any)
	list, _ := object["attributes"].([]any)
	for _, item := range list {
		attribute := item.(map[string]any)
		value, _ := attribute["value"].(map[string]any)
		text, _ := value["stringValue"].(string)
		if text == "" {
			if number, ok := value["intValue"].(string); ok {
				text = number
			}
		}
		if _, dup := found[attribute["key"].(string)]; dup {
			t.Errorf("attribute %q appears twice", attribute["key"])
		}
		found[attribute["key"].(string)] = text
	}
	return found
}

func TestRelayStampsTheLoopItCameFrom(t *testing.T) {
	fixture := newRelayFixture(t)
	request := traceRequest(loopAAddress, "10.1.0.3", forgedTrace)
	request.Header.Set("Authorization", "Bearer the-loops-own")
	request.Header.Set("X-Extra", "dropped")
	if got := fixture.serve(request).Code; got != http.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}

	received := fixture.last(t)
	if received.path != "/api/public/otel/v1/traces" {
		t.Errorf("upstream path = %q, want Langfuse's OTLP path", received.path)
	}
	if got := received.header.Get("Authorization"); got != syntheticAuthorization {
		t.Errorf("upstream Authorization = %q, want the relay's", got)
	}
	if got := received.header.Get("X-Extra"); got != "" {
		t.Errorf("a loop's header reached the upstream: X-Extra = %q", got)
	}
	if got := received.header.Get("Content-Type"); got != "application/json" {
		t.Errorf("upstream Content-Type = %q", got)
	}

	resourceSpan := received.body["resourceSpans"].([]any)[0].(map[string]any)
	resource := attributes(t, resourceSpan["resource"])
	if resource["spool.loop.id"] != "loop-a" || resource["spool.loop.name"] != "alpha" {
		t.Errorf("resource stamp = %v, want loop-a/alpha over the forged loop-b", resource)
	}
	if resource["service.name"] != "claude-code" {
		t.Errorf("the loop's own resource attributes were lost: %v", resource)
	}
	scopeSpan := resourceSpan["scopeSpans"].([]any)[0].(map[string]any)
	if scope := attributes(t, scopeSpan["scope"]); len(scope) != 0 {
		t.Errorf("scope kept %v, want every spool.loop.* removed", scope)
	}
	span := attributes(t, scopeSpan["spans"].([]any)[0])
	if _, kept := span["spool.loop.name"]; kept {
		t.Errorf("span kept spool.loop.name: %v", span)
	}
	if span["interaction.duration_ms"] != "1234" {
		t.Errorf("span lost its own attributes: %v", span)
	}
	spanObject := scopeSpan["spans"].([]any)[0].(map[string]any)
	for _, name := range []string{"events", "links"} {
		if kept := attributes(t, spanObject[name].([]any)[0]); len(kept) != 0 {
			t.Errorf("span %s kept %v, want every spool.loop.* removed", name, kept)
		}
	}
}

func TestRelayTellsTheTwoLoopsApart(t *testing.T) {
	fixture := newRelayFixture(t)
	for _, loop := range []struct{ local, from, id string }{
		{loopAAddress, "10.1.0.3", "loop-a"},
		{loopBAddress, "10.2.0.3", "loop-b"},
	} {
		if got := fixture.serve(traceRequest(loop.local, loop.from, `{"resourceSpans":[{}]}`)).Code; got != http.StatusOK {
			t.Fatalf("%s: status = %d", loop.id, got)
		}
		resourceSpan := fixture.last(t).body["resourceSpans"].([]any)[0].(map[string]any)
		if got := attributes(t, resourceSpan["resource"])["spool.loop.id"]; got != loop.id {
			t.Errorf("trace from %s stamped %q", loop.from, got)
		}
	}
}

func TestRelayRefuses(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*http.Request) *http.Request
		want   int
	}{
		{"another path", func(request *http.Request) *http.Request {
			request.URL.Path = "/api/public/traces"
			return request
		}, http.StatusNotFound},
		{"another method", func(request *http.Request) *http.Request {
			request.Method = http.MethodGet
			return request
		}, http.StatusMethodNotAllowed},
		{"protobuf", func(request *http.Request) *http.Request {
			request.Header.Set("Content-Type", "application/x-protobuf")
			return request
		}, http.StatusUnsupportedMediaType},
		{"gzip", func(request *http.Request) *http.Request {
			request.Header.Set("Content-Encoding", "gzip")
			return request
		}, http.StatusUnsupportedMediaType},
		{"an address no loop network has", func(request *http.Request) *http.Request {
			localAddr := &net.TCPAddr{IP: net.ParseIP("10.9.0.2"), Port: 4318}
			return request.WithContext(context.WithValue(request.Context(), http.LocalAddrContextKey, localAddr))
		}, http.StatusForbidden},
		{"a sender off the address's network", func(request *http.Request) *http.Request {
			// loop-b's workstation reaching the proxy's address on loop-a's network
			request.RemoteAddr = "10.2.0.3:40000"
			return request
		}, http.StatusForbidden},
		{"snake_case fields", func(request *http.Request) *http.Request {
			return traceRequest(loopAAddress, "10.1.0.3", `{"resource_spans":[{"resource":{}}]}`)
		}, http.StatusBadRequest},
		{"trailing data", func(request *http.Request) *http.Request {
			return traceRequest(loopAAddress, "10.1.0.3", `{"resourceSpans":[]} {}`)
		}, http.StatusBadRequest},
		{"a body over 4 MiB", func(request *http.Request) *http.Request {
			big := `{"resourceSpans":[],"pad":"` + strings.Repeat("x", maxRelayBody) + `"}`
			return traceRequest(loopAAddress, "10.1.0.3", big)
		}, http.StatusRequestEntityTooLarge},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRelayFixture(t)
			request := test.mutate(traceRequest(loopAAddress, "10.1.0.3", `{"resourceSpans":[]}`))
			if got := fixture.serve(request).Code; got != test.want {
				t.Errorf("status = %d, want %d", got, test.want)
			}
			if fixture.count() != 0 {
				t.Error("a refused request reached the upstream")
			}
		})
	}
}

func TestRelayWithNoUpstreamRefusesEverything(t *testing.T) {
	fixture := newRelayFixture(t)
	fixture.write(t, RelayFile{})
	if got := fixture.serve(traceRequest(loopAAddress, "10.1.0.3", `{"resourceSpans":[]}`)).Code; got != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", got)
	}
	if err := os.Remove(fixture.path); err != nil {
		t.Fatal(err)
	}
	if got := fixture.serve(traceRequest(loopAAddress, "10.1.0.3", `{"resourceSpans":[]}`)).Code; got != http.StatusServiceUnavailable {
		t.Errorf("with no file, status = %d, want 503", got)
	}
}

func TestRelayAnswersWithTheStatusAlone(t *testing.T) {
	fixture := newRelayFixture(t)
	fixture.answer = func(w http.ResponseWriter) {
		w.Header().Set("X-Langfuse", "internal")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"project secret details"}`)
	}
	recorder := fixture.serve(traceRequest(loopAAddress, "10.1.0.3", `{"resourceSpans":[]}`))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want the upstream's 401", recorder.Code)
	}
	if recorder.Body.Len() != 0 || recorder.Header().Get("X-Langfuse") != "" {
		t.Errorf("the loop read Langfuse's answer: body %q, header %v", recorder.Body.String(), recorder.Header())
	}
}

func TestRelayFollowsNoRedirect(t *testing.T) {
	fixture := newRelayFixture(t)
	fixture.answer = func(w http.ResponseWriter) {
		w.Header().Set("Location", "http://langfuse-web/elsewhere")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}
	if got := fixture.serve(traceRequest(loopAAddress, "10.1.0.3", `{"resourceSpans":[]}`)).Code; got != http.StatusTemporaryRedirect {
		t.Errorf("status = %d, want the redirect answered, not followed", got)
	}
	if got := fixture.count(); got != 1 {
		t.Errorf("upstream saw %d requests, want 1", got)
	}
}

func TestRelayHoldsALoopToItsBudget(t *testing.T) {
	fixture := newRelayFixture(t)
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	fixture.relay.now = func() time.Time { return now }
	send := func(local, from string) int {
		return fixture.serve(traceRequest(local, from, `{"resourceSpans":[]}`)).Code
	}
	for range relayPerMinute {
		if got := send(loopAAddress, "10.1.0.3"); got != http.StatusOK {
			t.Fatalf("within the budget, status = %d", got)
		}
	}
	if got := send(loopAAddress, "10.1.0.3"); got != http.StatusTooManyRequests {
		t.Errorf("past %d a minute, status = %d, want 429", relayPerMinute, got)
	}
	if got := send(loopBAddress, "10.2.0.3"); got != http.StatusOK {
		t.Errorf("another loop's budget was spent: status = %d", got)
	}
	now = now.Add(time.Minute)
	if got := send(loopAAddress, "10.1.0.3"); got != http.StatusOK {
		t.Errorf("the next minute, status = %d", got)
	}
}

func TestRelayBoundsALoopsRequestsInFlight(t *testing.T) {
	fixture := newRelayFixture(t)
	for range relayInFlight {
		if _, ok := fixture.relay.admit("loop-a"); !ok {
			t.Fatal("within the in-flight bound, refused")
		}
	}
	if got := fixture.serve(traceRequest(loopAAddress, "10.1.0.3", `{"resourceSpans":[]}`)).Code; got != http.StatusTooManyRequests {
		t.Errorf("past %d in flight, status = %d, want 429", relayInFlight, got)
	}
}

func TestProxyRefusesLangfuseWhateverTheAllowlist(t *testing.T) {
	fixture := newRelayFixture(t)
	proxy := NewProxy(New([]string{"langfuse.example", "langfuse-web", "allowed.example"}), nil, nil).
		RefusingLangfuse(NewRelayConfig(fixture.path, nil))
	for host, want := range map[string]bool{
		"langfuse.example":  false,
		"LANGFUSE.example.": false,
		"langfuse-web":      false,
		"allowed.example":   true,
	} {
		if got := proxy.allows(host, "443"); got != want {
			t.Errorf("allows(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestRelayUpstreams(t *testing.T) {
	for upstream, ok := range map[string]bool{
		"https://cloud.langfuse.com":         true,
		"https://langfuse.example/sub":       true,
		"http://langfuse-web":                true,
		"http://langfuse-web:3000":           true,
		"http://langfuse.example":            false,
		"https://user:pass@langfuse.example": false,
		"https://langfuse.example/?x=1":      false,
		"ftp://langfuse.example":             false,
		"https://":                           false,
	} {
		_, err := relayUpstream(upstream)
		if (err == nil) != ok {
			t.Errorf("relayUpstream(%q) err = %v, want ok=%v", upstream, err, ok)
		}
	}
}

func TestWriteRelayFileRefusesWhatTheRelayCantUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relay.json")
	for name, content := range map[string]string{
		"not JSON":               `{`,
		"no authorization":       `{"upstream":"https://langfuse.example"}`,
		"a header break":         `{"upstream":"https://langfuse.example","authorization":"Basic x\r\nX: y"}`,
		"an address named twice": `{"upstream":"https://langfuse.example","authorization":"Basic x","loops":[{"address":"10.1.0.2","id":"a"},{"address":"10.1.0.2","id":"b"}]}`,
		"plain http elsewhere":   `{"upstream":"http://langfuse.example","authorization":"Basic x"}`,
	} {
		if err := WriteRelayFile(path, []byte(content)); err == nil {
			t.Errorf("%s: written, want refused", name)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a refused file was left at the path: %v", err)
	}
	if err := WriteRelayFile(path, []byte(`{"upstream":"https://langfuse.example","authorization":"Basic x"}`)); err != nil {
		t.Fatalf("a usable file: %v", err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("written file: %v, mode %v, want 0600", err, info.Mode().Perm())
	}
}
