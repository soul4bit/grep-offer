package content

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	rendererhtml "github.com/yuin/goldmark/renderer/html"
	"gopkg.in/yaml.v3"
)

var ErrArticleNotFound = errors.New("article not found")

type Library struct {
	dir      string
	renderer goldmark.Markdown
	mu       sync.RWMutex
	cache    *librarySnapshot
}

type librarySnapshot struct {
	signature string
	articles  []cachedArticle
	slugIndex map[string]int
}

type cachedArticle struct {
	path      string
	meta      ArticleMeta
	body      string
	html      template.HTML
	updatedAt time.Time
}

type ArticleMeta struct {
	Title       string
	Slug        string
	Summary     string
	Badge       string
	Stage       string
	Module      string
	Kind        string
	ReadingTime string
	Order       int
	ModuleOrder int
	BlockOrder  int
	Published   bool
}

type Module struct {
	Title    string
	Index    string
	Order    int
	Lessons  []ArticleMeta
	LessonCt int
}

type Lesson struct {
	Article
	Module      Module
	ModuleItems []ArticleMeta
	Prev        *ArticleMeta
	Next        *ArticleMeta
}

type Article struct {
	ArticleMeta
	HTML template.HTML
}

type frontMatter struct {
	Title       string `yaml:"title"`
	Slug        string `yaml:"slug"`
	Summary     string `yaml:"summary"`
	Badge       string `yaml:"badge"`
	Stage       string `yaml:"stage"`
	Module      string `yaml:"module"`
	Kind        string `yaml:"kind"`
	ReadingTime string `yaml:"reading_time"`
	Order       int    `yaml:"order"`
	ModuleOrder int    `yaml:"module_order"`
	BlockOrder  int    `yaml:"block_order"`
	Published   *bool  `yaml:"published"`
}

func NewLibrary(dir string) *Library {
	return &Library{
		dir:      strings.TrimSpace(dir),
		renderer: newMarkdownRenderer(),
	}
}

func RenderMarkdown(body string) (template.HTML, error) {
	var rendered bytes.Buffer
	if err := newMarkdownRenderer().Convert([]byte(strings.TrimSpace(body)), &rendered); err != nil {
		return "", err
	}

	return template.HTML(rendered.String()), nil
}

func (l *Library) List() ([]ArticleMeta, error) {
	snapshot, err := l.snapshot()
	if err != nil {
		return nil, err
	}

	articles := make([]ArticleMeta, 0, len(snapshot.articles))
	for _, article := range snapshot.articles {
		if !article.meta.Published {
			continue
		}

		articles = append(articles, article.meta)
	}

	return articles, nil
}

func (l *Library) Curriculum() ([]Module, error) {
	articles, err := l.List()
	if err != nil {
		return nil, err
	}

	moduleMap := make(map[string]*Module)
	moduleOrder := make([]string, 0, 8)

	for _, article := range articles {
		key := fmt.Sprintf("%04d:%s", article.ModuleOrder, article.Module)
		module, ok := moduleMap[key]
		if !ok {
			module = &Module{
				Title: article.Module,
				Index: moduleIndex(article.ModuleOrder),
				Order: article.ModuleOrder,
			}
			moduleMap[key] = module
			moduleOrder = append(moduleOrder, key)
		}

		module.Lessons = append(module.Lessons, article)
		module.LessonCt++
	}

	sort.Slice(moduleOrder, func(i, j int) bool {
		return moduleOrder[i] < moduleOrder[j]
	})

	modules := make([]Module, 0, len(moduleOrder))
	for _, key := range moduleOrder {
		module := moduleMap[key]
		sortArticles(module.Lessons)
		modules = append(modules, *module)
	}

	return modules, nil
}

