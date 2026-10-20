package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"stepline/messaging-service/internal/app"
	"stepline/messaging-service/internal/secure"
	"stepline/messaging-service/internal/store"
	"stepline/messaging-service/migrations"
	"syscall"
	"time"
)

func main() {
	if e := run(); e != nil {
		slog.Error("service stopped", "error", e.Error())
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) > 1 && os.Args[1] == "health" {
		client := &http.Client{Timeout: 3 * time.Second}
		r, e := client.Get("http://127.0.0.1:8080/healthz")
		if e != nil {
			return errors.New("health check failed")
		}
		defer r.Body.Close()
		if r.StatusCode != 200 {
			return errors.New("health check failed")
		}
		return nil
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cfg, e := app.ConfigFromEnv()
	if e != nil {
		return e
	}
	vault, e := secure.NewVault(cfg.Key)
	if e != nil {
		return e
	}
	db, e := pgxpool.New(ctx, cfg.DSN)
	if e != nil {
		return errors.New("invalid database configuration")
	}
	defer db.Close()
	if e = db.Ping(ctx); e != nil {
		return errors.New("database unavailable")
	}
	if e = migrations.Run(ctx, db); e != nil {
		return fmt.Errorf("migration failed: %w", e)
	}
	s := &store.Store{DB: db, Vault: vault, MediaDir: cfg.MediaDir}
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "migrate":
			fmt.Println("Migrations applied")
			return nil
		case "bootstrap":
			if os.Getenv("BOOTSTRAP_EMAIL") == "" || os.Getenv("BOOTSTRAP_PASSWORD") == "" {
				return errors.New("BOOTSTRAP_EMAIL and BOOTSTRAP_PASSWORD are required")
			}
			if e = app.Bootstrap(ctx, s, os.Getenv("BOOTSTRAP_EMAIL"), os.Getenv("BOOTSTRAP_NAME"), os.Getenv("BOOTSTRAP_PASSWORD")); e != nil {
				return e
			}
			fmt.Println("Admin created")
			return nil
		default:
			return errors.New("usage: service [migrate|bootstrap]")
		}
	}
	// v1 runs one session/worker owner. Fail a second replica explicitly instead
	// of letting two processes drive the same personal-account sessions.
	lock, e := db.Acquire(ctx)
	if e != nil {
		return e
	}
	defer lock.Release()
	var owner bool
	if e = lock.QueryRow(ctx, `SELECT pg_try_advisory_lock(872953)`).Scan(&owner); e != nil {
		return e
	}
	if !owner {
		return errors.New("another service instance owns provider sessions; v1 supports one replica")
	}
	a := app.New(ctx, s, cfg)
	if e = a.Manager.Start(); e != nil {
		return e
	}
	defer a.Manager.Close()
	go a.Worker(ctx)
	server := &http.Server{Addr: cfg.Addr, Handler: a.Router(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20, BaseContext: func(net.Listener) context.Context { return ctx }}
	err := make(chan error, 1)
	go func() { slog.Info("API listening", "address", cfg.Addr); err <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdown, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		return server.Shutdown(shutdown)
	case e := <-err:
		return e
	}
}
