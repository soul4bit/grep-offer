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
	"strconv"
	"strings"
	"syscall"
	"time"

	"grep-offer/internal/app"
	"grep-offer/internal/content"
	"grep-offer/internal/notify"
	"grep-offer/internal/store"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	addr := envOrDefault("ADDR", ":8080")
	contentDir := envOrDefault("CONTENT_DIR", filepath.Join("content", "articles"))
	uploadsDir := envOrDefault("UPLOADS_DIR", filepath.Join("shared", "uploads"))

	driverName := "pgx"
	dsn := databaseURL()
	if err := os.MkdirAll(contentDir, 0o775); err != nil {
		log.Fatalf("create content dir: %v", err)
	}
	if err := os.MkdirAll(uploadsDir, 0o755); err != nil {
		log.Fatalf("create uploads dir: %v", err)
	}

	db, err := sql.Open(driverName, dsn)
	if err != nil {
		log.Fatalf("open %s: %v", driverName, err)
	}
	defer db.Close()

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxIdleTime(3 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("ping %s: %v", driverName, err)
	}

	st := store.New(db, driverName)
	if err := st.Init(ctx); err != nil {
		log.Fatalf("init store: %v", err)
	}

	if err := st.DeleteExpiredSessions(ctx); err != nil {
		log.Printf("cleanup sessions: %v", err)
	}
	if err := st.DeleteExpiredPasswordResetTokens(ctx); err != nil {
		log.Printf("cleanup password reset tokens: %v", err)
	}

	appConfig := app.Config{
		Articles:             content.NewLibrary(contentDir),
		UploadsDir:           uploadsDir,
		BootstrapAdminEmails: parseCSVEnv("ADMIN_EMAILS"),
	}
	mailer, err := buildSMTPMailer()
	if err != nil {
		log.Fatalf("build smtp mailer: %v", err)
	}

	if registration, webhookSecret, err := buildRegistrationConfig(st, mailer); err != nil {
		log.Fatalf("build registration config: %v", err)
	} else {
		appConfig.Registration = registration
		appConfig.TelegramWebhookSecret = webhookSecret
	}

	appConfig.PasswordReset = buildPasswordResetConfig(st, mailer)

	application, err := app.New(st, appConfig)
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

func databaseURL() string {
	if databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL")); databaseURL != "" {
		return databaseURL
	}

	log.Fatal("DATABASE_URL is required")
	return ""
}

func parseCSVEnv(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}

	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n'
	})

	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		values = append(values, part)
	}

	return values
}

func buildSMTPMailer() (*notify.SMTPMailer, error) {
	smtpHost := os.Getenv("SMTP_HOST")
	smtpUsername := os.Getenv("SMTP_USERNAME")
	smtpPassword := os.Getenv("SMTP_PASSWORD")
	if smtpHost == "" && smtpUsername == "" && smtpPassword == "" {
		log.Printf("smtp mailer disabled: SMTP env vars are not configured")
		return nil, nil
	}

	for _, key := range []string{"SMTP_HOST", "SMTP_USERNAME", "SMTP_PASSWORD"} {
		if os.Getenv(key) == "" {
			return nil, errors.New(key + " is required when SMTP mailer is enabled")
		}
	}

	smtpPort, err := strconv.Atoi(envOrDefault("SMTP_PORT", "465"))
	if err != nil {
		return nil, err
	}

	smtpSecure, err := strconv.ParseBool(envOrDefault("SMTP_SECURE", "true"))
	if err != nil {
		return nil, err
	}

	return notify.NewSMTPMailer(
		smtpHost,
		smtpPort,
		smtpSecure,
		smtpUsername,
		smtpPassword,
		envOrDefault("MAIL_FROM", smtpUsername),
	), nil
}

func buildRegistrationConfig(st *store.Store, mailer *notify.SMTPMailer) (*app.RegistrationCoordinator, string, error) {
	telegramToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	telegramAdminChatID := os.Getenv("TELEGRAM_ADMIN_CHAT_ID")
	telegramWebhookSecret := os.Getenv("TELEGRAM_WEBHOOK_SECRET")

	registrationConfigEnabled := telegramToken != "" || telegramAdminChatID != "" || telegramWebhookSecret != ""
	if !registrationConfigEnabled {
		log.Printf("registration approval workflow disabled: Telegram env vars are not configured")
		return nil, "", nil
	}

	for _, key := range []string{"TELEGRAM_BOT_TOKEN", "TELEGRAM_ADMIN_CHAT_ID", "TELEGRAM_WEBHOOK_SECRET"} {
		if os.Getenv(key) == "" {
			return nil, "", errors.New(key + " is required when registration approval workflow is enabled")
		}
	}

	if mailer == nil {
		return nil, "", errors.New("SMTP mailer is required when registration approval workflow is enabled")
	}

	telegramChatID, err := strconv.ParseInt(telegramAdminChatID, 10, 64)
	if err != nil {
		return nil, "", err
	}

	bot := notify.NewTelegramBot(telegramToken, telegramChatID)

	return app.NewRegistrationCoordinator(app.RegistrationCoordinatorConfig{
		Store:    st,
		Mailer:   mailer,
		Notifier: bot,
		BaseURL:  os.Getenv("APP_BASE_URL"),
	}), telegramWebhookSecret, nil
}

func buildPasswordResetConfig(st *store.Store, mailer *notify.SMTPMailer) *app.PasswordResetCoordinator {
	if mailer == nil {
		log.Printf("password reset disabled: SMTP env vars are not configured")
		return nil
	}

	return app.NewPasswordResetCoordinator(app.PasswordResetCoordinatorConfig{
		Store:   st,
		Mailer:  mailer,
		BaseURL: os.Getenv("APP_BASE_URL"),
	})
}
