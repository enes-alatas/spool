package httpapi

import (
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

func TestConnectionProblem(t *testing.T) {
	env := func(name string) store.ConnectionConfig { return store.ConnectionConfig{Env: name} }
	http := func(url string) store.ConnectionConfig {
		return store.ConnectionConfig{Transport: store.MCPTransportHTTP, URL: url}
	}
	stdio := store.ConnectionConfig{Transport: store.MCPTransportStdio, Command: "docs-mcp", Args: []string{"--read-only"}}
	cases := []struct {
		name       string
		connection store.Connection
		wantCode   string
	}{
		{"env-credential", store.Connection{Name: "github", Kind: store.ConnectionEnvCredential, Config: env("GH_TOKEN"), Secret: "s"}, ""},
		{"http mcp-server, no secret", store.Connection{Name: "docs", Kind: store.ConnectionMCPServer, Config: http("https://mcp.example.test/sse")}, ""},
		{"stdio mcp-server with a secret", store.Connection{Name: "docs-2", Kind: store.ConnectionMCPServer, Config: stdio, Secret: "s"}, ""},

		{"name with upper case", store.Connection{Name: "GitHub", Kind: store.ConnectionEnvCredential, Config: env("GH_TOKEN"), Secret: "s"}, codeConnectionNameInvalid},
		{"name with underscore", store.Connection{Name: "git_hub", Kind: store.ConnectionEnvCredential, Config: env("GH_TOKEN"), Secret: "s"}, codeConnectionNameInvalid},
		{"name leading dash", store.Connection{Name: "-github", Kind: store.ConnectionEnvCredential, Config: env("GH_TOKEN"), Secret: "s"}, codeConnectionNameInvalid},
		{"empty name", store.Connection{Kind: store.ConnectionEnvCredential, Config: env("GH_TOKEN"), Secret: "s"}, codeConnectionNameInvalid},
		{"name too long", store.Connection{Name: strings.Repeat("a", 33), Kind: store.ConnectionEnvCredential, Config: env("GH_TOKEN"), Secret: "s"}, codeConnectionNameInvalid},

		{"no kind", store.Connection{Name: "github", Config: env("GH_TOKEN"), Secret: "s"}, codeConnectionKindInvalid},
		{"unknown kind", store.Connection{Name: "github", Kind: "github-app", Config: env("GH_TOKEN"), Secret: "s"}, codeConnectionKindInvalid},

		{"env-credential, env with a dash", store.Connection{Name: "github", Kind: store.ConnectionEnvCredential, Config: env("GH-TOKEN"), Secret: "s"}, codeConnectionConfigInvalid},
		{"env-credential, no env", store.Connection{Name: "github", Kind: store.ConnectionEnvCredential, Secret: "s"}, codeConnectionConfigInvalid},
		{"env-credential with a url", store.Connection{Name: "github", Kind: store.ConnectionEnvCredential, Config: store.ConnectionConfig{Env: "GH_TOKEN", URL: "https://example.test"}, Secret: "s"}, codeConnectionConfigInvalid},
		{"mcp-server with an env", store.Connection{Name: "docs", Kind: store.ConnectionMCPServer, Config: store.ConnectionConfig{Env: "X", Transport: store.MCPTransportStdio, Command: "c"}}, codeConnectionConfigInvalid},
		{"mcp-server, no transport", store.Connection{Name: "docs", Kind: store.ConnectionMCPServer, Config: store.ConnectionConfig{URL: "https://example.test"}}, codeConnectionConfigInvalid},
		{"mcp-server, transport sse", store.Connection{Name: "docs", Kind: store.ConnectionMCPServer, Config: store.ConnectionConfig{Transport: "sse", URL: "https://example.test"}}, codeConnectionConfigInvalid},
		{"http mcp-server, no url", store.Connection{Name: "docs", Kind: store.ConnectionMCPServer, Config: http("")}, codeConnectionConfigInvalid},
		{"http mcp-server, ftp url", store.Connection{Name: "docs", Kind: store.ConnectionMCPServer, Config: http("ftp://example.test")}, codeConnectionConfigInvalid},
		{"http mcp-server, url with userinfo", store.Connection{Name: "docs", Kind: store.ConnectionMCPServer, Config: http("https://bot:fixture-password@mcp.example.test/sse")}, codeConnectionConfigInvalid},
		{"http mcp-server, url with a user only", store.Connection{Name: "docs", Kind: store.ConnectionMCPServer, Config: http("https://bot@mcp.example.test/sse")}, codeConnectionConfigInvalid},
		{"http mcp-server, url without a host", store.Connection{Name: "docs", Kind: store.ConnectionMCPServer, Config: http("https:///sse")}, codeConnectionConfigInvalid},
		{"http mcp-server with a command", store.Connection{Name: "docs", Kind: store.ConnectionMCPServer, Config: store.ConnectionConfig{Transport: store.MCPTransportHTTP, URL: "https://example.test", Command: "c"}}, codeConnectionConfigInvalid},
		{"stdio mcp-server, no command", store.Connection{Name: "docs", Kind: store.ConnectionMCPServer, Config: store.ConnectionConfig{Transport: store.MCPTransportStdio}}, codeConnectionConfigInvalid},
		{"stdio mcp-server with a url", store.Connection{Name: "docs", Kind: store.ConnectionMCPServer, Config: store.ConnectionConfig{Transport: store.MCPTransportStdio, Command: "c", URL: "https://example.test"}}, codeConnectionConfigInvalid},

		{"env-credential, no secret", store.Connection{Name: "github", Kind: store.ConnectionEnvCredential, Config: env("GH_TOKEN")}, codeConnectionSecretInvalid},
		{"secret too large", store.Connection{Name: "docs", Kind: store.ConnectionMCPServer, Config: stdio, Secret: strings.Repeat("s", maxSecretValueLen+1)}, codeConnectionSecretInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, problem := connectionProblem(&tc.connection)
			if code != tc.wantCode || (code == "") != (problem == "") {
				t.Fatalf("connectionProblem = %q (%q), want code %q", code, problem, tc.wantCode)
			}
		})
	}
}
