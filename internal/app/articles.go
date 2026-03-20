package app

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"grep-offer/internal/content"
	"grep-offer/internal/store"
)

const lessonTestMaxWrongAnswers = 3

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

	state, err := a.loadCourseState(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "load curriculum failed", http.StatusInternalServerError)
		return
	}

	a.render(w, r, http.StatusOK, "articles", ViewData{
		Notice:         noticeFromRequest(r),
		CourseModules:  state.Modules,
		CourseProgress: state.Progress,
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

	state, err := a.loadCourseState(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "load course state failed", http.StatusInternalServerError)
		return
	}

	lessonState, ok := state.LessonIndex[lesson.Slug]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if lessonState.Locked {
		http.Redirect(w, r, state.Progress.ContinueHref+"?notice=lesson-locked", http.StatusSeeOther)
		return
	}

	if !lessonState.Read {
		if err := a.store.SetLessonProgress(r.Context(), user.ID, lesson.Slug, true); err != nil {
			http.Error(w, "save lesson read state failed", http.StatusInternalServerError)
			return
		}

		state, err = a.loadCourseState(r.Context(), user.ID)
		if err != nil {
			http.Error(w, "reload course state failed", http.StatusInternalServerError)
			return
		}
		lessonState = state.LessonIndex[lesson.Slug]
	}

	var quizView *LessonQuizView
	if lesson.Kind == "test" {
		questions, err := a.store.LessonTestQuestions(r.Context(), lesson.Slug)
		if err != nil {
			http.Error(w, "load lesson quiz failed", http.StatusInternalServerError)
			return
		}
		quizView = buildLessonQuizView(questions)
	}

	page := &ArticlePage{
		Title:            lesson.Title,
		Slug:             lesson.Slug,
		Summary:          lesson.Summary,
		Badge:            lesson.Badge,
		Stage:            lesson.Stage,
		Module:           lesson.Module.Title,
		KindKey:          lesson.Kind,
		Kind:             lessonKindLabel(lesson.Kind),
		Index:            formatLessonIndex(lesson.ModuleOrder, lesson.BlockOrder),
		ReadingTime:      lesson.ReadingTime,
		HTML:             lesson.HTML,
		ModuleItems:      make([]ArticleCard, 0, len(lesson.ModuleItems)),
		Read:             lessonState.Read,
		Passed:           lessonState.Passed,
		ModuleTotalCount: len(lesson.ModuleItems),
		IsTest:           lesson.Kind == "test",
		Quiz:             quizView,
		TestResult: &LessonTestResultView{
			AttemptsCount:    lessonState.TestResult.AttemptsCount,
			LastWrongAnswers: lessonState.TestResult.LastWrongAnswers,
			Passed:           lessonState.TestResult.Passed,
		},
	}

	moduleState := findModuleState(state.Modules, lesson.Module.Title)
	page.ModuleReadCount = moduleState.ReadCount
	page.ModulePercent = moduleState.Percent

	for _, item := range lesson.ModuleItems {
		cardState := state.LessonIndex[item.Slug]
		page.ModuleItems = append(page.ModuleItems, mapCourseArticleCard(item, cardState))
	}

	if lesson.Prev != nil {
		page.Prev = &ArticleNav{
			Title: lesson.Prev.Title,
			Slug:  lesson.Prev.Slug,
			Index: formatLessonIndex(lesson.Prev.ModuleOrder, lesson.Prev.BlockOrder),
		}
	}
	if lesson.Next != nil {
		nextState := state.LessonIndex[lesson.Next.Slug]
		if !nextState.Locked {
			page.Next = &ArticleNav{
				Title: lesson.Next.Title,
				Slug:  lesson.Next.Slug,
				Index: formatLessonIndex(lesson.Next.ModuleOrder, lesson.Next.BlockOrder),
			}
		}
	}

	a.render(w, r, http.StatusOK, "article", ViewData{
		Notice:  noticeFromRequest(r),
		Article: page,
	})
}

