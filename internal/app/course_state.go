package app

import (
	"context"

	"grep-offer/internal/content"
	"grep-offer/internal/store"
)

type courseState struct {
	Modules     []CourseModule
	Progress    CourseProgressView
	LessonIndex map[string]courseLessonState
}

type courseLessonState struct {
	Meta       content.ArticleMeta
	Read       bool
	Passed     bool
	Complete   bool
	Locked     bool
	TestResult store.LessonTestResult
}

func (a *App) loadCourseState(ctx context.Context, userID int64) (*courseState, error) {
	state := &courseState{
		Progress: CourseProgressView{
			ContinueHref: "/learn",
		},
		LessonIndex: make(map[string]courseLessonState),
	}
	if a.articles == nil {
		return state, nil
	}

	modules, err := a.articles.Curriculum()
	if err != nil {
		return nil, err
	}

	readProgress, err := a.loadLessonProgress(ctx, userID)
	if err != nil {
		return nil, err
	}

	testResults, err := a.store.LessonTestResults(ctx, userID)
	if err != nil {
		return nil, err
	}

	nextUnlocked := true
	for _, module := range modules {
		viewModule := CourseModule{
			Index:   module.Index,
			Title:   module.Title,
			Lessons: make([]ArticleCard, 0, len(module.Lessons)),
		}

		for _, lesson := range module.Lessons {
			result := testResults[lesson.Slug]
			read := readProgress[lesson.Slug]
			complete := lessonComplete(lesson, read, result.Passed)
			locked := !nextUnlocked

			card := ArticleCard{
				Title:       lesson.Title,
				Slug:        lesson.Slug,
				Summary:     lesson.Summary,
				Badge:       lesson.Badge,
				Stage:       lesson.Stage,
				Module:      lesson.Module,
				KindKey:     lesson.Kind,
				Kind:        lessonKindLabel(lesson.Kind),
				Index:       formatLessonIndex(lesson.ModuleOrder, lesson.BlockOrder),
				ReadingTime: lesson.ReadingTime,
				Read:        read,
				Complete:    complete,
				Locked:      locked,
			}

			state.LessonIndex[lesson.Slug] = courseLessonState{
				Meta:       lesson,
				Read:       read,
				Passed:     result.Passed,
				Complete:   complete,
				Locked:     locked,
				TestResult: result,
			}

			viewModule.Lessons = append(viewModule.Lessons, card)
			viewModule.TotalCount++
			state.Progress.TotalLessons++

			if read {
				viewModule.ReadCount++
				state.Progress.ReadCount++
			}

			if lesson.Kind == "test" {
				viewModule.TotalTests++
				state.Progress.TotalTests++
				if result.Passed {
					viewModule.PassedCount++
					state.Progress.PassedCount++
				}
			}

			if !locked && state.Progress.NextSlug == "" && !complete {
				state.Progress.NextSlug = lesson.Slug
				state.Progress.NextTitle = lesson.Title
				state.Progress.ContinueHref = "/learn/" + lesson.Slug
			}

			nextUnlocked = nextUnlocked && complete
		}

		if viewModule.TotalTests > 0 {
			viewModule.Percent = viewModule.PassedCount * 100 / viewModule.TotalTests
		} else if viewModule.TotalCount > 0 {
			viewModule.Percent = viewModule.ReadCount * 100 / viewModule.TotalCount
		}

		state.Modules = append(state.Modules, viewModule)
	}

	if state.Progress.TotalTests > 0 {
		state.Progress.Percent = state.Progress.PassedCount * 100 / state.Progress.TotalTests
	} else if state.Progress.TotalLessons > 0 {
		state.Progress.Percent = state.Progress.ReadCount * 100 / state.Progress.TotalLessons
	}

	return state, nil
}

func lessonComplete(lesson content.ArticleMeta, read, passed bool) bool {
	if lesson.Kind == "test" {
		return passed
	}

	return read
}
