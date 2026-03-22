package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"grep-offer/internal/content"
	"grep-offer/internal/store"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"
)

const testPostgresPort uint32 = 55439

var (
	testPostgres       *embeddedpostgres.EmbeddedPostgres
	testPostgresConfig embeddedpostgres.Config
	testDatabaseCount  atomic.Uint64
)

func TestMain(m *testing.M) {
	http.DefaultTransport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}

	rootDir, err := os.MkdirTemp("", "grep-offer-embedded-postgres-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create embedded postgres dir: %v\n", err)
		os.Exit(1)
	}
	cacheDir := filepath.Join(os.TempDir(), "grep-offer-embedded-postgres-cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create embedded postgres cache dir: %v\n", err)
		_ = os.RemoveAll(rootDir)
		os.Exit(1)
	}

	testPostgresConfig = embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V16).
		Port(testPostgresPort).
		Username("postgres").
		Password("postgres").
		Database("postgres").
		BinaryRepositoryURL("https://repo.maven.apache.org/maven2").
		RuntimePath(filepath.Join(rootDir, "runtime")).
		DataPath(filepath.Join(rootDir, "data")).
		BinariesPath(filepath.Join(rootDir, "binaries")).
		CachePath(cacheDir).
		StartTimeout(60 * time.Second).
		Logger(io.Discard)

	testPostgres = embeddedpostgres.NewDatabase(testPostgresConfig)
	if err := testPostgres.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "start embedded postgres: %v\n", err)
		_ = os.RemoveAll(rootDir)
		os.Exit(1)
	}

	code := m.Run()

	if err := testPostgres.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "stop embedded postgres: %v\n", err)
	}
	_ = os.RemoveAll(rootDir)

	os.Exit(code)
}

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

	registerResponse, err := postFormWithCSRF(client, server.URL+"/register", form)
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

	registerResponse, err := postFormWithCSRF(client, server.URL+"/register", registerForm)
	if err != nil {
		t.Fatalf("register request: %v", err)
	}
	defer registerResponse.Body.Close()

	loginForm := url.Values{
		"email":    {"pending@example.com"},
		"password": {"supersecret123"},
	}

	loginResponse, err := postFormWithCSRF(client, server.URL+"/login", loginForm)
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

	response, err := postFormWithCSRF(server.Client(), server.URL+"/register", form)
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

func TestRegisterRejectsTakenUsernameFromExistingUser(t *testing.T) {
	t.Parallel()

	fakeMailer := &fakeConfirmationMailer{}
	fakeNotifier := &fakeApprovalNotifier{}
	testApp, st := newTestApp(t, testAppOptions{
		mailer:   fakeMailer,
		notifier: fakeNotifier,
	})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	if _, err := st.CreateUser(context.Background(), "Bash_Bandit", "taken@example.com", "hash"); err != nil {
		t.Fatalf("create existing user: %v", err)
	}

	form := url.Values{
		"username":         {"bash_bandit"},
		"email":            {"new@example.com"},
		"password":         {"supersecret123"},
		"confirm_password": {"supersecret123"},
	}

	response, err := postFormWithCSRF(server.Client(), server.URL+"/register", form)
	if err != nil {
		t.Fatalf("register request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusConflict {
		t.Fatalf("unexpected register status: %d", response.StatusCode)
	}

	if body := readBody(t, response.Body); !strings.Contains(strings.ToLower(body), "alias") {
		t.Fatalf("taken username error missing: %s", body)
	}
}

func TestRegisterRejectsTakenUsernameFromPendingRequest(t *testing.T) {
	t.Parallel()

	fakeMailer := &fakeConfirmationMailer{}
	fakeNotifier := &fakeApprovalNotifier{}
	testApp, _ := newTestApp(t, testAppOptions{
		mailer:   fakeMailer,
		notifier: fakeNotifier,
	})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	firstForm := url.Values{
		"username":         {"bash_bandit"},
		"email":            {"first@example.com"},
		"password":         {"supersecret123"},
		"confirm_password": {"supersecret123"},
	}

	firstResponse, err := postFormWithCSRF(server.Client(), server.URL+"/register", firstForm)
	if err != nil {
		t.Fatalf("first register request: %v", err)
	}
	defer firstResponse.Body.Close()

	if firstResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected first register status: %d", firstResponse.StatusCode)
	}

	secondForm := url.Values{
		"username":         {"BASH_BANDIT"},
		"email":            {"second@example.com"},
		"password":         {"supersecret123"},
		"confirm_password": {"supersecret123"},
	}

	secondResponse, err := postFormWithCSRF(server.Client(), server.URL+"/register", secondForm)
	if err != nil {
		t.Fatalf("second register request: %v", err)
	}
	defer secondResponse.Body.Close()

	if secondResponse.StatusCode != http.StatusConflict {
		t.Fatalf("unexpected second register status: %d", secondResponse.StatusCode)
	}

	if body := readBody(t, secondResponse.Body); !strings.Contains(strings.ToLower(body), "alias") {
		t.Fatalf("pending username error missing: %s", body)
	}
}

