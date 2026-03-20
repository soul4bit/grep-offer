package app

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"grep-offer/internal/store"
)

type dashboardStageSeed struct {
	Index       string
	Title       string
	Badge       string
	Summary     string
	Checkpoints []dashboardCheckpointSeed
}

type dashboardCheckpointSeed struct {
	Key         string
	Title       string
	Note        string
	DefaultDone bool
}

var dashboardRoadmapSeeds = []dashboardStageSeed{
	{
		Index:   "01",
		Title:   "Фундамент",
		Badge:   "linux / bash / git / network",
		Summary: "Собираешь базу: терминал, процессы, сеть и привычку не гадать по логам.",
		Checkpoints: []dashboardCheckpointSeed{
			{Key: "foundation-linux", Title: "Навигация по Linux без паники", Note: "Файлы, права, процессы, systemd и package manager без магии."},
			{Key: "foundation-bash", Title: "Bash как рабочий инструмент", Note: "Pipe, redirection, grep, sed, env и привычка читать man."},
			{Key: "foundation-network", Title: "Git и сеть без белой магии", Note: "SSH, remote, DNS, curl, ss и разбор обычных поломок."},
		},
	},
	{
		Index:   "02",
		Title:   "Доставка",
		Badge:   "docker / ci-cd / deploy",
		Summary: "Понимаешь, как код едет от коммита до сервера и где по дороге все обычно горит.",
		Checkpoints: []dashboardCheckpointSeed{
			{Key: "delivery-image", Title: "Собрать образ без шаманства", Note: "Dockerfile, layers, registry и разница между build и run."},
			{Key: "delivery-ci", Title: "Положить CI на рельсы", Note: "Pipeline, тесты, артефакты и нормальные healthchecks."},
			{Key: "delivery-deploy", Title: "Довезти deploy до предсказуемости", Note: "Rollback, env, секреты и понимание, где обычно рвется цепочка."},
		},
	},
	{
		Index:   "03",
		Title:   "Платформа",
		Badge:   "k8s / terraform / observability",
		Summary: "Подключаешь оркестрацию, инфраструктуру и наблюдаемость без культа YAML.",
		Checkpoints: []dashboardCheckpointSeed{
			{Key: "platform-orchestration", Title: "Понять orchestration, а не просто выучить YAML", Note: "Pods, services, ingress и что именно они решают."},
			{Key: "platform-observability", Title: "Наблюдать систему, а не надеяться", Note: "Logs, metrics, traces, alerts и что реально смотреть при инциденте."},
			{Key: "platform-iac", Title: "Описывать инфраструктуру как код", Note: "Terraform, state, secrets и аккуратная работа с cloud-ресурсами."},
		},
	},
	{
		Index:   "04",
		Title:   "Оффер",
		Badge:   "cv / interview / offer",
		Summary: "Упаковываешь опыт, проходишь собесы и разговариваешь про деньги уже с реальной опорой.",
		Checkpoints: []dashboardCheckpointSeed{
			{Key: "offer-resume", Title: "Собрать резюме вокруг реальных задач", Note: "Что делал, что ломалось, что улучшил и какой был эффект."},
			{Key: "offer-interview", Title: "Подготовить техразговор без легенд", Note: "Архитектура, инциденты, delivery, надежность и компромиссы."},
			{Key: "offer-deal", Title: "Договориться об оффере без тумана", Note: "Деньги, ожидания, зона ответственности и следующий уровень роста."},
		},
	},
}

var dashboardCheckpointIndex = func() map[string]struct{} {
	index := make(map[string]struct{}, 12)
	for _, stage := range dashboardRoadmapSeeds {
		for _, checkpoint := range stage.Checkpoints {
			index[checkpoint.Key] = struct{}{}
		}
	}

	return index
}()

func (a *App) loadDashboardView(ctx context.Context, userID int64) ([]DashboardStat, DashboardFocus, []DashboardStage, error) {
	if err := a.store.EnsureRoadmapProgress(ctx, userID, dashboardCheckpointDefaults()); err != nil {
		return nil, DashboardFocus{}, nil, err
	}

	progress, err := a.store.RoadmapProgress(ctx, userID)
	if err != nil {
		return nil, DashboardFocus{}, nil, err
	}

	stats, focus, stages := buildDashboardViewFromProgress(progress)
	return stats, focus, stages, nil
}

func (a *App) handleDashboardCheckpointToggle(w http.ResponseWriter, r *http.Request) {
	user := a.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login?notice=login-required", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	checkpointKey := strings.TrimSpace(r.FormValue("checkpoint"))
	if _, ok := dashboardCheckpointIndex[checkpointKey]; !ok {
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

	if err := a.store.EnsureRoadmapProgress(r.Context(), user.ID, dashboardCheckpointDefaults()); err != nil {
		http.Error(w, "ensure roadmap progress failed", http.StatusInternalServerError)
		return
	}

	if err := a.store.SetRoadmapCheckpoint(r.Context(), user.ID, checkpointKey, done); err != nil {
		http.Error(w, "save checkpoint failed", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/dashboard?notice=progress-saved", http.StatusSeeOther)
}

func buildDashboardViewFromProgress(progress map[string]bool) ([]DashboardStat, DashboardFocus, []DashboardStage) {
	stages := make([]DashboardStage, 0, len(dashboardRoadmapSeeds))
	totalCheckpoints := 0
	doneCheckpoints := 0
	currentStageIndex := len(dashboardRoadmapSeeds) - 1
	foundActive := false

	for _, seed := range dashboardRoadmapSeeds {
		stage := DashboardStage{
			Index:       seed.Index,
			Title:       seed.Title,
			Badge:       seed.Badge,
			Summary:     seed.Summary,
			Checkpoints: make([]DashboardCheckpoint, 0, len(seed.Checkpoints)),
		}

		for _, checkpointSeed := range seed.Checkpoints {
			done, ok := progress[checkpointSeed.Key]
			if !ok {
				done = checkpointSeed.DefaultDone
			}

			stage.Checkpoints = append(stage.Checkpoints, DashboardCheckpoint{
				Key:   checkpointSeed.Key,
				Title: checkpointSeed.Title,
				Note:  checkpointSeed.Note,
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
		case stage.DoneCount == stage.TotalCount:
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

func dashboardCheckpointDefaults() []store.CheckpointProgress {
	defaults := make([]store.CheckpointProgress, 0, len(dashboardCheckpointIndex))
	for _, stage := range dashboardRoadmapSeeds {
		for _, checkpoint := range stage.Checkpoints {
			defaults = append(defaults, store.CheckpointProgress{
				CheckpointKey: checkpoint.Key,
				Done:          checkpoint.DefaultDone,
			})
		}
	}

	return defaults
}

func formatProgress(done, total int) string {
	return strings.Join([]string{intString(done), intString(total)}, "/")
}

func formatPercent(value int) string {
	return intString(value) + "%"
}

func intString(value int) string {
	return strconv.Itoa(value)
}
