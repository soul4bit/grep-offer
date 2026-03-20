package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"grep-offer/internal/store"

	_ "modernc.org/sqlite"
)

func TestRegistrationApprovalFlow(t *testing.T) {
	t.Parallel()

	fakeMailer := &fakeConfirmationMailer{}
	fakeNotifier := &fakeApprovalNotifier{}
	testApp, st := newTestApp(t, testAppOptions{
		mailer:        fakeMailer,
		notifier:      fakeNotifier,
		webhookSecret: "test-secret",
	})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}

	client := server.Client()
	client.Jar = jar

	form := url.Values{
		"username":         {"bash_bandit"},
		"email":            {"smoke@example.com"},
		"password":         {"supersecret123"},
		"confirm_password": {"supersecret123"},
	}

	registerResponse, err := client.PostForm(server.URL+"/register", form)
	if err != nil {
		t.Fatalf("register request: %v", err)
	}
	defer registerResponse.Body.Close()

	if registerResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected register status: %d", registerResponse.StatusCode)
	}

	registerBody := readBody(t, registerResponse.Body)
	if !strings.Contains(registerBody, "Заявка отправлена") {
		t.Fatalf("register notice missing: %s", registerBody)
	}

	request, err := st.RegistrationRequestByEmail(context.Background(), "smoke@example.com")
	if err != nil {
		t.Fatalf("load registration request: %v", err)
	}

	if len(fakeNotifier.requests) != 1 {
		t.Fatalf("unexpected telegram requests: %#v", fakeNotifier.requests)
	}

	update := telegramUpdate{
		CallbackQuery: &telegramCallbackQuery{
			ID:   "callback-1",
			Data: "approve:" + strconv.FormatInt(request.ID, 10),
			Message: &telegramMessage{
				MessageID: 77,
				Chat:      telegramChat{ID: 8591726563},
			},
		},
	}

	updateBody, err := json.Marshal(update)
	if err != nil {
		t.Fatalf("marshal telegram update: %v", err)
	}

	webhookRequest, err := http.NewRequest(http.MethodPost, server.URL+"/telegram/webhook", bytes.NewReader(updateBody))
	if err != nil {
		t.Fatalf("create webhook request: %v", err)
	}
	webhookRequest.Header.Set("X-Telegram-Bot-Api-Secret-Token", "test-secret")

	webhookResponse, err := client.Do(webhookRequest)
	if err != nil {
		t.Fatalf("telegram webhook request: %v", err)
	}
	defer webhookResponse.Body.Close()

	if webhookResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected webhook status: %d", webhookResponse.StatusCode)
	}

	if len(fakeMailer.sent) != 1 {
		t.Fatalf("unexpected sent emails: %#v", fakeMailer.sent)
	}

	confirmResponse, err := client.Get(fakeMailer.sent[0].ConfirmURL)
	if err != nil {
		t.Fatalf("confirm request: %v", err)
	}
	defer confirmResponse.Body.Close()

	if confirmResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected confirm status: %d", confirmResponse.StatusCode)
	}

	confirmBody := readBody(t, confirmResponse.Body)
	if !strings.Contains(confirmBody, "Привет, bash_bandit") {
		t.Fatalf("dashboard greeting missing after confirm: %s", confirmBody)
	}
}

func TestLoginShowsPendingRegistrationMessage(t *testing.T) {
	t.Parallel()

	fakeMailer := &fakeConfirmationMailer{}
	fakeNotifier := &fakeApprovalNotifier{}
	testApp, _ := newTestApp(t, testAppOptions{
		mailer:        fakeMailer,
		notifier:      fakeNotifier,
		webhookSecret: "test-secret",
	})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	client := server.Client()

	registerForm := url.Values{
		"username":         {"bash_bandit"},
		"email":            {"pending@example.com"},
		"password":         {"supersecret123"},
		"confirm_password": {"supersecret123"},
	}

	registerResponse, err := client.PostForm(server.URL+"/register", registerForm)
	if err != nil {
		t.Fatalf("register request: %v", err)
	}
	defer registerResponse.Body.Close()

	loginForm := url.Values{
		"email":    {"pending@example.com"},
		"password": {"supersecret123"},
	}

	loginResponse, err := client.PostForm(server.URL+"/login", loginForm)
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	defer loginResponse.Body.Close()

	if loginResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unexpected login status: %d", loginResponse.StatusCode)
	}

	if body := readBody(t, loginResponse.Body); !strings.Contains(body, "ждет апрува в Telegram") {
		t.Fatalf("pending login message missing: %s", body)
	}
}

func TestRegisterRejectsMismatchedPasswords(t *testing.T) {
	t.Parallel()

	testApp, _ := newTestApp(t, testAppOptions{})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	form := url.Values{
		"username":         {"bash_bandit"},
		"email":            {"oops@example.com"},
		"password":         {"supersecret123"},
		"confirm_password": {"supersecret321"},
	}

	response, err := server.Client().PostForm(server.URL+"/register", form)
	if err != nil {
		t.Fatalf("register request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unexpected register status: %d", response.StatusCode)
	}

	if body := readBody(t, response.Body); !strings.Contains(body, "Пароли не совпали") {
		t.Fatalf("mismatch password error missing: %s", body)
	}
}

