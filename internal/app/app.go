package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"grep-offer/internal/store"
	"grep-offer/internal/ui"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookieName = "grep_offer_session"
	sessionTTL        = 7 * 24 * time.Hour
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

type contextKey string

const currentUserKey contextKey = "currentUser"

type App struct {
	store     *store.Store
	templates map[string]*template.Template
	static    http.Handler
}

type ViewData struct {
	CurrentUser *store.User
	Error       string
	Notice      string
	Form        AuthForm
	Roadmap     []string
}

type AuthForm struct {
	Username string
	Email    string
}

func New(st *store.Store) (*App, error) {
	templates, err := loadTemplates()
	if err != nil {
		return nil, err
	}

	staticFS, err := fs.Sub(ui.Files, "static")
	if err != nil {
		return nil, fmt.Errorf("load static fs: %w", err)
	}

	return &App{
		store:     st,
		templates: templates,
		static:    http.FileServer(http.FS(staticFS)),
	}, nil
}

func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", a.static))
	mux.HandleFunc("GET /healthz", a.handleHealth)
	mux.HandleFunc("GET /", a.handleHome)
	mux.HandleFunc("GET /dashboard", a.handleDashboard)
	mux.HandleFunc("GET /register", a.handleRegisterForm)
	mux.HandleFunc("POST /register", a.handleRegisterSubmit)
	mux.HandleFunc("GET /login", a.handleLoginForm)
	mux.HandleFunc("POST /login", a.handleLoginSubmit)
	mux.HandleFunc("POST /logout", a.handleLogout)
	return a.withCurrentUser(mux)
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

func (a *App) handleHome(w http.ResponseWriter, r *http.Request) {
	if a.currentUser(r) != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	data := ViewData{
		Notice: noticeFromRequest(r),
		Roadmap: []string{
			"Фундамент: Linux, bash, git, сеть и привычка читать логи, а не гадать.",
			"Доставка: Docker, registry, CI/CD и деплой без классики \"но локально же работало\".",
			"Платформа: Kubernetes, observability, Terraform и аккуратная работа с облаком.",
			"Оффер: резюме, собесы и разговор о деньгах без инфоцыганских фанфар.",
		},
	}

	a.render(w, r, http.StatusOK, "home", data)
}

func (a *App) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := a.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login?notice=login-required", http.StatusSeeOther)
		return
	}

	data := ViewData{
		CurrentUser: user,
		Notice:      noticeFromRequest(r),
		Roadmap: []string{
			"Поднять профиль: linux, git, bash, tcp/ip.",
			"Накатить доставку: docker, registry, CI/CD, healthchecks.",
			"Разобрать оркестрацию: k8s, helm, ingress, observability.",
			"Закрыть инфраструктуру: terraform, cloud, secrets, backup.",
		},
	}

	a.render(w, r, http.StatusOK, "dashboard", data)
}

func (a *App) handleRegisterForm(w http.ResponseWriter, r *http.Request) {
	if a.currentUser(r) != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	a.render(w, r, http.StatusOK, "register", ViewData{
		Notice: noticeFromRequest(r),
	})
}

func (a *App) handleRegisterSubmit(w http.ResponseWriter, r *http.Request) {
	if a.currentUser(r) != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")

	data := ViewData{
		Form: AuthForm{
			Username: username,
			Email:    email,
		},
	}

	if validationError := validateRegistration(username, email, password); validationError != "" {
		data.Error = validationError
		a.render(w, r, http.StatusUnprocessableEntity, "register", data)
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "password hashing failed", http.StatusInternalServerError)
		return
	}

	user, err := a.store.CreateUser(r.Context(), username, email, string(passwordHash))
	if err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			data.Error = "Такой email уже занят. Значит, кто-то уже пошел грести оффер."
			a.render(w, r, http.StatusConflict, "register", data)
			return
		}

		http.Error(w, "create user failed", http.StatusInternalServerError)
		return
	}

	if err := a.issueSession(r.Context(), w, r, user.ID); err != nil {
		http.Error(w, "create session failed", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/dashboard?notice=welcome", http.StatusSeeOther)
}

func (a *App) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if a.currentUser(r) != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	a.render(w, r, http.StatusOK, "login", ViewData{
		Notice: noticeFromRequest(r),
	})
}

