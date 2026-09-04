package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"kite/store"
)

type bmAPIBase struct {
	dataDir string
}

func (b *bmAPIBase) getServer(userID string) (*store.ThirdPartyServer, error) {
	servers := store.ConnectedServersForType(b.dataDir, userID, "bm_api")
	if len(servers) == 0 {
		return nil, fmt.Errorf("no BM API server connected")
	}
	return &servers[0], nil
}

func (b *bmAPIBase) apiGet(ctx context.Context, srv *store.ThirdPartyServer, path string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(srv.URL, "/")+path, nil)
	req.Header.Set("Authorization", "Bearer "+srv.APIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bm api %d: %s", resp.StatusCode, previewBody(bodyBytes, 200))
	}
	return bodyBytes, nil
}

func (b *bmAPIBase) apiJSON(ctx context.Context, srv *store.ThirdPartyServer, method, path string, body interface{}) ([]byte, error) {
	var r io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		r = bytes.NewReader(data)
	}
	req, _ := http.NewRequestWithContext(ctx, method, strings.TrimRight(srv.URL, "/")+path, r)
	if req == nil {
		return nil, fmt.Errorf("invalid request")
	}
	req.Header.Set("Authorization", "Bearer "+srv.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bm api %d: %s", resp.StatusCode, previewBody(bodyBytes, 200))
	}
	return bodyBytes, nil
}

type BMSearch struct{ DataDir string }

func (t *BMSearch) Name() string        { return "bm_search" }
func (t *BMSearch) Description() string { return "Search bookmarks on a connected BM API server. Supports substring search (q), tag filter (tag), and semantic search (semantic)." }
func (t *BMSearch) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"query":    strParam("Search query for title/description substring match."),
		"tag":      strParam("Filter by exact tag."),
		"semantic": strParam("Semantic search query (TF-IDF similarity)."),
		"limit":    intParam("Max results (default 20)."),
	}, []string{})
}
func (t *BMSearch) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := bmAPIBase{dataDir: t.DataDir}
	srv, err := b.getServer(userID)
	if err != nil {
		return err.Error(), nil
	}
	u := fmt.Sprintf("/bookmarks?limit=%d", getIntArg(args, "limit", 20))
	if q := getStringArg(args, "query"); q != "" {
		u += "&q=" + escapeQuery(q)
	}
	if tag := getStringArg(args, "tag"); tag != "" {
		u += "&tag=" + escapeQuery(tag)
	}
	if sem := getStringArg(args, "semantic"); sem != "" {
		u += "&semantic=" + escapeQuery(sem)
	}
	respBody, err := b.apiGet(ctx, srv, u)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var result struct {
		Items []struct {
			ID          int    `json:"id"`
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
			Tags        string `json:"tags"`
		} `json:"items"`
		Total int `json:"total"`
	}
	json.Unmarshal(respBody, &result)
	if len(result.Items) == 0 {
		return "No bookmarks found.", nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d bookmarks (total %d):\n", len(result.Items), result.Total))
	for i, item := range result.Items {
		sb.WriteString(fmt.Sprintf("%d. [#%d] %s\n   %s | tags: %s\n", i+1, item.ID, item.Title, item.URL, item.Tags))
	}
	return sb.String(), nil
}

type BMCreate struct{ DataDir string }

func (t *BMCreate) Name() string        { return "bm_create" }
func (t *BMCreate) Description() string { return "Create a new bookmark on a connected BM API server." }
func (t *BMCreate) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"url":         strParam("Bookmark URL (must start with http:// or https://)."),
		"title":       strParam("Bookmark title."),
		"description": strParam("Optional description."),
		"tags":        strParam("Comma-separated tags."),
	}, []string{"title"})
}
func (t *BMCreate) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := bmAPIBase{dataDir: t.DataDir}
	srv, err := b.getServer(userID)
	if err != nil {
		return err.Error(), nil
	}
	body := map[string]string{
		"title":       getStringArg(args, "title"),
		"url":         getStringArg(args, "url"),
		"description": getStringArg(args, "description"),
		"tags":        getStringArg(args, "tags"),
	}
	respBody, err := b.apiJSON(ctx, srv, "POST", "/bookmarks", body)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var result struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	}
	json.Unmarshal(respBody, &result)
	if result.ID > 0 {
		return fmt.Sprintf("Created bookmark #%d: %s", result.ID, result.Title), nil
	}
	return fmt.Sprintf("Response: %s", previewBody(respBody, 300)), nil
}

type BMGet struct{ DataDir string }

