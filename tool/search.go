package tool

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var insecureClient = &http.Client{
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	},
}

type SearXNGConfig struct {
	Endpoints []string
	Timeout   time.Duration
	Limit     int
}

type WebSearch struct {
	Config SearXNGConfig
}

func (t *WebSearch) Name() string        { return "web_search" }
func (t *WebSearch) Description() string { return "Search the web using an anonymous search engine. Returns titles, URLs, and content snippets. Use to find current information, facts, or answers to questions." }
func (t *WebSearch) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"query": strParam("What to search for. Be specific."),
		"limit": intParam("Max results to return (default 5, max 10)."),
		"categories": strEnumParam("Filter by category. Default is 'general', which searches all categories.", []string{
			"general", "news", "images", "videos", "maps", "music", "files", "science", "it", "social_media",
		}),
	}, []string{"query"})
}

func (t *WebSearch) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	query := getStringArg(args, "query")
	if query == "" {
		return "Error: query is required", nil
	}
	limit := getIntArg(args, "limit", 5)
	if limit > 10 {
		limit = 10
	}
	if limit < 1 {
		limit = 5
	}
	categories := getStringArg(args, "categories")
	if categories == "" {
		categories = "general"
	}

	if len(t.Config.Endpoints) == 0 {
		return "Search is not configured on this server.", nil
	}

	var lastErr error
	for _, endpoint := range t.Config.Endpoints {
		results, err := searchEndpoint(ctx, endpoint, query, limit, categories, t.Config.Timeout)
		if err == nil {
			return results, nil
		}
		lastErr = err
	}
	return fmt.Sprintf("All search backends unreachable (last error: %v)", lastErr), nil
}

func searchEndpoint(ctx context.Context, endpoint, query string, limit int, categories string, timeout time.Duration) (string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	params := url.Values{}
	params.Set("q", query)
	params.Set("format", "json")
	params.Set("categories", categories)

	reqURL := fmt.Sprintf("%s/search?%s", strings.TrimRight(endpoint, "/"), params.Encode())
	req, err := http.NewRequestWithContext(reqCtx, "GET", reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := insecureClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("%s returned %d", endpoint, resp.StatusCode)
	}

	var result struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}

	if len(result.Results) == 0 {
		return fmt.Sprintf("No results for '%s'.", query), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search results for '%s' (%d found, showing %d):\n", query, len(result.Results), min(limit, len(result.Results))))
	for i, r := range result.Results {
		if i >= limit {
			break
		}
		snippet := r.Content
		if snippet == "" {
			snippet = r.Snippet
		}
		if len(snippet) > 300 {
			snippet = snippet[:300] + "..."
		}
		sb.WriteString(fmt.Sprintf("\n%d. %s\n   URL: %s\n   %s\n", i+1, r.Title, r.URL, snippet))
	}
	return sb.String(), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
