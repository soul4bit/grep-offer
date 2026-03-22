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
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"grep-offer/internal/content"
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
	store                 *store.Store
	templates             map[string]*template.Template
	static                http.Handler
	uploads               http.Handler
	uploadsDir            string
	deployLockPath        string
	rateLimiter           *rateLimiter
	articles              *content.Library
	registration          *RegistrationCoordinator
	passwordReset         *PasswordResetCoordinator
	telegramWebhookSecret string
	bootstrapAdminEmails  map[string]struct{}
}

type Config struct {
	Registration          *RegistrationCoordinator
	PasswordReset         *PasswordResetCoordinator
	TelegramWebhookSecret string
	Articles              *content.Library
	UploadsDir            string
	DeployLockPath        string
	BootstrapAdminEmails  []string
}

type ViewData struct {
	CurrentUser         *store.User
	Error               string
	Notice              string
	DeployLocked        bool
	DeployLockMessage   string
	CSRFToken           string
	CSPNonce            string
	AdminSection        string
	AdminNav            []AdminNavItem
	Form                AuthForm
	PasswordResetToken  string
	LandingRoadmap      []LandingStage
	FeaturedArticles    []ArticleCard
	CourseModules       []CourseModule
	CourseProgress      CourseProgressView
	Articles            []ArticleCard
	Article             *ArticlePage
	AdminUsers          []AdminUserRow
	AdminArticles       []AdminArticleRow
	AdminArticleGroups  []AdminArticleGroup
	AdminArticleForm    AdminArticleForm
	AdminArticleOptions AdminArticleOptions
	AdminTestLessons    []AdminLessonOption
	AdminTestQuestions  []AdminTestQuestionRow
	AdminAuditLogs      []AdminAuditLogRow
	DashboardStats      []DashboardStat
	DashboardStages     []DashboardStage
	DashboardFocus      DashboardFocus
}

type AuthForm struct {
	Username string
	Email    string
}

type LandingStage struct {
	Index   string
	Title   string
	Badge   string
	Summary string
	Note    string
}

type ArticleCard struct {
	Title       string
	Slug        string
	Summary     string
	Badge       string
	Stage       string
	Module      string
	KindKey     string
	Kind        string
	Index       string
	ReadingTime string
	Read        bool
	Complete    bool
	Locked      bool
}

type CourseModule struct {
	Index       string
	Title       string
	Lessons     []ArticleCard
	ReadCount   int
	TotalCount  int
	Percent     int
	PassedCount int
	TotalTests  int
}

type CourseProgressView struct {
	ReadCount    int
	TotalLessons int
	PassedCount  int
	TotalTests   int
	Percent      int
	NextSlug     string
	NextTitle    string
	ContinueHref string
}

type ArticleNav struct {
	Title string
	Slug  string
	Index string
}

type ArticlePage struct {
	Title            string
	Slug             string
	Summary          string
	Badge            string
	Stage            string
	Module           string
	KindKey          string
	Kind             string
	Index            string
	ReadingTime      string
	Prev             *ArticleNav
	Next             *ArticleNav
	ModuleItems      []ArticleCard
	HTML             template.HTML
	Read             bool
	Passed           bool
	Locked           bool
	ModuleReadCount  int
	ModuleTotalCount int
	ModulePercent    int
	IsTest           bool
	Quiz             *LessonQuizView
	TestResult       *LessonTestResultView
}

type AdminUserRow struct {
	ID            int64
	Username      string
	Email         string
	IsAdmin       bool
	IsBanned      bool
	IsCurrentUser bool
	CreatedLabel  string
}

type AdminArticleRow struct {
	Stage        string
	Title        string
	Slug         string
	Module       string
	KindKey      string
	Kind         string
	Index        string
	ModuleOrder  int
	BlockOrder   int
	Published    bool
	UpdatedLabel string
}

