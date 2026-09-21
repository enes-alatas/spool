// Command spool runs the Spool orchestrator: a fleet of long-running
// Claude Code loops with a web control room and Telegram bridge.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/datadir"
	"github.com/enes-alatas/spool/internal/egress"
	"github.com/enes-alatas/spool/internal/httpapi"
	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/operator"
	"github.com/enes-alatas/spool/internal/redact"
	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/runtime"
	"github.com/enes-alatas/spool/internal/runtime/bare"
	"github.com/enes-alatas/spool/internal/runtime/docker"
	"github.com/enes-alatas/spool/internal/sched"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
	"github.com/enes-alatas/spool/internal/surface/telegram"
	"github.com/enes-alatas/spool/internal/version"
	"github.com/enes-alatas/spool/web"
)

// redactTTL bounds how long a secret written while the process runs can go
// unredacted. The API refreshes the redactor the moment it writes one, so
// this is only the backstop for a write that forgets to — short enough that
// the window is a blink, long enough that the log's hot path reloads rarely.
const redactTTL = 5 * time.Second

// Set by the linker: `make server` passes git describe, the commit and the
// build time. Empty in a plain `go build`, which internal/version answers
// from what Go recorded in the binary instead.
var (
	buildVersion string
	buildCommit  string
	buildTime    string
)

