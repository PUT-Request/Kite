package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"kite/store"
)

type RSSFetch struct {
	DataDir string
}

func (t *RSSFetch) Name() string { return "rss_fetch" }
func (t *RSSFetch) Description() string { return "Fetch and read articles from any RSS feed URL. Returns article titles, links, descriptions, and publish dates." }
func (t *RSSFetch) Parameters() map[string]interface{} {
	return ObjectParams(map[string]interface{}{
		"url": StrParam("The RSS feed URL to fetch."),
	}, []string{"url"})
}
func (t *RSSFetch) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	url := ExtractArgString(args, "url")
	if url == "" {
		return "Error: url is required", nil
	}

	title, desc, articles, err := store.FetchFeedURL(url)
	if err != nil {
		return fmt.Sprintf("Error fetching feed: %v", err), nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Feed: %s\n", title))
	if desc != "" {
		b.WriteString(fmt.Sprintf("Description: %s\n", desc))
	}
	b.WriteString(fmt.Sprintf("Articles: %d\n\n", len(articles)))

	for i, article := range articles {
		if i >= 20 {
			b.WriteString(fmt.Sprintf("\n... and %d more", len(articles)-20))
			break
		}
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, article.Title))
		if !article.PublishedAt.IsZero() {
			b.WriteString(fmt.Sprintf("   Date: %s\n", article.PublishedAt.Format("Jan 2, 2006 15:04")))
		} else if article.PubDate != "" {
			b.WriteString(fmt.Sprintf("   Date: %s\n", article.PubDate))
		}
		if article.Link != "" {
			b.WriteString(fmt.Sprintf("   Link: %s\n", article.Link))
		}
		desc := article.Description
		if len(desc) > 200 {
			desc = desc[:200] + "..."
		}
		if desc != "" {
			b.WriteString(fmt.Sprintf("   %s\n", desc))
		}
		b.WriteString("\n")
	}

	return b.String(), nil
}

type RSSList struct {
	DataDir string
}

func (t *RSSList) Name() string { return "rss_list" }
func (t *RSSList) Description() string { return "List the user's saved RSS feeds with article counts. Only available if feeds have been added." }
func (t *RSSList) Parameters() map[string]interface{} {
	return ObjectParams(map[string]interface{}{}, nil)
}
func (t *RSSList) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	feeds, err := store.LoadRSSFeeds(t.DataDir, userID)
	if err != nil {
		return fmt.Sprintf("Error loading feeds: %v", err), nil
	}
	if len(feeds) == 0 {
		return "No RSS feeds saved. Add feeds in the dashboard.", nil
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Saved RSS feeds (%d):\n\n", len(feeds)))
	for i, f := range feeds {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, f.Name))
		b.WriteString(fmt.Sprintf("   URL: %s\n", f.URL))
		if !f.LastFetch.IsZero() {
			b.WriteString(fmt.Sprintf("   Last fetched: %s\n", f.LastFetch.Format("Jan 2, 2006 15:04")))
		} else {
			b.WriteString("   Last fetched: never\n")
		}

		cache, _ := store.LoadCachedArticles(t.DataDir, userID, f.ID)
		if cache != nil {
			b.WriteString(fmt.Sprintf("   Cached articles: %d\n", len(cache.Articles)))
			b.WriteString(fmt.Sprintf("   Cached at: %s\n", cache.FetchedAt.Format("Jan 2, 2006 15:04")))
		} else {
			b.WriteString("   Cached articles: none\n")
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

type RSSArticles struct {
	DataDir string
}

func (t *RSSArticles) Name() string { return "rss_articles" }
func (t *RSSArticles) Description() string { return "Read cached articles for a saved RSS feed by its ID. Use rss_list first to see feed IDs and article counts." }
func (t *RSSArticles) Parameters() map[string]interface{} {
	return ObjectParams(map[string]interface{}{
		"feed_id": StrParam("The ID of the RSS feed to get articles for."),
	}, []string{"feed_id"})
}
func (t *RSSArticles) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	feedID := ExtractArgString(args, "feed_id")
	if feedID == "" {
		return "Error: feed_id is required", nil
	}

	cache, err := store.LoadCachedArticles(t.DataDir, userID, feedID)
	if err != nil {
		return fmt.Sprintf("Error loading cached articles: %v", err), nil
	}
	if cache == nil {
		return "No cached articles found. Wait for the next auto-fetch or use rss_fetch with the feed URL.", nil
	}

	elapsed := time.Since(cache.FetchedAt).Round(time.Minute)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Feed: %s\n", cache.FeedName))
	b.WriteString(fmt.Sprintf("Cached %s ago — %d articles\n\n", elapsed, len(cache.Articles)))

	for i, article := range cache.Articles {
		if i >= 20 {
			b.WriteString(fmt.Sprintf("\n... and %d more", len(cache.Articles)-20))
			break
		}
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, article.Title))
		if !article.PublishedAt.IsZero() {
			b.WriteString(fmt.Sprintf("   Date: %s\n", article.PublishedAt.Format("Jan 2, 2006 15:04")))
		} else if article.PubDate != "" {
			b.WriteString(fmt.Sprintf("   Date: %s\n", article.PubDate))
		}
		if article.Link != "" {
			b.WriteString(fmt.Sprintf("   Link: %s\n", article.Link))
		}
		desc := article.Description
		if len(desc) > 200 {
			desc = desc[:200] + "..."
		}
		if desc != "" {
			b.WriteString(fmt.Sprintf("   %s\n", desc))
		}
		b.WriteString("\n")
	}

	return b.String(), nil
}
