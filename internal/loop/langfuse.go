package loop

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
)

// LoadLangfuse reads the operator's Langfuse settings (ADR-0064). Unset,
// or "" after a forgotten hub key, is the zero Langfuse: no export.
func LoadLangfuse(ctx context.Context, settings store.SettingsStore) (store.Langfuse, error) {
	raw, err := settings.Get(ctx, store.SettingLangfuse)
	if errors.Is(err, store.ErrNotFound) || (err == nil && raw == "") {
		return store.Langfuse{}, nil
	}
	if err != nil {
		return store.Langfuse{}, err
	}
	var langfuse store.Langfuse
	if err := json.Unmarshal([]byte(raw), &langfuse); err != nil {
		return store.Langfuse{}, fmt.Errorf("langfuse settings: %w", err)
	}
	return langfuse, nil
}

// StoreLangfuse writes the operator's Langfuse settings, sealed.
func StoreLangfuse(ctx context.Context, settings store.SettingsStore, langfuse store.Langfuse) error {
	raw, err := json.Marshal(langfuse)
	if err != nil {
		return err
	}
	return settings.Set(ctx, store.SettingLangfuse, string(raw))
}

// RetireLangfuseKeys is after with each key pair before held and after
// no longer does added to its Retired list, once.
func RetireLangfuseKeys(before, after store.Langfuse) store.Langfuse {
	after.Retired = slices.Clone(before.Retired)
	for _, key := range []string{before.IngestKey, before.ReadKey} {
		if key != "" && key != after.IngestKey && key != after.ReadKey && !slices.Contains(after.Retired, key) {
			after.Retired = append(after.Retired, key)
		}
	}
	return after
}

// LangfuseExports reports whether a docker loop's traces have somewhere to
// go: the relay has an upstream and the key that lets traces in.
func LangfuseExports(langfuse store.Langfuse) bool {
	return langfuse.RelayUpstream != "" && langfuse.IngestKey != ""
}

// LangfuseAuthorization is the header value an ingest key pair sends:
// Langfuse takes its OTLP traces with HTTP Basic, the pair's public key
// as the user and its secret key as the password.
func LangfuseAuthorization(keyPair string) string {
	if keyPair == "" {
		return ""
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(keyPair))
}

// ValidLangfuseKey reports whether key is a Langfuse key pair as the
// settings take it: "public:secret", both halves present, and nothing a
// header value can't carry.
func ValidLangfuseKey(key string) error {
	public, secret, found := strings.Cut(key, ":")
	switch {
	case !found || public == "" || secret == "":
		return errors.New("a Langfuse key is its public and secret keys joined by a colon, public:secret")
	case strings.ContainsAny(key, " \t\r\n") || strings.Contains(secret, ":"):
		return errors.New("a Langfuse key pair holds no whitespace and one colon")
	}
	return nil
}

// ValidLangfuseReadAddress reports whether raw is where the hub may read
// Langfuse back, and send it the read key: https, or http on loopback, so
// the key never crosses a network in clear (ADR-0064).
func ValidLangfuseReadAddress(raw string) error {
	address, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("the read address doesn't parse: %w", err)
	}
	switch {
	case address.Host == "":
		return errors.New("the read address names no host")
	case address.User != nil, address.RawQuery != "", address.Fragment != "":
		return errors.New("the read address carries credentials, a query or a fragment")
	case address.Scheme == "https":
		return nil
	case address.Scheme == "http" && hostIsLoopback(address.Hostname()):
		return nil
	}
	return errors.New("the read address must be https, or http on this machine (localhost, 127.0.0.1 or ::1)")
}

// hostIsLoopback reports whether host names this machine's loopback.
// Unlike a connection's url, an unspecified address doesn't count: the
// read address is one the hub dials, and naming 0.0.0.0 there is a
// mistake worth refusing.
func hostIsLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
