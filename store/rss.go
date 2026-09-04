package store

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type RSSFeed struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	AddedAt   time.Time `json:"added_at"`
	LastFetch time.Time `json:"last_fetch"`
}

type RSSFeeds struct {
	Feeds []RSSFeed `json:"feeds"`
}

type RSSArticle struct {
	Title       string    `json:"title"`
	Link        string    `json:"link"`
	Description string    `json:"description"`
	PubDate     string    `json:"pub_date"`
	PublishedAt time.Time `json:"published_at"`
	GUID        string    `json:"guid"`
	FetchedAt   time.Time `json:"fetched_at"`
}

type RSSFeedCache struct {
	FeedID    string       `json:"feed_id"`
	FeedURL   string       `json:"feed_url"`
	FeedName  string       `json:"feed_name"`
	FetchedAt time.Time    `json:"fetched_at"`
	Articles  []RSSArticle `json:"articles"`
}

// RSS XML parsing structs
type rssXML struct {
	XMLName     xml.Name    `xml:"rss"`
	Title       string      `xml:"channel>title"`
	Description string      `xml:"channel>description"`
	Items       []rssItemXML `xml:"channel>item"`
}

type rssItemXML struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
	GUID        string `xml:"guid"`
}

var rssMu sync.Mutex

func rssPath(dataDir, userID string) string {
	return filepath.Join(dataDir, userID, "rss_feeds.json")
}

func articlesPath(dataDir, userID, feedID string) string {
	return filepath.Join(dataDir, userID, "rss_cache", feedID+".json")
}

func LoadRSSFeeds(dataDir, userID string) ([]RSSFeed, error) {
	rssMu.Lock()
	defer rssMu.Unlock()
	path := rssPath(dataDir, userID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var feeds RSSFeeds
	if err := json.Unmarshal(data, &feeds); err != nil {
		return nil, err
	}
	return feeds.Feeds, nil
}

func saveFeeds(dataDir, userID string, feeds []RSSFeed) error {
	path := rssPath(dataDir, userID)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return WriteJSON(path, RSSFeeds{Feeds: feeds})
}

func AddRSSFeed(dataDir, userID string, feed RSSFeed) error {
	rssMu.Lock()
	defer rssMu.Unlock()
	feeds, err := loadFeedsUnsafe(dataDir, userID)
	if err != nil {
		return err
	}
	for _, f := range feeds {
		if f.URL == feed.URL {
			return fmt.Errorf("feed already exists")
		}
	}
	feeds = append(feeds, feed)
	return saveFeeds(dataDir, userID, feeds)
}

func RemoveRSSFeed(dataDir, userID, feedID string) error {
	rssMu.Lock()
	defer rssMu.Unlock()
	feeds, err := loadFeedsUnsafe(dataDir, userID)
	if err != nil {
		return err
	}
	var kept []RSSFeed
	for _, f := range feeds {
		if f.ID != feedID {
			kept = append(kept, f)
		}
	}
	return saveFeeds(dataDir, userID, kept)
}

func UpdateRSSFetchTime(dataDir, userID, feedID string) error {
	rssMu.Lock()
	defer rssMu.Unlock()
	feeds, err := loadFeedsUnsafe(dataDir, userID)
	if err != nil {
		return err
	}
	for i, f := range feeds {
		if f.ID == feedID {
			feeds[i].LastFetch = time.Now()
			break
		}
	}
	return saveFeeds(dataDir, userID, feeds)
}

func HasRSSFeeds(dataDir, userID string) bool {
	feeds, _ := LoadRSSFeeds(dataDir, userID)
	return len(feeds) > 0
}

func loadFeedsUnsafe(dataDir, userID string) ([]RSSFeed, error) {
	data, err := os.ReadFile(rssPath(dataDir, userID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var feeds RSSFeeds
	if err := json.Unmarshal(data, &feeds); err != nil {
		return nil, err
	}
	return feeds.Feeds, nil
}

// FetchFeedURL fetches and parses an RSS feed from a URL.
// Returns feed title, description, articles, and error.
func FetchFeedURL(url string) (string, string, []RSSArticle, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", "", nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; KiteRSS/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", nil, fmt.Errorf("fetching feed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", nil, fmt.Errorf("feed returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", nil, fmt.Errorf("reading response: %w", err)
	}

	var feed rssXML
	if err := xml.Unmarshal(body, &feed); err != nil {
		return "", "", nil, fmt.Errorf("parsing XML: %w", err)
	}

	now := time.Now()
	articles := make([]RSSArticle, 0, len(feed.Items))
	for _, item := range feed.Items {
		article := RSSArticle{
			Title:       item.Title,
			Link:        item.Link,
			Description: stripHTMLTags(item.Description),
			PubDate:     item.PubDate,
			GUID:        item.GUID,
			FetchedAt:   now,
		}
		if t, err := parseRSSDate(item.PubDate); err == nil {
			article.PublishedAt = t
		}
		articles = append(articles, article)
	}

	return feed.Title, feed.Description, articles, nil
}

// FetchAndSaveFeed fetches a single feed and caches the articles.
func FetchAndSaveFeed(dataDir, userID, feedID string) error {
	rssMu.Lock()
	feeds, err := loadFeedsUnsafe(dataDir, userID)
	rssMu.Unlock()
	if err != nil {
		return err
	}

	var target *RSSFeed
	for i := range feeds {
		if feeds[i].ID == feedID {
			target = &feeds[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("feed not found: %s", feedID)
	}

	_, _, articles, err := FetchFeedURL(target.URL)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", target.URL, err)
	}

	now := time.Now()
	cache := RSSFeedCache{
		FeedID:    feedID,
		FeedURL:   target.URL,
		FeedName:  target.Name,
		FetchedAt: now,
		Articles:  articles,
	}

	dir := filepath.Dir(articlesPath(dataDir, userID, feedID))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating cache dir: %w", err)
	}
	if err := WriteJSON(articlesPath(dataDir, userID, feedID), cache); err != nil {
		return fmt.Errorf("saving cache: %w", err)
	}

	rssMu.Lock()
	feeds2, _ := loadFeedsUnsafe(dataDir, userID)
	for i := range feeds2 {
		if feeds2[i].ID == feedID {
			feeds2[i].LastFetch = now
			break
		}
	}
	err2 := saveFeeds(dataDir, userID, feeds2)
	rssMu.Unlock()
	return err2
}

// LoadCachedArticles reads cached articles for a feed.
func LoadCachedArticles(dataDir, userID, feedID string) (*RSSFeedCache, error) {
	path := articlesPath(dataDir, userID, feedID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var cache RSSFeedCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	return &cache, nil
}

// RefreshAllFeeds iterates all users and fetches all their RSS feeds.
func RefreshAllFeeds(dataDir string) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		userID := entry.Name()
		feeds, err := LoadRSSFeeds(dataDir, userID)
		if err != nil || len(feeds) == 0 {
			continue
		}
		for _, feed := range feeds {
			if err := FetchAndSaveFeed(dataDir, userID, feed.ID); err != nil {
				continue
			}
		}
	}
}

func parseRSSDate(s string) (time.Time, error) {
	formats := []string{
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		"Mon, 02 Jan 2006 15:04:05 -0700",
		"Mon, 2 Jan 2006 15:04:05 -0700",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05-07:00",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse date: %s", s)
}

func stripHTMLTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}