func TestHealthz(t *testing.T) {
	t.Parallel()

	testApp, _ := newTestApp(t, testAppOptions{})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("health request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected health status: %d", response.StatusCode)
	}

	if body := readBody(t, response.Body); body != "ok" {
		t.Fatalf("unexpected health body: %q", body)
	}
}

func TestDashboardCheckpointTogglePersists(t *testing.T) {
	t.Parallel()

	testApp, st := newTestApp(t, testAppOptions{})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	ctx := context.Background()
	user, err := st.CreateUser(ctx, "bash_bandit", "progress@example.com", "hash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	const sessionToken = "test-session-token"
	if err := st.CreateSession(ctx, user.ID, sessionToken, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}

	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	dashboardRequest, err := http.NewRequest(http.MethodGet, server.URL+"/dashboard", nil)
	if err != nil {
		t.Fatalf("build dashboard request: %v", err)
	}
	dashboardRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})

	dashboardResponse, err := client.Do(dashboardRequest)
	if err != nil {
		t.Fatalf("dashboard request: %v", err)
	}
	defer dashboardResponse.Body.Close()

	if dashboardResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected dashboard status: %d", dashboardResponse.StatusCode)
	}

	initialBody := readBody(t, dashboardResponse.Body)
	if !strings.Contains(initialBody, "0/12") {
		t.Fatalf("expected initial progress in dashboard body: %s", initialBody)
	}

	initialProgress, err := st.RoadmapProgress(ctx, user.ID)
	if err != nil {
		t.Fatalf("load initial roadmap progress: %v", err)
	}
	if len(initialProgress) != 12 {
		t.Fatalf("unexpected seeded checkpoint count: %d", len(initialProgress))
	}
	if initialProgress["foundation-linux"] {
		t.Fatalf("expected checkpoint to start pending")
	}

	form := url.Values{
		"checkpoint": {"foundation-linux"},
		"done":       {"1"},
	}

	toggleRequest, err := http.NewRequest(http.MethodPost, server.URL+"/dashboard/checkpoints", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build toggle request: %v", err)
	}
	toggleRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	toggleRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})

	toggleResponse, err := client.Do(toggleRequest)
	if err != nil {
		t.Fatalf("toggle request: %v", err)
	}
	defer toggleResponse.Body.Close()

	if toggleResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf("unexpected toggle status: %d", toggleResponse.StatusCode)
	}

	progress, err := st.RoadmapProgress(ctx, user.ID)
	if err != nil {
		t.Fatalf("load updated roadmap progress: %v", err)
	}
	if !progress["foundation-linux"] {
		t.Fatalf("expected checkpoint progress to persist")
	}

	reloadRequest, err := http.NewRequest(http.MethodGet, server.URL+"/dashboard", nil)
	if err != nil {
		t.Fatalf("build reload request: %v", err)
	}
	reloadRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})

	reloadResponse, err := client.Do(reloadRequest)
	if err != nil {
		t.Fatalf("reload dashboard request: %v", err)
	}
	defer reloadResponse.Body.Close()

	reloadBody := readBody(t, reloadResponse.Body)
	if !strings.Contains(reloadBody, "1/12") {
		t.Fatalf("expected updated progress in dashboard body: %s", reloadBody)
	}
}

type testAppOptions struct {
	mailer        ConfirmationMailer
	notifier      AdminApprovalNotifier
	webhookSecret string
}

func newTestApp(t *testing.T, options testAppOptions) (*App, *store.Store) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	st := store.New(db)
	if err := st.Init(ctx); err != nil {
		t.Fatalf("init store: %v", err)
	}

	var registration *RegistrationCoordinator
	if options.mailer != nil && options.notifier != nil {
		registration = NewRegistrationCoordinator(RegistrationCoordinatorConfig{
			Store:    st,
			Mailer:   options.mailer,
			Notifier: options.notifier,
			BaseURL:  "",
		})
	}

	app, err := New(st, Config{
		Registration:          registration,
		TelegramWebhookSecret: options.webhookSecret,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}

	return app, st
}

func readBody(t *testing.T, body io.Reader) string {
	t.Helper()

	content, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return string(content)
}

type fakeConfirmationMailer struct {
	sent []sentConfirmation
}

type sentConfirmation struct {
	Email      string
	Username   string
	ConfirmURL string
}

func (f *fakeConfirmationMailer) SendRegistrationConfirmation(_ context.Context, email, username, confirmURL string) error {
	f.sent = append(f.sent, sentConfirmation{
		Email:      email,
		Username:   username,
		ConfirmURL: confirmURL,
	})
	return nil
}

type fakeApprovalNotifier struct {
	requests []sentApprovalRequest
	answers  []string
}

type sentApprovalRequest struct {
	RequestID int64
	Username  string
	Email     string
}

func (f *fakeApprovalNotifier) SendRegistrationRequest(_ context.Context, requestID int64, username, email string, _ time.Time) error {
	f.requests = append(f.requests, sentApprovalRequest{
		RequestID: requestID,
		Username:  username,
		Email:     email,
	})
	return nil
}

func (f *fakeApprovalNotifier) AnswerCallbackQuery(_ context.Context, _ string, text string) error {
	f.answers = append(f.answers, text)
	return nil
}

func (f *fakeApprovalNotifier) MarkRegistrationApproved(context.Context, int64, int, string, string) error {
	return nil
}

func (f *fakeApprovalNotifier) MarkRegistrationRejected(context.Context, int64, int, string, string) error {
	return nil
}
