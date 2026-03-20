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
}

type ArticleMeta struct {
	Title       string
	Slug        string
	Summary     string
	Badge       string
	Stage       string
	ReadingTime string
	Order       int
	Published   bool
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
	ReadingTime string `yaml:"reading_time"`
	Order       int    `yaml:"order"`
	Published   *bool  `yaml:"published"`
}

func NewLibrary(dir string) *Library {
	return &Library{
		dir: strings.TrimSpace(dir),
		renderer: goldmark.New(
			goldmark.WithExtensions(extension.GFM),
			goldmark.WithRendererOptions(rendererhtml.WithUnsafe()),
		),
	}
}

func (l *Library) List() ([]ArticleMeta, error) {
	files, err := l.articleFiles()
	if err != nil {
		return nil, err
	}

	articles := make([]ArticleMeta, 0, len(files))
	for _, path := range files {
		meta, _, err := l.parseFile(path)
		if err != nil {
			return nil, err
		}
		if !meta.Published {
			continue
		}

		articles = append(articles, meta)
	}

	sort.Slice(articles, func(i, j int) bool {
		if articles[i].Order == articles[j].Order {
			return articles[i].Title < articles[j].Title
		}
		return articles[i].Order < articles[j].Order
	})

	return articles, nil
}

func (l *Library) ArticleBySlug(slug string) (*Article, error) {
	files, err := l.articleFiles()
	if err != nil {
		return nil, err
	}

	normalizedSlug := normalizeSlug(slug)
	for _, path := range files {
		meta, body, err := l.parseFile(path)
		if err != nil {
			return nil, err
		}
		if !meta.Published || meta.Slug != normalizedSlug {
			continue
		}

		var rendered bytes.Buffer
		if err := l.renderer.Convert([]byte(body), &rendered); err != nil {
			return nil, fmt.Errorf("render markdown %s: %w", path, err)
		}

		return &Article{
			ArticleMeta: meta,
			HTML:        template.HTML(rendered.String()),
		}, nil
	}

	return nil, ErrArticleNotFound
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
		ReadingTime: strings.TrimSpace(fm.ReadingTime),
		Order:       fm.Order,
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