type AdminArticleGroup struct {
	Stage          string
	Module         string
	ModuleOrder    int
	ModuleIndex    string
	LessonCount    int
	PublishedCount int
	Lessons        []AdminArticleRow
}

type AdminArticleForm struct {
	OriginalSlug  string
	Title         string
	Slug          string
	Summary       string
	Badge         string
	Stage         string
	Module        string
	Kind          string
	Body          string
	ModuleOrder   int
	BlockOrder    int
	Published     bool
	ModeLabel     string
	FileName      string
	LearnPath     string
	OpenLearnPath string
	WordCount     int
	LineCount     int
	KindHint      string
	PreviewHTML   template.HTML
}

type AdminArticleOptions struct {
	GlobalNextModuleOrder int
	Stages                []AdminStageOption
}

type AdminStageOption struct {
	Value           string
	NextModuleOrder int
	Modules         []AdminModuleOption
}

type AdminModuleOption struct {
	Value          string
	ModuleOrder    int
	NextBlockOrder int
}

type AdminLessonOption struct {
	Slug  string
	Title string
}

type AdminNavItem struct {
	Key    string
	Label  string
	Href   string
	Active bool
}

type AdminTestQuestionRow struct {
	ID          int64
	LessonSlug  string
	LessonTitle string
	Prompt      string
	Options     []string
	AnswerLabel string
}

type AdminAuditLogRow struct {
	Scope        string
	Action       string
	ActorLabel   string
	TargetLabel  string
	Status       string
	StatusTone   string
	Details      string
	IPAddress    string
	UserAgent    string
	CreatedLabel string
}

type DashboardStat struct {
	Value string
	Label string
}

type DashboardStage struct {
	Index       string
	Title       string
	Badge       string
	Summary     string
	Status      string
	StatusTone  string
	Percent     int
	DoneCount   int
	TotalCount  int
	Checkpoints []DashboardCheckpoint
}

type DashboardCheckpoint struct {
	Key   string
	Title string
	Note  string
	Done  bool
}

type DashboardFocus struct {
	Title          string
	Summary        string
	StageLabel     string
	NextCheckpoint string
	Percent        int
	DoneCount      int
	TotalCount     int
}

type LessonQuizView struct {
	Questions []LessonQuizQuestionView
}

type LessonQuizQuestionView struct {
	ID      int64
	Prompt  string
	Options []LessonQuizOptionView
}

type LessonQuizOptionView struct {
	Index int
	Text  string
}

type LessonTestResultView struct {
	AttemptsCount    int
	LastWrongAnswers int
	Passed           bool
}

func New(st *store.Store, cfg Config) (*App, error) {
	templates, err := loadTemplates()
	if err != nil {
		return nil, err
	}

	staticFS, err := fs.Sub(ui.Files, "static")
	if err != nil {
		return nil, fmt.Errorf("load static fs: %w", err)
	}

	uploadsDir := strings.TrimSpace(cfg.UploadsDir)
	var uploads http.Handler
	if uploadsDir != "" {
		uploads = http.FileServer(http.Dir(uploadsDir))
	}

	return &App{
		store:                 st,
		templates:             templates,
		static:                http.FileServer(http.FS(staticFS)),
		uploads:               uploads,
		uploadsDir:            uploadsDir,
		deployLockPath:        strings.TrimSpace(cfg.DeployLockPath),
		rateLimiter:           newRateLimiter(defaultRateLimitRules()),
		articles:              cfg.Articles,
		registration:          cfg.Registration,
		passwordReset:         cfg.PasswordReset,
		telegramWebhookSecret: cfg.TelegramWebhookSecret,
		bootstrapAdminEmails:  normalizeAdminEmails(cfg.BootstrapAdminEmails),
	}, nil
}

