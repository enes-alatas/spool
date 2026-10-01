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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/enes-alatas/spool/internal/attach"
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
	"github.com/enes-alatas/spool/internal/surface"
	"github.com/enes-alatas/spool/internal/surface/outbound"
	"github.com/enes-alatas/spool/internal/surface/slack"
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
	mcpListen := flag.String("mcp-listen", "", "address to serve the loop-facing /mcp endpoint on; the one port of this machine a workstation may reach (#238). Unset: the docker bridge's gateway when it is an address of this machine, else loopback, on --mcp-port (#475)")
	mcpPortFlag := flag.String("mcp-port", "8081", "port of the loop listener when --mcp-listen is unset")
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
	slackAPI := flag.String("slack-api-base", slack.APIBase, "Slack Web API base URL (tests point this at a stand-in server)")
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

	if *mcpListen != "" && flagPassed("mcp-port") {
		log.Error("--mcp-port", "err", "it names the port when --mcp-listen is unset; --mcp-listen already names one")
		os.Exit(1)
	}
	// Both listeners are bound here, before anything reads the MCP port, and
	// served later on these very sockets. A port of 0 is then the kernel's
	// choice, made once, and the address the hub reports is the one it holds:
	// nothing is released and bound again, so nothing else on the machine can
	// take a port in between (#344). A port that is taken fails the start.
	apiListener, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Error("--listen", "err", err)
		os.Exit(1)
	}
	// Only a hub whose loops default to docker workstations looks for the
	// bridge: --runtime auto resolves to docker or stops the hub below.
	var bridgeGateway func(context.Context) (string, error)
	if *runtimeChoice != store.RuntimeBare {
		bridgeGateway = docker.New(docker.Options{}).BridgeGateway
	}
	mcpListener, mcpFlagAddr, err := bindLoopListener(context.Background(), log, *listen, *mcpListen, *mcpPortFlag, bridgeGateway)
	if err != nil {
		log.Error("--mcp-listen", "err", err)
		os.Exit(1)
	}
	apiAddr, mcpAddr := boundAddr(*listen, apiListener), boundAddr(mcpFlagAddr, mcpListener)
	_, mcpPort, _ := net.SplitHostPort(mcpAddr)

	dockerRuntime := docker.New(docker.Options{
		DefaultImage: *workstationImage,
		EgressImage:  *egressImage,
		MCPPort:      mcpPort,
		EgressAllow:  allowEntries,
		HealthTTL:    healthCacheTTL(healthInterval),
	})
	hostRuntime := bare.New(*claudeBin)
	hostRuntime.KeepOut(*dataDir)
	runtimes := map[string]runtime.Runtime{
		store.RuntimeBare:   hostRuntime,
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
	// A containerized loop reaches the hub at the docker bridge gateway, so a
	// loop listener bound to loopback or to any other one address is the one
	// destination it is allowed and cannot use, and the fleet comes up
	// looking healthy and never wakes. The hub knows both halves here and nowhere earlier — the default
	// runtime is only resolved above. Creating a docker loop is refused for
	// the same reason (#474).
	listenerReachable := func(ctx context.Context) error {
		return loopListenerReachable(ctx, dockerRuntime, mcpListener.Addr().(*net.TCPAddr).IP, mcpPort)
	}
	if defaultRuntime == store.RuntimeDocker {
		if err := listenerReachable(context.Background()); err != nil {
			log.Warn("docker workstations cannot reach the loop listener (#238)", "mcp_listen", mcpAddr, "err", err)
		}
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

	pubsub := bus.New()

	// wire the object graph; router and manager reference each other through
	// late-bound deps
	var router *route.Router
	var scheduler *sched.Scheduler

	deps := loop.Deps{
		Store:                     rdb,
		Bus:                       pubsub,
		Runtimes:                  runtimes,
		WorkstationHealthInterval: healthInterval,
		PartialMessages:           *partials,
		Logger:                    log,
		RenderPrompt: func(loopRecord *store.Loop) loop.Prompt {
			cat, rules := catalogOf(rdb, loopRecord), rulesOf(rdb)
			return loop.Prompt{
				System:         loop.SystemPrompt(loopRecord, cat, rules, build.Version),
				StandingChange: loop.StandingInstructionsPreamble(loopRecord, cat, rules, build.Version),
			}
		},
		MCPEndpoint: func(loopRecord *store.Loop) string {
			host, port, err := net.SplitHostPort(mcpAddr)
			if err != nil {
				return ""
			}
			switch {
			case loopRecord.Runtime == store.RuntimeDocker:
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
		OnTurnStart: func(loopRecord *store.Loop) {
			router.StartTurn(loopRecord.ID)
		},
		SendsThisTurn: func(loopID string) []string {
			return router.TurnSends(loopID)
		},
		OnTurnDone: func(loopRecord *store.Loop, trailer time.Duration, has bool) {
			scheduler.ScheduleAfterTurn(loopRecord, trailer, has)
		},
		ClaudeToken: func(ctx context.Context) (string, error) {
			token, err := rdb.Settings().Get(ctx, store.SettingClaudeOAuthToken)
			if errors.Is(err, store.ErrNotFound) {
				return "", nil
			}
			return token, err
		},
	}
	models := loop.NewModels(rdb, pubsub, runtimes[defaultRuntime], defaultRuntime, ver, log)
	deps.ObserveModel = models.Observe
	manager := loop.NewManager(deps)
	router = route.New(rdb, pubsub, manager, log)
	files, err := attach.Open(filepath.Join(*dataDir, "files"))
	if err != nil {
		log.Error("files directory", "err", err)
		os.Exit(1)
	}
	router.SetFiles(files, redactor.Text)
	router.SetWorkstations(manager)
	scheduler = sched.New(rdb, pubsub, manager, log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := manager.Boot(ctx); err != nil {
		log.Error("boot recovery", "err", err)
		os.Exit(1)
	}
	// close sessions left dangling by a previous crash (best effort)
	_ = rdb.Sessions().EndDangling(ctx, store.EndReasonCrash, time.Now().UnixMilli())

	go scheduler.Run(ctx)
	models.Start(ctx)
	go pruneEvents(ctx, rdb, *retentionDays, log)
	go expireAttachments(ctx, router, log)

	// Every pending send is the last process's: nothing in this one can
	// send yet, because the MCP and API listeners are bound only after the
	// surfaces start. Sweeping after them would fail a send this process
	// is about to make.
	hubLedger := &outbound.Ledger{Store: rdb, Bus: pubsub, Log: log, Surface: "hub"}
	hubLedger.FailInterruptedSends(ctx, "at startup")
	bridge := telegram.NewBridge(rdb, pubsub, router, log, *telegramAPI)
	bridge.SetBindSettle(time.Duration(*bindSettleSec) * time.Second)
	bridge.Start(ctx)
	slackSurface := slack.New(rdb, pubsub, router, log, *slackAPI)
	slackSurface.Start(ctx)

	api := &httpapi.Server{
		Store:   rdb,
		Bus:     pubsub,
		Manager: manager,
		Router:  router,
		Sched:   scheduler,
		Models:  models,
		Surfaces: map[string]surface.Surface{
			store.SurfaceTelegram: bridge,
			store.SurfaceSlack:    slackSurface,
		},
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
		LoopListenerReachable: listenerReachable,
		SecretsChanged: func(ctx context.Context) {
			if err := redactor.Refresh(ctx); err != nil {
				log.Error("reload secrets for redaction", "err", err)
			}
		},
		OperatorToken: operatorToken,
		ListenAddr:    apiAddr,
		TrustedHosts:  splitList(*trustedHosts),
		Log:           log,
		WebFS:         web.Dist(),
	}

	srv := &http.Server{Handler: redact.HTTP(api.Handler(), redactor)}
	mcpSrv := &http.Server{Handler: redact.HTTP(api.MCPHandler(), redactor)}

	// The bound addresses, not the flags: with a port of 0 this line is the
	// only place the ports are said, and the itest harness reads them here.
	log.Info("spool listening", "addr", apiAddr, "mcp_addr", mcpAddr, "data", *dataDir, "ui", api.WebFS != nil)
	serveErr := serve(ctx, log,
		served{name: "api", srv: srv, listener: apiListener, addr: apiAddr},
		// Failing the loop-facing listener takes the whole hub down: a
		// workstation with no hub to reach cannot take a turn, and a fleet
		// that looks healthy and never wakes is the worse failure.
		served{name: "mcp", srv: mcpSrv, listener: mcpListener, addr: mcpAddr})
	stop() // a listener that failed takes the rest of the hub down with it
	if serveErr != nil {
		log.Error("serve", "err", serveErr)
	}
	manager.Shutdown()
	if stopSurfaces(log, bridge, slackSurface) {
		// Every surface has settled what it held. A send still pending was
		// on its way to one when it stopped, and nothing is left to send
		// it: it fails now, with the store still open, rather than at the
		// next start (ADR-0036).
		hubLedger.FailInterruptedSends(context.Background(), "at shutdown")
	}
	if serveErr != nil {
		// A hub that stopped serving is a failed hub, whatever it did
		// before: a supervisor reads the exit status, not the log (#253).
		// os.Exit skips the deferred close.
		_ = db.Close()
		os.Exit(1)
	}
}

// surfaceStopTimeout bounds how long a stopping hub waits for its surfaces
// to settle their sends. A send still mid-request when it runs out is left
// pending, and the next start fails it.
const surfaceStopTimeout = 10 * time.Second

// stopSurfaces stops every surface, all at once, and reports whether each
// settled its sends before the timeout ran out. One that did not may still
// be sending, so nothing it held can be called unsent yet.
func stopSurfaces(log *slog.Logger, surfaces ...surface.Surface) bool {
	ctx, cancel := context.WithTimeout(context.Background(), surfaceStopTimeout)
	defer cancel()
	var stopping sync.WaitGroup
	for _, one := range surfaces {
		stopping.Go(func() { one.Stop(ctx) })
	}
	stopping.Wait()
	if ctx.Err() != nil {
		log.Warn("surfaces did not stop in time; their unsettled sends fail at the next start", "timeout", surfaceStopTimeout)
		return false
	}
	return true
}

// served is one of the hub's listeners and the server on it.
type served struct {
	name     string
	srv      *http.Server
	listener net.Listener
	addr     string
}

// serve serves every listener until ctx ends or one of them fails, then
// shuts them all down. It returns the failure, naming the listener and its
// address, or nil for a shutdown the operator asked for.
func serve(ctx context.Context, log *slog.Logger, listeners ...served) error {
	failed := make(chan error, len(listeners))
	for _, one := range listeners {
		go func() {
			// Serve returns only on a failure, until Shutdown makes it
			// return ErrServerClosed
			if err := one.srv.Serve(one.listener); !errors.Is(err, http.ErrServerClosed) {
				failed <- fmt.Errorf("%s listener on %s: %w", one.name, one.addr, err)
			}
		}()
	}
	var serveErr error
	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case serveErr = <-failed:
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, one := range listeners {
		_ = one.srv.Shutdown(shutCtx)
	}
	return serveErr
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

// bindLoopListener binds the loop listener where --mcp-listen says, and,
// when it is unset, on loopback for a hub of bare loops (a nil
// bridgeGateway), or where a docker workstation can reach it (#475): the
// docker bridge's gateway, an address the workstations come in at and the
// network around this machine is not routed to by default (a same-link host
// reaches it only by routing the bridge subnet here), when it is one of this
// machine's addresses. An engine in a VM, Docker Desktop's, has no such address here
// and forwards workstations to this machine's loopback, which is where the
// listener goes then, and whenever docker does not answer or the gateway
// will not bind. Every bare loop reaches either. It returns the listener and
// the address it was bound for, spelled as asked.
func bindLoopListener(ctx context.Context, log *slog.Logger, apiAddr, flagAddr, port string, bridgeGateway func(context.Context) (string, error)) (net.Listener, string, error) {
	if flagAddr != "" {
		listener, err := listenLoop(apiAddr, flagAddr)
		return listener, flagAddr, err
	}
	loopback := net.JoinHostPort("127.0.0.1", port)
	if bridgeGateway == nil {
		listener, err := listenLoop(apiAddr, loopback)
		return listener, loopback, err
	}
	gateway, err := bridgeGateway(ctx)
	switch {
	case err != nil:
		log.Info("loop listener on loopback: docker reports no bridge to bind", "err", err)
	case !isLocalAddress(gateway):
		log.Info("loop listener on loopback: the docker bridge is not on this machine, so its engine forwards workstations to loopback", "bridge_gateway", gateway)
	default:
		addr := net.JoinHostPort(gateway, port)
		listener, err := listenLoop(apiAddr, addr)
		if err == nil {
			log.Info("loop listener on the docker bridge: workstations reach it, other hosts only by routing the bridge subnet through this machine", "mcp_listen", addr)
			return listener, addr, nil
		}
		log.Warn("loop listener on loopback: the docker bridge would not bind, so docker workstations cannot reach the hub", "mcp_listen", addr, "err", err)
	}
	listener, err := listenLoop(apiAddr, loopback)
	return listener, loopback, err
}

// listenLoop binds the loop listener on addr. The two listeners are the
// boundary this fleet rests on: workstations are allowlisted to the MCP port
// alone, so the API's address must not be it (#238). One address serving
// both would hand every workstation the operator's API back.
func listenLoop(apiAddr, addr string) (net.Listener, error) {
	if _, _, err := splitMCPListen(apiAddr, addr); err != nil {
		return nil, err
	}
	return net.Listen("tcp", addr)
}

// flagPassed reports whether a flag was set on the command line, as opposed
// to left at its default.
func flagPassed(name string) bool {
	passed := false
	flag.Visit(func(f *flag.Flag) { passed = passed || f.Name == name })
	return passed
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
	// Port 0 is the kernel's pick, which is never a port already bound.
	if mcpPort == apiPort && mcpPort != "0" && sameInterface(apiHost, mcpHost) {
		return "", "", fmt.Errorf("%q would serve /mcp on the API's own port; workstations reach this port, and the API must not be on it", mcpAddr)
	}
	return mcpHost, mcpPort, nil
}

// boundAddr is the address a listener holds, spelled with the host its flag
// named: the port is the one actually bound, which differs from the flag's
// when that asked for 0, and the host stays as the operator wrote it, since
// the API's Host check compares requests against that spelling.
func boundAddr(flagAddr string, listener net.Listener) string {
	host, _, _ := net.SplitHostPort(flagAddr)
	return net.JoinHostPort(host, strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
}

// sameInterface reports whether two host halves on one port could be the same
// socket. Spelling is not the test: "localhost" and "127.0.0.1" are one
// address, and a wildcard is every address. Names are resolved because an
// operator writing the two listeners differently is exactly the case where a
// silent collision would cost the most; a name that does not resolve falls
// back to its spelling, which is all there is to go on.
func sameInterface(hostA, hostB string) bool {
	if isWildcard(hostA) || isWildcard(hostB) || hostA == hostB {
		return true
	}
	aIPs, errA := net.LookupIP(hostA)
	bIPs, errB := net.LookupIP(hostB)
	if errA != nil || errB != nil {
		return false
	}
	for _, ipA := range aIPs {
		for _, ipB := range bIPs {
			if ipA.Equal(ipB) {
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

// loopListenerReachable refuses a loop listener a docker workstation cannot
// reach (#474). On an engine that runs on this machine, a workstation comes
// in at the bridge gateway, an address of this machine, so the listener
// must be bound to that address or to every address: loopback, or any other
// one address, refuses the connection. An engine in a VM, Docker Desktop's,
// has no such gateway here and forwards host.docker.internal to this
// machine itself, so nothing is refused there, nor when the engine cannot
// say where its bridge is: a guess that refuses a working setup is worse
// than the miss.
func loopListenerReachable(ctx context.Context, dockerRuntime *docker.Runtime, bound net.IP, mcpPort string) error {
	if bound.IsUnspecified() {
		return nil
	}
	gateway, err := dockerRuntime.BridgeGateway(ctx)
	if err != nil || !isLocalAddress(gateway) || bound.Equal(net.ParseIP(gateway)) {
		return nil
	}
	return fmt.Errorf("docker workstations reach the loop listener over the docker bridge at %s, and --mcp-listen is bound to %s; start spool with --mcp-listen %s",
		gateway, bound, net.JoinHostPort(gateway, mcpPort))
}

// isLocalAddress reports whether an IP is one of this machine's interface
// addresses.
func isLocalAddress(addr string) bool {
	ip := net.ParseIP(addr)
	addrs, err := net.InterfaceAddrs()
	if ip == nil || err != nil {
		return false
	}
	for _, ifaceAddr := range addrs {
		if ipNet, ok := ifaceAddr.(*net.IPNet); ok && ipNet.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// expireAttachments removes kept files past their 30 days, hourly (#123).
// Unlike events it is not the operator's to tune: 30 days is the retention
// the operator decided on for files, and the rows outlive it anyway.
func expireAttachments(ctx context.Context, router *route.Router, log *slog.Logger) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		if expired, err := router.ExpireAttachments(ctx); err != nil {
			log.Warn("attachments expiry", "err", err)
		} else if expired > 0 {
			log.Info("attachments expired", "files", expired)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
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
		if pruned, err := db.Events().DeleteBefore(ctx, cutoff); err != nil {
			log.Warn("events prune", "err", err)
		} else if pruned > 0 {
			log.Info("events pruned", "rows", pruned, "older_than_days", days)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// catalogOf resolves who a loop can address, fresh for each prompt build:
// its conversations, its peers in the fleet channel, the people allowed to
// talk to the fleet, and its own owner with whether a private chat to them
// exists yet (#45, #288).
func catalogOf(db store.Store, self *store.Loop) loop.Catalog {
	ctx := context.Background()
	// The caller's copy is the actor's, taken when it last loaded the loop;
	// ownership changes through the API, so read it back before describing
	// who this loop can reach.
	if fresh, err := db.Loops().Get(ctx, self.ID); err == nil {
		self = fresh
	}
	cat := loop.Catalog{Conversations: loop.ConversationsOf(self), OwnerDMReady: self.OwnerDMReady()}
	cat.BotUsername, _ = botOf(self)
	loops, err := db.Loops().List(ctx)
	if err != nil {
		return cat
	}
	for _, loopRecord := range loops {
		// a loop outside the fleet channel is reached by nobody's mention,
		// so naming it would teach a mention that goes nowhere
		if loopRecord.ID != self.ID && loopRecord.Status == store.StatusActive && !loopRecord.OutsideFleetChannel {
			bot, onSurface := botOf(loopRecord)
			cat.Peers = append(cat.Peers, loop.Peer{
				Name: loopRecord.Name, Mission: loopRecord.Mission, BotUsername: bot, Surface: onSurface,
			})
		}
	}
	// oldest first, so the catalog reads in the order people joined and
	// does not reshuffle between wakes
	if senders, err := db.TGSenders().List(ctx); err == nil {
		for i := len(senders) - 1; i >= 0; i-- {
			sender := senders[i]
			if sender.Status != store.SenderAllowed {
				continue
			}
			person := loop.Person{Username: sender.Username, Display: sender.Display, TGUserID: sender.TGUserID}
			cat.People = append(cat.People, person)
			// every loop carries the hub's default Telegram owner, so a
			// Slack loop's owner is only ever read from its Slack senders
			if sender.TGUserID == self.OwnerTGUserID && self.Surface() != store.SurfaceSlack {
				owner := person
				cat.Owner = &owner
			}
		}
	}
	if senders, err := db.SlackSenders().List(ctx); err == nil {
		for i := len(senders) - 1; i >= 0; i-- {
			sender := senders[i]
			if sender.Status != store.SenderAllowed {
				continue
			}
			person := loop.Person{Username: sender.Username, Display: sender.Display, SlackUserID: sender.SlackUserID}
			cat.People = append(cat.People, person)
			if sender.SlackUserID == self.OwnerSlackUserID && self.Surface() == store.SurfaceSlack {
				owner := person
				cat.Owner = &owner
			}
		}
	}
	return cat
}

// botOf names the bot a loop posts as and the surface it posts on, or two
// empty strings for a loop with none.
func botOf(loopRecord *store.Loop) (bot, onSurface string) {
	switch loopRecord.Surface() {
	case store.SurfaceTelegram:
		return loopRecord.TGBotUsername, store.SurfaceTelegram
	case store.SurfaceSlack:
		return loopRecord.SlackBotName, store.SurfaceSlack
	}
	return "", ""
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
