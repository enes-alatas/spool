package loop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// Synthetic key halves, as the hub's Langfuse file holds them.
const (
	filePublicKey = "pk-lf-synthetic-file"
	fileSecretKey = "sk-lf-synthetic-file-secret"
	fileKeyPair   = filePublicKey + ":" + fileSecretKey
)

// writeLangfuseEnv writes a Langfuse file with body and returns its path.
func writeLangfuseEnv(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hub.env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The hub's Langfuse file is the profile's addresses and the project's
// key pair as both keys, any other variable ignored; LANGFUSE_URL, quoted
// as compose takes it, moves the read address (#1388).
func TestReadLangfuseFile(t *testing.T) {
	script := "# Written by scripts/langfuse-setup.sh.\nSPOOL_EGRESS_NETWORK=spool-egress\n\n" +
		"LANGFUSE_PUBLIC_KEY=" + filePublicKey + "\nLANGFUSE_SECRET_KEY=" + fileSecretKey + "\n"
	path := writeLangfuseEnv(t, script)
	langfuse, found, err := ReadLangfuseFile(path)
	if err != nil || !found {
		t.Fatalf("ReadLangfuseFile = %v, %v", found, err)
	}
	want := store.Langfuse{RelayUpstream: "http://langfuse-web", ReadAddress: "http://127.0.0.1:3000",
		IngestKey: fileKeyPair, ReadKey: fileKeyPair, File: path}
	if !langfuseEqual(langfuse, want) {
		t.Fatalf("settings = %+v, want %+v", langfuse, want)
	}

	langfuse, _, err = ReadLangfuseFile(writeLangfuseEnv(t, script+`LANGFUSE_URL="https://langfuse.example"`+"\n"))
	if err != nil || langfuse.ReadAddress != "https://langfuse.example" {
		t.Fatalf("with LANGFUSE_URL: read address %q, %v", langfuse.ReadAddress, err)
	}

	if _, found, err := ReadLangfuseFile(filepath.Join(t.TempDir(), "absent.env")); found || err != nil {
		t.Fatalf("an absent file = %v, %v, want not found", found, err)
	}
}

// A file the hub can't act on refuses with the file's name and what is
// wrong in it, and never a value from it.
func TestReadLangfuseFileRefuses(t *testing.T) {
	keys := "LANGFUSE_PUBLIC_KEY=" + filePublicKey + "\nLANGFUSE_SECRET_KEY=" + fileSecretKey + "\n"
	for _, tc := range []struct {
		name, body, want string
	}{
		{"no secret key", "LANGFUSE_PUBLIC_KEY=" + filePublicKey + "\n", "LANGFUSE_SECRET_KEY is missing"},
		{"empty public key", "LANGFUSE_PUBLIC_KEY=\nLANGFUSE_SECRET_KEY=" + fileSecretKey + "\n", "LANGFUSE_PUBLIC_KEY is missing"},
		{"not a pair line", keys + fileSecretKey + "\n", "line 3 isn't NAME=VALUE"},
		{"a key with a space", "LANGFUSE_PUBLIC_KEY=pk-lf a\nLANGFUSE_SECRET_KEY=" + fileSecretKey + "\n", "no whitespace"},
		{"a plain-http read address", keys + "LANGFUSE_URL=http://langfuse.example\n", "LANGFUSE_URL"},
		// url.Parse quotes its input; the refusal mustn't, userinfo and all
		{"an unparsable url with userinfo", keys + "LANGFUSE_URL=http://admin:sk-lf-in-url@[x\n", "LANGFUSE_URL doesn't parse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeLangfuseEnv(t, tc.body)
			_, found, err := ReadLangfuseFile(path)
			if err == nil || !found {
				t.Fatalf("ReadLangfuseFile = %v, %v, want a refusal", found, err)
			}
			if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal %q names neither %s nor %q", err, path, tc.want)
			}
			if strings.Contains(err.Error(), fileSecretKey) || strings.Contains(err.Error(), "sk-lf") {
				t.Fatalf("refusal %q carries a key", err)
			}
		})
	}

	if os.Geteuid() != 0 {
		path := writeLangfuseEnv(t, keys)
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ReadLangfuseFile(path); !errors.Is(err, ErrLangfuseFileUnreadable) || !strings.Contains(err.Error(), path) {
			t.Fatalf("an unreadable file = %v, want ErrLangfuseFileUnreadable naming it", err)
		}
	}
}

// At start the file wins over what is stored; with the file gone,
// settings it set are cleared and settings the API stored stay. A pair
// let go of stays redacted (#1388).
func TestApplyLangfuseFile(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	settings := db.Settings()

	byAPI := store.Langfuse{RelayUpstream: "https://langfuse.example", ReadAddress: "https://langfuse.example",
		IngestKey: "pk-lf-api:sk-lf-api-secret", ReadKey: "pk-lf-api:sk-lf-api-secret"}
	if err := StoreLangfuse(ctx, settings, byAPI); err != nil {
		t.Fatal(err)
	}
	absent := filepath.Join(t.TempDir(), "hub.env")
	if got, cleared, err := ApplyLangfuseFile(ctx, settings, absent); err != nil || cleared != "" || !langfuseEqual(got, byAPI) {
		t.Fatalf("no file over the API's settings = %+v, %v, want them kept", got, err)
	}

	path := writeLangfuseEnv(t, "LANGFUSE_PUBLIC_KEY="+filePublicKey+"\nLANGFUSE_SECRET_KEY="+fileSecretKey+"\n")
	got, cleared, err := ApplyLangfuseFile(ctx, settings, path)
	if err != nil || cleared != "" || got.File != path || got.IngestKey != fileKeyPair || !LangfuseExports(got) {
		t.Fatalf("the file = %+v, %v, want its settings in force", got, err)
	}
	if !slices.Contains(got.Retired, byAPI.IngestKey) {
		t.Fatalf("the API's pair wasn't retired: %v", got.Retired)
	}
	stored, err := LoadLangfuse(ctx, settings)
	if err != nil || !langfuseEqual(stored, got) {
		t.Fatalf("stored = %+v, %v, want %+v", stored, err, got)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	got, cleared, err = ApplyLangfuseFile(ctx, settings, path)
	if err != nil || cleared != path || LangfuseExports(got) || got.File != "" || got.IngestKey != "" {
		t.Fatalf("with the file gone = %+v, cleared %q, %v, want traces off and the file named", got, cleared, err)
	}
	if !slices.Contains(got.Retired, fileKeyPair) || !slices.Contains(got.Retired, byAPI.IngestKey) {
		t.Fatalf("the let-go pairs aren't all retired: %v", got.Retired)
	}

	// a refused file changes nothing stored
	bad := writeLangfuseEnv(t, "LANGFUSE_PUBLIC_KEY="+filePublicKey+"\n")
	if _, _, err := ApplyLangfuseFile(ctx, settings, bad); err == nil {
		t.Fatal("a file missing its secret key was applied")
	}
	if after, _ := LoadLangfuse(ctx, settings); !langfuseEqual(after, got) {
		t.Fatalf("a refused file changed the stored settings to %+v", after)
	}
}

// langfuseEqual reports whether got and want are the same settings.
func langfuseEqual(got, want store.Langfuse) bool {
	return got.RelayUpstream == want.RelayUpstream && got.ReadAddress == want.ReadAddress &&
		got.IngestKey == want.IngestKey && got.ReadKey == want.ReadKey && got.File == want.File &&
		slices.Equal(got.Retired, want.Retired)
}
