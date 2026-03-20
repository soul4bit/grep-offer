package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"grep-offer/internal/content"
	"grep-offer/internal/store"

	"golang.org/x/crypto/bcrypt"
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

	response, err := server.Client().PostForm(server.URL+"/register", form)
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

	firstResponse, err := server.Client().PostForm(server.URL+"/register", firstForm)
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

	secondResponse, err := server.Client().PostForm(server.URL+"/register", secondForm)
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

	requestResponse, err := client.PostForm(server.URL+"/password/forgot", url.Values{
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

	resetResponse, err := client.PostForm(server.URL+"/password/reset", url.Values{
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

func TestArticlesRoutesRenderMarkdown(t *testing.T) {
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
order: 1
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

	indexResponse, err := server.Client().Get(server.URL + "/articles")
	if err != nil {
		t.Fatalf("articles index request: %v", err)
	}
	defer indexResponse.Body.Close()

	if indexResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected articles index status: %d", indexResponse.StatusCode)
	}

	indexBody := readBody(t, indexResponse.Body)
	if !strings.Contains(indexBody, "Docker без религии") {
		t.Fatalf("article title missing on index: %s", indexBody)
	}

	showResponse, err := server.Client().Get(server.URL + "/articles/docker-without-religion")
	if err != nil {
		t.Fatalf("article show request: %v", err)
	}
	defer showResponse.Body.Close()

	if showResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected article show status: %d", showResponse.StatusCode)
	}

	showBody := readBody(t, showResponse.Body)
	if !strings.Contains(showBody, "<h1>Docker без религии</h1>") {
		t.Fatalf("rendered markdown heading missing: %s", showBody)
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

	indexRequest, err := http.NewRequest(http.MethodGet, server.URL+"/admin", nil)
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
		t.Fatalf("unexpected admin page status: %d", indexResponse.StatusCode)
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

	response, err := server.Client().PostForm(server.URL+"/login", url.Values{
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
	mailer        ConfirmationMailer
	notifier      AdminApprovalNotifier
	webhookSecret string
	articleDir    string
	uploadsDir    string
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
