package app

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"grep-offer/internal/store"
)

func (a *App) handleAdminRoadmap(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}

	stages, err := a.loadAdminRoadmap(r.Context())
	if err != nil {
		http.Error(w, "load roadmap failed", http.StatusInternalServerError)
		return
	}
	activeStageID := adminRoadmapActiveStageID(r, stages)
	currentStage := adminRoadmapStageByID(stages, activeStageID)

	a.renderAdminPage(w, r, http.StatusOK, ViewData{
		Notice:                    noticeFromRequest(r),
		AdminSection:              "roadmap",
		AdminRoadmapStages:        stages,
		AdminRoadmapActiveStageID: activeStageID,
		AdminRoadmapCurrentStage:  currentStage,
	})
}

func (a *App) handleAdminRoadmapStageCreate(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		http.Redirect(w, r, "/admin/roadmap?notice=roadmap-stage-title-required", http.StatusSeeOther)
		return
	}

	orderIndex, err := parseAdminIntField(r.FormValue("order_index"))
	if err != nil {
		http.Redirect(w, r, "/admin/roadmap?notice=roadmap-order-invalid", http.StatusSeeOther)
		return
	}

	key, err := a.nextRoadmapStageKey(r.Context(), title)
	if err != nil {
		http.Error(w, "prepare roadmap stage failed", http.StatusInternalServerError)
		return
	}

	created, err := a.store.CreateRoadmapStage(r.Context(), store.RoadmapStage{
		Key:        key,
		Title:      title,
		Badge:      strings.TrimSpace(r.FormValue("badge")),
		Summary:    strings.TrimSpace(r.FormValue("summary")),
		Note:       strings.TrimSpace(r.FormValue("note")),
		OrderIndex: max(orderIndex, 1),
	})
	if err != nil {
		http.Error(w, "create roadmap stage failed", http.StatusInternalServerError)
		return
	}

	a.writeAuditLog(r.Context(), r, a.currentUser(r), store.AuditLogInput{
		Scope:      "admin",
		Action:     "roadmap_stage_created",
		TargetType: "roadmap_stage",
		TargetKey:  created.Key,
		Details: map[string]string{
			"title": created.Title,
		},
	})

	http.Redirect(w, r, "/admin/roadmap?notice=roadmap-stage-created&stage="+strconv.FormatInt(created.ID, 10), http.StatusSeeOther)
}

