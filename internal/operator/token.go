// Package operator owns the credential that distinguishes the human running
// Spool from everything else that can reach the hub's API.
//
// Binding the API to localhost is not a boundary: every other process and
// every other account on the machine reaches it, and so does a page in the
// operator's own browser, which can issue cross-origin requests at
// 127.0.0.1 (#239). The token is what a request has to carry to be the
// operator's. It is deliberately unlike a loop's MCP token — different file,
// different check, never in an exec env, a secret or a prompt — so nothing
// Spool hands a loop is this credential.
//
// Nothing *handed* to it: a contained loop cannot read the file either, since
// the data directory is not mounted into the workstation. A bare loop is a
// subprocess under the operator's own uid and can read anything they can,
// this file included — mode 0600 stops other accounts, not a process that
// already is the operator. That asymmetry is what the uncontained badge means
// (ADR-0017, ADR-0030), not a gap in this one.
package operator

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/enes-alatas/spool/internal/datadir"
)

// TokenFile is where the token lives, directly under the data directory the
// hub was started with: an operator who moves their data moves their
// credential with it, and a second fleet on one machine has its own.
const TokenFile = "operator-token"

// tokenBytes is the entropy behind the credential. 32 bytes is far past
// guessable over a loopback socket, and the hex is short enough to paste.
const tokenBytes = 32

// Path is where Load reads and writes the token for a data directory.
func Path(dataDir string) string { return filepath.Join(dataDir, TokenFile) }

// Load returns the operator token for a data directory, minting and storing
// one the first time. minted reports whether this call created it, which is
// the hub's cue to print the value once — the only time it is ever written to
// a terminal by itself.
//
// The file is created 0600 and an existing one that is readable by anyone
// else is narrowed, on the same reasoning as the rest of the data directory:
// a credential another local account can read is that account's credential
// too.
func Load(dataDir string) (token string, minted bool, err error) {
	path := Path(dataDir)
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		token = strings.TrimSpace(string(b))
		if token == "" {
			// An empty file is not a token. Treating it as one would
			// authenticate every request that presents nothing.
			return "", false, fmt.Errorf("operator token: %s is empty — delete it and restart to mint a new one", path)
		}
		if err := narrow(path); err != nil {
			return "", false, err
		}
		return token, false, nil
	case errors.Is(err, fs.ErrNotExist):
		token, err = mint(path)
		return token, err == nil, err
	default:
		return "", false, fmt.Errorf("operator token: %w", err)
	}
}

func mint(path string) (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("operator token: %w", err)
	}
	token := hex.EncodeToString(b)
	// O_EXCL so two hubs racing on one data directory cannot each believe
	// they minted the token the other is now checking against.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, datadir.FileMode)
	if err != nil {
		return "", fmt.Errorf("operator token: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(token + "\n"); err != nil {
		return "", fmt.Errorf("operator token: %w", err)
	}
	return token, nil
}

func narrow(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("operator token: %w", err)
	}
	if info.Mode().Perm()&^datadir.FileMode == 0 {
		return nil
	}
	if err := os.Chmod(path, datadir.FileMode); err != nil {
		return fmt.Errorf("operator token: %w", err)
	}
	return nil
}

// Matches compares a presented credential against the real one without
// leaking the answer through how long it took.
func Matches(want, got string) bool {
	if want == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}