func TestForgotPasswordFlow(t *testing.T) {
	t.Parallel()

	fakeMailer := &fakeConfirmationMailer{}
	testApp, st := newTestApp(t, testAppOptions{
		mailer: fakeMailer,
	})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("oldsecret123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	user, err := st.CreateUser(context.Background(), "bash_bandit", "reset@example.com", string(passwordHash))
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}

	client := server.Client()
	client.Jar = jar

	requestResponse, err := postFormWithCSRF(client, server.URL+"/password/forgot", url.Values{
		"email": {"reset@example.com"},
	})
	if err != nil {
		t.Fatalf("forgot password request: %v", err)
	}
	defer requestResponse.Body.Close()

	if requestResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected forgot password status: %d", requestResponse.StatusCode)
	}

	requestBody := readBody(t, requestResponse.Body)
	if !strings.Contains(requestBody, "ссылка на сброс уже улетела") {
		t.Fatalf("forgot password notice missing: %s", requestBody)
	}

	if len(fakeMailer.resetSent) != 1 {
		t.Fatalf("unexpected reset emails: %#v", fakeMailer.resetSent)
	}

	resetURL, err := url.Parse(fakeMailer.resetSent[0].ResetURL)
	if err != nil {
		t.Fatalf("parse reset url: %v", err)
	}

	token := resetURL.Query().Get("token")
	if token == "" {
		t.Fatalf("reset token missing from url: %s", fakeMailer.resetSent[0].ResetURL)
	}

	resetFormResponse, err := client.Get(fakeMailer.resetSent[0].ResetURL)
	if err != nil {
		t.Fatalf("open reset form: %v", err)
	}
	defer resetFormResponse.Body.Close()

	if resetFormResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected reset form status: %d", resetFormResponse.StatusCode)
	}

	resetResponse, err := postFormWithCSRF(client, server.URL+"/password/reset", url.Values{
		"token":            {token},
		"password":         {"newsecret123"},
		"confirm_password": {"newsecret123"},
	})
	if err != nil {
		t.Fatalf("submit password reset: %v", err)
	}
	defer resetResponse.Body.Close()

	if resetResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected password reset status: %d", resetResponse.StatusCode)
	}

	resetBody := readBody(t, resetResponse.Body)
	if !strings.Contains(resetBody, "Привет, bash_bandit") {
		t.Fatalf("dashboard greeting missing after password reset: %s", resetBody)
	}

	updatedUser, err := st.UserByEmail(context.Background(), "reset@example.com")
	if err != nil {
		t.Fatalf("load updated user: %v", err)
	}
	if updatedUser.ID != user.ID {
		t.Fatalf("unexpected user id after reset: %d", updatedUser.ID)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(updatedUser.PasswordHash), []byte("newsecret123")); err != nil {
		t.Fatalf("password was not updated: %v", err)
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

func TestDeployLockShowsBannerOnReadPages(t *testing.T) {
	t.Parallel()

	lockDir := t.TempDir()
	lockPath := filepath.Join(lockDir, "deploy.lock")
	if err := os.WriteFile(lockPath, []byte("deploy"), 0o644); err != nil {
		t.Fatalf("write deploy lock: %v", err)
	}

	testApp, _ := newTestApp(t, testAppOptions{deployLockPath: lockPath})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/login")
	if err != nil {
		t.Fatalf("login form request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", response.StatusCode)
	}

	body := readBody(t, response.Body)
	if !strings.Contains(body, "Сайт обновляется, формы и прогресс временно на паузе.") {
		t.Fatalf("deploy banner missing: %s", body)
	}
}

func TestDeployLockRejectsWriteRequests(t *testing.T) {
	t.Parallel()

	lockDir := t.TempDir()
	lockPath := filepath.Join(lockDir, "deploy.lock")
	if err := os.WriteFile(lockPath, []byte("deploy"), 0o644); err != nil {
		t.Fatalf("write deploy lock: %v", err)
	}

	testApp, _ := newTestApp(t, testAppOptions{deployLockPath: lockPath})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	response, err := postFormWithCSRF(server.Client(), server.URL+"/login", url.Values{
		"email":    {"member@example.com"},
		"password": {"password123"},
	})
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unexpected status: %d", response.StatusCode)
	}
	if got := response.Header.Get("Retry-After"); got != "30" {
		t.Fatalf("unexpected retry-after header: %q", got)
	}
	if body := readBody(t, response.Body); !strings.Contains(body, "Сайт обновляется") {
		t.Fatalf("unexpected deploy lock body: %s", body)
	}
}

func TestLoginRejectsMissingCSRF(t *testing.T) {
	t.Parallel()

	testApp, _ := newTestApp(t, testAppOptions{})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	response, err := server.Client().PostForm(server.URL+"/login", url.Values{
		"email":    {"nobody@example.com"},
		"password": {"whatever123"},
	})
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("unexpected status without csrf: %d", response.StatusCode)
	}
}

