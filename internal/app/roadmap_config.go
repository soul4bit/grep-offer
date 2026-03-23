package app

import (
	"context"
	"strconv"
	"strings"
	"unicode"

	"grep-offer/internal/store"
)

type roadmapStageSeed struct {
	Key     string
	Title   string
	Badge   string
	Summary string
	Note    string
	Modules []roadmapModuleSeed
}

type roadmapModuleSeed struct {
	Key   string
	Title string
	Note  string
}

var defaultRoadmapSeeds = []roadmapStageSeed{
	{
		Key:     "foundation",
		Title:   "Фундамент",
		Badge:   "linux / bash / git",
		Summary: "Собираешь базу: терминал, сеть, процессы, файлы и привычку не паниковать от логов.",
		Note:    "Без этого любой модный стек сверху просто ломается дороже и загадочнее.",
		Modules: []roadmapModuleSeed{
			{Key: "foundation-linux", Title: "Навигация по Linux без паники", Note: "Файлы, права, процессы, systemd и package manager без магии."},
			{Key: "foundation-bash", Title: "Bash как рабочий инструмент", Note: "Pipe, redirection, grep, sed, env и привычка читать man."},
			{Key: "foundation-network", Title: "Git и сеть без белой магии", Note: "SSH, remote, DNS, curl, ss и разбор обычных поломок."},
		},
	},
	{
		Key:     "delivery",
		Title:   "Доставка",
		Badge:   "docker / ci-cd / deploy",
		Summary: "Понимаешь, как код едет от коммита до сервера и где по дороге чаще всего все начинает гореть.",
		Note:    "Именно тут исчезает наивная вера в фразу «у меня локально работало».",
		Modules: []roadmapModuleSeed{
			{Key: "delivery-image", Title: "Собрать образ без шаманства", Note: "Dockerfile, layers, registry и разница между build и run."},
			{Key: "delivery-ci", Title: "Положить CI на рельсы", Note: "Pipeline, тесты, артефакты и нормальные healthchecks."},
			{Key: "delivery-deploy", Title: "Довезти deploy до предсказуемости", Note: "Rollback, env, секреты и понимание, где обычно рвется цепочка."},
		},
	},
	{
		Key:     "platform",
		Title:   "Платформа",
		Badge:   "k8s / terraform / observability",
		Summary: "Подключаешь оркестрацию, инфраструктуру и наблюдаемость без попытки называть магией обычную эксплуатацию.",
		Note:    "Сначала понимание систем, потом Kubernetes. Иначе получится дорогой квест.",
		Modules: []roadmapModuleSeed{
			{Key: "platform-orchestration", Title: "Понять orchestration, а не просто выучить YAML", Note: "Pods, services, ingress и что именно они решают."},
			{Key: "platform-observability", Title: "Наблюдать систему, а не надеяться", Note: "Logs, metrics, traces, alerts и что реально смотреть при инциденте."},
			{Key: "platform-iac", Title: "Описывать инфраструктуру как код", Note: "Terraform, state, secrets и аккуратная работа с cloud-ресурсами."},
		},
	},
	{
		Key:     "offer",
		Title:   "Оффер",
		Badge:   "cv / interview / offer",
		Summary: "Упаковываешь опыт, проходишь собесы и разговариваешь о деньгах уже с нормальной опорой на практику.",
		Note:    "Не инфоцыганский финал, а обычный рабочий результат последовательного пути.",
		Modules: []roadmapModuleSeed{
			{Key: "offer-resume", Title: "Собрать резюме вокруг реальных задач", Note: "Что делал, что ломалось, что улучшил и какой был эффект."},
			{Key: "offer-interview", Title: "Подготовить техразговор без легенд", Note: "Архитектура, инциденты, delivery, надежность и компромиссы."},
			{Key: "offer-deal", Title: "Договориться об оффере без тумана", Note: "Деньги, ожидания, зона ответственности и следующий уровень роста."},
		},
	},
}

func defaultRoadmapStages() []store.RoadmapStage {
	stages := make([]store.RoadmapStage, 0, len(defaultRoadmapSeeds))
	for stageIndex, seed := range defaultRoadmapSeeds {
		stage := store.RoadmapStage{
			Key:        seed.Key,
			Title:      seed.Title,
			Badge:      seed.Badge,
			Summary:    seed.Summary,
			Note:       seed.Note,
			OrderIndex: stageIndex + 1,
			Modules:    make([]store.RoadmapModule, 0, len(seed.Modules)),
		}
		for moduleIndex, moduleSeed := range seed.Modules {
			stage.Modules = append(stage.Modules, store.RoadmapModule{
				Key:        moduleSeed.Key,
				Title:      moduleSeed.Title,
				Note:       moduleSeed.Note,
				OrderIndex: moduleIndex + 1,
			})
		}
		stages = append(stages, stage)
	}
	return stages
}

func (a *App) ensureRoadmapConfig(ctx context.Context) error {
	if a == nil || a.store == nil {
		return nil
	}
	return a.store.EnsureRoadmap(ctx, defaultRoadmapStages())
}

func (a *App) roadmapStages(ctx context.Context) ([]store.RoadmapStage, error) {
	if err := a.ensureRoadmapConfig(ctx); err != nil {
		return nil, err
	}
	return a.store.Roadmap(ctx)
}

func buildLandingRoadmap(stages []store.RoadmapStage) []LandingStage {
	landing := make([]LandingStage, 0, len(stages))
	for index, stage := range stages {
		landing = append(landing, LandingStage{
			Index:   twoDigitIndex(index + 1),
			Title:   stage.Title,
			Badge:   stage.Badge,
			Summary: stage.Summary,
			Note:    stage.Note,
		})
	}
	return landing
}

func twoDigitIndex(value int) string {
	if value < 10 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}

func roadmapKeyFromTitle(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}

	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			builder.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}

	return strings.Trim(builder.String(), "-")
}
