package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// On macOS the CLI keeps a login in the operator's login Keychain, not in
// .credentials.json: it writes it with the system's security tool, and
// deletes the file once the Keychain holds it. The file is only its
// fallback, for a Keychain the tool can't reach (2.1.316, #1304). The hub
// reads the login from the file in a config directory of its own, so each
// claude it runs gets a security of the hub's first on its PATH that
// answers every call as errSecItemNotFound: the CLI calls the tool by name,
// so it falls back to the file, and the operator's Keychain is never
// touched.

// keychainItemNotFound is security's exit status for errSecItemNotFound,
// which the CLI reads as no item, and on a write as a failure that isn't
// a locked Keychain's, so it writes the file instead.
const keychainItemNotFound = "44"

// keychainShim is the security the hub's claude runs find. It takes the
// login in its arguments or on stdin when the CLI writes one, so it reads
// nothing and keeps nothing: it only exits.
const keychainShim = "#!/bin/sh\nexit " + keychainItemNotFound + "\n"

// KeychainShimDir is the directory in configDir that holds the hub's
// security, made fresh for each run and gone with configDir.
func KeychainShimDir(configDir string) string { return filepath.Join(configDir, ".spool-bin") }

// KeychainEnv is env for a claude run in configDir, made a fresh, private
// directory holding the hub's security first on its PATH. It pins
// CLAUDE_SECURESTORAGE_CONFIG_DIR to configDir as well, which the CLI's
// file and its Keychain item are both named for, so neither goes
// anywhere else and KeychainItem names the item exactly. configDir must
// be absolute: the CLI runs inside it, where a relative PATH entry names
// nothing and the lookup goes on to the system's security.
func KeychainEnv(env []string, configDir string) ([]string, error) {
	if !filepath.IsAbs(configDir) {
		return nil, fmt.Errorf("claude: config directory %q is not absolute", configDir)
	}
	dir := KeychainShimDir(configDir)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "security"), []byte(keychainShim), 0o500); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(env)+2)
	path := ""
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		switch name {
		case "PATH":
			path = value
			continue
		case "CLAUDE_SECURESTORAGE_CONFIG_DIR":
			continue
		}
		out = append(out, entry)
	}
	if path != "" {
		path = string(os.PathListSeparator) + path
	}
	return append(out, "PATH="+dir+path, "CLAUDE_SECURESTORAGE_CONFIG_DIR="+configDir), nil
}

// KeychainItem is the service name of the Keychain item a macOS claude run
// in configDir would write its login to, past the hub's security: "Claude
// Code-credentials-" and the first 8 hex digits of the SHA-256 of the
// directory (which the CLI takes in Unicode NFC, the same bytes for the
// hub's own paths when they are ASCII), under the user's account. A claude
// that called the system's tool by its path would leave one there; the hub
// names it rather than reading or deleting anything of the operator's.
func KeychainItem(configDir string) string {
	sum := sha256.Sum256([]byte(configDir))
	return "Claude Code-credentials-" + hex.EncodeToString(sum[:])[:8]
}
