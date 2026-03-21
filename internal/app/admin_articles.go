package app

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"grep-offer/internal/content"
)

func (a *App) handleAdminArticleNew(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}

	a.renderAdminArticleEditor(w, r, http.StatusOK, AdminArticleForm{
		Badge:       "linux",
		Stage:       "Linux Base",
		Kind:        "theory",
		Published:   false,
		ModeLabel:   "Новый урок",
		Body:        defaultArticleBody(),
		ModuleOrder: 1,
		BlockOrder:  1,
	})
}

func (a *App) handleAdminArticleEdit(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}
	if a.articles == nil {
		http.Error(w, "content editor is not configured", http.StatusServiceUnavailable)
		return
	}

	article, err := a.articles.EditableBySlug(r.PathValue("slug"))
	if err != nil {
		if errors.Is(err, content.ErrArticleNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "load article failed", http.StatusInternalServerError)
		return
	}

	a.renderAdminArticleEditor(w, r, http.StatusOK, adminArticleFormFromContent(*article))
}

func (a *App) handleAdminArticleSave(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}
	if a.articles == nil {
		http.Error(w, "content editor is not configured", http.StatusServiceUnavailable)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	moduleOrder, err := parseAdminIntField(r.FormValue("module_order"))
	if err != nil {
		a.renderAdminArticleEditorWithError(w, r, http.StatusUnprocessableEntity, adminArticleFormFromRequest(r), "Порядок модуля должен быть целым числом.")
		return
	}
	blockOrder, err := parseAdminIntField(r.FormValue("block_order"))
	if err != nil {
		a.renderAdminArticleEditorWithError(w, r, http.StatusUnprocessableEntity, adminArticleFormFromRequest(r), "Порядок блока должен быть целым числом.")
		return
	}

	form := adminArticleFormFromRequest(r)
	form.ModuleOrder = moduleOrder
	form.BlockOrder = blockOrder

	saved, err := a.articles.SaveEditable(content.EditableArticle{
		OriginalSlug: form.OriginalSlug,
		ArticleMeta: content.ArticleMeta{
			Title:       form.Title,
			Slug:        form.Slug,
			Summary:     form.Summary,
			Badge:       form.Badge,
			Stage:       form.Stage,
			Module:      form.Module,
			Kind:        form.Kind,
			ModuleOrder: form.ModuleOrder,
			BlockOrder:  form.BlockOrder,
			Published:   form.Published,
		},
		Body: form.Body,
	})
	if err != nil {
		switch {
		case errors.Is(err, content.ErrArticleAlreadyExists):
			a.renderAdminArticleEditorWithError(w, r, http.StatusConflict, form, "Такой slug уже занят другим уроком.")
		default:
			a.renderAdminArticleEditorWithError(w, r, http.StatusUnprocessableEntity, form, "Не удалось сохранить урок. Проверь slug, порядок и markdown.")
		}
		return
	}

	notice := "article-saved"
	if strings.TrimSpace(form.OriginalSlug) == "" {
		notice = "article-created"
	}

	http.Redirect(w, r, "/admin/articles/"+saved.Slug+"/edit?notice="+notice, http.StatusSeeOther)
}

func (a *App) handleAdminArticlePreview(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	form := adminArticleFormFromRequest(r)
	if moduleOrder, err := parseAdminIntField(r.FormValue("module_order")); err == nil {
		form.ModuleOrder = moduleOrder
	}
	if blockOrder, err := parseAdminIntField(r.FormValue("block_order")); err == nil {
		form.BlockOrder = blockOrder
	}

	form = hydrateAdminArticleForm(form)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		HTML      string `json:"html"`
		FileName  string `json:"file_name"`
		LearnPath string `json:"learn_path"`
		WordCount int    `json:"word_count"`
		LineCount int    `json:"line_count"`
		KindHint  string `json:"kind_hint"`
	}{
		HTML:      string(form.PreviewHTML),
		FileName:  form.FileName,
		LearnPath: form.LearnPath,
		WordCount: form.WordCount,
		LineCount: form.LineCount,
		KindHint:  form.KindHint,
	})
}

func (a *App) renderAdminArticleEditor(w http.ResponseWriter, r *http.Request, status int, form AdminArticleForm) {
	a.renderAdminArticleEditorWithError(w, r, status, form, "")
}

func (a *App) renderAdminArticleEditorWithError(w http.ResponseWriter, r *http.Request, status int, form AdminArticleForm, message string) {
	form = hydrateAdminArticleForm(form)

	a.render(w, r, status, "admin_article_edit", ViewData{
		Notice:           noticeFromRequest(r),
		Error:            message,
		AdminArticleForm: form,
	})
}