func (a *App) handleAdminRoadmapStageUpdate(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}

	stageID, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || stageID <= 0 {
		http.Error(w, "invalid roadmap stage id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	existing, err := a.store.RoadmapStageByID(r.Context(), stageID)
	if err != nil {
		if errors.Is(err, store.ErrRoadmapStageNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "load roadmap stage failed", http.StatusInternalServerError)
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		http.Redirect(w, r, "/admin/roadmap?notice=roadmap-stage-title-required&stage="+strconv.FormatInt(stageID, 10), http.StatusSeeOther)
		return
	}
	orderIndex, err := parseAdminIntField(r.FormValue("order_index"))
	if err != nil {
		http.Redirect(w, r, "/admin/roadmap?notice=roadmap-order-invalid&stage="+strconv.FormatInt(stageID, 10), http.StatusSeeOther)
		return
	}

	updated := *existing
	updated.Title = title
	updated.Badge = strings.TrimSpace(r.FormValue("badge"))
	updated.Summary = strings.TrimSpace(r.FormValue("summary"))
	updated.Note = strings.TrimSpace(r.FormValue("note"))
	updated.OrderIndex = max(orderIndex, 1)

	if err := a.store.UpdateRoadmapStage(r.Context(), updated); err != nil {
		http.Error(w, "update roadmap stage failed", http.StatusInternalServerError)
		return
	}
	if err := a.renameArticlesStage(r.Context(), existing.Title, updated.Title); err != nil {
		http.Error(w, "rename stage in articles failed", http.StatusInternalServerError)
		return
	}

	a.writeAuditLog(r.Context(), r, a.currentUser(r), store.AuditLogInput{
		Scope:      "admin",
		Action:     "roadmap_stage_updated",
		TargetType: "roadmap_stage",
		TargetKey:  updated.Key,
		Details: map[string]string{
			"title": updated.Title,
		},
	})

	http.Redirect(w, r, "/admin/roadmap?notice=roadmap-stage-saved&stage="+strconv.FormatInt(updated.ID, 10), http.StatusSeeOther)
}

func (a *App) handleAdminRoadmapStageDelete(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}

	stageID, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || stageID <= 0 {
		http.Error(w, "invalid roadmap stage id", http.StatusBadRequest)
		return
	}

	existing, err := a.store.RoadmapStageByID(r.Context(), stageID)
	if err != nil {
		if errors.Is(err, store.ErrRoadmapStageNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "load roadmap stage failed", http.StatusInternalServerError)
		return
	}

	stages, err := a.loadAdminRoadmap(r.Context())
	if err != nil {
		http.Error(w, "load roadmap failed", http.StatusInternalServerError)
		return
	}
	for _, stage := range stages {
		if stage.ID != existing.ID {
			continue
		}
		if len(stage.Modules) > 0 || stage.LessonCount > 0 {
			http.Redirect(w, r, "/admin/roadmap?notice=roadmap-stage-delete-blocked&stage="+strconv.FormatInt(existing.ID, 10), http.StatusSeeOther)
			return
		}
	}

	if err := a.store.DeleteRoadmapStage(r.Context(), existing.ID); err != nil {
		http.Error(w, "delete roadmap stage failed", http.StatusInternalServerError)
		return
	}

	a.writeAuditLog(r.Context(), r, a.currentUser(r), store.AuditLogInput{
		Scope:      "admin",
		Action:     "roadmap_stage_deleted",
		TargetType: "roadmap_stage",
		TargetKey:  existing.Key,
		Details: map[string]string{
			"title": existing.Title,
		},
	})

	http.Redirect(w, r, "/admin/roadmap?notice=roadmap-stage-deleted", http.StatusSeeOther)
}

func (a *App) handleAdminRoadmapModuleCreate(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	stageID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("stage_id")), 10, 64)
	if err != nil || stageID <= 0 {
		http.Redirect(w, r, "/admin/roadmap?notice=roadmap-stage-required", http.StatusSeeOther)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		http.Redirect(w, r, "/admin/roadmap?notice=roadmap-module-title-required&stage="+strconv.FormatInt(stageID, 10), http.StatusSeeOther)
		return
	}
	orderIndex, err := parseAdminIntField(r.FormValue("order_index"))
	if err != nil {
		http.Redirect(w, r, "/admin/roadmap?notice=roadmap-order-invalid&stage="+strconv.FormatInt(stageID, 10), http.StatusSeeOther)
		return
	}

	stage, err := a.store.RoadmapStageByID(r.Context(), stageID)
	if err != nil {
		if errors.Is(err, store.ErrRoadmapStageNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "load roadmap stage failed", http.StatusInternalServerError)
		return
	}

	key, err := a.nextRoadmapModuleKey(r.Context(), stage.Key, title)
	if err != nil {
		http.Error(w, "prepare roadmap module failed", http.StatusInternalServerError)
		return
	}

	created, err := a.store.CreateRoadmapModule(r.Context(), store.RoadmapModule{
		StageID:    stage.ID,
		Key:        key,
		Title:      title,
		Note:       strings.TrimSpace(r.FormValue("note")),
		OrderIndex: max(orderIndex, 1),
	})
	if err != nil {
		http.Error(w, "create roadmap module failed", http.StatusInternalServerError)
		return
	}

	a.writeAuditLog(r.Context(), r, a.currentUser(r), store.AuditLogInput{
		Scope:      "admin",
		Action:     "roadmap_module_created",
		TargetType: "roadmap_module",
		TargetKey:  created.Key,
		Details: map[string]string{
			"title": created.Title,
			"stage": stage.Title,
		},
	})

	http.Redirect(w, r, "/admin/roadmap?notice=roadmap-module-created&stage="+strconv.FormatInt(stage.ID, 10), http.StatusSeeOther)
}