func (l *Library) LessonBySlug(slug string) (*Lesson, error) {
	snapshot, err := l.snapshot()
	if err != nil {
		return nil, err
	}

	normalizedSlug := normalizeSlug(slug)
	if normalizedSlug == "" {
		return nil, ErrArticleNotFound
	}

	targetIndex, ok := snapshot.slugIndex[normalizedSlug]
	if !ok {
		return nil, ErrArticleNotFound
	}
	target := snapshot.articles[targetIndex]
	if !target.meta.Published {
		return nil, ErrArticleNotFound
	}

	published := make([]cachedArticle, 0, len(snapshot.articles))
	for _, article := range snapshot.articles {
		if article.meta.Published {
			published = append(published, article)
		}
	}

	lesson := &Lesson{
		Article: Article{
			ArticleMeta: target.meta,
			HTML:        target.html,
		},
		Module: Module{
			Title: target.meta.Module,
			Index: moduleIndex(target.meta.ModuleOrder),
			Order: target.meta.ModuleOrder,
		},
	}

	for i := range published {
		if published[i].meta.ModuleOrder == target.meta.ModuleOrder && published[i].meta.Module == target.meta.Module {
			lesson.ModuleItems = append(lesson.ModuleItems, published[i].meta)
		}
		if published[i].meta.Slug != target.meta.Slug {
			continue
		}
		if i > 0 {
			prev := published[i-1].meta
			lesson.Prev = &prev
		}
		if i+1 < len(published) {
			next := published[i+1].meta
			lesson.Next = &next
		}
	}

	lesson.Module.Lessons = append([]ArticleMeta(nil), lesson.ModuleItems...)
	lesson.Module.LessonCt = len(lesson.ModuleItems)

	return lesson, nil
}

func (l *Library) ArticleBySlug(slug string) (*Article, error) {
	lesson, err := l.LessonBySlug(slug)
	if err != nil {
		return nil, err
	}
	return &lesson.Article, nil
}

func (l *Library) snapshot() (*librarySnapshot, error) {
	if l == nil {
		return &librarySnapshot{}, nil
	}

	signature, files, err := l.signature()
	if err != nil {
		return nil, err
	}

	l.mu.RLock()
	if l.cache != nil && l.cache.signature == signature {
		snapshot := l.cache
		l.mu.RUnlock()
		return snapshot, nil
	}
	l.mu.RUnlock()

	snapshot, err := l.loadSnapshot(signature, files)
	if err != nil {
		return nil, err
	}

	l.mu.Lock()
	l.cache = snapshot
	l.mu.Unlock()

	return snapshot, nil
}

func (l *Library) invalidateCache() {
	if l == nil {
		return
	}

	l.mu.Lock()
	l.cache = nil
	l.mu.Unlock()
}

func (l *Library) signature() (string, []string, error) {
	files, err := l.articleFiles()
	if err != nil {
		return "", nil, err
	}

	var builder strings.Builder
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			return "", nil, err
		}
		builder.WriteString(path)
		builder.WriteByte('|')
		builder.WriteString(fmt.Sprintf("%d|%d\n", info.ModTime().UTC().UnixNano(), info.Size()))
	}

	return builder.String(), files, nil
}

func (l *Library) loadSnapshot(signature string, files []string) (*librarySnapshot, error) {
	articles := make([]cachedArticle, 0, len(files))
	for _, path := range files {
		meta, body, err := l.parseFile(path)
		if err != nil {
			return nil, err
		}

		var rendered bytes.Buffer
		if err := l.renderer.Convert([]byte(body), &rendered); err != nil {
			return nil, fmt.Errorf("render markdown %s: %w", meta.Slug, err)
		}

		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}

		articles = append(articles, cachedArticle{
			path:      path,
			meta:      meta,
			body:      body,
			html:      template.HTML(rendered.String()),
			updatedAt: info.ModTime().UTC(),
		})
	}

	sort.Slice(articles, func(i, j int) bool {
		switch {
		case articles[i].meta.ModuleOrder != articles[j].meta.ModuleOrder:
			return articles[i].meta.ModuleOrder < articles[j].meta.ModuleOrder
		case articles[i].meta.BlockOrder != articles[j].meta.BlockOrder:
			return articles[i].meta.BlockOrder < articles[j].meta.BlockOrder
		case articles[i].meta.Order != articles[j].meta.Order:
			return articles[i].meta.Order < articles[j].meta.Order
		default:
			return articles[i].meta.Title < articles[j].meta.Title
		}
	})

	snapshot := &librarySnapshot{
		signature: signature,
		articles:  articles,
		slugIndex: make(map[string]int, len(articles)),
	}
	for index, article := range articles {
		if article.meta.Slug == "" {
			continue
		}
		snapshot.slugIndex[article.meta.Slug] = index
	}

	return snapshot, nil
}

