package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/PV80/GoldenCloud/server/internal/auth"
	"github.com/PV80/GoldenCloud/server/internal/config"
	"github.com/PV80/GoldenCloud/server/internal/webdavx"
)

const (
	// readHeaderTimeout bounds how long a client may take to send its request
	// headers. Bodies are deliberately not bounded: a 20 GB upload over a slow
	// office link is a legitimate request.
	readHeaderTimeout = 60 * time.Second
	idleTimeout       = 2 * time.Minute
	shutdownGrace     = 30 * time.Second
	maxHeaderBytes    = 1 << 20
)

func cmdServe(args []string, s streams) int {
	fset := newFlagSet("serve", s)
	configPath := fset.String("config", DefaultConfigPath, "configuration file")
	if _, err := parseWithPositionals(fset, args); err != nil {
		return 2
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fail(s, "%v", err)
	}
	users, err := config.LoadUsers(cfg.UsersFile)
	if err != nil {
		return fail(s, "%v", err)
	}
	log := newLogger(s, cfg.LogLevel)

	if err := preflight(cfg, users); err != nil {
		return fail(s, "preflight check failed: %v", err)
	}

	store := auth.NewStore(users)
	authn := auth.New(store, auth.Options{
		TrustedProxy: cfg.TrustedProxy,
		Logger:       log,
	})
	dav := webdavx.New(webdavx.Options{StorageRoot: cfg.StorageRoot, Logger: log})
	defer dav.Close()

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fail(s, "cannot listen on %s: %v", cfg.Listen, err)
	}
	addr := ln.Addr().String()

	srv := &http.Server{
		Handler:           dav.Handler(authn),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelDebug),
	}

	// Announce the bound address on stdout. With listen: 127.0.0.1:0 this is
	// the only way to learn the port, which the integration suite relies on.
	fmt.Fprintf(s.out, "listening on %s\n", addr)
	if f, ok := s.out.(interface{ Sync() error }); ok {
		_ = f.Sync()
	}
	log.Info("goldencloud started",
		slog.String("version", version),
		slog.String("addr", addr),
		slog.String("storage_root", cfg.StorageRoot),
		slog.Int("users", len(users)),
		slog.Bool("tls", cfg.TLS.Enabled),
		slog.Bool("trusted_proxy", cfg.TrustedProxy))

	serveErr := make(chan error, 1)
	go func() {
		if cfg.TLS.Enabled {
			serveErr <- srv.ServeTLS(ln, cfg.TLS.CertFile, cfg.TLS.KeyFile)
			return
		}
		serveErr <- srv.Serve(ln)
	}()

	stop := make(chan os.Signal, 2)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stop)
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)

	for {
		select {
		case err := <-serveErr:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("server stopped", slog.Any("err", err))
				return fail(s, "server stopped: %v", err)
			}
			return 0

		case sig := <-hup:
			log.Info("reloading users", slog.String("signal", sig.String()))
			if err := reload(cfg, store, dav, log); err != nil {
				// Keep serving with the previous user list: a typo in
				// users.yaml must not take the office offline.
				log.Error("reload failed, keeping the previous user list", slog.Any("err", err))
			}

		case sig := <-stop:
			log.Info("shutting down", slog.String("signal", sig.String()))
			ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
			err := srv.Shutdown(ctx)
			cancel()
			if err != nil {
				log.Warn("graceful shutdown timed out, closing connections", slog.Any("err", err))
				_ = srv.Close()
			}
			<-serveErr
			log.Info("stopped")
			return 0
		}
	}
}

// reload re-reads users.yaml and swaps the user set in place. Existing
// connections keep working; a user removed from the file loses access on their
// next request.
func reload(cfg *config.Config, store *auth.Store, dav *webdavx.Server, log *slog.Logger) error {
	users, err := config.LoadUsers(cfg.UsersFile)
	if err != nil {
		return err
	}
	if err := preflight(cfg, users); err != nil {
		return err
	}
	store.Replace(users)
	keep := make(map[string]bool, len(users))
	for _, u := range users {
		keep[u.Username] = true
	}
	dav.Retain(keep)
	log.Info("users reloaded", slog.Int("users", len(users)))
	return nil
}

// preflight verifies the storage layout before the server accepts a single
// request. It implements D-008: if the share the operator meant to serve is not
// mounted, fail loudly here rather than write everyone's files to the SD card.
func preflight(cfg *config.Config, users []config.User) error {
	st, err := os.Stat(cfg.StorageRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("storage_root %s does not exist; create it, or check that the share is mounted", cfg.StorageRoot)
		}
		return fmt.Errorf("storage_root %s: %w", cfg.StorageRoot, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("storage_root %s is not a directory", cfg.StorageRoot)
	}

	if cfg.RequireMountpoint {
		mounted, err := isMountpoint(cfg.StorageRoot)
		if err != nil {
			return fmt.Errorf("checking whether %s is a mountpoint: %w", cfg.StorageRoot, err)
		}
		if !mounted {
			return fmt.Errorf(
				"storage_root %s is not a mountpoint, but require_mountpoint is set. "+
					"The share is probably not mounted: writing here would put everyone's "+
					"files on the local disk instead. Mount it (see deploy/RUNBOOK.md), or "+
					"set require_mountpoint: false if the storage really is a local directory",
				cfg.StorageRoot)
		}
	}

	for _, u := range users {
		dir := u.Dir(cfg.StorageRoot)
		st, err := os.Stat(dir)
		switch {
		case err == nil && st.IsDir():
			continue
		case err == nil:
			return fmt.Errorf("user %q: %s exists but is not a directory", u.Username, dir)
		case !errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("user %q: %s: %w", u.Username, dir, err)
		}
		// The storage-root checks above have already established that we are
		// writing in the right place, so creating a missing user folder here is
		// safe and beats refusing to serve everyone else.
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("user %q: creating %s: %w", u.Username, dir, err)
		}
	}
	return nil
}

func newLogger(s streams, level string) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(s.err, &slog.HandlerOptions{Level: lv}))
}
