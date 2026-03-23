package app

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"grep-offer/internal/content"
	"grep-offer/internal/store"
)

func (a *App) handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/admin/articles", http.StatusSeeOther)
}

func (a *App) handleAdminRoot(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}

	http.Redirect(w, r, "/admin/articles", http.StatusSeeOther)
}

func (a *App) handleAdminArticles(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}

	articles, err := a.loadAdminArticles()
	if err != nil {
		http.Error(w, "load articles failed", http.StatusInternalServerError)
		return
	}
	articleGroups, err := a.loadAdminArticleGroups()
	if err != nil {
		http.Error(w, "load article groups failed", http.StatusInternalServerError)
		return
	}
	archivedArticles, err := a.loadAdminArchivedArticles()
	if err != nil {
		http.Error(w, "load archived articles failed", http.StatusInternalServerError)
		return
	}

	testLessons, testQuestions, err := a.loadAdminTests(r.Context())
	if err != nil {
		http.Error(w, "load tests failed", http.StatusInternalServerError)
		return
	}

	a.renderAdminPage(w, r, http.StatusOK, ViewData{
		Notice:                noticeFromRequest(r),
		AdminSection:          "articles",
		AdminArticles:         articles,
		AdminArticleGroups:    articleGroups,
		AdminArchivedArticles: archivedArticles,
		AdminTestLessons:      testLessons,
		AdminTestQuestions:    testQuestions,
	})
}

func (a *App) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	currentUser := a.requireAdmin(w, r)
	if currentUser == nil {
		return
	}

	users, err := a.store.ListUsers(r.Context())
	if err != nil {
		http.Error(w, "load users failed", http.StatusInternalServerError)
		return
	}

	rows := make([]AdminUserRow, 0, len(users))
	for _, user := range users {
		rows = append(rows, AdminUserRow{
			ID:            user.ID,
			Username:      user.Username,
			Email:         user.Email,
			IsAdmin:       user.IsAdmin,
			IsBanned:      user.IsBanned,
			IsCurrentUser: user.ID == currentUser.ID,
			CreatedLabel:  user.CreatedAt.In(time.FixedZone("MSK", 3*60*60)).Format("02.01.2006 15:04"),
		})
	}

	a.renderAdminPage(w, r, http.StatusOK, ViewData{
		Notice:       noticeFromRequest(r),
		AdminSection: "users",
		AdminUsers:   rows,
	})
}

func (a *App) handleAdminLogs(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}

	logs, err := a.loadAdminAuditLogs(r.Context())
	if err != nil {
		http.Error(w, "load audit logs failed", http.StatusInternalServerError)
		return
	}

	a.renderAdminPage(w, r, http.StatusOK, ViewData{
		Notice:         noticeFromRequest(r),
		AdminSection:   "logs",
		AdminAuditLogs: logs,
	})
}

func (a *App) renderAdminPage(w http.ResponseWriter, r *http.Request, status int, data ViewData) {
	if data.AdminSection == "" {
		data.AdminSection = "articles"
	}
	data.AdminNav = buildAdminNav(data.AdminSection)
	a.render(w, r, status, "admin", data)
}

func (a *App) handleAdminUserAdmin(w http.ResponseWriter, r *http.Request) {
	currentUser := a.requireAdmin(w, r)
	if currentUser == nil {
		return
	}

	targetUser, ok := a.loadAdminTargetUser(w, r, currentUser)
	if !ok {
		return
	}

	makeAdmin, ok := parseAdminBool(r.FormValue("value"))
	if !ok {
		http.Error(w, "invalid admin flag", http.StatusBadRequest)
		return
	}

	if err := a.store.SetUserAdmin(r.Context(), targetUser.ID, makeAdmin); err != nil {
		http.Error(w, "save admin flag failed", http.StatusInternalServerError)
		return
	}

	a.writeAuditLog(r.Context(), r, currentUser, store.AuditLogInput{
		Scope:      "admin",
		Action:     "user_admin_changed",
		TargetType: "user",
		TargetKey:  strconv.FormatInt(targetUser.ID, 10),
		Details: map[string]string{
			"username": targetUser.Username,
			"email":    targetUser.Email,
			"value":    strconv.FormatBool(makeAdmin),
		},
	})

	http.Redirect(w, r, "/admin/users?notice=user-admin-updated", http.StatusSeeOther)
}

