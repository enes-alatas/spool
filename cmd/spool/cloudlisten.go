package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/httpapi"
	"github.com/enes-alatas/spool/internal/store"
)

// cloudListenerSwitch is the cloud listener as Settings → Cloud opens and
// closes it (ADR-0056). One --cloud-listen opened is served with the
// hub's other listeners, and failing takes the hub down as theirs does;
// the switch only reports it. One the page opened is the switch's own: it
// is opened again at the next start, and a failure to serve it is logged,
// since the rest of the hub doesn't need it.
type cloudListenerSwitch struct {
	settings store.SettingsStore
	log      *slog.Logger
	// handler is what the listener serves, set once the API is built and
	// before anything can call Open.
	handler http.Handler

	mu       sync.Mutex
	addr     string
	fromFlag bool
	srv      *http.Server
}

var _ httpapi.CloudListener = (*cloudListenerSwitch)(nil)

func (cloudSwitch *cloudListenerSwitch) Listening() (string, bool) {
	cloudSwitch.mu.Lock()
	defer cloudSwitch.mu.Unlock()
	return cloudSwitch.addr, cloudSwitch.fromFlag
}

// flagOpened records the listener --cloud-listen opened, on addr.
func (cloudSwitch *cloudListenerSwitch) flagOpened(addr string) {
	cloudSwitch.mu.Lock()
	defer cloudSwitch.mu.Unlock()
	cloudSwitch.addr, cloudSwitch.fromFlag = addr, true
}

func (cloudSwitch *cloudListenerSwitch) Open(listen string) (string, error) {
	cloudSwitch.mu.Lock()
	defer cloudSwitch.mu.Unlock()
	if cloudSwitch.addr != "" {
		if want, err := cloudListenAddr(listen); err != nil || want != cloudSwitch.addr {
			return "", fmt.Errorf("the cloud listener is already open on %s; switch it off first to open it elsewhere", cloudSwitch.addr)
		}
		return cloudSwitch.addr, nil
	}
	bound, err := cloudSwitch.serve(listen)
	if err != nil {
		return "", err
	}
	if err := cloudSwitch.settings.Set(context.Background(), store.SettingCloudListen, listen); err != nil {
		cloudSwitch.shutdown()
		return "", err
	}
	return bound, nil
}

// reopen opens the listener where the page last left it open, at the
// hub's start. A failure leaves it off, logged, and the page says so.
func (cloudSwitch *cloudListenerSwitch) reopen(ctx context.Context) {
	listen, err := cloudSwitch.settings.Get(ctx, store.SettingCloudListen)
	if errors.Is(err, store.ErrNotFound) || (err == nil && listen == "") {
		return
	}
	if err != nil {
		cloudSwitch.log.Warn("cloud listener not reopened", "err", err)
		return
	}
	cloudSwitch.mu.Lock()
	defer cloudSwitch.mu.Unlock()
	if cloudSwitch.addr != "" {
		return // --cloud-listen opened one
	}
	if _, err := cloudSwitch.serve(listen); err != nil {
		cloudSwitch.log.Warn("cloud listener not reopened", "listen", listen, "err", err)
	}
}

// serve binds listen and serves the listener there. The caller holds mu.
func (cloudSwitch *cloudListenerSwitch) serve(listen string) (string, error) {
	addr, err := cloudListenAddr(listen)
	if err != nil {
		return "", err
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("the cloud listener on %s: %w", addr, err)
	}
	srv := httpapi.CloudHTTPServer(cloudSwitch.handler)
	bound := boundAddr(addr, listener)
	go func() {
		if err := srv.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
			cloudSwitch.log.Error("cloud listener stopped", "addr", bound, "err", err)
			// it reads as off, not as on and unreachable; the setting
			// stays, so the next start tries it again
			cloudSwitch.mu.Lock()
			if cloudSwitch.srv == srv {
				cloudSwitch.addr, cloudSwitch.srv = "", nil
			}
			cloudSwitch.mu.Unlock()
		}
	}()
	cloudSwitch.addr, cloudSwitch.srv = bound, srv
	cloudSwitch.log.Info("cloud listener open", "cloud_addr", bound)
	return bound, nil
}

func (cloudSwitch *cloudListenerSwitch) Close() error {
	cloudSwitch.mu.Lock()
	defer cloudSwitch.mu.Unlock()
	if cloudSwitch.fromFlag {
		return errors.New("the cloud listener was opened with --cloud-listen")
	}
	if err := cloudSwitch.settings.Set(context.Background(), store.SettingCloudListen, ""); err != nil {
		return err
	}
	cloudSwitch.shutdown()
	return nil
}

// shutdown stops the listener the page opened, if one is open. The caller
// holds mu.
func (cloudSwitch *cloudListenerSwitch) shutdown() {
	if cloudSwitch.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = cloudSwitch.srv.Shutdown(ctx)
	cloudSwitch.log.Info("cloud listener closed", "cloud_addr", cloudSwitch.addr)
	cloudSwitch.addr, cloudSwitch.srv = "", nil
}

// stop closes the listener the page opened as the hub shuts down, leaving
// it set to open again at the next start.
func (cloudSwitch *cloudListenerSwitch) stop() {
	cloudSwitch.mu.Lock()
	defer cloudSwitch.mu.Unlock()
	cloudSwitch.shutdown()
}
