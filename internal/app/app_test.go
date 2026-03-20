package app

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"grep-offer/internal/store"

	_ "modernc.org/sqlite"
)

func TestRegisterAndLogoutFlow(t *testing.T) {
	t.Parallel()

	testApp := newTestApp(t)
	server := httptest.NewServer(testApp.Routes())
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}

	client := server.Client()
	client.Jar = jar

	form := url.Values{
		"username": {"bash_bandit"},
		"email":    {"smoke@example.com"},
		"password": {"supersecret123"},
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
	if !strings.Contains(registerBody, "Привет, bash_bandit") {
		t.Fatalf("dashboard greeting missing: %s", registerBody)
	}

	logoutRequest, err := http.NewRequest(http.MethodPost, server.URL+"/logout", nil)
	if err != nil {
		t.Fatalf("create logout request: %v", err)
	}

	logoutResponse, err := client.Do(logoutRequest)
	if err != nil {
		t.Fatalf("logout request: %v", err)
	}
	defer logoutResponse.Body.Close()

	if logoutResponse.StatusCode != http.StatusOK {
		t.Fatalf("unexpected logout status: %d", logoutResponse.StatusCode)
	}

	logoutBody := readBody(t, logoutResponse.Body)
	if !strings.Contains(logoutBody, "Сессия завершена") {
		t.Fatalf("logout notice missing: %s", logoutBody)
	}
}

func TestHealthz(t *testing.T) {
	t.Parallel()

	testApp := newTestApp(t)
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

func newTestApp(t *testing.T) *App {
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

	app, err := New(st)
	if err != nil {
		t.Fatalf("create app: %v", err)
	}

	return app
}

func readBody(t *testing.T, body io.Reader) string {
	t.Helper()

	content, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return string(content)
}