func (a *App) handleAdminUserBan(w http.ResponseWriter, r *http.Request) {
	currentUser := a.requireAdmin(w, r)
	if currentUser == nil {
		return
	}

	targetUser, ok := a.loadAdminTargetUser(w, r, currentUser)
	if !ok {
		return
	}

	banned, ok := parseAdminBool(r.FormValue("value"))
	if !ok {
		http.Error(w, "invalid ban flag", http.StatusBadRequest)
		return
	}

	if err := a.store.SetUserBanned(r.Context(), targetUser.ID, banned); err != nil {
		http.Error(w, "save ban flag failed", http.StatusInternalServerError)
		return
	}
	if banned {
		if err := a.store.DeleteSessionsByUserID(r.Context(), targetUser.ID); err != nil {
			http.Error(w, "drop user sessions failed", http.StatusInternalServerError)
			return
		}
	}

	a.writeAuditLog(r.Context(), r, currentUser, store.AuditLogInput{
		Scope:      "admin",
		Action:     "user_ban_changed",
		TargetType: "user",
		TargetKey:  strconv.FormatInt(targetUser.ID, 10),
		Details: map[string]string{
			"username": targetUser.Username,
			"email":    targetUser.Email,
			"value":    strconv.FormatBool(banned),
		},
	})

	http.Redirect(w, r, "/admin/users?notice=user-ban-updated", http.StatusSeeOther)
}

func (a *App) handleAdminUserDelete(w http.ResponseWriter, r *http.Request) {
	currentUser := a.requireAdmin(w, r)
	if currentUser == nil {
		return
	}

	targetUser, ok := a.loadAdminTargetUser(w, r, currentUser)
	if !ok {
		return
	}

	if err := a.store.DeleteUser(r.Context(), targetUser.ID); err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			http.NotFound(w, r)
			return
		}

		http.Error(w, "delete user failed", http.StatusInternalServerError)
		return
	}

	a.writeAuditLog(r.Context(), r, currentUser, store.AuditLogInput{
		Scope:      "admin",
		Action:     "user_deleted",
		TargetType: "user",
		TargetKey:  strconv.FormatInt(targetUser.ID, 10),
		Details: map[string]string{
			"username": targetUser.Username,
			"email":    targetUser.Email,
		},
	})

	http.Redirect(w, r, "/admin/users?notice=user-deleted", http.StatusSeeOther)
}

func (a *App) handleAdminTestQuestionCreate(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	lessonSlug := strings.TrimSpace(r.FormValue("lesson_slug"))
	prompt := strings.TrimSpace(r.FormValue("prompt"))
	options := splitAdminOptions(r.FormValue("options"))
	correctValue := strings.TrimSpace(r.FormValue("correct_option"))
	explanation := strings.TrimSpace(r.FormValue("explanation"))

	if lessonSlug == "" || prompt == "" {
		http.Error(w, "lesson and prompt are required", http.StatusBadRequest)
		return
	}
	if len(options) < 2 {
		http.Error(w, "at least two answer options are required", http.StatusBadRequest)
		return
	}

	correctOption, err := strconv.Atoi(correctValue)
	if err != nil || correctOption < 1 || correctOption > len(options) {
		http.Error(w, "invalid correct option", http.StatusBadRequest)
		return
	}

	lesson, err := a.adminTestLessonBySlug(r.Context(), lessonSlug)
	if err != nil {
		if errors.Is(err, content.ErrArticleNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "load lesson failed", http.StatusInternalServerError)
		return
	}
	if lesson.Kind != "test" {
		http.Error(w, "questions can only be added to test lessons", http.StatusBadRequest)
		return
	}

	if _, err := a.store.CreateLessonTestQuestion(r.Context(), lessonSlug, prompt, options, correctOption-1, explanation); err != nil {
		http.Error(w, "create test question failed", http.StatusInternalServerError)
		return
	}

	a.writeAuditLog(r.Context(), r, a.currentUser(r), store.AuditLogInput{
		Scope:      "admin",
		Action:     "test_question_created",
		TargetType: "lesson",
		TargetKey:  lessonSlug,
		Details: map[string]string{
			"prompt": prompt,
		},
	})

	http.Redirect(w, r, "/admin/articles?notice=test-question-created", http.StatusSeeOther)
}

func (a *App) handleAdminTestQuestionDelete(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}

	questionID, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || questionID <= 0 {
		http.Error(w, "invalid question id", http.StatusBadRequest)
		return
	}

	if err := a.store.DeleteLessonTestQuestion(r.Context(), questionID); err != nil {
		if errors.Is(err, store.ErrLessonTestQuestionNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "delete test question failed", http.StatusInternalServerError)
		return
	}

	a.writeAuditLog(r.Context(), r, a.currentUser(r), store.AuditLogInput{
		Scope:      "admin",
		Action:     "test_question_deleted",
		TargetType: "test_question",
		TargetKey:  strconv.FormatInt(questionID, 10),
	})

	http.Redirect(w, r, "/admin/articles?notice=test-question-deleted", http.StatusSeeOther)
}