func TestLoginFormSetsSecurityHeadersAndCSRFCookie(t *testing.T) {
	t.Parallel()

	testApp, _ := newTestApp(t, testAppOptions{})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/login")
	if err != nil {
		t.Fatalf("login form request: %v", err)
	}
	defer response.Body.Close()

	if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("unexpected nosniff header: %q", got)
	}
	if got := response.Header.Get("Content-Security-Policy"); !strings.Contains(got, "script-src 'self' 'nonce-") {
		t.Fatalf("unexpected csp header: %q", got)
	}

	foundCSRFCookie := false
	for _, cookie := range response.Cookies() {
		if cookie.Name == csrfCookieName {
			foundCSRFCookie = true
			break
		}
	}
	if !foundCSRFCookie {
		t.Fatalf("csrf cookie missing")
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
	addCSRFCookieAndHeader(toggleRequest)

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

func TestLearnRoutesRequireAuthAndRenderMarkdown(t *testing.T) {
	t.Parallel()

	contentDir := filepath.Join(t.TempDir(), "articles")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatalf("create content dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentDir, "intro.md"), []byte(`---
title: "Docker без религии"
slug: "docker-without-religion"
summary: "Быстрый конспект по доставке."
badge: "delivery"
stage: "Доставка"
module: "Доставка"
module_order: 2
block_order: 1
kind: "theory"
published: true
---

# Docker без религии

Собираем образ и не надеемся на магию.`), 0o644); err != nil {
		t.Fatalf("write article: %v", err)
	}

	testApp, _ := newTestApp(t, testAppOptions{
		articleDir: contentDir,
	})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	redirectClient := server.Client()
	redirectClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	indexResponse, err := redirectClient.Get(server.URL + "/learn")
	if err != nil {
		t.Fatalf("learn index request: %v", err)
	}
	defer indexResponse.Body.Close()

	if indexResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf("unexpected learn index status for guest: %d", indexResponse.StatusCode)
	}

	userApp, st := newTestApp(t, testAppOptions{
		articleDir: contentDir,
	})
	userServer := httptest.NewServer(userApp.Routes())
	defer userServer.Close()

	user, err := st.CreateUser(context.Background(), "bash_bandit", "reader@example.com", "hash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	const sessionToken = "reader-session-token"
	if err := st.CreateSession(context.Background(), user.ID, sessionToken, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}

	indexRequest, err := http.NewRequest(http.MethodGet, userServer.URL+"/learn", nil)
	if err != nil {
		t.Fatalf("build learn index request: %v", err)
	}
	indexRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})

	indexResponse, err = userServer.Client().Do(indexRequest)
	if err != nil {
		t.Fatalf("authorized learn index request: %v", err)
	}
	defer indexResponse.Body.Close()

	if indexResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected authorized learn index status: %d", indexResponse.StatusCode)
	}

	indexBody := readBody(t, indexResponse.Body)
	if !strings.Contains(indexBody, "Docker без религии") {
		t.Fatalf("lesson title missing on learn index: %s", indexBody)
	}

	showRequest, err := http.NewRequest(http.MethodGet, userServer.URL+"/learn/docker-without-religion", nil)
	if err != nil {
		t.Fatalf("build learn show request: %v", err)
	}
	showRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})

	showResponse, err := userServer.Client().Do(showRequest)
	if err != nil {
		t.Fatalf("learn show request: %v", err)
	}
	defer showResponse.Body.Close()

	if showResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected learn show status: %d", showResponse.StatusCode)
	}

	showBody := readBody(t, showResponse.Body)
	if !strings.Contains(showBody, "<h1>Docker без религии</h1>") {
		t.Fatalf("rendered markdown heading missing: %s", showBody)
	}
}

func TestLessonProgressPersistsAcrossLearnAndDashboard(t *testing.T) {
	t.Parallel()

	contentDir := filepath.Join(t.TempDir(), "articles")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatalf("create content dir: %v", err)
	}

	lessonOne := `---
title: "История Linux"
slug: "linux-history"
summary: "Быстрый старт по базе Linux."
badge: "linux"
stage: "Фундамент"
module: "Фундамент"
module_order: 1
block_order: 1
kind: "theory"
published: true
---

# История Linux

Начинаем с базы.`
	lessonTwo := `---
title: "Проверка Linux"
slug: "linux-quiz"
summary: "Короткий тест по блоку."
badge: "test"
stage: "Фундамент"
module: "Фундамент"
module_order: 1
block_order: 2
kind: "test"
published: true
---

# Проверка Linux

Отвечай без гугла.`
	lessonThree := `---
title: "Следующий блок"
slug: "linux-next"
summary: "Этот блок открывается только после теста."
badge: "linux"
stage: "Фундамент"
module: "Фундамент"
module_order: 1
block_order: 3
kind: "theory"
published: true
---

# Следующий блок

Продолжаем маршрут.`

	if err := os.WriteFile(filepath.Join(contentDir, "01-history.md"), []byte(lessonOne), 0o644); err != nil {
		t.Fatalf("write first lesson: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentDir, "02-quiz.md"), []byte(lessonTwo), 0o644); err != nil {
		t.Fatalf("write second lesson: %v", err)
	}
	if err := os.WriteFile(filepath.Join(contentDir, "03-next.md"), []byte(lessonThree), 0o644); err != nil {
		t.Fatalf("write third lesson: %v", err)
	}

	testApp, st := newTestApp(t, testAppOptions{
		articleDir: contentDir,
	})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	ctx := context.Background()
	user, err := st.CreateUser(ctx, "bash_bandit", "learn-progress@example.com", "hash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	const sessionToken = "learn-progress-session"
	if err := st.CreateSession(ctx, user.ID, sessionToken, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}

	for i := 1; i <= 4; i++ {
		if _, err := st.CreateLessonTestQuestion(
			ctx,
			"linux-quiz",
			"Вопрос "+strconv.Itoa(i),
			[]string{"правильный", "неправильный"},
			0,
			"",
		); err != nil {
			t.Fatalf("create lesson test question %d: %v", i, err)
		}
	}

	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	lockedRequest, err := http.NewRequest(http.MethodGet, server.URL+"/learn/linux-next", nil)
	if err != nil {
		t.Fatalf("build locked lesson request: %v", err)
	}
	lockedRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})

	lockedResponse, err := client.Do(lockedRequest)
	if err != nil {
		t.Fatalf("locked lesson request: %v", err)
	}
	defer lockedResponse.Body.Close()

	if lockedResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf("unexpected locked lesson status: %d", lockedResponse.StatusCode)
	}

	firstLessonRequest, err := http.NewRequest(http.MethodGet, server.URL+"/learn/linux-history", nil)
	if err != nil {
		t.Fatalf("build first lesson request: %v", err)
	}
	firstLessonRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})

	firstLessonResponse, err := client.Do(firstLessonRequest)
	if err != nil {
		t.Fatalf("first lesson request: %v", err)
	}
	defer firstLessonResponse.Body.Close()

	if firstLessonResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected first lesson status: %d", firstLessonResponse.StatusCode)
	}

	progress, err := st.LessonProgress(ctx, user.ID)
	if err != nil {
		t.Fatalf("load lesson progress: %v", err)
	}
	if !progress["linux-history"] {
		t.Fatalf("expected first lesson to become read automatically")
	}

	quizRequest, err := http.NewRequest(http.MethodGet, server.URL+"/learn/linux-quiz", nil)
	if err != nil {
		t.Fatalf("build quiz request: %v", err)
	}
	quizRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})

	quizResponse, err := client.Do(quizRequest)
	if err != nil {
		t.Fatalf("quiz request: %v", err)
	}
	defer quizResponse.Body.Close()

	if quizResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected quiz status: %d", quizResponse.StatusCode)
	}

	failForm := url.Values{
		"question_1": {"1"},
		"question_2": {"1"},
		"question_3": {"1"},
		"question_4": {"1"},
	}

	failRequest, err := http.NewRequest(http.MethodPost, server.URL+"/learn/tests/linux-quiz", strings.NewReader(failForm.Encode()))
	if err != nil {
		t.Fatalf("build fail test request: %v", err)
	}
	failRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	failRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
	addCSRFCookieAndHeader(failRequest)

	failResponse, err := client.Do(failRequest)
	if err != nil {
		t.Fatalf("fail test request: %v", err)
	}
	defer failResponse.Body.Close()

	if failResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf("unexpected fail test status: %d", failResponse.StatusCode)
	}

	secondLockedRequest, err := http.NewRequest(http.MethodGet, server.URL+"/learn/linux-next", nil)
	if err != nil {
		t.Fatalf("build second locked request: %v", err)
	}
	secondLockedRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})

	secondLockedResponse, err := client.Do(secondLockedRequest)
	if err != nil {
		t.Fatalf("second locked request: %v", err)
	}
	defer secondLockedResponse.Body.Close()

	if secondLockedResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected next lesson to stay locked after failed test: %d", secondLockedResponse.StatusCode)
	}

	passForm := url.Values{
		"question_1": {"0"},
		"question_2": {"0"},
		"question_3": {"0"},
		"question_4": {"0"},
	}

	passRequest, err := http.NewRequest(http.MethodPost, server.URL+"/learn/tests/linux-quiz", strings.NewReader(passForm.Encode()))
	if err != nil {
		t.Fatalf("build pass test request: %v", err)
	}
	passRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	passRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
	addCSRFCookieAndHeader(passRequest)

	passResponse, err := client.Do(passRequest)
	if err != nil {
		t.Fatalf("pass test request: %v", err)
	}
	defer passResponse.Body.Close()

	if passResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf("unexpected pass test status: %d", passResponse.StatusCode)
	}

	testResults, err := st.LessonTestResults(ctx, user.ID)
	if err != nil {
		t.Fatalf("load lesson test results: %v", err)
	}
	if !testResults["linux-quiz"].Passed {
		t.Fatalf("expected quiz to become passed")
	}

	nextLessonRequest, err := http.NewRequest(http.MethodGet, server.URL+"/learn/linux-next", nil)
	if err != nil {
		t.Fatalf("build next lesson request: %v", err)
	}
	nextLessonRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})

	nextLessonResponse, err := client.Do(nextLessonRequest)
	if err != nil {
		t.Fatalf("next lesson request: %v", err)
	}
	defer nextLessonResponse.Body.Close()

	if nextLessonResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected next lesson to unlock after passing test: %d", nextLessonResponse.StatusCode)
	}

	indexReloadRequest, err := http.NewRequest(http.MethodGet, server.URL+"/learn", nil)
	if err != nil {
		t.Fatalf("build learn reload request: %v", err)
	}
	indexReloadRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})

	indexReloadResponse, err := client.Do(indexReloadRequest)
	if err != nil {
		t.Fatalf("learn reload request: %v", err)
	}
	defer indexReloadResponse.Body.Close()

	reloadedBody := readBody(t, indexReloadResponse.Body)
	if !strings.Contains(reloadedBody, "1 / 1 тестов passed") {
		t.Fatalf("expected test-based progress on learn page: %s", reloadedBody)
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

	dashboardBody := readBody(t, dashboardResponse.Body)
	if !strings.Contains(dashboardBody, "1/1") {
		t.Fatalf("expected test progress on dashboard: %s", dashboardBody)
	}
	if !strings.Contains(dashboardBody, "Продолжить Linux") {
		t.Fatalf("expected dashboard continue CTA: %s", dashboardBody)
	}
}

func TestAdminCanCreateLessonTestQuestion(t *testing.T) {
	t.Parallel()

	contentDir := filepath.Join(t.TempDir(), "articles")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatalf("create content dir: %v", err)
	}

	testLesson := `---
title: "Проверка Linux"
slug: "linux-quiz"
summary: "Тест по базе."
badge: "test"
stage: "Фундамент"
module: "Фундамент"
module_order: 1
block_order: 2
kind: "test"
published: true
---

# Проверка Linux

Отвечай честно.`

	if err := os.WriteFile(filepath.Join(contentDir, "quiz.md"), []byte(testLesson), 0o644); err != nil {
		t.Fatalf("write test lesson: %v", err)
	}

	testApp, st := newTestApp(t, testAppOptions{
		articleDir: contentDir,
	})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	ctx := context.Background()
	adminUser, err := st.CreateUser(ctx, "root_ops", "admin@example.com", "hash")
	if err != nil {
		t.Fatalf("create admin user: %v", err)
	}
	if err := st.SetUserAdmin(ctx, adminUser.ID, true); err != nil {
		t.Fatalf("promote admin user: %v", err)
	}

	const sessionToken = "admin-test-session"
	if err := st.CreateSession(ctx, adminUser.ID, sessionToken, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	form := url.Values{
		"lesson_slug":    {"linux-quiz"},
		"prompt":         {"Где лежат системные логи?"},
		"options":        {"/var/log\n/tmp\n/home"},
		"correct_option": {"1"},
		"explanation":    {"Потому что это стандартное место для логов."},
	}

	request, err := http.NewRequest(http.MethodPost, server.URL+"/admin/tests/questions", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build admin test request: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
	addCSRFCookieAndHeader(request)

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("admin test request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("unexpected create test question status: %d", response.StatusCode)
	}

	questions, err := st.LessonTestQuestions(ctx, "linux-quiz")
	if err != nil {
		t.Fatalf("load created questions: %v", err)
	}
	if len(questions) != 1 {
		t.Fatalf("unexpected question count: %d", len(questions))
	}
	if questions[0].Prompt != "Где лежат системные логи?" {
		t.Fatalf("unexpected question prompt: %#v", questions[0])
	}
}

func TestAdminCanCreateMarkdownLesson(t *testing.T) {
	t.Parallel()

	contentDir := filepath.Join(t.TempDir(), "articles")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatalf("create content dir: %v", err)
	}

	testApp, st := newTestApp(t, testAppOptions{
		articleDir: contentDir,
	})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	ctx := context.Background()
	adminUser, err := st.CreateUser(ctx, "root_ops", "admin@example.com", "hash")
	if err != nil {
		t.Fatalf("create admin user: %v", err)
	}
	if err := st.SetUserAdmin(ctx, adminUser.ID, true); err != nil {
		t.Fatalf("promote admin user: %v", err)
	}

	const sessionToken = "admin-article-session"
	if err := st.CreateSession(ctx, adminUser.ID, sessionToken, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	form := url.Values{
		"title":        {"Файловая система Linux"},
		"slug":         {"linux-filesystem"},
		"summary":      {"Базовая карта каталогов и зачем она нужна."},
		"badge":        {"linux"},
		"stage":        {"Linux Base"},
		"module":       {"Файловая система"},
		"kind":         {"theory"},
		"module_order": {"2"},
		"block_order":  {"3"},
		"published":    {"1"},
		"body":         {"# Файловая система Linux\n\nКороткий текст.\n"},
	}

	request, err := http.NewRequest(http.MethodPost, server.URL+"/admin/articles", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build admin article request: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
	addCSRFCookieAndHeader(request)

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("admin article request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("unexpected create article status: %d", response.StatusCode)
	}

	library := content.NewLibrary(contentDir)
	article, err := library.EditableBySlug("linux-filesystem")
	if err != nil {
		t.Fatalf("load created article: %v", err)
	}
	if article.Title != "Файловая система Linux" {
		t.Fatalf("unexpected article title: %#v", article)
	}
	if !article.Published {
		t.Fatalf("expected article to be published")
	}
	if !strings.Contains(article.Body, "Короткий текст.") {
		t.Fatalf("unexpected article body: %q", article.Body)
	}
}

func TestAdminCanSaveAndOpenPublishedLesson(t *testing.T) {
	t.Parallel()

	contentDir := filepath.Join(t.TempDir(), "articles")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatalf("create content dir: %v", err)
	}

	testApp, st := newTestApp(t, testAppOptions{
		articleDir: contentDir,
	})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	ctx := context.Background()
	adminUser, err := st.CreateUser(ctx, "root_ops", "admin@example.com", "hash")
	if err != nil {
		t.Fatalf("create admin user: %v", err)
	}
	if err := st.SetUserAdmin(ctx, adminUser.ID, true); err != nil {
		t.Fatalf("promote admin user: %v", err)
	}

	const sessionToken = "admin-article-open-session"
	if err := st.CreateSession(ctx, adminUser.ID, sessionToken, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	form := url.Values{
		"title":        {"Логи в Linux"},
		"slug":         {"linux-logs"},
		"summary":      {"Короткий блок про journalctl и syslog."},
		"badge":        {"linux"},
		"stage":        {"Linux Base"},
		"module":       {"Логи"},
		"kind":         {"theory"},
		"module_order": {"3"},
		"block_order":  {"2"},
		"published":    {"1"},
		"after_save":   {"open"},
		"body":         {"# Логи в Linux\n\nСначала смотрим journalctl.\n"},
	}

	request, err := http.NewRequest(http.MethodPost, server.URL+"/admin/articles", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build admin article request: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
	addCSRFCookieAndHeader(request)

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("admin article request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("unexpected save-and-open status: %d", response.StatusCode)
	}
	if location := response.Header.Get("Location"); location != "/learn/linux-logs" {
		t.Fatalf("unexpected save-and-open redirect: %q", location)
	}
}

func TestAdminCanDeleteLesson(t *testing.T) {
	t.Parallel()

	contentDir := filepath.Join(t.TempDir(), "articles")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatalf("create content dir: %v", err)
	}

	seed := `---
title: "Логи в Linux"
slug: "linux-logs"
summary: "Короткий блок про journalctl."
badge: "linux"
stage: "Linux Base"
module: "Логи"
module_order: 3
block_order: 2
kind: "theory"
published: true
---

# Логи в Linux

Смотрим journalctl.
`
	if err := os.WriteFile(filepath.Join(contentDir, "03-02-linux-logs.md"), []byte(seed), 0o644); err != nil {
		t.Fatalf("write seed article: %v", err)
	}

	testApp, st := newTestApp(t, testAppOptions{
		articleDir: contentDir,
	})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	ctx := context.Background()
	adminUser, err := st.CreateUser(ctx, "root_ops", "admin@example.com", "hash")
	if err != nil {
		t.Fatalf("create admin user: %v", err)
	}
	if err := st.SetUserAdmin(ctx, adminUser.ID, true); err != nil {
		t.Fatalf("promote admin user: %v", err)
	}

	const sessionToken = "admin-article-delete-session"
	if err := st.CreateSession(ctx, adminUser.ID, sessionToken, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	request, err := http.NewRequest(http.MethodPost, server.URL+"/admin/articles/linux-logs/delete", nil)
	if err != nil {
		t.Fatalf("build delete request: %v", err)
	}
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
	addCSRFCookieAndHeader(request)

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("delete request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("unexpected delete status: %d", response.StatusCode)
	}
	if location := response.Header.Get("Location"); location != "/admin/articles?notice=article-deleted" {
		t.Fatalf("unexpected delete redirect: %q", location)
	}

	library := content.NewLibrary(contentDir)
	if _, err := library.EditableBySlug("linux-logs"); !errors.Is(err, content.ErrArticleNotFound) {
		t.Fatalf("expected article to be deleted, got %v", err)
	}
}

func TestAdminCanOpenDuplicateLessonEditor(t *testing.T) {
	t.Parallel()

	contentDir := filepath.Join(t.TempDir(), "articles")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatalf("create content dir: %v", err)
	}

	seed := `---
title: "Файловая система Linux"
slug: "linux-filesystem"
summary: "Базовый блок про каталоги."
badge: "linux"
stage: "Linux Base"
module: "Файловая система"
module_order: 2
block_order: 1
kind: "theory"
published: true
---

# Файловая система Linux

Короткий текст.
`
	if err := os.WriteFile(filepath.Join(contentDir, "02-01-linux-filesystem.md"), []byte(seed), 0o644); err != nil {
		t.Fatalf("write seed article: %v", err)
	}

	testApp, st := newTestApp(t, testAppOptions{
		articleDir: contentDir,
	})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	ctx := context.Background()
	adminUser, err := st.CreateUser(ctx, "root_ops", "admin@example.com", "hash")
	if err != nil {
		t.Fatalf("create admin user: %v", err)
	}
	if err := st.SetUserAdmin(ctx, adminUser.ID, true); err != nil {
		t.Fatalf("promote admin user: %v", err)
	}

	const sessionToken = "admin-article-duplicate-session"
	if err := st.CreateSession(ctx, adminUser.ID, sessionToken, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	request, err := http.NewRequest(http.MethodGet, server.URL+"/admin/articles/linux-filesystem/duplicate", nil)
	if err != nil {
		t.Fatalf("build duplicate request: %v", err)
	}
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})

	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("duplicate request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected duplicate status: %d", response.StatusCode)
	}

	body := readBody(t, response.Body)
	for _, fragment := range []string{
		"Дубликат урока",
		"Файловая система Linux (копия)",
		"linux-filesystem-copy",
	} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("expected duplicate editor to contain %q, got: %s", fragment, body)
		}
	}
}

func TestAdminArticleSlugCheckEndpoint(t *testing.T) {
	t.Parallel()

	contentDir := filepath.Join(t.TempDir(), "articles")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatalf("create content dir: %v", err)
	}

	seed := `---
title: "Файловая система Linux"
slug: "linux-filesystem"
summary: "Базовый блок про каталоги."
badge: "linux"
stage: "Linux Base"
module: "Файловая система"
module_order: 2
block_order: 1
kind: "theory"
published: true
---

# Файловая система Linux
`
	if err := os.WriteFile(filepath.Join(contentDir, "02-01-linux-filesystem.md"), []byte(seed), 0o644); err != nil {
		t.Fatalf("write seed article: %v", err)
	}

	testApp, st := newTestApp(t, testAppOptions{
		articleDir: contentDir,
	})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	ctx := context.Background()
	adminUser, err := st.CreateUser(ctx, "root_ops", "admin@example.com", "hash")
	if err != nil {
		t.Fatalf("create admin user: %v", err)
	}
	if err := st.SetUserAdmin(ctx, adminUser.ID, true); err != nil {
		t.Fatalf("promote admin user: %v", err)
	}

	const sessionToken = "admin-article-slug-check-session"
	if err := st.CreateSession(ctx, adminUser.ID, sessionToken, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	for _, tc := range []struct {
		name          string
		query         string
		wantAvailable bool
	}{
		{name: "taken", query: "?slug=linux-filesystem", wantAvailable: false},
		{name: "same-original", query: "?slug=linux-filesystem&original_slug=linux-filesystem", wantAvailable: true},
		{name: "free", query: "?slug=linux-journalctl", wantAvailable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, server.URL+"/admin/articles/slug-check"+tc.query, nil)
			if err != nil {
				t.Fatalf("build slug check request: %v", err)
			}
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})

			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatalf("slug check request: %v", err)
			}
			defer response.Body.Close()

			if response.StatusCode != http.StatusOK {
				t.Fatalf("unexpected slug check status: %d", response.StatusCode)
			}

			var payload struct {
				NormalizedSlug string `json:"normalized_slug"`
				Available      bool   `json:"available"`
				Message        string `json:"message"`
			}
			if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
				t.Fatalf("decode slug check payload: %v", err)
			}

			if payload.Available != tc.wantAvailable {
				t.Fatalf("unexpected slug availability for %s: %#v", tc.name, payload)
			}
		})
	}
}

func TestAdminArticleEditorShowsExistingRouteOptions(t *testing.T) {
	t.Parallel()

	contentDir := filepath.Join(t.TempDir(), "articles")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatalf("create content dir: %v", err)
	}

	seed := `---
title: "Файловая система"
slug: "linux-filesystem"
summary: "Базовый блок про каталоги."
badge: "linux"
stage: "Linux Base"
module: "Файловая система"
module_order: 2
block_order: 1
kind: "theory"
published: true
---

# Файловая система

Короткий текст.
`
	if err := os.WriteFile(filepath.Join(contentDir, "02-01-linux-filesystem.md"), []byte(seed), 0o644); err != nil {
		t.Fatalf("write seed article: %v", err)
	}

	testApp, st := newTestApp(t, testAppOptions{
		articleDir: contentDir,
	})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	ctx := context.Background()
	adminUser, err := st.CreateUser(ctx, "root_ops", "admin@example.com", "hash")
	if err != nil {
		t.Fatalf("create admin user: %v", err)
	}
	if err := st.SetUserAdmin(ctx, adminUser.ID, true); err != nil {
		t.Fatalf("promote admin user: %v", err)
	}

	const sessionToken = "admin-route-picker-session"
	if err := st.CreateSession(ctx, adminUser.ID, sessionToken, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	request, err := http.NewRequest(http.MethodGet, server.URL+"/admin/articles/new", nil)
	if err != nil {
		t.Fatalf("build admin editor request: %v", err)
	}
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})

	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("admin editor request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected editor status: %d", response.StatusCode)
	}

	body := readBody(t, response.Body)
	for _, fragment := range []string{
		"data-editor-stage-chip",
		"data-editor-module-chip",
		"Linux Base",
		"Файловая система",
	} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("expected editor body to contain %q, got: %s", fragment, body)
		}
	}
}

func TestAdminArticleOptionsSuggestNextOrders(t *testing.T) {
	t.Parallel()

	contentDir := filepath.Join(t.TempDir(), "articles")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatalf("create content dir: %v", err)
	}

	files := map[string]string{
		"02-01-linux-filesystem.md": `---
title: "Файловая система"
slug: "linux-filesystem"
stage: "Linux Base"
module: "Файловая система"
module_order: 2
block_order: 1
kind: "theory"
published: true
---

# Файловая система
`,
		"02-02-linux-permissions.md": `---
title: "Права в Linux"
slug: "linux-permissions"
stage: "Linux Base"
module: "Файловая система"
module_order: 2
block_order: 2
kind: "practice"
published: true
---

# Права в Linux
`,
		"03-01-network-basics.md": `---
title: "Сеть"
slug: "network-basics"
stage: "Linux Base"
module: "Сеть"
module_order: 3
block_order: 1
kind: "theory"
published: true
---

# Сеть
`,
		"04-01-docker.md": `---
title: "Docker"
slug: "docker-basics"
stage: "Delivery"
module: "Docker"
module_order: 4
block_order: 1
kind: "theory"
published: true
---

# Docker
`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(contentDir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write article %s: %v", name, err)
		}
	}

	testApp, _ := newTestApp(t, testAppOptions{
		articleDir: contentDir,
	})

	options := testApp.loadAdminArticleOptions()
	if options.GlobalNextModuleOrder != 5 {
		t.Fatalf("unexpected global next module order: %d", options.GlobalNextModuleOrder)
	}
	if len(options.Stages) != 2 {
		t.Fatalf("unexpected stage count: %d", len(options.Stages))
	}

	var linuxStage AdminStageOption
	for _, stage := range options.Stages {
		if stage.Value == "Linux Base" {
			linuxStage = stage
			break
		}
	}
	if linuxStage.Value == "" {
		t.Fatalf("linux stage missing from options: %#v", options.Stages)
	}
	if linuxStage.NextModuleOrder != 4 {
		t.Fatalf("unexpected linux next module order: %d", linuxStage.NextModuleOrder)
	}
	if len(linuxStage.Modules) != 2 {
		t.Fatalf("unexpected linux module count: %d", len(linuxStage.Modules))
	}

	if linuxStage.Modules[0].Value != "Файловая система" || linuxStage.Modules[0].ModuleOrder != 2 || linuxStage.Modules[0].NextBlockOrder != 3 {
		t.Fatalf("unexpected first linux module option: %#v", linuxStage.Modules[0])
	}
}

func TestAdminArticleReorderPersistsBlockOrder(t *testing.T) {
	t.Parallel()

	contentDir := filepath.Join(t.TempDir(), "articles")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatalf("create content dir: %v", err)
	}

	files := map[string]string{
		"02-01-linux-filesystem.md": `---
title: "Файловая система"
slug: "linux-filesystem"
stage: "Linux Base"
module: "Файловая система"
module_order: 2
block_order: 1
kind: "theory"
published: true
---

# Файловая система
`,
		"02-02-linux-permissions.md": `---
title: "Права в Linux"
slug: "linux-permissions"
stage: "Linux Base"
module: "Файловая система"
module_order: 2
block_order: 2
kind: "practice"
published: true
---

# Права в Linux
`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(contentDir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write article %s: %v", name, err)
		}
	}

	testApp, st := newTestApp(t, testAppOptions{
		articleDir: contentDir,
	})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	ctx := context.Background()
	adminUser, err := st.CreateUser(ctx, "root_ops", "admin@example.com", "hash")
	if err != nil {
		t.Fatalf("create admin user: %v", err)
	}
	if err := st.SetUserAdmin(ctx, adminUser.ID, true); err != nil {
		t.Fatalf("promote admin user: %v", err)
	}

	const sessionToken = "admin-reorder-session"
	if err := st.CreateSession(ctx, adminUser.ID, sessionToken, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	form := url.Values{
		"slug":         {"linux-permissions", "linux-filesystem"},
		"stage":        {"Linux Base"},
		"module":       {"Файловая система"},
		"module_order": {"2"},
	}

	request, err := http.NewRequest(http.MethodPost, server.URL+"/admin/articles/reorder", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build reorder request: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
	addCSRFCookieAndHeader(request)

	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("reorder request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected reorder status: %d", response.StatusCode)
	}

	var payload struct {
		Items []struct {
			Slug  string `json:"slug"`
			Index string `json:"index"`
		} `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode reorder payload: %v", err)
	}
	if len(payload.Items) != 2 || payload.Items[0].Slug != "linux-permissions" || payload.Items[0].Index != "2.1" {
		t.Fatalf("unexpected reorder payload: %#v", payload.Items)
	}

	library := content.NewLibrary(contentDir)
	first, err := library.EditableBySlug("linux-permissions")
	if err != nil {
		t.Fatalf("load reordered first article: %v", err)
	}
	second, err := library.EditableBySlug("linux-filesystem")
	if err != nil {
		t.Fatalf("load reordered second article: %v", err)
	}

	if first.BlockOrder != 1 || second.BlockOrder != 2 {
		t.Fatalf("unexpected block orders after reorder: first=%d second=%d", first.BlockOrder, second.BlockOrder)
	}
}

func TestAdminArticlePreviewRendersMarkdown(t *testing.T) {
	t.Parallel()

	testApp, st := newTestApp(t, testAppOptions{})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	ctx := context.Background()
	adminUser, err := st.CreateUser(ctx, "root_ops", "admin@example.com", "hash")
	if err != nil {
		t.Fatalf("create admin user: %v", err)
	}
	if err := st.SetUserAdmin(ctx, adminUser.ID, true); err != nil {
		t.Fatalf("promote admin user: %v", err)
	}

	const sessionToken = "admin-preview-session"
	if err := st.CreateSession(ctx, adminUser.ID, sessionToken, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	form := url.Values{
		"title":        {"Файловая система Linux"},
		"slug":         {"linux-filesystem"},
		"kind":         {"practice"},
		"module_order": {"2"},
		"block_order":  {"3"},
		"body":         {"# Файловая система Linux\n\n## Минимум руками\n\n1. Открой `/etc`.\n"},
	}

	request, err := http.NewRequest(http.MethodPost, server.URL+"/admin/articles/preview", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build preview request: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
	addCSRFCookieAndHeader(request)

	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("preview request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected preview status: %d", response.StatusCode)
	}

	var payload struct {
		HTML      string `json:"html"`
		FileName  string `json:"file_name"`
		LearnPath string `json:"learn_path"`
		KindHint  string `json:"kind_hint"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode preview payload: %v", err)
	}

	if !strings.Contains(payload.HTML, "<h1>Файловая система Linux</h1>") {
		t.Fatalf("preview html missing heading: %s", payload.HTML)
	}
	if payload.FileName != "02-03-linux-filesystem.md" {
		t.Fatalf("unexpected preview file name: %s", payload.FileName)
	}
	if payload.LearnPath != "/learn/linux-filesystem" {
		t.Fatalf("unexpected preview path: %s", payload.LearnPath)
	}
	if !strings.Contains(strings.ToLower(payload.KindHint), "практика") {
		t.Fatalf("unexpected kind hint: %s", payload.KindHint)
	}
}

func TestAdminCanUploadEditorImage(t *testing.T) {
	t.Parallel()

	uploadsDir := filepath.Join(t.TempDir(), "uploads")
	testApp, st := newTestApp(t, testAppOptions{
		uploadsDir: uploadsDir,
	})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	ctx := context.Background()
	adminUser, err := st.CreateUser(ctx, "root_ops", "admin@example.com", "hash")
	if err != nil {
		t.Fatalf("create admin user: %v", err)
	}
	if err := st.SetUserAdmin(ctx, adminUser.ID, true); err != nil {
		t.Fatalf("promote admin user: %v", err)
	}

	const sessionToken = "admin-upload-session"
	if err := st.CreateSession(ctx, adminUser.ID, sessionToken, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("image", "linux-diagram.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}

	pngBytes := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
		0x89, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9C, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
		0x00, 0x03, 0x01, 0x01, 0x00, 0x18, 0xDD, 0x8D,
		0x18, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E,
		0x44, 0xAE, 0x42, 0x60, 0x82,
	}
	if _, err := part.Write(pngBytes); err != nil {
		t.Fatalf("write png payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	request, err := http.NewRequest(http.MethodPost, server.URL+"/admin/uploads/images", &body)
	if err != nil {
		t.Fatalf("build upload request: %v", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
	addCSRFCookieAndHeader(request)

	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("upload request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected upload status: %d", response.StatusCode)
	}

	var payload struct {
		Path     string `json:"path"`
		Markdown string `json:"markdown"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode upload payload: %v", err)
	}

	if !strings.HasPrefix(payload.Path, "/uploads/editor/") {
		t.Fatalf("unexpected upload path: %s", payload.Path)
	}
	if !strings.Contains(payload.Markdown, payload.Path) {
		t.Fatalf("unexpected markdown payload: %s", payload.Markdown)
	}

	storedPath := filepath.Join(uploadsDir, filepath.FromSlash(strings.TrimPrefix(payload.Path, "/uploads/")))
	if _, err := os.Stat(storedPath); err != nil {
		t.Fatalf("expected uploaded file to exist at %s: %v", storedPath, err)
	}
}

func TestAdminCanManageUsers(t *testing.T) {
	t.Parallel()

	testApp, st := newTestApp(t, testAppOptions{})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	ctx := context.Background()
	adminUser, err := st.CreateUser(ctx, "root_ops", "admin@example.com", "hash")
	if err != nil {
		t.Fatalf("create admin user: %v", err)
	}
	if err := st.SetUserAdmin(ctx, adminUser.ID, true); err != nil {
		t.Fatalf("promote admin user: %v", err)
	}

	member, err := st.CreateUser(ctx, "bash_bandit", "member@example.com", "hash")
	if err != nil {
		t.Fatalf("create member user: %v", err)
	}

	const sessionToken = "admin-session-token"
	if err := st.CreateSession(ctx, adminUser.ID, sessionToken, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	indexRequest, err := http.NewRequest(http.MethodGet, server.URL+"/admin/users", nil)
	if err != nil {
		t.Fatalf("build admin request: %v", err)
	}
	indexRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})

	indexResponse, err := client.Do(indexRequest)
	if err != nil {
		t.Fatalf("admin page request: %v", err)
	}
	defer indexResponse.Body.Close()

	if indexResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected admin users page status: %d", indexResponse.StatusCode)
	}

	if body := readBody(t, indexResponse.Body); !strings.Contains(body, "member@example.com") {
		t.Fatalf("member missing from admin page: %s", body)
	}

	promoteRequest, err := http.NewRequest(http.MethodPost, server.URL+"/admin/users/"+strconv.FormatInt(member.ID, 10)+"/admin", strings.NewReader("value=1"))
	if err != nil {
		t.Fatalf("build promote request: %v", err)
	}
	promoteRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	promoteRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
	addCSRFCookieAndHeader(promoteRequest)

	promoteResponse, err := client.Do(promoteRequest)
	if err != nil {
		t.Fatalf("promote request: %v", err)
	}
	defer promoteResponse.Body.Close()

	if promoteResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf("unexpected promote status: %d", promoteResponse.StatusCode)
	}

	member, err = st.UserByID(ctx, member.ID)
	if err != nil {
		t.Fatalf("reload promoted user: %v", err)
	}
	if !member.IsAdmin {
		t.Fatalf("expected user to become admin")
	}

	banRequest, err := http.NewRequest(http.MethodPost, server.URL+"/admin/users/"+strconv.FormatInt(member.ID, 10)+"/ban", strings.NewReader("value=1"))
	if err != nil {
		t.Fatalf("build ban request: %v", err)
	}
	banRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	banRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
	addCSRFCookieAndHeader(banRequest)

	banResponse, err := client.Do(banRequest)
	if err != nil {
		t.Fatalf("ban request: %v", err)
	}
	defer banResponse.Body.Close()

	if banResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf("unexpected ban status: %d", banResponse.StatusCode)
	}

	member, err = st.UserByID(ctx, member.ID)
	if err != nil {
		t.Fatalf("reload banned user: %v", err)
	}
	if !member.IsBanned {
		t.Fatalf("expected user to become banned")
	}

	deleteRequest, err := http.NewRequest(http.MethodPost, server.URL+"/admin/users/"+strconv.FormatInt(member.ID, 10)+"/delete", nil)
	if err != nil {
		t.Fatalf("build delete request: %v", err)
	}
	deleteRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
	addCSRFCookieAndHeader(deleteRequest)

	deleteResponse, err := client.Do(deleteRequest)
	if err != nil {
		t.Fatalf("delete request: %v", err)
	}
	defer deleteResponse.Body.Close()

	if deleteResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf("unexpected delete status: %d", deleteResponse.StatusCode)
	}

	if _, err := st.UserByID(ctx, member.ID); !errors.Is(err, store.ErrUserNotFound) {
		t.Fatalf("expected deleted user to be gone, got %v", err)
	}

	logsRequest, err := http.NewRequest(http.MethodGet, server.URL+"/admin/logs", nil)
	if err != nil {
		t.Fatalf("build audit logs request: %v", err)
	}
	logsRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})

	logsResponse, err := client.Do(logsRequest)
	if err != nil {
		t.Fatalf("audit logs request: %v", err)
	}
	defer logsResponse.Body.Close()

	if logsResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected audit logs page status: %d", logsResponse.StatusCode)
	}

	logsBody := readBody(t, logsResponse.Body)
	for _, fragment := range []string{"admin@example.com", "member@example.com"} {
		if !strings.Contains(logsBody, fragment) {
			t.Fatalf("expected audit logs page to contain %q, got: %s", fragment, logsBody)
		}
	}
}

