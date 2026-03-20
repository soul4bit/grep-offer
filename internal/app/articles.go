package app

import (
	"errors"
	"net/http"

	"grep-offer/internal/content"
	"grep-offer/internal/store"
)

func (a *App) handleArticlesIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/articles" {
		http.Redirect(w, r, "/learn", http.StatusMovedPermanently)
		return
	}

	if a.currentUser(r) == nil {
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

	viewModules := make([]CourseModule, 0, len(modules))
	for _, module := range modules {
		viewModules = append(viewModules, mapCourseModule(module))
	}

	a.render(w, r, http.StatusOK, "articles", ViewData{
		Notice:        noticeFromRequest(r),
		CourseModules: viewModules,
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
	}

	for _, item := range lesson.ModuleItems {
		page.ModuleItems = append(page.ModuleItems, mapArticleCard(item))
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
		cards = append(cards, mapArticleCard(article))
	}

	return cards, nil
}

func mapCourseModule(module content.Module) CourseModule {
	viewModule := CourseModule{
		Index:   module.Index,
		Title:   module.Title,
		Lessons: make([]ArticleCard, 0, len(module.Lessons)),
	}

	for _, lesson := range module.Lessons {
		viewModule.Lessons = append(viewModule.Lessons, mapArticleCard(lesson))
	}

	return viewModule
}

func mapArticleCard(article content.ArticleMeta) ArticleCard {
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
	}
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
