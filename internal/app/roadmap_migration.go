package app

import (
	"context"
	"strings"

	"grep-offer/internal/store"
)

var legacyRoadmapStageAliases = map[string][]string{
	"foundation": {"linux base"},
	"delivery":   {"delivery"},
	"platform":   {"platform"},
	"offer":      {"offer"},
}

var legacyRoadmapModuleAliases = map[string]map[string][]string{
	"foundation": {
		"foundation-linux":   {"basics", "basic", "filesystem", "permissions", "linux filesystem", "linux basics", "files", "navigation", "файловая система", "права", "навигация"},
		"foundation-bash":    {"bash", "shell", "cli"},
		"foundation-network": {"network", "networking", "git", "git network", "ssh", "dns", "сеть", "git и сеть"},
	},
	"delivery": {
		"delivery-image":  {"docker", "container", "containers", "image", "build", "докер"},
		"delivery-ci":     {"ci", "pipeline", "pipelines", "github actions"},
		"delivery-deploy": {"deploy", "deployment", "cd", "release", "деплой"},
	},
	"platform": {
		"platform-orchestration": {"k8s", "kubernetes", "orchestration"},
		"platform-observability": {"observability", "monitoring", "logs", "metrics", "tracing"},
		"platform-iac":           {"terraform", "iac", "infrastructure", "infra"},
	},
	"offer": {
		"offer-resume":    {"cv", "resume"},
		"offer-interview": {"interview", "tech interview", "technical interview"},
		"offer-deal":      {"offer", "negotiation", "salary"},
	},
}

type roadmapRouteCatalog struct {
	stageKeysByAlias     map[string]string
	stagesByKey          map[string]store.RoadmapStage
	moduleKeysByStage    map[string]map[string]string
	modulesByStageAndKey map[string]map[string]store.RoadmapModule
}

func buildRoadmapRouteCatalog(stages []store.RoadmapStage) roadmapRouteCatalog {
	catalog := roadmapRouteCatalog{
		stageKeysByAlias:     make(map[string]string, len(stages)*3),
		stagesByKey:          make(map[string]store.RoadmapStage, len(stages)),
		moduleKeysByStage:    make(map[string]map[string]string, len(stages)),
		modulesByStageAndKey: make(map[string]map[string]store.RoadmapModule, len(stages)),
	}

	for _, stage := range stages {
		stageKey := strings.TrimSpace(stage.Key)
		if stageKey == "" {
			continue
		}

		catalog.stagesByKey[stageKey] = stage
		catalog.stageKeysByAlias[normalizeRoadmapRouteAlias(stageKey)] = stageKey
		catalog.stageKeysByAlias[normalizeRoadmapRouteAlias(stage.Title)] = stageKey
		for _, alias := range legacyRoadmapStageAliases[stageKey] {
			catalog.stageKeysByAlias[normalizeRoadmapRouteAlias(alias)] = stageKey
		}

		moduleKeys := make(map[string]string, len(stage.Modules)*3)
		modulesByKey := make(map[string]store.RoadmapModule, len(stage.Modules))
		for _, module := range stage.Modules {
			moduleKey := strings.TrimSpace(module.Key)
			if moduleKey == "" {
				continue
			}

			modulesByKey[moduleKey] = module
			moduleKeys[normalizeRoadmapRouteAlias(moduleKey)] = moduleKey
			moduleKeys[normalizeRoadmapRouteAlias(module.Title)] = moduleKey
			for _, alias := range legacyRoadmapModuleAliases[stageKey][moduleKey] {
				moduleKeys[normalizeRoadmapRouteAlias(alias)] = moduleKey
			}
		}

		catalog.moduleKeysByStage[stageKey] = moduleKeys
		catalog.modulesByStageAndKey[stageKey] = modulesByKey
	}

	return catalog
}

func normalizeRoadmapRouteAlias(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	normalized := roadmapKeyFromTitle(value)
	if normalized != "" {
		return normalized
	}

	return strings.ToLower(value)
}

func (c roadmapRouteCatalog) canonicalize(stageTitle, moduleTitle string) (string, string, bool) {
	rawStage := strings.TrimSpace(stageTitle)
	rawModule := strings.TrimSpace(moduleTitle)
	if rawStage == "" {
		return rawStage, rawModule, false
	}

	stageKey := c.stageKeysByAlias[normalizeRoadmapRouteAlias(rawStage)]
	if stageKey == "" {
		return rawStage, rawModule, false
	}

	canonicalStage := rawStage
	stage := c.stagesByKey[stageKey]
	if strings.TrimSpace(stage.Title) != "" {
		canonicalStage = stage.Title
	}

	changed := canonicalStage != rawStage
	if rawModule == "" {
		return canonicalStage, rawModule, changed
	}

	moduleKey := c.moduleKeysByStage[stageKey][normalizeRoadmapRouteAlias(rawModule)]
	if moduleKey == "" {
		return canonicalStage, rawModule, changed
	}

	canonicalModule := rawModule
	module := c.modulesByStageAndKey[stageKey][moduleKey]
	if strings.TrimSpace(module.Title) != "" {
		canonicalModule = module.Title
	}

	return canonicalStage, canonicalModule, changed || canonicalModule != rawModule
}

func (a *App) canonicalizeArticleRoute(ctx context.Context, stageTitle, moduleTitle string) (string, string, error) {
	roadmapStages, err := a.roadmapStages(ctx)
	if err != nil {
		return stageTitle, moduleTitle, err
	}

	catalog := buildRoadmapRouteCatalog(roadmapStages)
	stageTitle, moduleTitle, _ = catalog.canonicalize(stageTitle, moduleTitle)
	return stageTitle, moduleTitle, nil
}

func (a *App) migrateLegacyArticleRoutes(ctx context.Context) error {
	if a == nil || a.articles == nil {
		return nil
	}

	roadmapStages, err := a.roadmapStages(ctx)
	if err != nil {
		return err
	}
	catalog := buildRoadmapRouteCatalog(roadmapStages)

	articles, err := a.articles.ListAll()
	if err != nil {
		return err
	}

	for _, article := range articles {
		stageTitle, moduleTitle, changed := catalog.canonicalize(article.Stage, article.Module)
		if !changed {
			continue
		}

		editable, err := a.articles.EditableBySlug(article.Slug)
		if err != nil {
			return err
		}

		editable.Stage = stageTitle
		editable.Module = moduleTitle
		if _, err := a.articles.SaveEditable(*editable); err != nil {
			return err
		}
	}

	return nil
}