func (a *App) handleLessonTestSubmit(w http.ResponseWriter, r *http.Request) {
	user := a.requireSignedInUser(w, r)
	if user == nil {
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
	if lesson.Kind != "test" {
		http.Error(w, "lesson has no test", http.StatusBadRequest)
		return
	}

	state, err := a.loadCourseState(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "load course state failed", http.StatusInternalServerError)
		return
	}
	lessonState, ok := state.LessonIndex[lesson.Slug]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if lessonState.Locked {
		http.Redirect(w, r, state.Progress.ContinueHref+"?notice=lesson-locked", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	questions, err := a.store.LessonTestQuestions(r.Context(), lesson.Slug)
	if err != nil {
		http.Error(w, "load lesson quiz failed", http.StatusInternalServerError)
		return
	}
	if len(questions) == 0 {
		http.Redirect(w, r, "/learn/"+lesson.Slug+"?notice=test-missing", http.StatusSeeOther)
		return
	}

	wrongAnswers := 0
	for _, question := range questions {
		answerValue := strings.TrimSpace(r.FormValue(testAnswerFieldName(question.ID)))
		answerIndex, err := strconv.Atoi(answerValue)
		if err != nil || answerIndex != question.CorrectOption {
			wrongAnswers++
		}
	}

	passed := wrongAnswers <= lessonTestMaxWrongAnswers
	if err := a.store.SetLessonProgress(r.Context(), user.ID, lesson.Slug, true); err != nil {
		http.Error(w, "save lesson read state failed", http.StatusInternalServerError)
		return
	}
	if err := a.store.UpsertLessonTestResult(r.Context(), user.ID, lesson.Slug, wrongAnswers, passed); err != nil {
		http.Error(w, "save lesson test result failed", http.StatusInternalServerError)
		return
	}

	if passed {
		http.Redirect(w, r, "/learn/"+lesson.Slug+"?notice=test-passed", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/learn/"+lesson.Slug+"?notice=test-retry", http.StatusSeeOther)
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
		cards = append(cards, ArticleCard{
			Title:       article.Title,
			Slug:        article.Slug,
			Summary:     article.Summary,
			Badge:       article.Badge,
			Stage:       article.Stage,
			Module:      article.Module,
			KindKey:     article.Kind,
			Kind:        lessonKindLabel(article.Kind),
			Index:       formatLessonIndex(article.ModuleOrder, article.BlockOrder),
			ReadingTime: article.ReadingTime,
		})
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
	state, err := a.loadCourseState(ctx, userID)
	if err != nil {
		return CourseProgressView{}, err
	}

	return state.Progress, nil
}

func mapCourseArticleCard(article content.ArticleMeta, state courseLessonState) ArticleCard {
	return ArticleCard{
		Title:       article.Title,
		Slug:        article.Slug,
		Summary:     article.Summary,
		Badge:       article.Badge,
		Stage:       article.Stage,
		Module:      article.Module,
		KindKey:     article.Kind,
		Kind:        lessonKindLabel(article.Kind),
		Index:       formatLessonIndex(article.ModuleOrder, article.BlockOrder),
		ReadingTime: article.ReadingTime,
		Read:        state.Read,
		Complete:    state.Complete,
		Locked:      state.Locked,
	}
}

func findModuleState(modules []CourseModule, title string) CourseModule {
	for _, module := range modules {
		if module.Title == title {
			return module
		}
	}

	return CourseModule{}
}

func buildLessonQuizView(questions []store.LessonTestQuestion) *LessonQuizView {
	if len(questions) == 0 {
		return nil
	}

	view := &LessonQuizView{
		Questions: make([]LessonQuizQuestionView, 0, len(questions)),
	}
	for _, question := range questions {
		item := LessonQuizQuestionView{
			ID:      question.ID,
			Prompt:  question.Prompt,
			Options: make([]LessonQuizOptionView, 0, len(question.Options)),
		}
		for index, option := range question.Options {
			item.Options = append(item.Options, LessonQuizOptionView{
				Index: index,
				Text:  option,
			})
		}
		view.Questions = append(view.Questions, item)
	}

	return view
}

func testAnswerFieldName(questionID int64) string {
	return "question_" + strconv.FormatInt(questionID, 10)
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
