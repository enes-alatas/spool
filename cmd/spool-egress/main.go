// Command spool-egress is the door in the workstation wall (ADR-0028).
//
// Workstations sit on an internal docker network with no route off it; this
// process is the one container on both that network and the bridge, so every
// outbound request a loop makes arrives here as a proxy request and is either
// forwarded to an allowlisted host or refused. The proxy holds no
// credentials, reads no request bodies, and terminates no TLS.
//
// Beside it runs the trace relay (ADR-0064): a loop's Claude Code exports
// its traces to it, and it forwards them to the operator's Langfuse with
// the ingest key and the loop's identity, which the loop never holds. That
// key is the one credential the process holds, and the relay's
// configuration arrives by `spool-egress relay-config` on stdin, which the
// hub runs with docker exec.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/enes-alatas/spool/internal/egress"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "relay-config" {
		if err := relayConfig(os.Stdin, egress.RelayFilePath); err != nil {
			fmt.Fprintln(os.Stderr, "spool-egress relay-config:", err)
			os.Exit(1)
		}
		return
	}

	listen := flag.String("listen", ":3128", "address to serve the proxy on")
	extra := flag.String("allow", "", "comma-separated entries to permit on top of the built-in defaults, each host or host:port")
	fleetFile := flag.String("fleet-file", "", "file of the operator's extra entries, re-read when it changes")
	relayListen := flag.String("relay-listen", "", "address to serve the trace relay on; empty serves none")
	relayFile := flag.String("relay-file", egress.RelayFilePath, "file of the trace relay's configuration, re-read when it changes")
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
	entries := egress.DefaultHosts()
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

	var fleet *egress.FleetFile
	if *fleetFile != "" {
		fleet = egress.NewFleetFile(*fleetFile, log)
	}
	// The proxy refuses Langfuse's hosts even when no relay listens, so a
	// relay turned off doesn't leave Langfuse to the allowlist.
	relay := egress.NewRelayConfig(*relayFile, log)
	if *relayListen != "" {
		go func() {
			err := egress.NewRelay(relay, log).Server(*relayListen).ListenAndServe()
			log.Error("trace relay stopped", "err", err)
			os.Exit(1)
		}()
		log.Info("trace relay ready", "listen", *relayListen)
	}

	// No read or write timeout: a CONNECT tunnel is long-lived by nature and
	// a loop's turn can hold one open for the length of a model response.
	srv := &http.Server{Addr: *listen, Handler: egress.NewProxy(allow, fleet, log).RefusingLangfuse(relay)}
	if err := srv.ListenAndServe(); err != nil {
		log.Error("egress proxy stopped", "err", err)
		os.Exit(1)
	}
}

// relayConfig writes the relay configuration the hub sends on stdin to
// path, after checking it can be used. One byte over the bound is read so
// an oversized file is refused, not cut short.
func relayConfig(stdin io.Reader, path string) error {
	data, err := io.ReadAll(io.LimitReader(stdin, egress.MaxRelayFile+1))
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return errors.New("nothing on stdin")
	}
	return egress.WriteRelayFile(path, data)
}
