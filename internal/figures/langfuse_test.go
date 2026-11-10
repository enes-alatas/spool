package figures

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/dialguard"
	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
)

// fixturePage is an Observations API v2 page in the shape #1300's probe
// read on a live hub (2026-11-11): newest first, every attribute
// flattened into metadata, tokens as attributes with usage empty and no
// cost, and a tool call as a span named after the tool with an execution
// child that carries its success. One span is stamped nested, as an
// older export might, and one observation has no loop's stamp.
const fixturePage = `{
  "data": [
    {"id": "o3", "traceId": "t2", "type": "SPAN", "name": "x", "startTime": "2026-11-10T11:01:00Z", "metadata": "not an object"},
    {"id": "o2x", "traceId": "t1", "parentObservationId": "o2", "type": "TOOL", "name": "claude_code.tool.execution", "level": "DEFAULT",
     "startTime": "2026-11-10T11:00:06Z", "endTime": "2026-11-10T11:00:07Z", "usageDetails": {}, "costDetails": {}, "totalCost": null,
     "metadata": {"resourceAttributes.spool.loop.id": "L1", "attributes.success": false, "attributes.session.id": "s1"}},
    {"id": "o2", "traceId": "t1", "parentObservationId": "o0", "type": "TOOL", "name": "Bash", "level": "DEFAULT",
     "startTime": "2026-11-10T11:00:06Z", "endTime": "2026-11-10T11:00:07Z",
     "metadata": {"resourceAttributes": {"spool.loop.id": "L1"}, "attributes": {"tool_name": "Bash"}}},
    {"id": "o1", "traceId": "t1", "parentObservationId": "o0", "type": "GENERATION", "name": "claude_code.llm_request", "level": "DEFAULT",
     "startTime": "2026-11-10T11:00:00Z", "endTime": "2026-11-10T11:00:05Z", "usageDetails": {}, "costDetails": {}, "totalCost": null,
     "metadata": {"resourceAttributes.spool.loop.id": "L1", "resourceAttributes.spool.loop.name": "terra", "attributes.session.id": "s1",
       "attributes.success": true, "attributes.input_tokens": 120, "attributes.output_tokens": 30,
       "attributes.cache_read_tokens": 9000, "attributes.cache_creation_tokens": 400}}
  ],
  "meta": {"cursor": "next-page"}
}`

func TestParsePageReadsNumbersAndTheStamp(t *testing.T) {
	page, err := parsePage([]byte(fixturePage))
	if err != nil {
		t.Fatal(err)
	}
	if page.Next != "next-page" || len(page.Observations) != 4 {
		t.Fatalf("page = %+v, want four observations and the cursor", page)
	}
	unstamped, execution, tool, generation := page.Observations[0], page.Observations[1], page.Observations[2], page.Observations[3]
	if generation.LoopID != "L1" || generation.SessionID != "s1" || generation.Failed || generation.ParentID != "o0" ||
		generation.Tokens != (Tokens{Input: 120, Output: 30, CacheRead: 9000, CacheWrite: 400}) ||
		generation.End.Sub(generation.Start) != 5*time.Second {
		t.Errorf("generation = %+v, want L1's, of s1, its tokens read from its attributes", generation)
	}
	if tool.LoopID != "L1" || tool.Failed || tool.Name != "Bash" || tool.Type != "TOOL" {
		t.Errorf("tool = %+v, want L1's Bash call, stamped nested", tool)
	}
	if execution.ParentID != "o2" || !execution.Failed || execution.Name != toolExecution {
		t.Errorf("execution = %+v, want the Bash call's failed execution", execution)
	}
	if unstamped.LoopID != "" || !unstamped.End.IsZero() {
		t.Errorf("unstamped = %+v, want no loop and no end", unstamped)
	}
}

