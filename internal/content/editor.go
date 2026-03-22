package content

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var ErrArticleAlreadyExists = errors.New("article already exists")

type ManagedArticle struct {
	ArticleMeta
	UpdatedAt time.Time
}

type EditableArticle struct {
	OriginalSlug string
	ArticleMeta
	Body string
}

type saveFrontMatter struct {
	Title       string `yaml:"title"`
	Slug        string `yaml:"slug"`
	Summary     string `yaml:"summary,omitempty"`
	Badge       string `yaml:"badge,omitempty"`
	Stage       string `yaml:"stage,omitempty"`
	Module      string `yaml:"module,omitempty"`
	Kind        string `yaml:"kind,omitempty"`
	Status      string `yaml:"status"`
	ModuleOrder int    `yaml:"module_order,omitempty"`
	BlockOrder  int    `yaml:"block_order,omitempty"`
}

func (l *Library) ListAll() ([]ManagedArticle, error) {
	snapshot, err := l.snapshot()
	if err != nil {
		return nil, err
	}

	articles := make([]ManagedArticle, 0, len(snapshot.articles))
	for _, article := range snapshot.articles {
		articles = append(articles, ManagedArticle{
			ArticleMeta: article.meta,
			UpdatedAt:   article.updatedAt,
		})
	}

	sortManagedArticles(articles)
	return articles, nil
}

func (l *Library) EditableBySlug(slug string) (*EditableArticle, error) {
	snapshot, err := l.snapshot()
	if err != nil {
		return nil, err
	}

	index, ok := snapshot.slugIndex[normalizeSlug(slug)]
	if !ok {
		return nil, ErrArticleNotFound
	}
	article := snapshot.articles[index]

	return &EditableArticle{
		OriginalSlug: article.meta.Slug,
		ArticleMeta:  article.meta,
		Body:         article.body,
	}, nil
}

func (l *Library) SaveEditable(article EditableArticle) (*EditableArticle, error) {
	if l == nil || strings.TrimSpace(l.dir) == "" {
		return nil, errors.New("content directory is not configured")
	}

	title := strings.TrimSpace(article.Title)
	if title == "" {
		return nil, errors.New("title is required")
	}

	slug := normalizeSlug(article.Slug)
	if slug == "" {
		slug = normalizeSlug(title)
	}
	if slug == "" {
		return nil, errors.New("slug is required")
	}

	body := strings.TrimSpace(strings.ReplaceAll(article.Body, "\r\n", "\n"))
	if body == "" {
		return nil, errors.New("body is required")
	}

	meta := ArticleMeta{
		Title:       title,
		Slug:        slug,
		Summary:     strings.TrimSpace(article.Summary),
		Badge:       strings.TrimSpace(article.Badge),
		Stage:       strings.TrimSpace(article.Stage),
		Module:      strings.TrimSpace(article.Module),
		Kind:        normalizeKind(article.Kind),
		Status:      normalizeArticleStatus(article.Status, nil),
		ModuleOrder: article.ModuleOrder,
		BlockOrder:  article.BlockOrder,
	}

	if meta.Stage == "" {
		meta.Stage = "Linux Base"
	}
	if meta.Module == "" {
		meta.Module = meta.Stage
	}
	if meta.Badge == "" {
		meta.Badge = "linux"
	}
	if meta.Status == "" {
		meta.Status = ArticleStatusDraft
	}
	if meta.ModuleOrder < 0 || meta.BlockOrder < 0 {
		return nil, errors.New("module and block order must be positive")
	}

	targetPath, err := l.safeArticlePath(fileNameForArticle(meta))
	if err != nil {
		return nil, err
	}

	currentPath := ""
	if originalSlug := normalizeSlug(article.OriginalSlug); originalSlug != "" {
		if path, err := l.articlePathBySlug(originalSlug); err == nil {
			currentPath = path
		} else if !errors.Is(err, ErrArticleNotFound) {
			return nil, err
		}
	}

	if existingPath, err := l.articlePathBySlug(slug); err == nil {
		if currentPath == "" || !samePath(existingPath, currentPath) {
			return nil, ErrArticleAlreadyExists
		}
	} else if !errors.Is(err, ErrArticleNotFound) {
		return nil, err
	}

	if err := os.MkdirAll(strings.TrimSpace(l.dir), 0o775); err != nil {
		return nil, err
	}

	contentBytes, err := marshalEditableArticle(meta, body)
	if err != nil {
		return nil, err
	}

	tmpFile, err := os.CreateTemp(strings.TrimSpace(l.dir), ".article-*.md")
	if err != nil {
		return nil, err
	}

	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmpFile.Write(contentBytes); err != nil {
		return nil, err
	}
	if err := tmpFile.Chmod(0o664); err != nil {
		return nil, err
	}
	if err := tmpFile.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return nil, err
	}

	if currentPath != "" && !samePath(currentPath, targetPath) {
		if err := os.Remove(currentPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}

	l.invalidateCache()
	return l.EditableBySlug(slug)
}