func (l *Library) articleFiles() ([]string, error) {
	if l == nil || l.dir == "" {
		return nil, nil
	}

	info, err := os.Stat(l.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("content dir is not a directory: %s", l.dir)
	}

	files := make([]string, 0, 16)
	err = filepath.WalkDir(l.dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(files)
	return files, nil
}

func (l *Library) parseFile(path string) (ArticleMeta, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ArticleMeta{}, "", err
	}

	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	metaRaw, body := splitFrontMatter(content)

	var fm frontMatter
	if metaRaw != "" {
		if err := yaml.Unmarshal([]byte(metaRaw), &fm); err != nil {
			return ArticleMeta{}, "", fmt.Errorf("parse front matter %s: %w", path, err)
		}
	}

	meta := ArticleMeta{
		Title:       strings.TrimSpace(fm.Title),
		Slug:        normalizeSlug(fm.Slug),
		Summary:     strings.TrimSpace(fm.Summary),
		Badge:       strings.TrimSpace(fm.Badge),
		Stage:       strings.TrimSpace(fm.Stage),
		Module:      strings.TrimSpace(fm.Module),
		Kind:        normalizeKind(fm.Kind),
		ReadingTime: strings.TrimSpace(fm.ReadingTime),
		Order:       fm.Order,
		ModuleOrder: fm.ModuleOrder,
		BlockOrder:  fm.BlockOrder,
		Published:   fm.Published == nil || *fm.Published,
	}

	if meta.Slug == "" {
		meta.Slug = normalizeSlug(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	}
	if meta.Title == "" {
		meta.Title = titleFromBody(body)
	}
	if meta.Summary == "" {
		meta.Summary = summaryFromBody(body)
	}
	if meta.ReadingTime == "" {
		meta.ReadingTime = estimateReadingTime(body)
	}
	if meta.Module == "" {
		meta.Module = meta.Stage
	}
	if meta.Stage == "" {
		meta.Stage = "Linux"
	}
	if meta.Badge == "" {
		meta.Badge = "linux"
	}
	if meta.Kind == "" {
		meta.Kind = "theory"
	}
	if meta.Order == 0 {
		meta.Order = meta.ModuleOrder*100 + meta.BlockOrder
	}

	return meta, strings.TrimSpace(body), nil
}

func splitFrontMatter(document string) (string, string) {
	if !strings.HasPrefix(document, "---\n") {
		return "", document
	}

	rest := strings.TrimPrefix(document, "---\n")
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return "", document
	}

	return rest[:end], rest[end+5:]
}

func titleFromBody(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if line != "" {
			return line
		}
	}

	return "Статья"
}

func summaryFromBody(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "```") {
			continue
		}

		if len([]rune(line)) > 170 {
			runes := []rune(line)
			return strings.TrimSpace(string(runes[:170])) + "…"
		}

		return line
	}

	return "Практический конспект по этапу роадмапа."
}

func estimateReadingTime(body string) string {
	wordCount := 0
	for _, token := range strings.FieldsFunc(body, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsNumber(r))
	}) {
		if token != "" {
			wordCount++
		}
	}

	minutes := int(math.Ceil(float64(max(wordCount, 1)) / 180.0))
	if minutes < 1 {
		minutes = 1
	}

	return fmt.Sprintf("~%d мин", minutes)
}

func normalizeSlug(value string) string {
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
		case r == '-' || r == '_' || unicode.IsSpace(r):
			if !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}

	return strings.Trim(builder.String(), "-")
}

func NormalizeSlug(value string) string {
	return normalizeSlug(value)
}

func normalizeKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "practice":
		return "practice"
	case "test":
		return "test"
	default:
		return "theory"
	}
}

func sortArticles(articles []ArticleMeta) {
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

func moduleIndex(order int) string {
	if order <= 0 {
		return "0"
	}
	return fmt.Sprintf("%d", order)
}

func newMarkdownRenderer() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(rendererhtml.WithUnsafe()),
	)
}