func (a *App) loadAdminArticles() ([]AdminArticleRow, error) {
	if a.articles == nil {
		return nil, nil
	}

	articles, err := a.articles.ListAll()
	if err != nil {
		return nil, err
	}

	rows := make([]AdminArticleRow, 0, len(articles))
	for _, article := range articles {
		rows = append(rows, AdminArticleRow{
			Title:        article.Title,
			Slug:         article.Slug,
			Module:       article.Module,
			Kind:         lessonKindLabel(article.Kind),
			Published:    article.Published,
			UpdatedLabel: article.UpdatedAt.In(time.FixedZone("MSK", 3*60*60)).Format("02.01.2006 15:04"),
		})
	}

	return rows, nil
}

func adminArticleFormFromContent(article content.EditableArticle) AdminArticleForm {
	return AdminArticleForm{
		OriginalSlug: article.OriginalSlug,
		Title:        article.Title,
		Slug:         article.Slug,
		Summary:      article.Summary,
		Badge:        article.Badge,
		Stage:        article.Stage,
		Module:       article.Module,
		Kind:         article.Kind,
		Body:         article.Body,
		ModuleOrder:  article.ModuleOrder,
		BlockOrder:   article.BlockOrder,
		Published:    article.Published,
		ModeLabel:    "Редактирование урока",
	}
}

func adminArticleFormFromRequest(r *http.Request) AdminArticleForm {
	modeLabel := "Новый урок"
	if strings.TrimSpace(r.FormValue("original_slug")) != "" {
		modeLabel = "Редактирование урока"
	}

	return AdminArticleForm{
		OriginalSlug: strings.TrimSpace(r.FormValue("original_slug")),
		Title:        strings.TrimSpace(r.FormValue("title")),
		Slug:         strings.TrimSpace(r.FormValue("slug")),
		Summary:      strings.TrimSpace(r.FormValue("summary")),
		Badge:        strings.TrimSpace(r.FormValue("badge")),
		Stage:        strings.TrimSpace(r.FormValue("stage")),
		Module:       strings.TrimSpace(r.FormValue("module")),
		Kind:         strings.TrimSpace(r.FormValue("kind")),
		Body:         strings.ReplaceAll(r.FormValue("body"), "\r\n", "\n"),
		Published:    r.FormValue("published") != "",
		ModeLabel:    modeLabel,
	}
}

func parseAdminIntField(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, errors.New("invalid integer")
	}

	return value, nil
}

func defaultArticleBody() string {
	return "# Новый блок\n\nКороткий вводный абзац.\n\n## Что нужно понять\n\n- пункт 1\n- пункт 2\n\n## Минимум руками\n\n1. шаг 1\n2. шаг 2\n\n## Что считать результатом\n\nКороткий критерий, что блок действительно пройден."
}

func hydrateAdminArticleForm(form AdminArticleForm) AdminArticleForm {
	if strings.TrimSpace(form.ModeLabel) == "" {
		form.ModeLabel = "Редактор урока"
	}

	form.Slug = strings.TrimSpace(form.Slug)
	form.FileName = content.SuggestedFileName(content.ArticleMeta{
		Slug:        form.Slug,
		ModuleOrder: form.ModuleOrder,
		BlockOrder:  form.BlockOrder,
	})
	if form.Slug != "" {
		form.LearnPath = "/learn/" + form.Slug
	}
	form.WordCount = countEditorWords(form.Body)
	form.LineCount = countEditorLines(form.Body)
	form.KindHint = editorKindHint(form.Kind)

	previewHTML, err := content.RenderMarkdown(form.Body)
	switch {
	case strings.TrimSpace(form.Body) == "":
		form.PreviewHTML = template.HTML(`<p class="admin-editor-preview-empty">Добавь markdown, и тут сразу появится живая превьюшка урока.</p>`)
	case err != nil:
		form.PreviewHTML = template.HTML(`<p class="admin-editor-preview-empty">Markdown пока не отрендерился. Проверь кодовый блок или YAML в шапке.</p>`)
	default:
		form.PreviewHTML = previewHTML
	}

	return form
}

func countEditorWords(body string) int {
	return len(strings.Fields(body))
}

func countEditorLines(body string) int {
	if strings.TrimSpace(body) == "" {
		return 0
	}

	return strings.Count(strings.ReplaceAll(body, "\r\n", "\n"), "\n") + 1
}

func editorKindHint(kind string) string {
	switch strings.TrimSpace(kind) {
	case "practice":
		return "Практика открывает следующий блок после прочтения. Формулируй руками: что сделать и какой результат принять."
	case "test":
		return "У теста прогресс считается только после сдачи. После сохранения добавь вопросы в секции test-блоков."
	default:
		return "Теория помечается как прочитанная при открытии. Держи блок коротким и без лекции на полторы жизни."
	}
}