func TestBannedUserCannotLogin(t *testing.T) {
	t.Parallel()

	testApp, st := newTestApp(t, testAppOptions{})
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("supersecret123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	user, err := st.CreateUser(context.Background(), "frozen_ops", "banned@example.com", string(passwordHash))
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.SetUserBanned(context.Background(), user.ID, true); err != nil {
		t.Fatalf("ban user: %v", err)
	}

	response, err := postFormWithCSRF(server.Client(), server.URL+"/login", url.Values{
		"email":    {"banned@example.com"},
		"password": {"supersecret123"},
	})
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("unexpected login status for banned user: %d", response.StatusCode)
	}

	if body := readBody(t, response.Body); !strings.Contains(body, "заморожен") {
		t.Fatalf("banned user message missing: %s", body)
	}
}

type testAppOptions struct {
	mailer         ConfirmationMailer
	notifier       AdminApprovalNotifier
	webhookSecret  string
	articleDir     string
	uploadsDir     string
	deployLockPath string
}

func newTestApp(t *testing.T, options testAppOptions) (*App, *store.Store) {
	t.Helper()

	db := openTestDatabase(t)

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

	var passwordReset *PasswordResetCoordinator
	if options.mailer != nil {
		passwordReset = NewPasswordResetCoordinator(PasswordResetCoordinatorConfig{
			Store:   st,
			Mailer:  options.mailer,
			BaseURL: "",
		})
	}

	var articles *content.Library
	if options.articleDir != "" {
		articles = content.NewLibrary(options.articleDir)
	}

	uploadsDir := options.uploadsDir
	if uploadsDir == "" {
		uploadsDir = filepath.Join(t.TempDir(), "uploads")
	}
	if err := os.MkdirAll(uploadsDir, 0o755); err != nil {
		t.Fatalf("create uploads dir: %v", err)
	}

	app, err := New(st, Config{
		Registration:          registration,
		PasswordReset:         passwordReset,
		TelegramWebhookSecret: options.webhookSecret,
		Articles:              articles,
		UploadsDir:            uploadsDir,
		DeployLockPath:        options.deployLockPath,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}

	return app, st
}

func openTestDatabase(t *testing.T) *sql.DB {
	t.Helper()

	adminDB, err := sql.Open("pgx", testPostgresConfig.GetConnectionURL())
	if err != nil {
		t.Fatalf("open postgres admin db: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := adminDB.PingContext(ctx); err != nil {
		_ = adminDB.Close()
		t.Fatalf("ping postgres admin db: %v", err)
	}

	dbName := fmt.Sprintf("grep_offer_test_%d", testDatabaseCount.Add(1))
	if _, err := adminDB.ExecContext(ctx, fmt.Sprintf(`CREATE DATABASE "%s"`, dbName)); err != nil {
		_ = adminDB.Close()
		t.Fatalf("create postgres test database %s: %v", dbName, err)
	}
	if err := adminDB.Close(); err != nil {
		t.Fatalf("close postgres admin db: %v", err)
	}

	databaseConfig := testPostgresConfig.Database(dbName)
	db, err := sql.Open("pgx", databaseConfig.GetConnectionURL())
	if err != nil {
		t.Fatalf("open postgres test db: %v", err)
	}

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		t.Fatalf("ping postgres test db: %v", err)
	}

	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()

		adminDB, err := sql.Open("pgx", testPostgresConfig.GetConnectionURL())
		if err != nil {
			return
		}
		defer adminDB.Close()

		if _, err := adminDB.ExecContext(dropCtx, fmt.Sprintf(`DROP DATABASE IF EXISTS "%s" WITH (FORCE)`, dbName)); err == nil {
			return
		}

		_, _ = adminDB.ExecContext(dropCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, dbName)
		_, _ = adminDB.ExecContext(dropCtx, fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, dbName))
	})
	t.Cleanup(func() {
		_ = db.Close()
	})

	return db
}