func (a *App) handleAdminRoadmapModuleUpdate(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}

	moduleID, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || moduleID <= 0 {
		http.Error(w, "invalid roadmap module id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	existing, err := a.store.RoadmapModuleByID(r.Context(), moduleID)
	if err != nil {
		if errors.Is(err, store.ErrRoadmapModuleNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "load roadmap module failed", http.StatusInternalServerError)
		return
	}
	stage, err := a.store.RoadmapStageByID(r.Context(), existing.StageID)
	if err != nil {
		if errors.Is(err, store.ErrRoadmapStageNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "load roadmap stage failed", http.StatusInternalServerError)
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		http.Redirect(w, r, "/admin/roadmap?notice=roadmap-module-title-required&stage="+strconv.FormatInt(stage.ID, 10), http.StatusSeeOther)
		return
	}
	orderIndex, err := parseAdminIntField(r.FormValue("order_index"))
	if err != nil {
		http.Redirect(w, r, "/admin/roadmap?notice=roadmap-order-invalid&stage="+strconv.FormatInt(stage.ID, 10), http.StatusSeeOther)
		return
	}

	updated := *existing
	updated.Title = title
	updated.Note = strings.TrimSpace(r.FormValue("note"))
	updated.OrderIndex = max(orderIndex, 1)

	if err := a.store.UpdateRoadmapModule(r.Context(), updated); err != nil {
		http.Error(w, "update roadmap module failed", http.StatusInternalServerError)
		return
	}
	if err := a.renameArticlesModule(r.Context(), stage.Title, existing.Title, updated.Title); err != nil {
		http.Error(w, "rename module in articles failed", http.StatusInternalServerError)
		return
	}

	a.writeAuditLog(r.Context(), r, a.currentUser(r), store.AuditLogInput{
		Scope:      "admin",
		Action:     "roadmap_module_updated",
		TargetType: "roadmap_module",
		TargetKey:  updated.Key,
		Details: map[string]string{
			"title": updated.Title,
			"stage": stage.Title,
		},
	})

	http.Redirect(w, r, "/admin/roadmap?notice=roadmap-module-saved&stage="+strconv.FormatInt(stage.ID, 10), http.StatusSeeOther)
}

func (a *App) handleAdminRoadmapModuleDelete(w http.ResponseWriter, r *http.Request) {
	if a.requireAdmin(w, r) == nil {
		return
	}

	moduleID, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || moduleID <= 0 {
		http.Error(w, "invalid roadmap module id", http.StatusBadRequest)
		return
	}

	module, err := a.store.RoadmapModuleByID(r.Context(), moduleID)
	if err != nil {
		if errors.Is(err, store.ErrRoadmapModuleNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "load roadmap module failed", http.StatusInternalServerError)
		return
	}

	stages, err := a.loadAdminRoadmap(r.Context())
	if err != nil {
		http.Error(w, "load roadmap failed", http.StatusInternalServerError)
		return
	}
	for _, stage := range stages {
		for _, row := range stage.Modules {
			if row.ID == module.ID && row.LessonCount > 0 {
				http.Redirect(w, r, "/admin/roadmap?notice=roadmap-module-delete-blocked&stage="+strconv.FormatInt(module.StageID, 10), http.StatusSeeOther)
				return
			}
		}
	}

	if err := a.store.DeleteRoadmapModule(r.Context(), module.ID); err != nil {
		http.Error(w, "delete roadmap module failed", http.StatusInternalServerError)
		return
	}

	a.writeAuditLog(r.Context(), r, a.currentUser(r), store.AuditLogInput{
		Scope:      "admin",
		Action:     "roadmap_module_deleted",
		TargetType: "roadmap_module",
		TargetKey:  module.Key,
		Details: map[string]string{
			"title": module.Title,
		},
	})

	http.Redirect(w, r, "/admin/roadmap?notice=roadmap-module-deleted&stage="+strconv.FormatInt(module.StageID, 10), http.StatusSeeOther)
}

func adminRoadmapActiveStageID(r *http.Request, stages []AdminRoadmapStageRow) int64 {
	if len(stages) == 0 {
		return 0
	}

	rawID := strings.TrimSpace(r.URL.Query().Get("stage"))
	if rawID != "" {
		stageID, err := strconv.ParseInt(rawID, 10, 64)
		if err == nil && stageID > 0 {
			for _, stage := range stages {
				if stage.ID == stageID {
					return stageID
				}
			}
		}
	}

	return stages[0].ID
}

func adminRoadmapStageByID(stages []AdminRoadmapStageRow, stageID int64) *AdminRoadmapStageRow {
	for i := range stages {
		if stages[i].ID == stageID {
			return &stages[i]
		}
	}
	return nil
}

func (a *App) loadAdminRoadmap(ctx context.Context) ([]AdminRoadmapStageRow, error) {
	roadmapStages, err := a.roadmapStages(ctx)
	if err != nil {
		return nil, err
	}
	catalog := buildRoadmapRouteCatalog(roadmapStages)

	rows := make([]AdminRoadmapStageRow, 0, len(roadmapStages))
	stageIndexes := make(map[string]int, len(roadmapStages))
	moduleIndexes := make(map[string]map[string]int, len(roadmapStages))
	for _, stage := range roadmapStages {
		row := AdminRoadmapStageRow{
			ID:                stage.ID,
			Key:               stage.Key,
			Title:             stage.Title,
			Badge:             stage.Badge,
			Summary:           stage.Summary,
			Note:              stage.Note,
			OrderIndex:        stage.OrderIndex,
			Modules:           make([]AdminRoadmapModuleRow, 0, len(stage.Modules)),
			UnassignedLessons: make([]AdminRoadmapLessonRow, 0, 1),
		}
		moduleIndexes[stage.Title] = make(map[string]int, len(stage.Modules))
		for _, module := range stage.Modules {
			moduleIndexes[stage.Title][module.Title] = len(row.Modules)
			row.Modules = append(row.Modules, AdminRoadmapModuleRow{
				ID:         module.ID,
				StageID:    stage.ID,
				Key:        module.Key,
				Title:      module.Title,
				Note:       module.Note,
				OrderIndex: module.OrderIndex,
				Lessons:    make([]AdminRoadmapLessonRow, 0, 1),
			})
		}
		stageIndexes[stage.Title] = len(rows)
		rows = append(rows, row)
	}

	if a.articles == nil {
		return rows, nil
	}

	articles, err := a.articles.ListAll()
	if err != nil {
		return nil, err
	}

	for _, article := range articles {
		stageTitle, moduleTitle, _ := catalog.canonicalize(article.Stage, article.Module)
		stageTitle = strings.TrimSpace(stageTitle)
		moduleTitle = strings.TrimSpace(moduleTitle)
		if stageTitle == "" {
			continue
		}

		stageIndex, ok := stageIndexes[stageTitle]
		if !ok {
			continue
		}

		lesson := AdminRoadmapLessonRow{
			Title:       article.Title,
			Slug:        article.Slug,
			StatusLabel: adminArticleStatusLabel(article.Status),
			StatusTone:  adminArticleStatusTone(article.Status),
			Index:       formatLessonIndex(article.ModuleOrder, article.BlockOrder),
		}

		rows[stageIndex].LessonCount++

		moduleIndex, ok := moduleIndexes[stageTitle][moduleTitle]
		if !ok {
			rows[stageIndex].UnassignedLessons = append(rows[stageIndex].UnassignedLessons, lesson)
			continue
		}

		rows[stageIndex].Modules[moduleIndex].LessonCount++
		rows[stageIndex].Modules[moduleIndex].Lessons = append(rows[stageIndex].Modules[moduleIndex].Lessons, lesson)
	}

	return rows, nil
}

func (a *App) loadAdminArticleOptions(ctx context.Context) AdminArticleOptions {
	roadmapStages, err := a.roadmapStages(ctx)
	catalog := buildRoadmapRouteCatalog(roadmapStages)
	stats := a.loadArticleRouteStats(catalog)

	options := AdminArticleOptions{
		GlobalNextModuleOrder: stats.globalNextModuleOrder,
		Stages:                make([]AdminStageOption, 0, len(stats.stageNextModuleOrder)),
	}

	if err == nil {
		stageIndexes := make(map[string]int, len(roadmapStages))
		for _, roadmapStage := range roadmapStages {
			nextModuleOrder := stats.stageNextModuleOrder[roadmapStage.Title]
			if nextModuleOrder <= 0 {
				nextModuleOrder = stats.globalNextModuleOrder
			}

			stageIndexes[roadmapStage.Title] = len(options.Stages)
			stageOption := AdminStageOption{
				Value:           roadmapStage.Title,
				NextModuleOrder: nextModuleOrder,
				Modules:         make([]AdminModuleOption, 0, len(roadmapStage.Modules)),
			}

			for _, roadmapModule := range roadmapStage.Modules {
				moduleOption, ok := stats.moduleOptions[roadmapStage.Title][roadmapModule.Title]
				if !ok {
					moduleOption = AdminModuleOption{
						Value:          roadmapModule.Title,
						ModuleOrder:    nextModuleOrder,
						NextBlockOrder: 1,
					}
				}
				moduleOption.Value = roadmapModule.Title
				stageOption.Modules = append(stageOption.Modules, moduleOption)
			}

			options.Stages = append(options.Stages, stageOption)
		}

		for stageTitle, modules := range stats.moduleOptions {
			stageIndex, ok := stageIndexes[stageTitle]
			if !ok {
				stageIndex = len(options.Stages)
				stageIndexes[stageTitle] = stageIndex
				options.Stages = append(options.Stages, AdminStageOption{
					Value:           stageTitle,
					NextModuleOrder: max(stats.stageNextModuleOrder[stageTitle], stats.globalNextModuleOrder),
				})
			}

			moduleIndexes := make(map[string]struct{}, len(options.Stages[stageIndex].Modules))
			for _, module := range options.Stages[stageIndex].Modules {
				moduleIndexes[module.Value] = struct{}{}
			}

			for moduleTitle, moduleOption := range modules {
				if _, exists := moduleIndexes[moduleTitle]; exists {
					continue
				}
				moduleOption.Value = moduleTitle
				options.Stages[stageIndex].Modules = append(options.Stages[stageIndex].Modules, moduleOption)
			}
		}

		return options
	}

	for stageTitle, nextModuleOrder := range stats.stageNextModuleOrder {
		stageOption := AdminStageOption{
			Value:           stageTitle,
			NextModuleOrder: nextModuleOrder,
		}
		for moduleTitle, moduleOption := range stats.moduleOptions[stageTitle] {
			moduleOption.Value = moduleTitle
			stageOption.Modules = append(stageOption.Modules, moduleOption)
		}
		options.Stages = append(options.Stages, stageOption)
	}

	return options
}

type articleRouteStats struct {
	globalNextModuleOrder int
	stageNextModuleOrder  map[string]int
	moduleOptions         map[string]map[string]AdminModuleOption
}

func (a *App) loadArticleRouteStats(catalog roadmapRouteCatalog) articleRouteStats {
	stats := articleRouteStats{
		globalNextModuleOrder: 1,
		stageNextModuleOrder:  make(map[string]int),
		moduleOptions:         make(map[string]map[string]AdminModuleOption),
	}
	if a.articles == nil {
		return stats
	}

	articles, err := a.articles.ListAll()
	if err != nil {
		return stats
	}

	for _, article := range articles {
		if article.ModuleOrder+1 > stats.globalNextModuleOrder {
			stats.globalNextModuleOrder = article.ModuleOrder + 1
		}

		stageTitle, moduleTitle, _ := catalog.canonicalize(article.Stage, article.Module)
		stageTitle = strings.TrimSpace(stageTitle)
		moduleTitle = strings.TrimSpace(moduleTitle)
		if stageTitle == "" {
			continue
		}

		if article.ModuleOrder+1 > stats.stageNextModuleOrder[stageTitle] {
			stats.stageNextModuleOrder[stageTitle] = article.ModuleOrder + 1
		}
		if moduleTitle == "" {
			continue
		}

		if stats.moduleOptions[stageTitle] == nil {
			stats.moduleOptions[stageTitle] = make(map[string]AdminModuleOption)
		}

		moduleOption := stats.moduleOptions[stageTitle][moduleTitle]
		moduleOption.Value = moduleTitle
		if moduleOption.ModuleOrder == 0 || (article.ModuleOrder > 0 && article.ModuleOrder < moduleOption.ModuleOrder) {
			moduleOption.ModuleOrder = article.ModuleOrder
		}
		if article.BlockOrder+1 > moduleOption.NextBlockOrder {
			moduleOption.NextBlockOrder = article.BlockOrder + 1
		}
		if moduleOption.NextBlockOrder <= 0 {
			moduleOption.NextBlockOrder = 1
		}
		stats.moduleOptions[stageTitle][moduleTitle] = moduleOption
	}

	for stageTitle, modules := range stats.moduleOptions {
		nextModuleOrder := max(stats.stageNextModuleOrder[stageTitle], stats.globalNextModuleOrder)
		for moduleTitle, moduleOption := range modules {
			if moduleOption.ModuleOrder <= 0 {
				moduleOption.ModuleOrder = nextModuleOrder
			}
			if moduleOption.NextBlockOrder <= 0 {
				moduleOption.NextBlockOrder = 1
			}
			modules[moduleTitle] = moduleOption
		}
	}

	return stats
}

func (a *App) nextRoadmapStageKey(ctx context.Context, title string) (string, error) {
	stages, err := a.roadmapStages(ctx)
	if err != nil {
		return "", err
	}

	existing := make(map[string]struct{}, len(stages))
	for _, stage := range stages {
		existing[stage.Key] = struct{}{}
	}

	base := roadmapKeyFromTitle(title)
	if base == "" {
		base = "stage"
	}
	candidate := base
	for index := 2; ; index++ {
		if _, ok := existing[candidate]; !ok {
			return candidate, nil
		}
		candidate = base + "-" + strconv.Itoa(index)
	}
}

func (a *App) nextRoadmapModuleKey(ctx context.Context, stageKey, title string) (string, error) {
	stages, err := a.roadmapStages(ctx)
	if err != nil {
		return "", err
	}

	existing := make(map[string]struct{}, len(stages)*3)
	for _, stage := range stages {
		for _, module := range stage.Modules {
			existing[module.Key] = struct{}{}
		}
	}

	basePart := roadmapKeyFromTitle(title)
	if basePart == "" {
		basePart = "module"
	}
	base := strings.Trim(strings.Join([]string{strings.TrimSpace(stageKey), basePart}, "-"), "-")
	candidate := base
	for index := 2; ; index++ {
		if _, ok := existing[candidate]; !ok {
			return candidate, nil
		}
		candidate = base + "-" + strconv.Itoa(index)
	}
}

func (a *App) renameArticlesStage(ctx context.Context, oldTitle, newTitle string) error {
	oldTitle = strings.TrimSpace(oldTitle)
	newTitle = strings.TrimSpace(newTitle)
	if oldTitle == "" || newTitle == "" || oldTitle == newTitle || a.articles == nil {
		return nil
	}

	articles, err := a.articles.ListAll()
	if err != nil {
		return err
	}

	for _, article := range articles {
		if strings.TrimSpace(article.Stage) != oldTitle {
			continue
		}
		editable, err := a.articles.EditableBySlug(article.Slug)
		if err != nil {
			return err
		}
		editable.Stage = newTitle
		if _, err := a.articles.SaveEditable(*editable); err != nil {
			return err
		}
	}

	return nil
}

func (a *App) renameArticlesModule(ctx context.Context, stageTitle, oldTitle, newTitle string) error {
	stageTitle = strings.TrimSpace(stageTitle)
	oldTitle = strings.TrimSpace(oldTitle)
	newTitle = strings.TrimSpace(newTitle)
	if stageTitle == "" || oldTitle == "" || newTitle == "" || oldTitle == newTitle || a.articles == nil {
		return nil
	}

	articles, err := a.articles.ListAll()
	if err != nil {
		return err
	}

	for _, article := range articles {
		if strings.TrimSpace(article.Stage) != stageTitle || strings.TrimSpace(article.Module) != oldTitle {
			continue
		}
		editable, err := a.articles.EditableBySlug(article.Slug)
		if err != nil {
			return err
		}
		editable.Module = newTitle
		if _, err := a.articles.SaveEditable(*editable); err != nil {
			return err
		}
	}

	return nil
}