func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", a.withCacheControl("public, max-age=3600", http.StripPrefix("/static/", a.static)))
	if a.uploads != nil {
		mux.Handle("GET /uploads/", a.requireAuthenticatedHandler(a.withCacheControl("private, max-age=3600", http.StripPrefix("/uploads/", a.uploads))))
	}
	mux.HandleFunc("GET /healthz", a.handleHealth)
	mux.HandleFunc("GET /", a.handleHome)
	mux.HandleFunc("GET /learn", a.handleArticlesIndex)
	mux.HandleFunc("GET /learn/{slug}", a.handleArticleShow)
	mux.HandleFunc("POST /learn/tests/{slug}", a.handleLessonTestSubmit)
	mux.HandleFunc("GET /articles", a.handleArticlesIndex)
	mux.HandleFunc("GET /articles/{slug}", a.handleArticleShow)
	mux.HandleFunc("GET /dashboard", a.handleDashboard)
	mux.HandleFunc("POST /dashboard/checkpoints", a.handleDashboardCheckpointToggle)
	mux.HandleFunc("GET /admin", a.handleAdminRoot)
	mux.HandleFunc("GET /admin/articles", a.handleAdminArticles)
	mux.HandleFunc("GET /admin/users", a.handleAdminUsers)
	mux.HandleFunc("GET /admin/logs", a.handleAdminLogs)
	mux.HandleFunc("GET /admin/articles/new", a.handleAdminArticleNew)
	mux.HandleFunc("GET /admin/articles/slug-check", a.handleAdminArticleSlugCheck)
	mux.HandleFunc("GET /admin/articles/{slug}/edit", a.handleAdminArticleEdit)
	mux.HandleFunc("GET /admin/articles/{slug}/duplicate", a.handleAdminArticleDuplicate)
	mux.HandleFunc("POST /admin/articles", a.handleAdminArticleSave)
	mux.HandleFunc("POST /admin/articles/{slug}/delete", a.handleAdminArticleDelete)
	mux.HandleFunc("POST /admin/articles/preview", a.handleAdminArticlePreview)
	mux.HandleFunc("POST /admin/articles/reorder", a.handleAdminArticleReorder)
	mux.HandleFunc("POST /admin/uploads/images", a.handleAdminImageUpload)
	mux.HandleFunc("POST /admin/users/{id}/admin", a.handleAdminUserAdmin)
	mux.HandleFunc("POST /admin/users/{id}/ban", a.handleAdminUserBan)
	mux.HandleFunc("POST /admin/users/{id}/delete", a.handleAdminUserDelete)
	mux.HandleFunc("POST /admin/tests/questions", a.handleAdminTestQuestionCreate)
	mux.HandleFunc("POST /admin/tests/questions/{id}/delete", a.handleAdminTestQuestionDelete)
	mux.HandleFunc("GET /register", a.handleRegisterForm)
	mux.HandleFunc("POST /register", a.handleRegisterSubmit)
	mux.HandleFunc("GET /register/confirm", a.handleRegisterConfirm)
	mux.HandleFunc("GET /login", a.handleLoginForm)
	mux.HandleFunc("POST /login", a.handleLoginSubmit)
	mux.HandleFunc("GET /password/forgot", a.handleForgotPasswordForm)
	mux.HandleFunc("POST /password/forgot", a.handleForgotPasswordSubmit)
	mux.HandleFunc("GET /password/reset", a.handlePasswordResetForm)
	mux.HandleFunc("POST /password/reset", a.handlePasswordResetSubmit)
	mux.HandleFunc("POST /logout", a.handleLogout)
	mux.HandleFunc("POST /telegram/webhook", a.handleTelegramWebhook)
	return a.withSecurityHeaders(a.withRateLimit(a.withDeployLock(a.withCSRFProtection(a.withCurrentUser(mux)))))
}

