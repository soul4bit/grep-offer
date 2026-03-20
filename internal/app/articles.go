package app

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"grep-offer/internal/content"
	"grep-offer/internal/store"
)

func (a *App) handleArticlesIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/articles" {
		http.Redirect(w, r, "/learn", http.StatusMovedPermanently)
		return
	}

	user := a.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login?notice=login-required", http.StatusSeeOther)
		return
	}

	if a.articles == nil {
		http.NotFound(w, r)
		return
	}

	modules, err := a.articles.Curriculum()
	if err != nil {
		http.Error(w, "load curriculum failed", http.StatusInternalServerError)
		return
	}

	progress, err := a.loadLessonProgress(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "load lesson progress failed", http.StatusInternalServerError)
		return
	}

	viewModules, courseProgress := mapCourseModules(modules, progress)

	a.render(w, r, http.StatusOK, "articles", ViewData{
		Notice:         noticeFromRequest(r),
		CourseModules:  viewModules,
		CourseProgress: courseProgress,
	})
}

func (a *App) handleArticleShow(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "" && len(r.URL.Path) > len("/articles/") && r.URL.Path[:len("/articles/")] == "/articles/" {
		http.Redirect(w, r, "/learn/"+r.PathValue("slug"), http.StatusMovedPermanently)
		return
	}

	user := a.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login?notice=login-required", http.StatusSeeOther)
		return
	}
	if user.IsBanned {
		http.Redirect(w, r, "/login?notice=login-required", http.StatusSeeOther)
		return
	}

	if a.articles == nil {
		http.NotFound(w, r)
		return
	}

	progress, err := a.loadLessonProgress(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "load lesson progress failed", http.StatusInternalServerError)
		return
	}

	lesson, err := a.articles.LessonBySlug(r.PathValue("slug"))
	if err != nil {
		if errors.Is(err, content.ErrArticleNotFound) {
			http.NotFound(w, r)
			return
		}

		http.Error(w, "load lesson failed", http.StatusInternalServerError)
		return
	}

	page := &ArticlePage{
		Title:       lesson.Title,
		Slug:        lesson.Slug,
		Summary:     lesson.Summary,
		Badge:       lesson.Badge,
		Stage:       lesson.Stage,
		Module:      lesson.Module.Title,
		Kind:        lesson.Kind,
		Index:       formatLessonIndex(lesson.ModuleOrder, lesson.BlockOrder),
		ReadingTime: lesson.ReadingTime,
		HTML:        lesson.HTML,
		ModuleItems: make([]ArticleCard, 0, len(lesson.ModuleItems)),
		Done:        progress[lesson.Slug],
	}

	for _, item := range lesson.ModuleItems {
		done := progress[item.Slug]
		page.ModuleItems = append(page.ModuleItems, mapArticleCard(item, done))
		page.ModuleTotalCount++
		if done {
			page.ModuleDoneCount++
		}
	}
	if page.ModuleTotalCount > 0 {
		page.ModulePercent = page.ModuleDoneCount * 100 / page.ModuleTotalCount
	}
	if lesson.Prev != nil {
		page.Prev = &ArticleNav{
			Title: lesson.Prev.Title,
			Slug:  lesson.Prev.Slug,
			Index: formatLessonIndex(lesson.Prev.ModuleOrder, lesson.Prev.BlockOrder),
		}
	}
	if lesson.Next != nil {
		page.Next = &ArticleNav{
			Title: lesson.Next.Title,
			Slug:  lesson.Next.Slug,
			Index: formatLessonIndex(lesson.Next.ModuleOrder, lesson.Next.BlockOrder),
		}
	}

	a.render(w, r, http.StatusOK, "article", ViewData{
		Notice:  noticeFromRequest(r),
		Article: page,
	})
}

