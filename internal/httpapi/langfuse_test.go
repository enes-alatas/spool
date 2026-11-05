package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// A change hands the relay what was stored, and tells the redactor when a
// key moved; a PUT that changes nothing does neither. A refused field
// leaves the rest unwritten.
func TestPutLangfuseHandsTheRelayItsSettings(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var handed []store.Langfuse
	secretsChanged := 0
	server := &Server{
		Store: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		TraceRelay: func(_ context.Context, langfuse store.Langfuse) error {
			handed = append(handed, langfuse)
			return nil
		},
		SecretsChanged: func(context.Context) { secretsChanged++ },
	}
	put := func(body string) int {
		recorder := httptest.NewRecorder()
		server.handlePutLangfuse(recorder, httptest.NewRequest("PUT", "/api/settings/langfuse", strings.NewReader(body)))
		return recorder.Code
	}

	if code := put(`{"relay_upstream":"http://langfuse-web","ingest_key":"pk-lf-a:sk-lf-a"}`); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	want := store.Langfuse{RelayUpstream: "http://langfuse-web", IngestKey: "pk-lf-a:sk-lf-a"}
	if len(handed) != 1 || !reflect.DeepEqual(handed[0], want) || secretsChanged != 1 {
		t.Fatalf("handed %+v with %d secret changes, want %+v once", handed, secretsChanged, want)
	}

	if code := put(`{"relay_upstream":" http://langfuse-web "}`); code != http.StatusOK {
		t.Fatalf("an unchanged PUT = %d", code)
	}
	if len(handed) != 1 || secretsChanged != 1 {
		t.Errorf("an unchanged PUT handed the relay again (%d) or moved the secrets (%d)", len(handed), secretsChanged)
	}

	if code := put(`{"read_address":"https://langfuse.example","read_key":"bad"}`); code != http.StatusBadRequest {
		t.Fatalf("a bad key = %d, want 400", code)
	}
	if len(handed) != 1 {
		t.Errorf("a refused PUT handed the relay %+v", handed[len(handed)-1])
	}

	if code := put(`{"read_address":"https://langfuse.example"}`); code != http.StatusOK {
		t.Fatalf("PUT = %d", code)
	}
	want.ReadAddress = "https://langfuse.example"
	if len(handed) != 2 || !reflect.DeepEqual(handed[1], want) || secretsChanged != 1 {
		t.Errorf("an address change handed %+v with %d secret changes, want %+v and no secret change", handed, secretsChanged, want)
	}

	// A replaced pair, and then a cleared one, stay retired, each once:
	// Langfuse still honours them (ADR-0043).
	for _, body := range []string{`{"ingest_key":"pk-lf-b:sk-lf-b"}`, `{"ingest_key":"pk-lf-a:sk-lf-a"}`, `{"ingest_key":""}`} {
		if code := put(body); code != http.StatusOK {
			t.Fatalf("PUT %s = %d", body, code)
		}
	}
	want.IngestKey, want.Retired = "", []string{"pk-lf-a:sk-lf-a", "pk-lf-b:sk-lf-b"}
	if len(handed) != 5 || !reflect.DeepEqual(handed[4], want) || secretsChanged != 4 {
		t.Errorf("after two key changes and a clear: handed %+v with %d secret changes, want %+v and 4", handed[len(handed)-1], secretsChanged, want)
	}
}
