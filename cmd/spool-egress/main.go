// Command spool-egress is the door in the workstation wall (ADR-0028).
//
// Workstations sit on an internal docker network with no route off it; this
// process is the one container on both that network and the bridge, so every
// outbound request a loop makes arrives here as a proxy request and is either
// forwarded to an allowlisted host or refused. It holds no credentials, reads
// no request bodies, and terminates no TLS.
package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/enes-alatas/spool/internal/egress"
)

func main() {
	listen := flag.String("listen", ":3128", "address to serve the proxy on")
	extra := flag.String("allow", "", "comma-separated entries to permit on top of the built-in defaults, each host or host:port")
	verbose := flag.Bool("verbose", false, "log every allowed request, not just refusals")
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	// The hub's own entry arrives through --allow: its port is the operator's
	// choice, so unlike every public host it cannot be a compiled-in default.
	// An entry that cannot be acted on is dropped loudly rather than kept as
	// a rule matching nothing — the hub rejects those at startup, so one
	// reaching here is worth a line.
	entries := egress.DefaultHosts
	for _, entry := range strings.Split(*extra, ",") {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		if err := egress.Validate(entry); err != nil {
			log.Warn("ignoring unusable allowlist entry", "err", err)
			continue
		}
		entries = append(entries, entry)
	}
	allow := egress.New(entries)
	log.Info("egress proxy ready", "listen", *listen, "allows", allow.Entries())

	// No read or write timeout: a CONNECT tunnel is long-lived by nature and
	// a loop's turn can hold one open for the length of a model response.
	srv := &http.Server{Addr: *listen, Handler: egress.NewProxy(allow, log)}
	if err := srv.ListenAndServe(); err != nil {
		log.Error("egress proxy stopped", "err", err)
		os.Exit(1)
	}
}