func readBody(t *testing.T, body io.Reader) string {
	t.Helper()

	content, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return string(content)
}

func postFormWithCSRF(client *http.Client, targetURL string, form url.Values) (*http.Response, error) {
	if form == nil {
		form = url.Values{}
	}

	request, err := http.NewRequest(http.MethodPost, targetURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}

	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addCSRFCookieAndHeader(request)
	return client.Do(request)
}

func addCSRFCookieAndHeader(request *http.Request) {
	request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: testCSRFToken()})
	request.Header.Set(csrfHeaderName, testCSRFToken())
}

func testCSRFToken() string {
	return "csrf-token-for-tests-only-1234567890"
}

type fakeConfirmationMailer struct {
	sent      []sentConfirmation
	resetSent []sentPasswordReset
}

type sentConfirmation struct {
	Email      string
	Username   string
	ConfirmURL string
}

type sentPasswordReset struct {
	Email    string
	Username string
	ResetURL string
}

func (f *fakeConfirmationMailer) SendRegistrationConfirmation(_ context.Context, email, username, confirmURL string) error {
	f.sent = append(f.sent, sentConfirmation{
		Email:      email,
		Username:   username,
		ConfirmURL: confirmURL,
	})
	return nil
}

func (f *fakeConfirmationMailer) SendPasswordReset(_ context.Context, email, username, resetURL string) error {
	f.resetSent = append(f.resetSent, sentPasswordReset{
		Email:    email,
		Username: username,
		ResetURL: resetURL,
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
