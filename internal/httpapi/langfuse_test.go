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

	"github.com/enes-alatas/spool/internal/loop"
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

// Settings the hub's Langfuse file set are the file's: a PUT is
// refused, naming the file, and changes nothing (#1388).
func TestPutLangfuseRefusesTheFilesSettings(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	fromFile := store.Langfuse{RelayUpstream: "http://langfuse-web", IngestKey: "pk-lf-a:sk-lf-a",
		ReadKey: "pk-lf-a:sk-lf-a", File: "/opt/spool/docker/langfuse/hub.env"}
	if err := loop.StoreLangfuse(context.Background(), db.Settings(), fromFile); err != nil {
		t.Fatal(err)
	}
	handed := 0
	server := &Server{
		Store: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		TraceRelay: func(context.Context, store.Langfuse) error { handed++; return nil },
	}
	recorder := httptest.NewRecorder()
	server.handlePutLangfuse(recorder, httptest.NewRequest("PUT", "/api/settings/langfuse",
		strings.NewReader(`{"relay_upstream":""}`)))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), codeLangfuseFromFile) ||
		!strings.Contains(recorder.Body.String(), fromFile.File) {
		t.Fatalf("PUT over the file's settings = %d %s, want 409 naming the file", recorder.Code, recorder.Body)
	}
	stored, err := loop.LoadLangfuse(context.Background(), db.Settings())
	if err != nil || !reflect.DeepEqual(stored, fromFile) || handed != 0 {
		t.Fatalf("a refused PUT stored %+v (%v) and handed the relay %d times", stored, err, handed)
	}

	recorder = httptest.NewRecorder()
	server.handleGetLangfuse(recorder, httptest.NewRequest("GET", "/api/settings/langfuse", nil))
	if !strings.Contains(recorder.Body.String(), `"file":"`+fromFile.File+`"`) {
		t.Fatalf("GET = %s, want the file named", recorder.Body)
	}
}
