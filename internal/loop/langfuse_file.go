package loop

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"strings"

	"github.com/enes-alatas/spool/internal/egress"
	"github.com/enes-alatas/spool/internal/store"
)

// ComposeLangfuseReadAddress is where a native hub reads the bundled
// Langfuse back: the profile's UI port on loopback (ADR-0064).
const ComposeLangfuseReadAddress = "http://127.0.0.1:3000"

// The variables the hub's Langfuse file holds. The setup script writes the
// key pair alone there, and the operator may add LANGFUSE_URL; the
// profile's other secrets stay in its own env file, which the hub never
// reads, since every bare loop runs as the hub's user (#1388).
const (
	envLangfusePublicKey = "LANGFUSE_PUBLIC_KEY"
	envLangfuseSecretKey = "LANGFUSE_SECRET_KEY"
	envLangfuseURL       = "LANGFUSE_URL"
)

// ErrLangfuseFileUnreadable is a Langfuse file the hub's user may not
// read, as when the setup script ran as another user. It warns rather
// than stops the hub: Langfuse is optional, and the fleet runs without it.
var ErrLangfuseFileUnreadable = errors.New("the hub's user can't read it")

// ReadLangfuseFile reads the hub's Langfuse file, docker/langfuse/hub.env
// as scripts/langfuse-setup.sh writes it, into the hub's Langfuse settings
// (ADR-0064, #1388): the profile's upstream, LANGFUSE_URL or the UI port
// as the read address, and the project's key pair as both keys. File is
// path. found is false when there is no file, which is no Langfuse. An
// error names the file and the line or variable at fault, never a value;
// one the hub's user may not read is ErrLangfuseFileUnreadable.
func ReadLangfuseFile(path string) (langfuse store.Langfuse, found bool, err error) {
	if path == "" {
		return store.Langfuse{}, false, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return store.Langfuse{}, false, nil
	}
	if errors.Is(err, fs.ErrPermission) {
		return store.Langfuse{}, true, fmt.Errorf("%s: %w; chown it to the hub's user, keeping it 0600", path, ErrLangfuseFileUnreadable)
	}
	if err != nil {
		return store.Langfuse{}, true, fmt.Errorf("%s: %w", path, err)
	}
	vars, err := parseEnvFile(raw)
	if err != nil {
		return store.Langfuse{}, true, fmt.Errorf("%s: %w", path, err)
	}
	for _, name := range []string{envLangfusePublicKey, envLangfuseSecretKey} {
		if vars[name] == "" {
			return store.Langfuse{}, true, fmt.Errorf("%s: %s is missing", path, name)
		}
	}
	key := vars[envLangfusePublicKey] + ":" + vars[envLangfuseSecretKey]
	if err := ValidLangfuseKey(key); err != nil {
		return store.Langfuse{}, true, fmt.Errorf("%s: %s and %s: %w", path, envLangfusePublicKey, envLangfuseSecretKey, err)
	}
	readAddress := ComposeLangfuseReadAddress
	if address := vars[envLangfuseURL]; address != "" {
		// url.Parse's error quotes its input, which may hold userinfo, and
		// the redactor hasn't loaded yet: say only that it doesn't parse
		if _, err := url.Parse(address); err != nil {
			return store.Langfuse{}, true, fmt.Errorf("%s: %s doesn't parse as a URL", path, envLangfuseURL)
		}
		if err := ValidLangfuseReadAddress(address); err != nil {
			return store.Langfuse{}, true, fmt.Errorf("%s: %s: %w", path, envLangfuseURL, err)
		}
		readAddress = address
	}
	return store.Langfuse{
		RelayUpstream: egress.ComposeLangfuseUpstream,
		ReadAddress:   readAddress,
		IngestKey:     key,
		ReadKey:       key,
		File:          path,
	}, true, nil
}

// parseEnvFile reads the KEY=VALUE lines of an env file: blank lines and
// # comments skipped, and one pair of matching quotes around a value
// removed, as compose removes them. A line it can't read is an error
// naming its number only, since the line may hold a secret.
func parseEnvFile(raw []byte) (map[string]string, error) {
	vars := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for number := 1; scanner.Scan(); number++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, found := strings.Cut(line, "=")
		name = strings.TrimSpace(strings.TrimPrefix(name, "export "))
		if !found || name == "" || strings.ContainsAny(name, " \t") {
			return nil, fmt.Errorf("line %d isn't NAME=VALUE", number)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		vars[name] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return vars, nil
}

// ApplyLangfuseFile brings the stored Langfuse settings in line with the
// hub's Langfuse file at path, at the hub's start (#1388). A file
// that is there wins over whatever is stored. With none, settings an
// earlier start took from a file are cleared, which is traces off, and
// cleared is that file, for the hub to say so; settings an admin stored
// through the API are left as they are. A key pair let go of either way
// is retired, so it stays redacted. It runs before the redactor's first
// load, which then masks what it stored.
func ApplyLangfuseFile(ctx context.Context, settings store.SettingsStore, path string) (langfuse store.Langfuse, cleared string, err error) {
	stored, err := LoadLangfuse(ctx, settings)
	if err != nil {
		return store.Langfuse{}, "", err
	}
	fromFile, found, err := ReadLangfuseFile(path)
	if err != nil {
		return store.Langfuse{}, "", err
	}
	switch {
	case found:
		langfuse = fromFile
	case stored.File != "":
		// the file that set them is gone: traces off
		cleared = stored.File
	default:
		return stored, "", nil
	}
	langfuse = RetireLangfuseKeys(stored, langfuse)
	if langfuse.RelayUpstream == stored.RelayUpstream && langfuse.ReadAddress == stored.ReadAddress &&
		langfuse.IngestKey == stored.IngestKey && langfuse.ReadKey == stored.ReadKey && langfuse.File == stored.File {
		return stored, "", nil
	}
	if err := StoreLangfuse(ctx, settings, langfuse); err != nil {
		return store.Langfuse{}, "", err
	}
	return langfuse, cleared, nil
}