func (a *App) handleLessonProgressToggle(w http.ResponseWriter, r *http.Request) {
	user := a.requireSignedInUser(w, r)
	if user == nil {
		return
	}

	if a.articles == nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	lessonSlug := strings.TrimSpace(r.FormValue("lesson"))
	if lessonSlug == "" {
		http.Error(w, "lesson is required", http.StatusBadRequest)
		return
	}

	if _, err := a.articles.LessonBySlug(lessonSlug); err != nil {
		if errors.Is(err, content.ErrArticleNotFound) {
			http.NotFound(w, r)
			return
		}

		http.Error(w, "load lesson failed", http.StatusInternalServerError)
		return
	}

	doneValue := r.FormValue("done")
	var done bool
	switch doneValue {
	case "1":
		done = true
	case "0":
		done = false
	default:
		http.Error(w, "invalid lesson state", http.StatusBadRequest)
		return
	}

	if err := a.store.SetLessonProgress(r.Context(), user.ID, lessonSlug, done); err != nil {
		http.Error(w, "save lesson progress failed", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, safeLearnRedirect(r.FormValue("return_to"), lessonSlug), http.StatusSeeOther)
}

func (a *App) loadFeaturedArticles(limit int) ([]ArticleCard, error) {
	if a.articles == nil {
		return nil, nil
	}

	articles, err := a.articles.List()
	if err != nil {
		return nil, err
	}

	if limit > 0 && len(articles) > limit {
		articles = articles[:limit]
	}

	cards := make([]ArticleCard, 0, len(articles))
	for _, article := range articles {
		cards = append(cards, mapArticleCard(article, false))
	}

	return cards, nil
}

func (a *App) loadLessonProgress(ctx context.Context, userID int64) (map[string]bool, error) {
	if userID == 0 {
		return nil, nil
	}

	return a.store.LessonProgress(ctx, userID)
}

func (a *App) loadCourseOverview(ctx context.Context, userID int64) (CourseProgressView, error) {
	summary := CourseProgressView{
		ContinueHref: "/learn",
	}
	if a.articles == nil {
		return summary, nil
	}

	modules, err := a.articles.Curriculum()
	if err != nil {
		return summary, err
	}

	progress, err := a.loadLessonProgress(ctx, userID)
	if err != nil {
		return summary, err
	}

	_, summary = mapCourseModules(modules, progress)
	return summary, nil
}

func mapCourseModules(modules []content.Module, progress map[string]bool) ([]CourseModule, CourseProgressView) {
	viewModules := make([]CourseModule, 0, len(modules))
	summary := CourseProgressView{
		ContinueHref: "/learn",
	}

	for _, module := range modules {
		viewModule := mapCourseModule(module, progress)
		viewModules = append(viewModules, viewModule)

		summary.DoneCount += viewModule.DoneCount
		summary.TotalCount += viewModule.TotalCount

		if summary.NextSlug != "" {
			continue
		}

		for _, lesson := range viewModule.Lessons {
			if lesson.Done {
				continue
			}

			summary.NextSlug = lesson.Slug
			summary.NextTitle = lesson.Title
			summary.ContinueHref = "/learn/" + lesson.Slug
			break
		}
	}

	if summary.TotalCount > 0 {
		summary.Percent = summary.DoneCount * 100 / summary.TotalCount
	}

	return viewModules, summary
}

func mapCourseModule(module content.Module, progress map[string]bool) CourseModule {
	viewModule := CourseModule{
		Index:   module.Index,
		Title:   module.Title,
		Lessons: make([]ArticleCard, 0, len(module.Lessons)),
	}

	for _, lesson := range module.Lessons {
		done := progress[lesson.Slug]
		viewModule.Lessons = append(viewModule.Lessons, mapArticleCard(lesson, done))
		viewModule.TotalCount++
		if done {
			viewModule.DoneCount++
		}
	}

	if viewModule.TotalCount > 0 {
		viewModule.Percent = viewModule.DoneCount * 100 / viewModule.TotalCount
	}

	return viewModule
}

func mapArticleCard(article content.ArticleMeta, done bool) ArticleCard {
	return ArticleCard{
		Title:       article.Title,
		Slug:        article.Slug,
		Summary:     article.Summary,
		Badge:       article.Badge,
		Stage:       article.Stage,
		Module:      article.Module,
		Kind:        lessonKindLabel(article.Kind),
		Index:       formatLessonIndex(article.ModuleOrder, article.BlockOrder),
		ReadingTime: article.ReadingTime,
		Done:        done,
	}
}

func safeLearnRedirect(value, lessonSlug string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "/learn") && !strings.HasPrefix(value, "//") {
		return value
	}

	if lessonSlug != "" {
		return "/learn/" + lessonSlug
	}

	return "/learn"
}

func formatLessonIndex(moduleOrder, blockOrder int) string {
	if moduleOrder <= 0 && blockOrder <= 0 {
		return ""
	}
	return intString(moduleOrder) + "." + intString(blockOrder)
}

func lessonKindLabel(kind string) string {
	switch kind {
	case "practice":
		return "практика"
	case "test":
		return "тест"
	default:
		return "теория"
	}
}

func (a *App) requireSignedInUser(w http.ResponseWriter, r *http.Request) *store.User {
	user := a.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login?notice=login-required", http.StatusSeeOther)
		return nil
	}
	return user
}