func (a *App) requireAdmin(w http.ResponseWriter, r *http.Request) *store.User {
	user := a.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login?notice=login-required", http.StatusSeeOther)
		return nil
	}
	if !user.IsAdmin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return nil
	}
	return user
}

func (a *App) loadAdminTargetUser(w http.ResponseWriter, r *http.Request, currentUser *store.User) (*store.User, bool) {
	targetID, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || targetID <= 0 {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return nil, false
	}
	if targetID == currentUser.ID {
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return nil, false
	}

	targetUser, err := a.store.UserByID(r.Context(), targetID)
	if err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			http.NotFound(w, r)
			return nil, false
		}
		http.Error(w, "load user failed", http.StatusInternalServerError)
		return nil, false
	}

	return targetUser, true
}

func (a *App) ensureBootstrapAdmin(ctx context.Context, user *store.User) (*store.User, error) {
	if user == nil || user.IsAdmin || len(a.bootstrapAdminEmails) == 0 {
		return user, nil
	}

	if _, ok := a.bootstrapAdminEmails[strings.ToLower(strings.TrimSpace(user.Email))]; !ok {
		return user, nil
	}

	if err := a.store.SetUserAdmin(ctx, user.ID, true); err != nil {
		return nil, err
	}

	return a.store.UserByID(ctx, user.ID)
}

func parseAdminBool(value string) (bool, bool) {
	switch strings.TrimSpace(value) {
	case "1", "true":
		return true, true
	case "0", "false":
		return false, true
	default:
		return false, false
	}
}

func (a *App) loadAdminTests(ctx context.Context) ([]AdminLessonOption, []AdminTestQuestionRow, error) {
	lessonMap, testLessons, err := a.adminTestLessons(ctx)
	if err != nil {
		return nil, nil, err
	}

	questions, err := a.store.ListLessonTestQuestions(ctx)
	if err != nil {
		return nil, nil, err
	}

	rows := make([]AdminTestQuestionRow, 0, len(questions))
	for _, question := range questions {
		lessonTitle := question.LessonSlug
		if lesson, ok := lessonMap[question.LessonSlug]; ok {
			lessonTitle = formatLessonIndex(lesson.ModuleOrder, lesson.BlockOrder) + " " + lesson.Title
		}

		answerLabel := ""
		if question.CorrectOption >= 0 && question.CorrectOption < len(question.Options) {
			answerLabel = strconv.Itoa(question.CorrectOption+1) + ". " + question.Options[question.CorrectOption]
		}

		rows = append(rows, AdminTestQuestionRow{
			ID:          question.ID,
			LessonSlug:  question.LessonSlug,
			LessonTitle: strings.TrimSpace(lessonTitle),
			Prompt:      question.Prompt,
			Options:     question.Options,
			AnswerLabel: answerLabel,
		})
	}

	return testLessons, rows, nil
}

func (a *App) adminTestLessons(ctx context.Context) (map[string]content.ArticleMeta, []AdminLessonOption, error) {
	lessonMap := make(map[string]content.ArticleMeta)
	if a.articles == nil {
		return lessonMap, nil, nil
	}

	articles, err := a.articles.ListAll()
	if err != nil {
		return nil, nil, err
	}

	options := make([]AdminLessonOption, 0, len(articles))
	for _, article := range articles {
		if article.Kind != "test" || article.Status == content.ArticleStatusArchived {
			continue
		}

		lessonMap[article.Slug] = article.ArticleMeta
		options = append(options, AdminLessonOption{
			Slug:  article.Slug,
			Title: formatLessonIndex(article.ModuleOrder, article.BlockOrder) + " " + article.Title,
		})
	}

	return lessonMap, options, nil
}

func (a *App) adminTestLessonBySlug(ctx context.Context, slug string) (*content.ArticleMeta, error) {
	if a.articles == nil {
		return nil, content.ErrArticleNotFound
	}

	articles, err := a.articles.ListAll()
	if err != nil {
		return nil, err
	}

	for _, article := range articles {
		if article.Slug == strings.TrimSpace(slug) && article.Status != content.ArticleStatusArchived {
			meta := article.ArticleMeta
			return &meta, nil
		}
	}

	return nil, content.ErrArticleNotFound
}

func splitAdminOptions(raw string) []string {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	options := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		options = append(options, trimmed)
	}

	return options
}

func buildAdminNav(active string) []AdminNavItem {
	items := []AdminNavItem{
		{Key: "articles", Label: "Статьи", Href: "/admin/articles"},
		{Key: "roadmap", Label: "Маршрут", Href: "/admin/roadmap"},
		{Key: "users", Label: "Пользователи", Href: "/admin/users"},
		{Key: "logs", Label: "Лог", Href: "/admin/logs"},
	}

	for i := range items {
		items[i].Active = items[i].Key == active
	}

	return items
}
