package app

import (
	"errors"
	"net/http"

	"grep-offer/internal/content"
)

func (a *App) handleArticlesIndex(w http.ResponseWriter, r *http.Request) {
	articles, err := a.loadFeaturedArticles(0)
	if err != nil {
		http.Error(w, "load articles failed", http.StatusInternalServerError)
		return
	}

	a.render(w, r, http.StatusOK, "articles", ViewData{
		Notice:   noticeFromRequest(r),
		Articles: articles,
	})
}

func (a *App) handleArticleShow(w http.ResponseWriter, r *http.Request) {
	if a.articles == nil {
		http.NotFound(w, r)
		return
	}

	article, err := a.articles.ArticleBySlug(r.PathValue("slug"))
	if err != nil {
		if errors.Is(err, content.ErrArticleNotFound) {
			http.NotFound(w, r)
			return
		}

		http.Error(w, "load article failed", http.StatusInternalServerError)
		return
	}

	a.render(w, r, http.StatusOK, "article", ViewData{
		Notice: noticeFromRequest(r),
		Article: &ArticlePage{
			Title:       article.Title,
			Slug:        article.Slug,
			Summary:     article.Summary,
			Badge:       article.Badge,
			Stage:       article.Stage,
			ReadingTime: article.ReadingTime,
			HTML:        article.HTML,
		},
	})
}

func (a *App) loadFeaturedArticles(limit int) ([]ArticleCard, error) {
	if a.articles == nil {
		return nil, nil
	}

	articles, err := a.articles.List()
	if err != nil {
		return nil, err
	}

	if limit > 0 && len(articles) > limit {
		articles = articles[:limit]
	}

	cards := make([]ArticleCard, 0, len(articles))
	for _, article := range articles {
		cards = append(cards, ArticleCard{
			Title:       article.Title,
			Slug:        article.Slug,
			Summary:     article.Summary,
			Badge:       article.Badge,
			Stage:       article.Stage,
			ReadingTime: article.ReadingTime,
		})
	}

	return cards, nil
}