func (l *Library) DeleteBySlug(slug string) error {
	if l == nil || strings.TrimSpace(l.dir) == "" {
		return errors.New("content directory is not configured")
	}

	path, err := l.articlePathBySlug(slug)
	if err != nil {
		return err
	}

	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrArticleNotFound
		}
		return err
	}

	l.invalidateCache()
	return nil
}

func (l *Library) SetStatusBySlug(slug, status string) (*EditableArticle, error) {
	article, err := l.EditableBySlug(slug)
	if err != nil {
		return nil, err
	}

	article.Status = normalizeArticleStatus(status, nil)
	if article.Status == "" {
		article.Status = ArticleStatusDraft
	}

	return l.SaveEditable(*article)
}

func (l *Library) SlugAvailable(slug, originalSlug string) (string, bool, error) {
	normalizedSlug := normalizeSlug(slug)
	if normalizedSlug == "" {
		return "", false, nil
	}

	existingPath, err := l.articlePathBySlug(normalizedSlug)
	if err != nil {
		if errors.Is(err, ErrArticleNotFound) {
			return normalizedSlug, true, nil
		}
		return normalizedSlug, false, err
	}

	normalizedOriginal := normalizeSlug(originalSlug)
	if normalizedOriginal == "" {
		return normalizedSlug, false, nil
	}

	currentPath, err := l.articlePathBySlug(normalizedOriginal)
	if err != nil {
		if errors.Is(err, ErrArticleNotFound) {
			return normalizedSlug, false, nil
		}
		return normalizedSlug, false, err
	}

	return normalizedSlug, samePath(existingPath, currentPath), nil
}

func (l *Library) articlePathBySlug(slug string) (string, error) {
	normalizedSlug := normalizeSlug(slug)
	if normalizedSlug == "" {
		return "", ErrArticleNotFound
	}

	snapshot, err := l.snapshot()
	if err != nil {
		return "", err
	}

	index, ok := snapshot.slugIndex[normalizedSlug]
	if !ok {
		return "", ErrArticleNotFound
	}

	return snapshot.articles[index].path, nil
}

func (l *Library) safeArticlePath(fileName string) (string, error) {
	baseDir, err := filepath.Abs(strings.TrimSpace(l.dir))
	if err != nil {
		return "", err
	}

	targetPath, err := filepath.Abs(filepath.Join(baseDir, fileName))
	if err != nil {
		return "", err
	}

	prefix := baseDir + string(os.PathSeparator)
	if targetPath != baseDir && !strings.HasPrefix(targetPath, prefix) {
		return "", fmt.Errorf("article path escapes content dir: %s", targetPath)
	}

	return targetPath, nil
}

func marshalEditableArticle(meta ArticleMeta, body string) ([]byte, error) {
	frontMatterBytes, err := yaml.Marshal(saveFrontMatter{
		Title:       meta.Title,
		Slug:        meta.Slug,
		Summary:     meta.Summary,
		Badge:       meta.Badge,
		Stage:       meta.Stage,
		Module:      meta.Module,
		Kind:        meta.Kind,
		Status:      normalizeArticleStatus(meta.Status, nil),
		ModuleOrder: meta.ModuleOrder,
		BlockOrder:  meta.BlockOrder,
	})
	if err != nil {
		return nil, err
	}

	document := "---\n" + string(frontMatterBytes) + "---\n\n" + strings.TrimSpace(body) + "\n"
	return []byte(document), nil
}

func SuggestedFileName(meta ArticleMeta) string {
	meta.Slug = normalizeSlug(meta.Slug)
	if meta.Slug == "" {
		meta.Slug = "new-lesson"
	}

	return fileNameForArticle(meta)
}

func fileNameForArticle(meta ArticleMeta) string {
	base := normalizeSlug(meta.Slug)
	if meta.ModuleOrder > 0 && meta.BlockOrder > 0 {
		return fmt.Sprintf("%02d-%02d-%s.md", meta.ModuleOrder, meta.BlockOrder, base)
	}
	if meta.ModuleOrder > 0 {
		return fmt.Sprintf("%02d-%s.md", meta.ModuleOrder, base)
	}
	return base + ".md"
}

func sortManagedArticles(articles []ManagedArticle) {
	sort.Slice(articles, func(i, j int) bool {
		switch {
		case articles[i].ModuleOrder != articles[j].ModuleOrder:
			return articles[i].ModuleOrder < articles[j].ModuleOrder
		case articles[i].BlockOrder != articles[j].BlockOrder:
			return articles[i].BlockOrder < articles[j].BlockOrder
		case articles[i].Order != articles[j].Order:
			return articles[i].Order < articles[j].Order
		default:
			return articles[i].Title < articles[j].Title
		}
	})
}

func samePath(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}