func (a *App) requireAuthenticatedHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.currentUser(r) == nil {
			http.Redirect(w, r, "/login?notice=login-required", http.StatusSeeOther)
			return
		}

		next.ServeHTTP(w, r)
	})
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

	featuredArticles, err := a.loadFeaturedArticles(3)
	if err != nil {
		http.Error(w, "load articles failed", http.StatusInternalServerError)
		return
	}

	data := ViewData{
		Notice:           noticeFromRequest(r),
		FeaturedArticles: featuredArticles,
		LandingRoadmap: []LandingStage{
			{
				Index:   "01",
				Title:   "Фундамент",
				Badge:   "linux / bash / git",
				Summary: "Собираешь базу: терминал, сеть, процессы, файлы и привычку не паниковать от логов.",
				Note:    "Без этого любой модный стек сверху просто ломается дороже и загадочнее.",
			},
			{
				Index:   "02",
				Title:   "Доставка",
				Badge:   "docker / ci-cd / deploy",
				Summary: "Понимаешь, как код едет от коммита до сервера и где по дороге чаще всего все начинает гореть.",
				Note:    "Именно тут исчезает наивная вера в фразу «у меня локально работало».",
			},
			{
				Index:   "03",
				Title:   "Платформа",
				Badge:   "k8s / terraform / observability",
				Summary: "Подключаешь оркестрацию, инфраструктуру и наблюдаемость без попытки называть магией обычную эксплуатацию.",
				Note:    "Сначала понимание систем, потом Kubernetes. Иначе получится дорогой квест.",
			},
			{
				Index:   "04",
				Title:   "Оффер",
				Badge:   "cv / interviews / money",
				Summary: "Упаковываешь опыт, проходишь собесы и разговариваешь о деньгах уже с нормальной опорой на практику.",
				Note:    "Не инфоцыганский финал, а обычный рабочий результат последовательного пути.",
			},
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

	stats, focus, stages, err := a.loadDashboardView(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "load dashboard failed", http.StatusInternalServerError)
		return
	}

	courseProgress, err := a.loadCourseOverview(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "load course progress failed", http.StatusInternalServerError)
		return
	}
	if courseProgress.TotalLessons > 0 {
		stats = []DashboardStat{
			{
				Value: formatProgress(courseProgress.PassedCount, courseProgress.TotalTests),
				Label: "тестов Linux",
			},
			{
				Value: formatProgress(courseProgress.ReadCount, courseProgress.TotalLessons),
				Label: "прочитано блоков",
			},
			{
				Value: currentValueOrFallback(courseProgress.NextTitle, "маршрут закрыт"),
				Label: "следующий блок",
			},
			{
				Value: formatPercent(courseProgress.Percent),
				Label: "прогресс Linux",
			},
		}
		focus.Percent = courseProgress.Percent
	}

	data := ViewData{
		CurrentUser:     user,
		Notice:          noticeFromRequest(r),
		DashboardStats:  stats,
		DashboardFocus:  focus,
		DashboardStages: stages,
		CourseProgress:  courseProgress,
	}

	a.render(w, r, http.StatusOK, "dashboard", data)
}

