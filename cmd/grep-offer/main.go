package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"grep-offer/internal/app"
	"grep-offer/internal/store"

	_ "modernc.org/sqlite"
)

func main() {
	addr := envOrDefault("ADDR", ":8080")
	dbPath := envOrDefault("DB_PATH", filepath.Join("data", "grep-offer.db"))

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(1)
	db.SetConnMaxIdleTime(3 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("ping sqlite: %v", err)
	}

	st := store.New(db)
	if err := st.Init(ctx); err != nil {
		log.Fatalf("init store: %v", err)
	}

	if err := st.DeleteExpiredSessions(ctx); err != nil {
		log.Printf("cleanup sessions: %v", err)
	}

	application, err := app.New(st)
	if err != nil {
		log.Fatalf("create app: %v", err)
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           application.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       time.Minute,
	}

	shutdownErrors := make(chan error, 1)

	go func() {
		log.Printf("grep-offer listening on %s", addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			shutdownErrors <- err
		}
		close(shutdownErrors)
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-shutdownErrors:
		if err != nil {
			log.Fatalf("serve: %v", err)
		}
	case sig := <-signals:
		log.Printf("received %s, shutting down", sig)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("shutdown: %v", err)
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
