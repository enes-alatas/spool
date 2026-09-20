// Command spool runs the Spool orchestrator: a fleet of long-running
// Claude Code loops with a web control room and Telegram bridge.
package main

import (
	"context"
	"errors"
	"flag"
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
	"github.com/enes-alatas/spool/internal/redact"
	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/runtime"
	"github.com/enes-alatas/spool/internal/runtime/bare"
	"github.com/enes-alatas/spool/internal/runtime/docker"
	"github.com/enes-alatas/spool/internal/sched"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
	"github.com/enes-alatas/spool/internal/surface/telegram"
	"github.com/enes-alatas/spool/web"
)

// redactTTL bounds how long a secret written while the process runs can go
// unredacted. The API refreshes the redactor the moment it writes one, so
// this is only the backstop for a write that forgets to — short enough that
// the window is a blink, long enough that the log's hot path reloads rarely.
const redactTTL = 5 * time.Second

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "address to serve the API/UI on")
	dataDir := flag.String("data-dir", defaultDataDir(), "directory for spool.db, loop homes and worktrees")
	claudeBin := flag.String("claude-bin", "claude", "path to the claude binary (bare runtime)")
	runtimeChoice := flag.String("runtime", "auto", "default runtime for new loops: auto (docker when the daemon is reachable), docker, or bare")
	workstationImage := flag.String("workstation-image", "spool-workstation", "default image for docker workstations")
	egressImage := flag.String("egress-image", "spool-egress", "image for the workstation egress proxy; empty leaves workstation egress open (ADR-0028)")
	egressAllow := flag.String("egress-allow", "", "comma-separated hosts (each \"host\" or \"host:port\") workstations may reach on top of the built-in allowlist")
	healthSec := flag.Int("workstation-health-sec", 45, "seconds between workstation liveness polls")
	partials := flag.Bool("partial-messages", true, "stream token deltas to the UI (--include-partial-messages)")
	telegramAPI := flag.String("telegram-api-base", telegram.APIBase, "Telegram Bot API base URL (tests point this at a stand-in server)")
	bindSettleSec := flag.Int("telegram-bind-settle-sec", 0, "seconds a newly bound bot waits before it may ingest a group (0 = the production margin; tests against a stand-in API shorten it)")
	retentionDays := flag.Int("events-retention-days", 30, "prune raw claude events older than this many days (0 disables; messages and turns are never pruned)")
	flag.Parse()

	logTo := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	log := slog.New(logTo)
	slog.SetDefault(log)

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

	_, hubPort, _ := net.SplitHostPort(*listen)
	dockerRuntime := docker.New(docker.Options{
		DefaultImage: *workstationImage,
		EgressImage:  *egressImage,
		HubPort:      hubPort,
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
	log.Info("runtime ready", "default", defaultRuntime, "claude_version", ver)
	logEgressPosture(log, dockerRuntime)

	if err := datadir.Secure(*dataDir, log); err != nil {
		log.Error("data dir", "err", err)
		os.Exit(1)
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
				System:         loop.SystemPrompt(l, cat, rules),
				StandingChange: loop.StandingInstructionsPreamble(l, cat, rules),
			}
		},
		MCPEndpoint: func(l *store.Loop) string {
			host, port, err := net.SplitHostPort(*listen)
			if err != nil {
				return ""
			}
			switch {
			case l.Runtime == store.RuntimeDocker:
				// containers reach the host through the gateway alias the
				// workstation is created with; the hub must listen on an
				// address the docker bridge can reach
				host = "host.docker.internal"
			case host == "" || host == "0.0.0.0" || host == "::":
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
		DefaultRuntime: defaultRuntime,
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
		Log:   log,
		WebFS: web.Dist(),
	}

	srv := &http.Server{Addr: *listen, Handler: redact.HTTP(api.Handler(), redactor)}
	go func() {
		<-ctx.Done()
		log.Info("shutting down")
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Info("spool listening", "addr", *listen, "data", *dataDir, "ui", api.WebFS != nil)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("serve", "err", err)
	}
	manager.Shutdown()
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
// default whenever the daemon is reachable, bare the explicit uncontained
// fallback. Preflight failure is fatal only for the chosen default — the
// other runtime stays wired, and a loop on a broken one surfaces
// workstation_down at wake instead of blocking boot.
func selectDefaultRuntime(log *slog.Logger, choice string, runtimes map[string]runtime.Runtime) (kind, claudeVersion string) {
	ctx := context.Background()
	if choice == "auto" {
		version, err := runtimes[store.RuntimeDocker].Preflight(ctx)
		if err == nil {
			return store.RuntimeDocker, version
		}
		log.Info("docker unreachable; new loops default to the uncontained bare runtime", "err", err)
		choice = store.RuntimeBare
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