func buildDashboardView() ([]DashboardStat, DashboardFocus, []DashboardStage) {
	stages := []DashboardStage{
		{
			Index:   "01",
			Title:   "Фундамент",
			Badge:   "linux / bash / git / network",
			Summary: "Собираешь базу: терминал, процессы, сеть и привычку не гадать по логам.",
			Checkpoints: []DashboardCheckpoint{
				{Title: "Навигация по Linux без паники", Note: "Файлы, права, процессы, systemd и package manager без магии.", Done: true},
				{Title: "Bash как рабочий инструмент", Note: "Pipe, redirection, grep, sed, env и привычка читать man.", Done: true},
				{Title: "Git и сеть без белой магии", Note: "SSH, remote, DNS, curl, ss и разбор обычных поломок.", Done: true},
			},
		},
		{
			Index:   "02",
			Title:   "Доставка",
			Badge:   "docker / ci-cd / deploy",
			Summary: "Понимаешь, как код едет от коммита до сервера и где по дороге все обычно горит.",
			Checkpoints: []DashboardCheckpoint{
				{Title: "Собрать образ без шаманства", Note: "Dockerfile, layers, registry и разница между build и run.", Done: true},
				{Title: "Положить CI на рельсы", Note: "Pipeline, тесты, артефакты и нормальные healthchecks.", Done: false},
				{Title: "Довезти deploy до предсказуемости", Note: "Rollback, env, секреты и понимание, где обычно рвется цепочка.", Done: false},
			},
		},
		{
			Index:   "03",
			Title:   "Платформа",
			Badge:   "k8s / terraform / observability",
			Summary: "Подключаешь оркестрацию, инфраструктуру и наблюдаемость без культа YAML.",
			Checkpoints: []DashboardCheckpoint{
				{Title: "Понять orchestration, а не просто выучить YAML", Note: "Pods, services, ingress и что именно они решают.", Done: false},
				{Title: "Наблюдать систему, а не надеяться", Note: "Logs, metrics, traces, alerts и что реально смотреть при инциденте.", Done: false},
				{Title: "Описывать инфраструктуру как код", Note: "Terraform, state, secrets и аккуратная работа с cloud-ресурсами.", Done: false},
			},
		},
		{
			Index:   "04",
			Title:   "Оффер",
			Badge:   "cv / interview / offer",
			Summary: "Упаковываешь опыт, проходишь собесы и разговариваешь про деньги уже с реальной опорой.",
			Checkpoints: []DashboardCheckpoint{
				{Title: "Собрать резюме вокруг реальных задач", Note: "Что делал, что ломалось, что улучшил и какой был эффект.", Done: false},
				{Title: "Подготовить техразговор без легенд", Note: "Архитектура, инциденты, delivery, надежность и компромиссы.", Done: false},
				{Title: "Договориться об оффере без тумана", Note: "Деньги, ожидания, зона ответственности и следующий уровень роста.", Done: false},
			},
		},
	}

	totalCheckpoints := 0
	doneCheckpoints := 0
	currentStageIndex := len(stages) - 1
	foundActive := false

	for i := range stages {
		total := len(stages[i].Checkpoints)
		done := 0

		for _, checkpoint := range stages[i].Checkpoints {
			totalCheckpoints++
			if checkpoint.Done {
				done++
				doneCheckpoints++
			}
		}

		stages[i].DoneCount = done
		stages[i].TotalCount = total
		if total > 0 {
			stages[i].Percent = done * 100 / total
		}

		switch {
		case done == total:
			stages[i].Status = "готово"
			stages[i].StatusTone = "done"
		case !foundActive:
			stages[i].Status = "в работе"
			stages[i].StatusTone = "active"
			currentStageIndex = i
			foundActive = true
		default:
			stages[i].Status = "в очереди"
			stages[i].StatusTone = "queued"
		}
	}

	currentStage := stages[currentStageIndex]
	nextCheckpoint := "Все чекпоинты закрыты. Можно идти за оффером."
	for _, checkpoint := range currentStage.Checkpoints {
		if !checkpoint.Done {
			nextCheckpoint = checkpoint.Title
			break
		}
	}

	overallPercent := 0
	if totalCheckpoints > 0 {
		overallPercent = doneCheckpoints * 100 / totalCheckpoints
	}

	stats := []DashboardStat{
		{Value: fmt.Sprintf("%d/%d", doneCheckpoints, totalCheckpoints), Label: "закрыто по маршруту"},
		{Value: currentStage.Title, Label: "текущий этап"},
		{Value: fmt.Sprintf("%d%%", overallPercent), Label: "общий прогресс"},
	}

	focus := DashboardFocus{
		Title:          currentStage.Title,
		Summary:        currentStage.Summary,
		StageLabel:     fmt.Sprintf("этап %d из %d", currentStageIndex+1, len(stages)),
		NextCheckpoint: nextCheckpoint,
		Percent:        overallPercent,
		DoneCount:      doneCheckpoints,
		TotalCount:     totalCheckpoints,
	}

	return stats, focus, stages
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
	confirmPassword := r.FormValue("confirm_password")

	data := ViewData{
		Form: AuthForm{
			Username: username,
			Email:    email,
		},
	}

	if validationError := validateRegistration(username, email, password, confirmPassword); validationError != "" {
		data.Error = validationError
		a.render(w, r, http.StatusUnprocessableEntity, "register", data)
		return
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "password hashing failed", http.StatusInternalServerError)
		return
	}

	if a.registration == nil || !a.registration.Enabled() {
		data.Error = "Регистрация сейчас выключена. Сначала надо настроить апрув в Telegram и письмо с подтверждением."
		a.render(w, r, http.StatusServiceUnavailable, "register", data)
		return
	}

	if err := a.registration.Submit(r.Context(), username, email, string(passwordHash)); err != nil {
		switch {
		case errors.Is(err, store.ErrUsernameTaken):
			a.writeAuditLog(r.Context(), r, nil, store.AuditLogInput{
				Scope:      "registration",
				Action:     "registration_submitted",
				TargetType: "email",
				TargetKey:  email,
				Status:     "warn",
				Details: map[string]string{
					"reason":   "username_taken",
					"username": username,
				},
			})
			data.Error = "Такой ник уже занят. Придется придумать другой alias для похода за оффером."
			a.render(w, r, http.StatusConflict, "register", data)
		case errors.Is(err, store.ErrEmailTaken):
			a.writeAuditLog(r.Context(), r, nil, store.AuditLogInput{
				Scope:      "registration",
				Action:     "registration_submitted",
				TargetType: "email",
				TargetKey:  email,
				Status:     "warn",
				Details: map[string]string{
					"reason":   "email_taken",
					"username": username,
				},
			})
			data.Error = "Такой email уже занят. Значит, кто-то уже пошел грести оффер."
			a.render(w, r, http.StatusConflict, "register", data)
		case errors.Is(err, store.ErrRegistrationPending):
			a.writeAuditLog(r.Context(), r, nil, store.AuditLogInput{
				Scope:      "registration",
				Action:     "registration_submitted",
				TargetType: "email",
				TargetKey:  email,
				Status:     "warn",
				Details: map[string]string{
					"reason":   "pending",
					"username": username,
				},
			})
			data.Error = "Заявка на этот email уже висит. Сначала дождись апрува в Telegram и письма на почту."
			a.render(w, r, http.StatusConflict, "register", data)
		default:
			http.Error(w, "submit registration failed", http.StatusInternalServerError)
		}
		return
	}

	a.writeAuditLog(r.Context(), r, nil, store.AuditLogInput{
		Scope:      "registration",
		Action:     "registration_submitted",
		TargetType: "email",
		TargetKey:  email,
		Details: map[string]string{
			"username": username,
		},
	})

	http.Redirect(w, r, "/register?notice=registration-requested", http.StatusSeeOther)
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
			a.writeAuditLog(r.Context(), r, nil, store.AuditLogInput{
				Scope:      "auth",
				Action:     "login_failed",
				TargetType: "email",
				TargetKey:  email,
				Status:     "warn",
			})
			data.Error = a.loginErrorForEmail(r.Context(), email)
			a.render(w, r, http.StatusUnauthorized, "login", data)
			return
		}

		http.Error(w, "load user failed", http.StatusInternalServerError)
		return
	}

	user, err = a.ensureBootstrapAdmin(r.Context(), user)
	if err != nil {
		http.Error(w, "bootstrap admin failed", http.StatusInternalServerError)
		return
	}

	if user.IsBanned {
		a.writeAuditLog(r.Context(), r, user, store.AuditLogInput{
			Scope:      "auth",
			Action:     "login_failed",
			TargetType: "user",
			TargetKey:  strconv.FormatInt(user.ID, 10),
			Status:     "warn",
			Details: map[string]string{
				"reason": "banned",
				"email":  user.Email,
			},
		})
		data.Error = "Этот доступ заморожен. Админ уже снял этот маршрут с релиза."
		a.render(w, r, http.StatusForbidden, "login", data)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		a.writeAuditLog(r.Context(), r, user, store.AuditLogInput{
			Scope:      "auth",
			Action:     "login_failed",
			TargetType: "user",
			TargetKey:  strconv.FormatInt(user.ID, 10),
			Status:     "warn",
			Details: map[string]string{
				"reason": "bad_password",
				"email":  user.Email,
			},
		})
		data.Error = "Почта или пароль не совпали. grep ничего не нашел."
		a.render(w, r, http.StatusUnauthorized, "login", data)
		return
	}

	if err := a.issueSession(r.Context(), w, r, user.ID); err != nil {
		http.Error(w, "create session failed", http.StatusInternalServerError)
		return
	}

	a.writeAuditLog(r.Context(), r, user, store.AuditLogInput{
		Scope:      "auth",
		Action:     "login_succeeded",
		TargetType: "user",
		TargetKey:  strconv.FormatInt(user.ID, 10),
		Details: map[string]string{
			"email": user.Email,
		},
	})

	http.Redirect(w, r, "/dashboard?notice=logged-in", http.StatusSeeOther)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	user := a.currentUser(r)
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		if err := a.store.DeleteSession(r.Context(), cookie.Value); err != nil && !errors.Is(err, store.ErrSessionNotFound) {
			log.Printf("delete session: %v", err)
		}
	}

	if user != nil {
		a.writeAuditLog(r.Context(), r, user, store.AuditLogInput{
			Scope:      "auth",
			Action:     "logout",
			TargetType: "user",
			TargetKey:  strconv.FormatInt(user.ID, 10),
			Details: map[string]string{
				"email": user.Email,
			},
		})
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

	user, err := a.store.UserBySession(r.Context(), cookie.Value)
	if err != nil {
		return nil, err
	}

	user, err = a.ensureBootstrapAdmin(r.Context(), user)
	if err != nil {
		return nil, err
	}

	if user.IsBanned {
		if deleteErr := a.store.DeleteSession(r.Context(), cookie.Value); deleteErr != nil && !errors.Is(deleteErr, store.ErrSessionNotFound) {
			log.Printf("delete banned session: %v", deleteErr)
		}
		return nil, store.ErrSessionNotFound
	}

	return user, nil
}