// Langfuse's own usage, where it has some, is read before the
// attributes.
func TestParsePagePrefersLangfuseUsage(t *testing.T) {
	page, err := parsePage([]byte(`{"data": [{"id": "o1", "traceId": "t1", "type": "GENERATION",
		"startTime": "2026-11-10T11:00:00Z", "usageDetails": {"input": 7},
		"metadata": {"attributes.input_tokens": 120, "attributes.output_tokens": 30}}], "meta": {}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := page.Observations[0].Tokens; got != (Tokens{Input: 7, Output: 30}) {
		t.Errorf("tokens = %+v, want usage's input and the attribute's output", got)
	}
}

// The source sends the read key as Basic auth to the read address, asks
// for no input or output, passes the cursor on, follows no redirect,
// refuses a page over its bound, and reports a refused read as an error
// that names no key.
func TestLangfuseSourceReadsOnePage(t *testing.T) {
	// a second server the read address redirects to, which must hear
	// nothing: Go keeps Authorization on a redirect to a subdomain, so
	// the source's own refusal is what holds
	var elsewhereHit atomic.Bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { elsewhereHit.Store(true) }))
	defer elsewhere.Close()
	var asked *http.Request
	langfuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r
		switch r.URL.Query().Get("cursor") {
		case "moved":
			http.Redirect(w, r, elsewhere.URL+"/steal", http.StatusFound)
		case "refused":
			http.Error(w, "nope", http.StatusUnauthorized)
		default:
			fmt.Fprint(w, fixturePage)
		}
	}))
	defer langfuse.Close()
	source := NewLangfuseSource(5 * time.Second)
	settings := store.Langfuse{ReadAddress: langfuse.URL + "/", ReadKey: syntheticKey}
	from := time.Date(2026, 11, 10, 6, 0, 0, 0, time.UTC)

	page, err := source.Observations(context.Background(), settings, from, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Observations) != 4 {
		t.Fatalf("page = %+v", page)
	}
	if asked.URL.Path != observationsPath || asked.Header.Get("Authorization") != loop.LangfuseAuthorization(syntheticKey) {
		t.Errorf("asked %s with Authorization %q", asked.URL.Path, asked.Header.Get("Authorization"))
	}
	query := asked.URL.Query()
	if query.Get("fromStartTime") != "2026-11-10T06:00:00Z" || strings.Contains(query.Get("fields"), "io") {
		t.Errorf("query = %v, want fromStartTime and no io fields", query)
	}

	for _, cursor := range []string{"moved", "refused"} {
		_, err := source.Observations(context.Background(), settings, from, cursor)
		if err == nil {
			t.Errorf("cursor %s: no error", cursor)
			continue
		}
		if strings.Contains(err.Error(), "sk-lf") || strings.Contains(err.Error(), "pk-lf") {
			t.Errorf("cursor %s: the error carries the key: %v", cursor, err)
		}
	}
	if elsewhereHit.Load() {
		t.Error("a redirect was followed: the read key went to another address")
	}

	source.pageBytes = 100
	if _, err := source.Observations(context.Background(), settings, from, ""); err == nil || !strings.Contains(err.Error(), "a page over 100 bytes") {
		t.Errorf("a page over the bound = %v, want it refused by size", err)
	}
}

// The read's dial is guarded as a connection check's is: for a read
// address naming another host, a dial that lands on this machine is
// refused, whatever the name resolved to, as a link-local or cloud
// metadata address would be; a loopback read address reaches it.
func TestLangfuseSourceGuardsItsDial(t *testing.T) {
	here := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer here.Close()
	source := NewLangfuseSource(5 * time.Second)
	dial := func(address string) error {
		parsed, err := url.Parse(address)
		if err != nil {
			t.Fatal(err)
		}
		transport := source.clientFor(parsed).Transport.(*http.Transport)
		conn, err := transport.DialContext(context.Background(), "tcp", here.Listener.Addr().String())
		if err != nil {
			return err
		}
		return conn.Close()
	}
	for _, address := range []string{"https://langfuse.example", "https://169.254.169.254"} {
		if err := dial(address); !errors.Is(err, dialguard.ErrRefused) {
			t.Errorf("%s: a dial to this machine = %v, want the guard's refusal", address, err)
		}
	}
	if err := dial("http://127.0.0.1:3000"); err != nil {
		t.Errorf("a loopback read address can't reach this machine: %v", err)
	}
}

// Each page's client is built for that page alone, so a read asks to
// close its connection rather than leave it idle behind it.
func TestLangfuseSourceLeavesNoConnectionOpen(t *testing.T) {
	var kept atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !request.Close {
			kept.Add(1)
		}
		fmt.Fprint(writer, `{"data": [], "meta": {}}`)
	}))
	defer server.Close()
	source := NewLangfuseSource(5 * time.Second)
	settings := store.Langfuse{ReadAddress: server.URL, ReadKey: syntheticKey}
	for range 3 {
		if _, err := source.Observations(context.Background(), settings, time.Now(), ""); err != nil {
			t.Fatal(err)
		}
	}
	if kept.Load() != 0 {
		t.Errorf("%d of 3 reads kept their connection open", kept.Load())
	}
}

// The read sets no proxy, whatever the environment names: a proxy's
// address is what the guard would judge, not the read address's.
func TestLangfuseSourceSetsNoProxy(t *testing.T) {
	parsed, err := url.Parse("https://langfuse.example")
	if err != nil {
		t.Fatal(err)
	}
	if transport := NewLangfuseSource(time.Second).clientFor(parsed).Transport.(*http.Transport); transport.Proxy != nil {
		t.Error("the read's transport sets a proxy")
	}
}
