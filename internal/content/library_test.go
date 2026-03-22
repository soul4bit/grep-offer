package content

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLibraryReloadsWhenMarkdownChangesOnDisk(t *testing.T) {
	t.Parallel()

	contentDir := t.TempDir()
	lessonPath := filepath.Join(contentDir, "01-01-linux-intro.md")

	writeTestLesson(t, lessonPath, "Linux Intro", "linux-intro", "# Linux Intro\n\nold body")

	library := NewLibrary(contentDir)

	lesson, err := library.LessonBySlug("linux-intro")
	if err != nil {
		t.Fatalf("load initial lesson: %v", err)
	}
	if lesson.Title != "Linux Intro" {
		t.Fatalf("unexpected initial title: %q", lesson.Title)
	}

	time.Sleep(10 * time.Millisecond)
	writeTestLesson(t, lessonPath, "Linux Intro Reloaded", "linux-intro", "# Linux Intro Reloaded\n\nnew body with more text")

	reloaded, err := library.LessonBySlug("linux-intro")
	if err != nil {
		t.Fatalf("reload lesson after file change: %v", err)
	}
	if reloaded.Title != "Linux Intro Reloaded" {
		t.Fatalf("lesson cache was not refreshed: %q", reloaded.Title)
	}
}

func TestLibraryInvalidatesCacheAfterSaveAndDelete(t *testing.T) {
	t.Parallel()

	contentDir := t.TempDir()
	lessonPath := filepath.Join(contentDir, "01-01-linux-files.md")
	writeTestLesson(t, lessonPath, "Linux Files", "linux-files", "# Linux Files\n\nbody")

	library := NewLibrary(contentDir)

	if _, err := library.ListAll(); err != nil {
		t.Fatalf("warm list cache: %v", err)
	}

	saved, err := library.SaveEditable(EditableArticle{
		OriginalSlug: "linux-files",
		ArticleMeta: ArticleMeta{
			Title:       "Linux Files Updated",
			Slug:        "linux-files",
			Summary:     "updated",
			Badge:       "linux",
			Stage:       "Linux Base",
			Module:      "Filesystem",
			Kind:        "theory",
			Status:      ArticleStatusPublished,
			ModuleOrder: 1,
			BlockOrder:  1,
		},
		Body: "# Linux Files Updated\n\nfresh body",
	})
	if err != nil {
		t.Fatalf("save editable lesson: %v", err)
	}
	if saved.Title != "Linux Files Updated" {
		t.Fatalf("unexpected saved title: %q", saved.Title)
	}

	editable, err := library.EditableBySlug("linux-files")
	if err != nil {
		t.Fatalf("reload saved lesson: %v", err)
	}
	if editable.Title != "Linux Files Updated" {
		t.Fatalf("cache still returned stale lesson title: %q", editable.Title)
	}

	if err := library.DeleteBySlug("linux-files"); err != nil {
		t.Fatalf("delete lesson: %v", err)
	}

	if _, err := library.EditableBySlug("linux-files"); !errors.Is(err, ErrArticleNotFound) {
		t.Fatalf("expected article to be gone after delete, got: %v", err)
	}
}

func writeTestLesson(t *testing.T, path, title, slug, body string) {
	t.Helper()

	document := "---\n" +
		"title: \"" + title + "\"\n" +
		"slug: \"" + slug + "\"\n" +
		"stage: \"Linux Base\"\n" +
		"module: \"Basics\"\n" +
		"module_order: 1\n" +
		"block_order: 1\n" +
		"kind: \"theory\"\n" +
		"status: \"published\"\n" +
		"---\n\n" +
		body + "\n"

	if err := os.WriteFile(path, []byte(document), 0o644); err != nil {
		t.Fatalf("write lesson %s: %v", path, err)
	}
}