func (a *App) currentUser(r *http.Request) *store.User {
	user, _ := r.Context().Value(currentUserKey).(*store.User)
	return user
}

func (a *App) issueSession(ctx context.Context, w http.ResponseWriter, r *http.Request, userID int64) error {
	user, err := a.store.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	user, err = a.ensureBootstrapAdmin(ctx, user)
	if err != nil {
		return err
	}
	if user.IsBanned {
		return store.ErrUserBanned
	}

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
		Secure:   requestIsSecure(r),
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
	if data.CSRFToken == "" {
		data.CSRFToken = a.ensureCSRFCookie(w, r)
	}
	if data.CSPNonce == "" {
		nonce, err := generateSessionToken()
		if err != nil {
			http.Error(w, "render failed", http.StatusInternalServerError)
			return
		}
		data.CSPNonce = nonce
	}
	if !data.DeployLocked {
		data.DeployLocked = a.deployLockActive()
	}
	if data.DeployLocked && data.DeployLockMessage == "" {
		data.DeployLockMessage = deployLockMessage
	}

	var buffer bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buffer, "base", data); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy(data.CSPNonce))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buffer.WriteTo(w); err != nil {
		log.Printf("write response: %v", err)
	}
}

func (a *App) deployLockActive() bool {
	if a.deployLockPath == "" {
		return false
	}

	_, err := os.Stat(a.deployLockPath)
	return err == nil
}