func (t *BMGet) Name() string        { return "bm_get" }
func (t *BMGet) Description() string { return "Get a single bookmark by ID from a connected BM API server." }
func (t *BMGet) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"id": intParam("Bookmark ID."),
	}, []string{"id"})
}
func (t *BMGet) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := bmAPIBase{dataDir: t.DataDir}
	srv, err := b.getServer(userID)
	if err != nil {
		return err.Error(), nil
	}
	id := getIntArg(args, "id", 0)
	respBody, err := b.apiGet(ctx, srv, fmt.Sprintf("/bookmarks/%d", id))
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var result struct {
		ID          int    `json:"id"`
		Title       string `json:"title"`
		URL         string `json:"url"`
		Description string `json:"description"`
		Tags        string `json:"tags"`
		CreatedAt   string `json:"created_at"`
	}
	json.Unmarshal(respBody, &result)
	if result.ID == 0 {
		return fmt.Sprintf("Bookmark #%d not found.", id), nil
	}
	return fmt.Sprintf("#%d: %s\nurl: %s\ndesc: %s\ntags: %s\ncreated: %s",
		result.ID, result.Title, result.URL, result.Description, result.Tags, result.CreatedAt), nil
}

type BMUpdate struct{ DataDir string }

func (t *BMUpdate) Name() string        { return "bm_update" }
func (t *BMUpdate) Description() string { return "Update an existing bookmark on a connected BM API server." }
func (t *BMUpdate) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"id":          intParam("Bookmark ID to update."),
		"url":         strParam("New URL."),
		"title":       strParam("New title."),
		"description": strParam("New description."),
		"tags":        strParam("New comma-separated tags."),
	}, []string{"id"})
}
func (t *BMUpdate) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := bmAPIBase{dataDir: t.DataDir}
	srv, err := b.getServer(userID)
	if err != nil {
		return err.Error(), nil
	}
	id := getIntArg(args, "id", 0)
	body := make(map[string]string)
	if v := getStringArg(args, "title"); v != "" {
		body["title"] = v
	}
	if v := getStringArg(args, "url"); v != "" {
		body["url"] = v
	}
	if v := getStringArg(args, "description"); v != "" {
		body["description"] = v
	}
	if v := getStringArg(args, "tags"); v != "" {
		body["tags"] = v
	}
	respBody, err := b.apiJSON(ctx, srv, "PUT", fmt.Sprintf("/bookmarks/%d", id), body)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	if strings.Contains(string(respBody), `"id"`) {
		return fmt.Sprintf("Updated bookmark #%d.", id), nil
	}
	return fmt.Sprintf("Response: %s", previewBody(respBody, 300)), nil
}

type BMDelete struct{ DataDir string }

func (t *BMDelete) Name() string        { return "bm_delete" }
func (t *BMDelete) Description() string { return "Delete a bookmark by ID from a connected BM API server." }
func (t *BMDelete) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"id": intParam("Bookmark ID to delete."),
	}, []string{"id"})
}
func (t *BMDelete) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := bmAPIBase{dataDir: t.DataDir}
	srv, err := b.getServer(userID)
	if err != nil {
		return err.Error(), nil
	}
	id := getIntArg(args, "id", 0)
	respBody, err := b.apiJSON(ctx, srv, "DELETE", fmt.Sprintf("/bookmarks/%d", id), nil)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	_ = respBody
	return fmt.Sprintf("Deleted bookmark #%d.", id), nil
}

type BMList struct{ DataDir string }

func (t *BMList) Name() string        { return "bm_list" }
func (t *BMList) Description() string { return "List recent bookmarks from a connected BM API server with pagination." }
func (t *BMList) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"limit":  intParam("Max results (default 20)."),
		"offset": intParam("Pagination offset (default 0)."),
	}, []string{})
}
func (t *BMList) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := bmAPIBase{dataDir: t.DataDir}
	srv, err := b.getServer(userID)
	if err != nil {
		return err.Error(), nil
	}
	limit := getIntArg(args, "limit", 20)
	offset := getIntArg(args, "offset", 0)
	respBody, err := b.apiGet(ctx, srv, fmt.Sprintf("/bookmarks?limit=%d&offset=%d", limit, offset))
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var result struct {
		Items []struct {
			ID    int    `json:"id"`
			Title string `json:"title"`
			URL   string `json:"url"`
			Tags  string `json:"tags"`
		} `json:"items"`
		Total int `json:"total"`
	}
	json.Unmarshal(respBody, &result)
	if len(result.Items) == 0 {
		return "No bookmarks.", nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d bookmarks (offset %d, total %d):\n", len(result.Items), offset, result.Total))
	for i, item := range result.Items {
		sb.WriteString(fmt.Sprintf("%d. [#%d] %s\n   %s | %s\n", i+1, item.ID, item.Title, item.URL, item.Tags))
	}
	return sb.String(), nil
}
