package figures

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/enes-alatas/spool/internal/dialguard"
	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
)

// observationsPath is Langfuse's Observations API v2.
const observationsPath = "/api/public/v2/observations"

// pageLimit is the most observations a page holds.
const pageLimit = 1000

// LangfuseSource reads observations from the operator's Langfuse at its
// read address, with the read key. It asks for no input or output, and
// keeps only the numbers and names an Observation holds.
type LangfuseSource struct {
	timeout time.Duration
	// pageBytes bounds one page's body, so a Langfuse that answers
	// without end can't hold the hub's memory.
	pageBytes int64
}

// NewLangfuseSource returns a source whose every request gives up after
// timeout.
func NewLangfuseSource(timeout time.Duration) *LangfuseSource {
	return &LangfuseSource{timeout: timeout, pageBytes: 64 << 20}
}

// clientFor is the client a read of address goes through. It follows no
// redirect, so the read key goes to the read address and nowhere else.
// Its dial is guarded as a connection check's is (#1323): the address a
// name resolves to is judged as it is dialled, so the key never reaches
// a link-local or cloud metadata address, and a read address naming this
// machine reaches this machine alone. It sets no proxy, which the guard
// would judge instead. Each client serves one page and is then dropped, so,
// as a connection check's does, it closes its connection after the read:
// kept alive, a refresh of 500 pages would leave 500 idle ones behind.
func (source *LangfuseSource) clientFor(address *url.URL) *http.Client {
	host := address.Hostname()
	ip := net.ParseIP(host)
	loopback := host == "localhost" || (ip != nil && ip.IsLoopback())
	dialer := dialguard.Dialer(source.timeout, loopback)
	return &http.Client{
		Timeout: source.timeout,
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         dialer.DialContext,
			TLSHandshakeTimeout: source.timeout,
			DisableKeepAlives:   true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Observations reads one page of observations started at from or later.
func (source *LangfuseSource) Observations(ctx context.Context, langfuse store.Langfuse, from time.Time, cursor string) (ObservationPage, error) {
	query := url.Values{
		"fromStartTime": {from.UTC().Format(time.RFC3339)},
		"limit":         {strconv.Itoa(pageLimit)},
		"fields":        {"core,basic,usage,metadata"},
	}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	address := strings.TrimRight(langfuse.ReadAddress, "/") + observationsPath + "?" + query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return ObservationPage{}, err
	}
	request.Header.Set("Authorization", loop.LangfuseAuthorization(langfuse.ReadKey))
	response, err := source.clientFor(request.URL).Do(request)
	if err != nil {
		// the error names the address, never the key, which is a header
		return ObservationPage{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ObservationPage{}, fmt.Errorf("langfuse observations: %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, source.pageBytes+1))
	if err != nil {
		return ObservationPage{}, err
	}
	if int64(len(body)) > source.pageBytes {
		return ObservationPage{}, fmt.Errorf("langfuse observations: a page over %d bytes", source.pageBytes)
	}
	return parsePage(body)
}

// rawPage is an Observations API v2 page as far as the hub reads it.
type rawPage struct {
	Data []rawObservation `json:"data"`
	Meta struct {
		Cursor string `json:"cursor"`
	} `json:"meta"`
}

type rawObservation struct {
	ID                  string             `json:"id"`
	TraceID             string             `json:"traceId"`
	ParentObservationID string             `json:"parentObservationId"`
	Type                string             `json:"type"`
	Name                string             `json:"name"`
	Level               string             `json:"level"`
	StartTime           time.Time          `json:"startTime"`
	EndTime             *time.Time         `json:"endTime"`
	UsageDetails        map[string]float64 `json:"usageDetails"`
	Metadata            json.RawMessage    `json:"metadata"`
}

// parsePage reads a page into observations. Only the fields an
// Observation holds are read; metadata yields the loop's stamp, the
// session, a span's success and a generation's tokens, and nothing else
// of it is kept. Claude Code's spans carry their tokens as attributes,
// not as Langfuse usage, which #1300's probe found empty, so the
// attributes are read when usage holds none.
func parsePage(body []byte) (ObservationPage, error) {
	var page rawPage
	if err := json.Unmarshal(body, &page); err != nil {
		return ObservationPage{}, fmt.Errorf("langfuse observations: %w", err)
	}
	out := ObservationPage{Next: page.Meta.Cursor, Observations: make([]Observation, 0, len(page.Data))}
	for _, raw := range page.Data {
		metadata := flatten(raw.Metadata)
		observation := Observation{
			ID: raw.ID, TraceID: raw.TraceID, ParentID: raw.ParentObservationID, Type: raw.Type, Name: raw.Name, Start: raw.StartTime,
			LoopID:     metadata["resourceAttributes.spool.loop.id"],
			SessionID:  metadata["attributes.session.id"],
			ErrorClass: metadata["attributes.error_class"],
			Failed:     raw.Level == "ERROR" || metadata["attributes.success"] == "false",
			Tokens: Tokens{
				Input:      usage(raw.UsageDetails, metadata, "input", "attributes.input_tokens"),
				Output:     usage(raw.UsageDetails, metadata, "output", "attributes.output_tokens"),
				CacheRead:  usage(raw.UsageDetails, metadata, "cache_read_input_tokens", "attributes.cache_read_tokens"),
				CacheWrite: usage(raw.UsageDetails, metadata, "cache_creation_input_tokens", "attributes.cache_creation_tokens"),
			},
		}
		if observation.ErrorClass != "" {
			observation.Failed = true
		}
		if raw.EndTime != nil {
			observation.End = *raw.EndTime
		}
		out.Observations = append(out.Observations, observation)
	}
	return out, nil
}

// usage is Langfuse's usage under key, or else the span's attribute
// under attribute, 0 for neither.
func usage(details map[string]float64, metadata map[string]string, key, attribute string) int64 {
	if value, ok := details[key]; ok {
		return int64(value)
	}
	value, _ := strconv.ParseFloat(metadata[attribute], 64)
	return int64(value)
}

// flatten reads metadata as dotted keys to string values, whether
// Langfuse stored the attributes flattened ("resourceAttributes.spool.loop.id")
// or nested ({"resourceAttributes": {"spool.loop.id": …}}). Metadata that
// isn't an object reads as none.
func flatten(raw json.RawMessage) map[string]string {
	out := map[string]string{}
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil {
		return out
	}
	var walk func(prefix string, value any)
	walk = func(prefix string, value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, inner := range typed {
				walk(prefix+key+".", inner)
			}
		case string:
			out[strings.TrimSuffix(prefix, ".")] = typed
		case bool:
			out[strings.TrimSuffix(prefix, ".")] = strconv.FormatBool(typed)
		case float64:
			out[strings.TrimSuffix(prefix, ".")] = strconv.FormatFloat(typed, 'f', -1, 64)
		}
	}
	walk("", object)
	return out
}