func loadTemplates() (map[string]*template.Template, error) {
	cache := make(map[string]*template.Template)
	pages := []string{"home", "login", "register", "forgot_password", "reset_password", "dashboard", "articles", "article", "admin", "admin_article_edit"}

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

func validateRegistration(username, email, password, confirmPassword string) string {
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

	if password != confirmPassword {
		return "Пароли не совпали. Значит, надо еще раз свериться с реальностью."
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

	if len(password) < 8 {
		return "Пароль короче 8 символов."
	}

	return ""
}

func validatePasswordResetRequest(email string) string {
	if err := validateEmail(email); err != nil {
		return "Нужен валидный email. Иначе письмо уйдет в /dev/null."
	}

	return ""
}

func validatePasswordReset(password, confirmPassword string) string {
	if len(password) < 8 {
		return "Новый пароль короче 8 символов. Так не пойдет."
	}

	if password != confirmPassword {
		return "Пароли не совпали. Сверь их еще раз без heroic fail."
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
	case "registration-requested":
		return "Заявка отправлена. Теперь ждем апрув в Telegram, потом письмо с подтверждением, и только после этого вход."
	case "welcome":
		return "Аккаунт создан. Теперь можно строить путь из bash-тыка в DevOps."
	case "email-confirmed":
		return "Почта подтверждена. Сессия уже открыта, можно идти в кабинет."
	case "confirmation-invalid":
		return "Ссылка подтверждения устарела или уже недействительна."
	case "logged-in":
		return "Сессия активна. Продолжаем путь к офферу."
	case "progress-saved":
		return "Прогресс обновлен. Теперь это уже твоя карта пути, а не демо-заготовка."
	case "logged-out":
		return "Сессия завершена. Никаких хвостов в проде."
	case "password-reset-sent":
		return "Если такая почта есть в системе, ссылка на сброс уже улетела."
	case "password-reset-invalid":
		return "Ссылка на сброс устарела или уже была использована."
	case "password-reset-complete":
		return "Пароль обновлен. Можно снова идти к офферу без старых секретов."
	case "lesson-locked":
		return "Сначала закрой предыдущий блок. Маршрут дальше не пускает."
	case "test-missing":
		return "Для этого теста пока не добавили вопросы. Админке пора проснуться."
	case "test-passed":
		return "Тест пройден. Следующий блок уже открыт."
	case "test-retry":
		return "Ошибок больше трех. Пересобирай фундамент и проходи тест заново."
	case "test-question-created":
		return "Вопрос для test-блока добавлен."
	case "test-question-deleted":
		return "Вопрос удален из test-блока."
	case "user-admin-updated":
		return "Роль пользователя обновлена."
	case "user-ban-updated":
		return "Статус доступа пользователя обновлен."
	case "user-deleted":
		return "Пользователь удален."
	case "article-created":
		return "Урок создан. Теперь можно спокойно шлифовать markdown и порядок блока."
	case "article-saved":
		return "Урок сохранен. Маршрут подхватит обновление без магии."
	case "article-duplicated":
		return "Создана копия урока. Можно править ее отдельно, не ломая оригинал."
	case "article-open-requires-publish":
		return "Чтобы открыть урок у ученика сразу после сохранения, сначала включи публикацию."
	case "article-deleted":
		return "Урок удален. Маршрут и админка уже без него."
	default:
		return ""
	}
}

func normalizeAdminEmails(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}

	index := make(map[string]struct{}, len(values))
	for _, value := range values {
		email := strings.ToLower(strings.TrimSpace(value))
		if email == "" {
			continue
		}
		index[email] = struct{}{}
	}

	if len(index) == 0 {
		return nil
	}

	return index
}

func currentValueOrFallback(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}

	return fallback
}