func main() {
	// `spool token` prints the operator credential for a data directory and
	// exits: the value is shown once when it is minted, and an operator who
	// did not keep it needs a way back to it that is not reading the file
	// path out of the docs (#239).
	if len(os.Args) > 1 && os.Args[1] == "token" {
		printToken(os.Args[2:])
		return
	}

	listen := flag.String("listen", "127.0.0.1:8080", "address to serve the operator's API/UI on; never reachable from a workstation")
	mcpListen := flag.String("mcp-listen", "127.0.0.1:8081", "address to serve the loop-facing /mcp endpoint on; the one port of this machine a workstation may reach (#238)")
	trustedHosts := flag.String("trusted-host", "", "comma-separated Host/Origin names this hub also answers to, each \"host\" or \"host:port\" — for a hub reached through a proxy under that proxy's name (ADR-0030)")
	dataDir := flag.String("data-dir", defaultDataDir(), "directory for spool.db, loop homes and worktrees")
	claudeBin := flag.String("claude-bin", "claude", "path to the claude binary (bare runtime)")
	runtimeChoice := flag.String("runtime", "auto", "default runtime for new loops: auto (docker when the daemon is reachable), docker, or bare")
	allowBare := flag.Bool("allow-bare", false, "let the control room create uncontained bare loops on a hub whose default is docker; implied by --runtime bare (ADR-0017)")
	workstationImage := flag.String("workstation-image", "spool-workstation", "default image for docker workstations")
	egressImage := flag.String("egress-image", "spool-egress", "image for the workstation egress proxy; empty leaves workstation egress open (ADR-0028)")
	egressAllow := flag.String("egress-allow", "", "comma-separated hosts (each \"host\" or \"host:port\") workstations may reach on top of the built-in allowlist")
	healthSec := flag.Int("workstation-health-sec", 45, "seconds between workstation liveness polls")
	partials := flag.Bool("partial-messages", true, "stream token deltas to the UI (--include-partial-messages)")
	telegramAPI := flag.String("telegram-api-base", telegram.APIBase, "Telegram Bot API base URL (tests point this at a stand-in server)")
	bindSettleSec := flag.Int("telegram-bind-settle-sec", 0, "seconds a newly bound bot waits before it may ingest a group (0 = the production margin; tests against a stand-in API shorten it)")
	retentionDays := flag.Int("events-retention-days", 30, "prune raw claude events older than this many days (0 disables; messages and turns are never pruned)")
	showVersion := flag.Bool("version", false, "print the build's version and exit")
	flag.Parse()

	// Spool takes flags, not subcommands, and Go's flag package stops
	// parsing at the first non-flag argument rather than complaining about
	// it. So `spool serve --data-dir /tmp/x` silently discarded every flag
	// after `serve` and ran the *default* fleet: the operator's own, on the
	// default port, minting and printing that fleet's operator token into
	// whatever log the caller was writing (#247). Refusing here is the whole
	// fix for the class, and it has to be here — ahead of every line that
	// opens a directory, binds a port or mints a credential.
	if flag.NArg() > 0 {
		usageError("unknown argument %q — spool takes flags, not subcommands", flag.Arg(0))
	}

	build := version.Resolve(buildVersion, buildCommit, buildTime)
	if *showVersion {
		fmt.Println(build)
		return
	}

	logTo := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	log := slog.New(logTo)
	slog.SetDefault(log)

	// Which fleet this is, said before the directory is opened and long
	// before a token is minted into it. "Wrong fleet" is then the first line
	// of the log rather than something inferred afterwards from what changed
	// (#247).
	log.Info("fleet", "data", *dataDir)

	// The SandboxRuntime seam (ADR-0004): both implementations stay wired —
	// the runtime is a per-loop choice (ADR-0017) — and --runtime only picks
	// which one new loops default to.
	healthInterval := time.Duration(*healthSec) * time.Second
	// An unusable --egress-allow entry would permit nothing and say nothing,
	// leaving a host mysteriously unreachable from inside the wall; the
	// operator hears about it here instead (ADR-0028).
	allowEntries := splitList(*egressAllow)
	for _, entry := range allowEntries {
		if err := egress.Validate(entry); err != nil {
			log.Error("--egress-allow", "err", err)
			os.Exit(1)
		}
	}

	// The two listeners are the boundary this fleet rests on: workstations
	// are allowlisted to the MCP port alone, so the API port must not be it
	// (#238). One address serving both would hand every workstation the
	// unauthenticated API back.
	mcpHost, mcpPort, err := splitMCPListen(*listen, *mcpListen)
	if err != nil {
		log.Error("--mcp-listen", "err", err)
		os.Exit(1)
	}

	dockerRuntime := docker.New(docker.Options{
		DefaultImage: *workstationImage,
		EgressImage:  *egressImage,
		MCPPort:      mcpPort,
		EgressAllow:  allowEntries,
		HealthTTL:    healthCacheTTL(healthInterval),
	})
	runtimes := map[string]runtime.Runtime{
		store.RuntimeBare:   bare.New(*claudeBin),
		store.RuntimeDocker: dockerRuntime,
	}

	defaultRuntime, ver := selectDefaultRuntime(log, *runtimeChoice, runtimes)
	switch {
	case ver == "":
		log.Warn("claude version unknown until the workstation image exists — run `make image` (#14)",
			"image", *workstationImage)
	case !containsVersion(ver, claude.TestedVersion):
		log.Warn("claude version differs from the one Spool was verified against",
			"found", ver, "tested", claude.TestedVersion)
	}
	// A containerized loop reaches the hub through the docker bridge, which
	// has no route to loopback: the one destination it is allowed is then the
	// one it cannot use, and the fleet comes up looking healthy and never
	// wakes. The hub knows both halves here and nowhere earlier — the default
	// runtime is only resolved above.
	if defaultRuntime == store.RuntimeDocker && isLoopback(mcpHost) {
		log.Warn("docker workstations cannot reach --mcp-listen on loopback — bind it to an address the docker bridge can reach (#238)",
			"mcp_listen", *mcpListen, "example", "0.0.0.0:"+mcpPort)
	}
	log.Info("runtime ready", "default", defaultRuntime, "claude_version", ver,
		"spool_version", build.Version, "commit", build.Commit, "built_at", build.BuiltAt)
	logEgressPosture(log, dockerRuntime)

	if err := datadir.Secure(*dataDir, log); err != nil {
		log.Error("data dir", "err", err)
		os.Exit(1)
	}
	// After the directory is private, before anything serves: a token minted
	// into a world-readable directory would be everyone's.
	operatorToken, minted, err := operator.Load(*dataDir)
	if err != nil {
		log.Error("operator token", "err", err)
		os.Exit(1)
	}
	if minted {
		// Straight to stdout, once, and never again. The log goes through
		// redaction and into files an operator shares; this is the one place
		// the value is meant to be read by a person.
		fmt.Printf("\nOperator token (this is the only time it is shown):\n\n    %s\n\n"+
			"The control room asks for it once. `spool token --data-dir %s` prints it again.\n\n",
			operatorToken, *dataDir)
	}

	db, err := sqlite.Open(filepath.Join(*dataDir, "spool.db"))
	if err != nil {
		log.Error("open db", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	// Again, now that the database and its WAL sidecars exist: the first call
	// had to run before the open, to create the directory private, and on a
	// first run there was nothing inside it yet to narrow. Secure only ever
	// removes permission bits, so running it twice costs a stat apiece.
	if err := datadir.Secure(*dataDir, log); err != nil {
		log.Error("data dir", "err", err)
		os.Exit(1)
	}

	// Redaction (#150): every secret Spool holds, kept out of the
	// log, the stored transcripts and the API's responses. It reads the raw
	// db — it is the thing that knows the values — and everything else from
	// here down gets the decorated store instead, so no writer has to
	// remember the rule. Load once now: until the first load it redacts
	// nothing, and the wiring below starts writing immediately.
	redactor := redact.New(redact.StoreSource{Store: db}, redactTTL)
	if err := redactor.Refresh(context.Background()); err != nil {
		log.Error("load secrets for redaction", "err", err)
		os.Exit(1)
	}
	log = slog.New(redact.Handler(logTo, redactor))
	slog.SetDefault(log)
	rdb := redact.Store(db, redactor)

	b := bus.New()

	// wire the object graph; router and manager reference each other through
	// late-bound deps
	var router *route.Router
	var scheduler *sched.Scheduler

	deps := loop.Deps{
		Store:                     rdb,
		Bus:                       b,
		Runtimes:                  runtimes,
		WorkstationHealthInterval: healthInterval,
		PartialMessages:           *partials,
		Logger:                    log,
		RenderPrompt: func(l *store.Loop) loop.Prompt {
			cat, rules := catalogOf(rdb, l), rulesOf(rdb)
			return loop.Prompt{
				System:         loop.SystemPrompt(l, cat, rules, build.Version),
				StandingChange: loop.StandingInstructionsPreamble(l, cat, rules, build.Version),
			}
		},
		MCPEndpoint: func(l *store.Loop) string {
			host, port, err := net.SplitHostPort(*mcpListen)
			if err != nil {
				return ""
			}
			switch {
			case l.Runtime == store.RuntimeDocker:
				// containers reach the host through the gateway alias the
				// proxy is created with; --mcp-listen must therefore name an
				// address the docker bridge can reach — and only it, the
				// API's --listen stays on loopback (#238)
				host = "host.docker.internal"
			case isWildcard(host):
				host = "127.0.0.1"
			}
			return "http://" + net.JoinHostPort(host, port) + "/mcp"
		},
		OnTurnStart: func(l *store.Loop) {
			router.StartTurn(l.ID)
		},
		SendsThisTurn: func(loopID string) []string {
			return router.TurnSends(loopID)
		},
		OnTurnDone: func(l *store.Loop, trailer time.Duration, has bool) {
			scheduler.ScheduleAfterTurn(l, trailer, has)
		},
		ClaudeToken: func(ctx context.Context) (string, error) {
			token, err := rdb.Settings().Get(ctx, store.SettingClaudeOAuthToken)
			if errors.Is(err, store.ErrNotFound) {
				return "", nil
			}
			return token, err
		},
	}
	manager := loop.NewManager(deps)
	router = route.New(rdb, b, manager, log)
	scheduler = sched.New(rdb, b, manager, log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := manager.Boot(ctx); err != nil {
		log.Error("boot recovery", "err", err)
		os.Exit(1)
	}
	// close sessions left dangling by a previous crash (best effort)
	_ = rdb.Sessions().EndDangling(ctx, store.EndReasonCrash, time.Now().UnixMilli())

	go scheduler.Run(ctx)
	go pruneEvents(ctx, rdb, *retentionDays, log)

	bridge := telegram.NewBridge(rdb, b, router, log, *telegramAPI)
	bridge.SetBindSettle(time.Duration(*bindSettleSec) * time.Second)
	bridge.Start(ctx)

	api := &httpapi.Server{
		Store:          rdb,
		Bus:            b,
		Manager:        manager,
		Router:         router,
		Sched:          scheduler,
		Surface:        bridge,
		DataDir:        *dataDir,
		ClaudeVer:      ver,
		Build:          build,
		DefaultRuntime: defaultRuntime,
		BareAllowed:    *allowBare || *runtimeChoice == store.RuntimeBare,
		RuntimeAvailable: func(ctx context.Context, kind string) error {
			if kind == store.RuntimeDocker {
				return dockerRuntime.Available(ctx)
			}
			return nil
		},
		SecretsChanged: func(ctx context.Context) {
			if err := redactor.Refresh(ctx); err != nil {
				log.Error("reload secrets for redaction", "err", err)
			}
		},
		OperatorToken: operatorToken,
		ListenAddr:    *listen,
		TrustedHosts:  splitList(*trustedHosts),
		Log:           log,
		WebFS:         web.Dist(),
	}

	srv := &http.Server{Addr: *listen, Handler: redact.HTTP(api.Handler(), redactor)}
	mcpSrv := &http.Server{Addr: *mcpListen, Handler: redact.HTTP(api.MCPHandler(), redactor)}
	go func() {
		<-ctx.Done()
		log.Info("shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		_ = mcpSrv.Shutdown(shutCtx)
	}()

	// The loop-facing listener runs on its own goroutine; serving the API
	// blocks below. Failing to bind it takes the whole hub down: a
	// workstation with no hub to reach cannot take a turn, and a fleet that
	// looks healthy and never wakes is the worse failure.
	go func() {
		if err := mcpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("serve mcp", "err", err)
			stop()
		}
	}()

	log.Info("spool listening", "addr", *listen, "mcp_addr", *mcpListen, "data", *dataDir, "ui", api.WebFS != nil)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("serve", "err", err)
	}
	manager.Shutdown()
}

// printToken serves `spool token`, which reads the credential rather than
// minting one where a fleet already runs: an operator who lost the value
// needs it back, and a data directory with no token yet has had no first run
// to print it.
func printToken(args []string) {
	fs := flag.NewFlagSet("token", flag.ExitOnError)
	dataDir := fs.String("data-dir", defaultDataDir(), "directory holding the fleet whose token to print")
	_ = fs.Parse(args)
	// `spool token ~/.spool` reads as the obvious thing to type and means
	// nothing: the path is a flag's argument. Printing the *default* fleet's
	// credential in answer to a command naming another one is the same
	// mistake as #247, with the value on stdout by design.
	if fs.NArg() > 0 {
		usageError("unknown argument %q — the directory goes to --data-dir", fs.Arg(0))
	}

	token, minted, err := operator.Load(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if minted {
		fmt.Fprintf(os.Stderr, "no fleet has run in %s yet; minted a token for it\n", *dataDir)
	}
	fmt.Println(token)
}

// splitMCPListen takes --mcp-listen apart into the host the loop-facing
// endpoint binds and the port workstations are allowlisted to reach. It
// refuses an address that would put that endpoint back on the operator's own
// listener: the separation is what makes the API unreachable from inside a
// workstation, so a collision is a misconfiguration to stop on with a reason,
// not to leave to whichever bind loses the race (#238).
func splitMCPListen(apiAddr, mcpAddr string) (host, port string, err error) {
	apiHost, apiPort, err := net.SplitHostPort(apiAddr)
	if err != nil {
		return "", "", fmt.Errorf("--listen %q: %w", apiAddr, err)
	}
	mcpHost, mcpPort, err := net.SplitHostPort(mcpAddr)
	if err != nil {
		return "", "", fmt.Errorf("%q: %w", mcpAddr, err)
	}
	if mcpPort == "" {
		return "", "", fmt.Errorf("%q names no port", mcpAddr)
	}
	if mcpPort == apiPort && sameInterface(apiHost, mcpHost) {
		return "", "", fmt.Errorf("%q would serve /mcp on the API's own port; workstations reach this port, and the API must not be on it", mcpAddr)
	}
	return mcpHost, mcpPort, nil
}

// sameInterface reports whether two host halves on one port could be the same
// socket. Spelling is not the test: "localhost" and "127.0.0.1" are one
// address, and a wildcard is every address. Names are resolved because an
// operator writing the two listeners differently is exactly the case where a
// silent collision would cost the most; a name that does not resolve falls
// back to its spelling, which is all there is to go on.
func sameInterface(a, b string) bool {
	if isWildcard(a) || isWildcard(b) || a == b {
		return true
	}
	aIPs, errA := net.LookupIP(a)
	bIPs, errB := net.LookupIP(b)
	if errA != nil || errB != nil {
		return false
	}
	for _, x := range aIPs {
		for _, y := range bIPs {
			if x.Equal(y) {
				return true
			}
		}
	}
	return false
}

// isWildcard reports whether an address's host half binds every interface,
// which makes it both a collision with any other host on the same port and a
// thing a client has to be given a real address for.
func isWildcard(host string) bool {
	return host == "" || host == "0.0.0.0" || host == "::"
}

// isLoopback reports whether an address's host half is reachable only from
// this machine — which a docker workstation, coming in over the bridge, is
// not. An unresolvable name is not called loopback: the warning it would
// raise is worse than the one it would miss.
func isLoopback(host string) bool {
	if isWildcard(host) {
		return false
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return false
		}
	}
	return true
}

// pruneEvents enforces the events-retention baseline (docs/QUALITY.md):
// hourly, delete raw events older than the retention window.
func pruneEvents(ctx context.Context, db store.Store, days int, log *slog.Logger) {
	if days <= 0 {
		return
	}
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		cutoff := time.Now().AddDate(0, 0, -days).UnixMilli()
		if n, err := db.Events().DeleteBefore(ctx, cutoff); err != nil {
			log.Warn("events prune", "err", err)
		} else if n > 0 {
			log.Info("events pruned", "rows", n, "older_than_days", days)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// catalogOf resolves who a loop can address, fresh for each prompt build:
// its peers, the people allowed to talk to the fleet, and its own owner
// with whether a private chat to them exists yet (#45).
func catalogOf(db store.Store, self *store.Loop) loop.Catalog {
	ctx := context.Background()
	// The caller's copy is the actor's, taken when it last loaded the loop;
	// ownership changes through the API, so read it back before describing
	// who this loop can reach.
	if fresh, err := db.Loops().Get(ctx, self.ID); err == nil {
		self = fresh
	}
	cat := loop.Catalog{BotUsername: self.TGBotUsername}
	loops, err := db.Loops().List(ctx)
	if err != nil {
		return cat
	}
	for _, l := range loops {
		if l.ID != self.ID && l.Status == store.StatusActive {
			cat.Peers = append(cat.Peers, loop.Peer{
				Name: l.Name, Mission: l.Mission, BotUsername: l.TGBotUsername,
			})
		}
	}
	senders, err := db.TGSenders().List(ctx)
	if err != nil {
		return cat
	}
	// oldest first, so the catalog reads in the order people joined and
	// does not reshuffle between wakes
	for i := len(senders) - 1; i >= 0; i-- {
		s := senders[i]
		if s.Status != store.SenderAllowed {
			continue
		}
		person := loop.Person{Username: s.Username, Display: s.Display, TGUserID: s.TGUserID}
		cat.People = append(cat.People, person)
		if s.TGUserID == self.OwnerTGUserID {
			owner := person
			cat.Owner = &owner
			cat.OwnerDMReady = self.OwnerDMChatID != 0
		}
	}
	return cat
}

// rulesOf reads the fleet rules fresh for each prompt build, so an edit in
// the control room reaches every loop on its next wake (ADR-0024).
func rulesOf(db store.Store) []*store.FleetRule {
	rules, err := db.FleetRules().List(context.Background())
	if err != nil {
		return nil
	}
	return rules
}

// selectDefaultRuntime resolves --runtime per ADR-0017: docker is the
// default whenever the daemon is reachable, bare the opt-in uncontained
// alternative. Preflight failure is fatal only for the chosen default — the
// other runtime stays wired, and a loop on a broken one surfaces
// workstation_down at wake instead of blocking boot.
//
// `auto` means docker or nothing. It used to mean docker-if-you-have-it and
// otherwise host subprocesses, which is the one outcome a person who wrote
// `auto` cannot have asked for: claude with permissions bypassed on their own
// machine, announced by a log line among a hundred others (#240). Running
// uncontained is a legitimate choice and stays available — it is just not
// something Spool decides on the operator's behalf because a daemon happened
// to be down.
func selectDefaultRuntime(log *slog.Logger, choice string, runtimes map[string]runtime.Runtime) (kind, claudeVersion string) {
	ctx := context.Background()
	if choice == "auto" {
		version, err := runtimes[store.RuntimeDocker].Preflight(ctx)
		if err == nil {
			return store.RuntimeDocker, version
		}
		log.Error("--runtime auto needs a reachable docker daemon: install or start docker, "+
			"or ask for host subprocesses deliberately with --runtime bare, which runs loops "+
			"uncontained under your own account (ADR-0017)", "err", err)
		os.Exit(1)
	}
	selected, ok := runtimes[choice]
	if !ok {
		log.Error("--runtime must be auto, docker, or bare", "got", choice)
		os.Exit(1)
	}
	version, err := selected.Preflight(ctx)
	if err != nil {
		log.Error("runtime preflight failed", "runtime", choice, "err", err)
		os.Exit(1)
	}
	return choice, version
}

// logEgressPosture says out loud, once at boot, whether the wall around
// workstation egress is up (ADR-0028). Both warnings describe a fleet that
// runs — with open egress, or with contained loops that cannot wake until the
// image exists — so neither is fatal.
func logEgressPosture(log *slog.Logger, rt *docker.Runtime) {
	image, ready := rt.EgressWall(context.Background())
	switch {
	case image == "":
		log.Warn("workstation egress is open: no egress proxy configured, so a loop can reach any host (ADR-0028)")
	case !ready:
		log.Warn("egress proxy image missing — contained loops cannot wake until it exists; run `make image`", "image", image)
	default:
		log.Info("workstation egress allowlisted", "proxy_image", image)
	}
}

// splitList reads a comma-separated flag, dropping empties so a trailing
// comma is not an entry.
func splitList(value string) []string {
	var items []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// healthCacheTTL sizes the docker runtime's batched-sweep cache to the poll
// cadence: half the interval, clamped to [1s, 10s].
func healthCacheTTL(interval time.Duration) time.Duration {
	ttl := interval / 2
	if ttl < time.Second {
		ttl = time.Second
	}
	if ttl > 10*time.Second {
		ttl = 10 * time.Second
	}
	return ttl
}

// usageError refuses an invocation Spool cannot honour, on stderr and with
// exit 2: the conventional code for "your command line is wrong", said
// before anything starts. It names the only subcommand there is and where
// the flag list lives, because someone who typed a subcommand is exactly who
// does not know either.
func usageError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "spool: "+format+"\n\n", args...)
	fmt.Fprint(os.Stderr, "usage: spool [flags]\n"+
		"  or:  spool token [--data-dir dir]\n\n"+
		"`spool --help` lists the flags.\n")
	os.Exit(2)
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".spool"
	}
	return filepath.Join(home, ".spool")
}

func containsVersion(full, version string) bool {
	return len(full) >= len(version) && full[:len(version)] == version
}