func (a *App) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if a.currentUser(r) != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")

	data := ViewData{
		Form: AuthForm{
			Email: email,
		},
	}

	if validationError := validateLogin(email, password); validationError != "" {
		data.Error = validationError
		a.render(w, r, http.StatusUnprocessableEntity, "login", data)
		return
	}

	user, err := a.store.UserByEmail(r.Context(), email)
	if err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			data.Error = "Почта или пароль не совпали. grep ничего не нашел."
			a.render(w, r, http.StatusUnauthorized, "login", data)
			return
		}

		http.Error(w, "load user failed", http.StatusInternalServerError)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		data.Error = "Почта или пароль не совпали. grep ничего не нашел."
		a.render(w, r, http.StatusUnauthorized, "login", data)
		return
	}

	if err := a.issueSession(r.Context(), w, r, user.ID); err != nil {
		http.Error(w, "create session failed", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/dashboard?notice=logged-in", http.StatusSeeOther)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		if err := a.store.DeleteSession(r.Context(), cookie.Value); err != nil && !errors.Is(err, store.ErrSessionNotFound) {
			log.Printf("delete session: %v", err)
		}
	}

	a.clearSessionCookie(w)
	http.Redirect(w, r, "/login?notice=logged-out", http.StatusSeeOther)
}

func (a *App) withCurrentUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := a.userFromRequest(r)
		if err != nil {
			if errors.Is(err, store.ErrSessionNotFound) {
				a.clearSessionCookie(w)
			} else {
				log.Printf("resolve current user: %v", err)
			}
		}

		ctx := context.WithValue(r.Context(), currentUserKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *App) userFromRequest(r *http.Request) (*store.User, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		if errors.Is(err, http.ErrNoCookie) {
			return nil, nil
		}
		return nil, err
	}

	if cookie.Value == "" {
		return nil, nil
	}

	return a.store.UserBySession(r.Context(), cookie.Value)
}

func (a *App) currentUser(r *http.Request) *store.User {
	user, _ := r.Context().Value(currentUserKey).(*store.User)
	return user
}

func (a *App) issueSession(ctx context.Context, w http.ResponseWriter, r *http.Request, userID int64) error {
	token, err := generateSessionToken()
	if err != nil {
		return err
	}

	expiresAt := time.Now().UTC().Add(sessionTTL)
	if err := a.store.CreateSession(ctx, userID, token, expiresAt); err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
	})

	return nil
}

func (a *App) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *App) render(w http.ResponseWriter, r *http.Request, status int, name string, data ViewData) {
	tmpl, ok := a.templates[name]
	if !ok {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}

	if data.CurrentUser == nil {
		data.CurrentUser = a.currentUser(r)
	}

	var buffer bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buffer, "base", data); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buffer.WriteTo(w); err != nil {
		log.Printf("write response: %v", err)
	}
}

func loadTemplates() (map[string]*template.Template, error) {
	cache := make(map[string]*template.Template)
	pages := []string{"home", "login", "register", "dashboard"}

	for _, page := range pages {
		tmpl, err := template.ParseFS(
			ui.Files,
			"templates/base.html",
			fmt.Sprintf("templates/%s.html", page),
		)
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", page, err)
		}

		cache[page] = tmpl
	}

	return cache, nil
}

func validateRegistration(username, email, password string) string {
	switch {
	case len(username) < 3:
		return "Ник должен быть хотя бы из 3 символов."
	case len(username) > 24:
		return "Ник длиннее 24 символов уже выглядит как плохой hostname."
	case !usernamePattern.MatchString(username):
		return "Ник пока только в ASCII: буквы, цифры, точка, дефис и нижнее подчеркивание."
	}

	if err := validateEmail(email); err != nil {
		return "Email выглядит подозрительно. Нужен нормальный адрес."
	}

	if len(password) < 8 {
		return "Пароль короче 8 символов. Так нас даже junior brute-force засмеет."
	}

	return ""
}

func validateLogin(email, password string) string {
	if err := validateEmail(email); err != nil {
		return "Нужен валидный email."
	}

	if strings.TrimSpace(password) == "" {
		return "Пароль не может быть пустым."
	}

	return ""
}

func validateEmail(email string) error {
	address, err := mail.ParseAddress(strings.TrimSpace(email))
	if err != nil {
		return err
	}

	if address.Address != strings.TrimSpace(email) {
		return errors.New("unexpected display name")
	}

	return nil
}

func generateSessionToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func noticeFromRequest(r *http.Request) string {
	switch r.URL.Query().Get("notice") {
	case "login-required":
		return "Сначала войди. Роадмап офферов сам себя не посмотрит."
	case "welcome":
		return "Аккаунт создан. Теперь можно строить путь из bash-тыка в DevOps."
	case "logged-in":
		return "Сессия активна. Продолжаем путь к офферу."
	case "logged-out":
		return "Сессия завершена. Никаких хвостов в проде."
	default:
		return ""
	}
}
