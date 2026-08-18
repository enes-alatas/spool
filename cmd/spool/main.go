// Command spool runs the Spool orchestrator: a fleet of long-running
// Claude Code loops with a web control room and Telegram bridge.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/httpapi"
	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/runtime"
	"github.com/enes-alatas/spool/internal/runtime/bare"
	"github.com/enes-alatas/spool/internal/sched"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
	"github.com/enes-alatas/spool/internal/telegram"
	"github.com/enes-alatas/spool/web"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "address to serve the API/UI on")
	dataDir := flag.String("data-dir", defaultDataDir(), "directory for spool.db, loop homes and worktrees")
	claudeBin := flag.String("claude-bin", "claude", "path to the claude binary")
	partials := flag.Bool("partial-messages", true, "stream token deltas to the UI (--include-partial-messages)")
	retentionDays := flag.Int("events-retention-days", 30, "prune raw claude events older than this many days (0 disables; messages and turns are never pruned)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	// The SandboxRuntime seam (ADR-0004): bare is the only implementation
	// until docker lands, so wiring picks it unconditionally.
	rt := bare.New(*claudeBin)

	ver, err := rt.Preflight(context.Background())
	if err != nil {
		log.Error("claude preflight failed — install Claude Code or pass --claude-bin", "err", err)
		os.Exit(1)
	}
	if ver != "" && !containsVersion(ver, claude.TestedVersion) {
		log.Warn("claude version differs from the one Spool was verified against",
			"found", ver, "tested", claude.TestedVersion)
	}
	log.Info("claude ok", "version", ver, "runtime", rt.Kind())

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Error("data dir", "err", err)
		os.Exit(1)
	}
	db, err := sqlite.Open(filepath.Join(*dataDir, "spool.db"))
	if err != nil {
		log.Error("open db", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	b := bus.New()

	// wire the object graph; router and manager reference each other through
	// late-bound deps
	var router *route.Router
	var scheduler *sched.Scheduler

	deps := loop.Deps{
		Store:           db,
		Bus:             b,
		Runtimes:        map[string]runtime.Runtime{store.RuntimeBare: rt},
		PartialMessages: *partials,
		Logger:          log,
		SystemPrompt: func(l *store.Loop) string {
			peers := peersOf(db, l)
			return loop.SystemPrompt(l, peers)
		},
		OnReply: func(l *store.Loop, text string, dms []int64) {
			router.LoopReply(l, text, dms)
		},
		OnTurnDone: func(l *store.Loop, trailer time.Duration, has bool) {
			scheduler.ScheduleAfterTurn(l, trailer, has)
		},
	}
	manager := loop.NewManager(deps)
	router = route.New(db, b, manager, log)
	scheduler = sched.New(db, b, manager, log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := manager.Boot(ctx); err != nil {
		log.Error("boot recovery", "err", err)
		os.Exit(1)
	}
	// close sessions left dangling by a previous crash (best effort)
	_ = db.Sessions().EndDangling(ctx, store.EndReasonCrash, time.Now().UnixMilli())

	go scheduler.Run(ctx)
	go pruneEvents(ctx, db, *retentionDays, log)

	bridge := telegram.NewBridge(db, b, router, log)
	bridge.Start(ctx)

	api := &httpapi.Server{
		Store:     db,
		Bus:       b,
		Manager:   manager,
		Router:    router,
		Sched:     scheduler,
		Telegram:  bridge,
		DataDir:   *dataDir,
		ClaudeVer: ver,
		Log:       log,
		WebFS:     web.Dist(),
	}

	srv := &http.Server{Addr: *listen, Handler: api.Handler()}
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

func peersOf(db store.Store, self *store.Loop) []loop.Peer {
	loops, err := db.Loops().List(context.Background())
	if err != nil {
		return nil
	}
	var peers []loop.Peer
	for _, l := range loops {
		if l.ID != self.ID && l.Status == store.StatusActive {
			peers = append(peers, loop.Peer{Name: l.Name, Mission: l.Mission})
		}
	}
	return peers
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
