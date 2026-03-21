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
	"grep-offer/internal/store"
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

	action := "article-saved"
	if strings.TrimSpace(form.OriginalSlug) == "" {
		action = "article-created"
	}
	a.writeAuditLog(r.Context(), r, a.currentUser(r), store.AuditLogInput{
		Scope:      "admin",
		Action:     "article_saved",
		TargetType: "lesson",
		TargetKey:  saved.Slug,
		Details: map[string]string{
			"title":      saved.Title,
			"kind":       saved.Kind,
			"published":  strconv.FormatBool(saved.Published),
			"module":     saved.Module,
			"module_pos": formatLessonIndex(saved.ModuleOrder, saved.BlockOrder),
		},
	})

	if strings.TrimSpace(r.FormValue("after_save")) == "open" {
		if saved.Published {
			http.Redirect(w, r, "/learn/"+saved.Slug, http.StatusSeeOther)
			return
		}

		http.Redirect(w, r, "/admin/articles/"+saved.Slug+"/edit?notice=article-open-requires-publish", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/admin/articles/"+saved.Slug+"/edit?notice="+action, http.StatusSeeOther)
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

func (a *App) handleAdminArticleDelete(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}
	if a.articles == nil {
		http.Error(w, "content editor is not configured", http.StatusServiceUnavailable)
		return
	}

	slug := strings.TrimSpace(r.PathValue("slug"))
	if slug == "" {
		http.Error(w, "invalid article slug", http.StatusBadRequest)
		return
	}

	article, err := a.articles.EditableBySlug(slug)
	if err != nil {
		if errors.Is(err, content.ErrArticleNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "load article failed", http.StatusInternalServerError)
		return
	}

	if err := a.articles.DeleteBySlug(slug); err != nil {
		if errors.Is(err, content.ErrArticleNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "delete article failed", http.StatusInternalServerError)
		return
	}

	a.writeAuditLog(r.Context(), r, a.currentUser(r), store.AuditLogInput{
		Scope:      "admin",
		Action:     "article_deleted",
		TargetType: "lesson",
		TargetKey:  article.Slug,
		Details: map[string]string{
			"title":  article.Title,
			"module": article.Module,
		},
	})

	http.Redirect(w, r, "/admin/articles?notice=article-deleted", http.StatusSeeOther)
}

func (a *App) handleAdminArticleReorder(w http.ResponseWriter, r *http.Request) {
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

	slugs := r.Form["slug"]
	if len(slugs) == 0 {
		http.Error(w, "slugs are required", http.StatusBadRequest)
		return
	}

	stage := strings.TrimSpace(r.FormValue("stage"))
	module := strings.TrimSpace(r.FormValue("module"))
	moduleOrder, err := parseAdminIntField(r.FormValue("module_order"))
	if err != nil {
		http.Error(w, "invalid module order", http.StatusBadRequest)
		return
	}

	if stage == "" || module == "" || moduleOrder <= 0 {
		http.Error(w, "stage, module and module order are required", http.StatusBadRequest)
		return
	}

	updated := make([]struct {
		Slug  string `json:"slug"`
		Index string `json:"index"`
	}, 0, len(slugs))

	for i, rawSlug := range slugs {
		slug := strings.TrimSpace(rawSlug)
		if slug == "" {
			http.Error(w, "slug is required", http.StatusBadRequest)
			return
		}

		article, err := a.articles.EditableBySlug(slug)
		if err != nil {
			if errors.Is(err, content.ErrArticleNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "load article failed", http.StatusInternalServerError)
			return
		}

		article.Stage = stage
		article.Module = module
		article.ModuleOrder = moduleOrder
		article.BlockOrder = i + 1

		saved, err := a.articles.SaveEditable(*article)
		if err != nil {
			http.Error(w, "reorder articles failed", http.StatusInternalServerError)
			return
		}

		updated = append(updated, struct {
			Slug  string `json:"slug"`
			Index string `json:"index"`
		}{
			Slug:  saved.Slug,
			Index: formatLessonIndex(saved.ModuleOrder, saved.BlockOrder),
		})
	}

	a.writeAuditLog(r.Context(), r, a.currentUser(r), store.AuditLogInput{
		Scope:      "admin",
		Action:     "article_reordered",
		TargetType: "module",
		TargetKey:  stage + "::" + module,
		Details: map[string]string{
			"module_order": strconv.Itoa(moduleOrder),
			"lesson_count": strconv.Itoa(len(updated)),
		},
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Items []struct {
			Slug  string `json:"slug"`
			Index string `json:"index"`
		} `json:"items"`
	}{
		Items: updated,
	})
}

func (a *App) renderAdminArticleEditor(w http.ResponseWriter, r *http.Request, status int, form AdminArticleForm) {
	a.renderAdminArticleEditorWithError(w, r, status, form, "")
}

func (a *App) renderAdminArticleEditorWithError(w http.ResponseWriter, r *http.Request, status int, form AdminArticleForm, message string) {
	form = hydrateAdminArticleForm(form)
	options := a.loadAdminArticleOptions()

	a.render(w, r, status, "admin_article_edit", ViewData{
		Notice:              noticeFromRequest(r),
		Error:               message,
		AdminArticleForm:    form,
		AdminArticleOptions: options,
	})
}

func (a *App) loadAdminArticleOptions() AdminArticleOptions {
	if a.articles == nil {
		return AdminArticleOptions{GlobalNextModuleOrder: 1}
	}

	articles, err := a.articles.ListAll()
	if err != nil {
		return AdminArticleOptions{GlobalNextModuleOrder: 1}
	}

	options := AdminArticleOptions{
		GlobalNextModuleOrder: 1,
		Stages:                make([]AdminStageOption, 0, len(articles)),
	}

	stageIndexes := make(map[string]int)
	moduleIndexes := make(map[string]map[string]int)

	for _, article := range articles {
		if article.ModuleOrder+1 > options.GlobalNextModuleOrder {
			options.GlobalNextModuleOrder = article.ModuleOrder + 1
		}

		stage := strings.TrimSpace(article.Stage)
		if stage == "" {
			continue
		}

		stageIndex, ok := stageIndexes[stage]
		if !ok {
			stageIndex = len(options.Stages)
			stageIndexes[stage] = stageIndex
			options.Stages = append(options.Stages, AdminStageOption{
				Value:           stage,
				NextModuleOrder: max(article.ModuleOrder+1, 1),
			})
		}
		if article.ModuleOrder+1 > options.Stages[stageIndex].NextModuleOrder {
			options.Stages[stageIndex].NextModuleOrder = article.ModuleOrder + 1
		}

		module := strings.TrimSpace(article.Module)
		if module == "" {
			continue
		}

		if moduleIndexes[stage] == nil {
			moduleIndexes[stage] = make(map[string]int)
		}

		moduleIndex, ok := moduleIndexes[stage][module]
		if !ok {
			moduleIndex = len(options.Stages[stageIndex].Modules)
			moduleIndexes[stage][module] = moduleIndex
			options.Stages[stageIndex].Modules = append(options.Stages[stageIndex].Modules, AdminModuleOption{
				Value:          module,
				ModuleOrder:    article.ModuleOrder,
				NextBlockOrder: max(article.BlockOrder+1, 1),
			})
		} else {
			if article.ModuleOrder > 0 && (options.Stages[stageIndex].Modules[moduleIndex].ModuleOrder == 0 || article.ModuleOrder < options.Stages[stageIndex].Modules[moduleIndex].ModuleOrder) {
				options.Stages[stageIndex].Modules[moduleIndex].ModuleOrder = article.ModuleOrder
			}
			if article.BlockOrder+1 > options.Stages[stageIndex].Modules[moduleIndex].NextBlockOrder {
				options.Stages[stageIndex].Modules[moduleIndex].NextBlockOrder = article.BlockOrder + 1
			}
		}
	}

	for i := range options.Stages {
		stage := &options.Stages[i]
		if stage.NextModuleOrder <= 0 {
			stage.NextModuleOrder = options.GlobalNextModuleOrder
		}
		for j := range stage.Modules {
			if stage.Modules[j].ModuleOrder <= 0 {
				stage.Modules[j].ModuleOrder = stage.NextModuleOrder
			}
			if stage.Modules[j].NextBlockOrder <= 0 {
				stage.Modules[j].NextBlockOrder = 1
			}
		}
	}

	return options
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
			Stage:        article.Stage,
			Title:        article.Title,
			Slug:         article.Slug,
			Module:       article.Module,
			KindKey:      article.Kind,
			Kind:         lessonKindLabel(article.Kind),
			Index:        formatLessonIndex(article.ModuleOrder, article.BlockOrder),
			ModuleOrder:  article.ModuleOrder,
			BlockOrder:   article.BlockOrder,
			Published:    article.Published,
			UpdatedLabel: article.UpdatedAt.In(time.FixedZone("MSK", 3*60*60)).Format("02.01.2006 15:04"),
		})
	}

	return rows, nil
}

func (a *App) loadAdminArticleGroups() ([]AdminArticleGroup, error) {
	if a.articles == nil {
		return nil, nil
	}

	articles, err := a.loadAdminArticles()
	if err != nil {
		return nil, err
	}

	groups := make([]AdminArticleGroup, 0, len(articles))
	groupIndexes := make(map[string]int)

	for _, article := range articles {
		key := strconv.Itoa(article.ModuleOrder) + "::" + article.Stage + "::" + article.Module
		groupIndex, ok := groupIndexes[key]
		if !ok {
			groupIndex = len(groups)
			groupIndexes[key] = groupIndex
			groups = append(groups, AdminArticleGroup{
				Stage:       article.Stage,
				Module:      article.Module,
				ModuleOrder: article.ModuleOrder,
				ModuleIndex: intString(article.ModuleOrder),
			})
		}

		groups[groupIndex].Lessons = append(groups[groupIndex].Lessons, article)
		groups[groupIndex].LessonCount++
		if article.Published {
			groups[groupIndex].PublishedCount++
		}
	}

	return groups, nil
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
	if originalSlug := strings.TrimSpace(form.OriginalSlug); originalSlug != "" && form.Published {
		form.OpenLearnPath = "/learn/" + originalSlug
	}
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
