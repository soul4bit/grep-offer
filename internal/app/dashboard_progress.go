package app

import (
	"context"
	"net/http"
	"strings"

	"grep-offer/internal/store"
)

func (a *App) loadDashboardView(ctx context.Context, userID int64) ([]DashboardStat, DashboardFocus, []DashboardStage, error) {
	roadmapStages, err := a.roadmapStages(ctx)
	if err != nil {
		return nil, DashboardFocus{}, nil, err
	}

	if err := a.store.EnsureRoadmapProgress(ctx, userID, dashboardCheckpointDefaults(roadmapStages)); err != nil {
		return nil, DashboardFocus{}, nil, err
	}

	progress, err := a.store.RoadmapProgress(ctx, userID)
	if err != nil {
		return nil, DashboardFocus{}, nil, err
	}

	stats, focus, stages := buildDashboardViewFromProgress(progress, roadmapStages)
	return stats, focus, stages, nil
}

func (a *App) handleDashboardCheckpointToggle(w http.ResponseWriter, r *http.Request) {
	user := a.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login?notice=login-required", http.StatusSeeOther)
		return
	}

	roadmapStages, err := a.roadmapStages(r.Context())
	if err != nil {
		http.Error(w, "load roadmap failed", http.StatusInternalServerError)
		return
	}
	checkpointIndex := dashboardCheckpointIndex(roadmapStages)

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	checkpointKey := strings.TrimSpace(r.FormValue("checkpoint"))
	if _, ok := checkpointIndex[checkpointKey]; !ok {
		http.Error(w, "unknown checkpoint", http.StatusBadRequest)
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
		http.Error(w, "invalid checkpoint state", http.StatusBadRequest)
		return
	}

	if err := a.store.EnsureRoadmapProgress(r.Context(), user.ID, dashboardCheckpointDefaults(roadmapStages)); err != nil {
		http.Error(w, "ensure roadmap progress failed", http.StatusInternalServerError)
		return
	}

	if err := a.store.SetRoadmapCheckpoint(r.Context(), user.ID, checkpointKey, done); err != nil {
		http.Error(w, "save checkpoint failed", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/dashboard?notice=progress-saved", http.StatusSeeOther)
}

func buildDashboardViewFromProgress(progress map[string]bool, roadmapStages []store.RoadmapStage) ([]DashboardStat, DashboardFocus, []DashboardStage) {
	if len(roadmapStages) == 0 {
		return nil, DashboardFocus{}, nil
	}

	stages := make([]DashboardStage, 0, len(roadmapStages))
	totalCheckpoints := 0
	doneCheckpoints := 0
	currentStageIndex := len(roadmapStages) - 1
	foundActive := false

	for stageIndex, sourceStage := range roadmapStages {
		stage := DashboardStage{
			Index:       twoDigitIndex(stageIndex + 1),
			Title:       sourceStage.Title,
			Badge:       sourceStage.Badge,
			Summary:     sourceStage.Summary,
			Checkpoints: make([]DashboardCheckpoint, 0, len(sourceStage.Modules)),
		}

		for _, sourceModule := range sourceStage.Modules {
			done := progress[sourceModule.Key]
			stage.Checkpoints = append(stage.Checkpoints, DashboardCheckpoint{
				Key:   sourceModule.Key,
				Title: sourceModule.Title,
				Note:  sourceModule.Note,
				Done:  done,
			})
			stage.TotalCount++
			totalCheckpoints++
			if done {
				stage.DoneCount++
				doneCheckpoints++
			}
		}

		if stage.TotalCount > 0 {
			stage.Percent = stage.DoneCount * 100 / stage.TotalCount
		}

		switch {
		case stage.TotalCount > 0 && stage.DoneCount == stage.TotalCount:
			stage.Status = "готово"
			stage.StatusTone = "done"
		case !foundActive:
			stage.Status = "в работе"
			stage.StatusTone = "active"
			currentStageIndex = len(stages)
			foundActive = true
		default:
			stage.Status = "в очереди"
			stage.StatusTone = "queued"
		}

		stages = append(stages, stage)
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
		{Value: formatProgress(doneCheckpoints, totalCheckpoints), Label: "закрыто по маршруту"},
		{Value: currentStage.Title, Label: "текущий этап"},
		{Value: formatPercent(overallPercent), Label: "общий прогресс"},
	}

	focus := DashboardFocus{
		Title:          currentStage.Title,
		Summary:        currentStage.Summary,
		StageLabel:     "этап " + intString(currentStageIndex+1) + " из " + intString(len(stages)),
		NextCheckpoint: nextCheckpoint,
		Percent:        overallPercent,
		DoneCount:      currentStage.DoneCount,
		TotalCount:     currentStage.TotalCount,
	}

	return stats, focus, stages
}

func dashboardCheckpointDefaults(roadmapStages []store.RoadmapStage) []store.CheckpointProgress {
	defaults := make([]store.CheckpointProgress, 0, len(roadmapStages)*3)
	for _, stage := range roadmapStages {
		for _, module := range stage.Modules {
			defaults = append(defaults, store.CheckpointProgress{
				CheckpointKey: module.Key,
				Done:          false,
			})
		}
	}
	return defaults
}

func dashboardCheckpointIndex(roadmapStages []store.RoadmapStage) map[string]struct{} {
	index := make(map[string]struct{}, len(roadmapStages)*3)
	for _, stage := range roadmapStages {
		for _, module := range stage.Modules {
			index[module.Key] = struct{}{}
		}
	}
	return index
}
