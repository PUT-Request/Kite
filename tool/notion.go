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

type notionBase struct {
	dataDir string
}

func (b *notionBase) token(userID string) (string, error) {
	i, err := store.LoadIntegration(b.dataDir, userID, "notion")
	if err != nil || !i.Connected {
		return "", fmt.Errorf("notion not connected")
	}
	return i.AccessToken, nil
}

func (b *notionBase) apiCall(ctx context.Context, method, url string, body interface{}, token string) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Notion-Version", "2022-06-28")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("notion api %d: %s", resp.StatusCode, previewBody(bodyBytes, 200))
	}
	return bodyBytes, nil
}

type NotionSearch struct{ DataDir string }

func (t *NotionSearch) Name() string        { return "notion_search" }
func (t *NotionSearch) Description() string { return "Search all Notion pages and databases by title. Returns matching items with IDs and types." }
func (t *NotionSearch) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"query": strParam("Search query text."),
		"limit": intParam("Max results (default 10)."),
	}, []string{"query"})
}
func extractNotionTitle(item map[string]interface{}) string {
	if props, ok := item["properties"].(map[string]interface{}); ok {
		for _, prop := range props {
			p, ok := prop.(map[string]interface{})
			if !ok {
				continue
			}
			ptype, _ := p["type"].(string)
			if ptype != "title" {
				continue
			}
			titleArr, _ := p["title"].([]interface{})
			if len(titleArr) > 0 {
				if first, ok := titleArr[0].(map[string]interface{}); ok {
					if pt, ok := first["plain_text"].(string); ok {
						return pt
					}
				}
			}
		}
	}
	if titleArr, ok := item["title"].([]interface{}); ok && len(titleArr) > 0 {
		if first, ok := titleArr[0].(map[string]interface{}); ok {
			if pt, ok := first["plain_text"].(string); ok {
				return pt
			}
		}
	}
	return "Untitled"
}

func extractNotionType(item map[string]interface{}) string {
	t, _ := item["object"].(string)
	return t
}

func (t *NotionSearch) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := notionBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	query := getStringArg(args, "query")
	limit := getIntArg(args, "limit", 10)
	respBody, err := b.apiCall(ctx, "POST", "https://api.notion.com/v1/search", map[string]interface{}{
		"query":    query,
		"page_size": limit,
	}, token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var result struct {
		Results []map[string]interface{} `json:"results"`
	}
	json.Unmarshal(respBody, &result)
	if len(result.Results) == 0 {
		return fmt.Sprintf("No Notion results for '%s'.", query), nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Notion search results for '%s':\n", query))
	for i, item := range result.Results {
		if i >= limit {
			break
		}
		id, _ := item["id"].(string)
		url, _ := item["url"].(string)
		title := extractNotionTitle(item)
		objType := extractNotionType(item)
		shortID := id
		if len(shortID) > 8 {
			shortID = id[:8] + "..."
		}
		sb.WriteString(fmt.Sprintf("%d. [%s] %s (%s)\n", i+1, objType, title, shortID))
		_ = url
	}
	return sb.String(), nil
}

type NotionReadPage struct{ DataDir string }

func (t *NotionReadPage) Name() string        { return "notion_read_page" }
func (t *NotionReadPage) Description() string { return "Read a Notion page's content (blocks). Returns text content of all blocks on the page." }
func (t *NotionReadPage) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"page_id": strParam("The Notion page ID (UUID from URL or search results)."),
	}, []string{"page_id"})
}
func (t *NotionReadPage) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := notionBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	pageID := getStringArg(args, "page_id")
	respBody, err := b.apiCall(ctx, "GET", "https://api.notion.com/v1/blocks/"+pageID+"/children?page_size=50", nil, token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var result struct {
		Results []map[string]interface{} `json:"results"`
		HasMore bool                     `json:"has_more"`
	}
	json.Unmarshal(respBody, &result)
	if len(result.Results) == 0 {
		return "Page is empty or not accessible.", nil
	}
	var sb strings.Builder
	sb.WriteString("Page content:\n")
	for _, block := range result.Results {
		if text, ok := extractBlockText(block); ok {
			sb.WriteString(text)
			sb.WriteString("\n")
		}
	}
	if result.HasMore {
		sb.WriteString("\n(has_more: true — call again with offset for next page)")
	}
	return sb.String(), nil
}

func extractBlockText(block map[string]interface{}) (string, bool) {
	blockType, _ := block["type"].(string)
	typeData, ok := block[blockType].(map[string]interface{})
	if !ok {
		return "", false
	}
	texts, ok := typeData["rich_text"].([]interface{})
	if !ok {
		return "", false
	}
	var sb strings.Builder
	for _, t := range texts {
		tm, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		if plain, ok := tm["plain_text"].(string); ok {
			sb.WriteString(plain)
		}
	}
	return sb.String(), sb.Len() > 0
}

type NotionCreatePage struct{ DataDir string }

func (t *NotionCreatePage) Name() string        { return "notion_create_page" }
func (t *NotionCreatePage) Description() string { return "Create a new Notion page in a parent page or database." }
func (t *NotionCreatePage) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"parent_id": strParam("Parent page or database ID."),
		"title":     strParam("Page title."),
		"content":   strParam("Page content (plain text)."),
	}, []string{"parent_id", "title"})
}
func (t *NotionCreatePage) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := notionBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	parentID := getStringArg(args, "parent_id")
	title := getStringArg(args, "title")
	content := getStringArg(args, "content")

	makeBody := func(parentType string) map[string]interface{} {
		return map[string]interface{}{
			"parent": map[string]interface{}{parentType: parentID},
			"properties": map[string]interface{}{
				"title": map[string]interface{}{
					"title": []map[string]interface{}{
						{"type": "text", "text": map[string]interface{}{"content": title}},
					},
				},
			},
			"children": []map[string]interface{}{
				{
					"object": "block",
					"type":   "paragraph",
					"paragraph": map[string]interface{}{
						"rich_text": []map[string]interface{}{
							{"type": "text", "text": map[string]interface{}{"content": content}},
						},
					},
				},
			},
		}
	}

	respBody, err := b.apiCall(ctx, "POST", "https://api.notion.com/v1/pages", makeBody("page_id"), token)
	if err != nil && strings.Contains(err.Error(), "database_id") {
		respBody, err = b.apiCall(ctx, "POST", "https://api.notion.com/v1/pages", makeBody("database_id"), token)
	}
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var result struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	json.Unmarshal(respBody, &result)
	if result.ID != "" {
		return fmt.Sprintf("Page created: %s (%s)", title, result.URL), nil
	}
	return fmt.Sprintf("Response: %s", previewBody(respBody, 200)), nil
}

type NotionUpdatePage struct{ DataDir string }

func (t *NotionUpdatePage) Name() string        { return "notion_update_page" }
func (t *NotionUpdatePage) Description() string { return "Update a Notion page's properties or append content blocks." }
func (t *NotionUpdatePage) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"page_id": strParam("Page ID to update."),
		"content": strParam("New text content to append to the page."),
	}, []string{"page_id", "content"})
}
func (t *NotionUpdatePage) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := notionBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	pageID := getStringArg(args, "page_id")
	content := getStringArg(args, "content")

	body := map[string]interface{}{
		"children": []map[string]interface{}{
			{
				"object": "block",
				"type":   "paragraph",
				"paragraph": map[string]interface{}{
					"rich_text": []map[string]interface{}{
						{"type": "text", "text": map[string]interface{}{"content": content}},
					},
				},
			},
		},
	}

	respBody, err := b.apiCall(ctx, "PATCH", "https://api.notion.com/v1/blocks/"+pageID+"/children", body, token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	if strings.Contains(string(respBody), `"id"`) {
		return fmt.Sprintf("Appended content to page %s.", pageID[:8]+"..."), nil
	}
	return fmt.Sprintf("Response: %s", previewBody(respBody, 200)), nil
}

type NotionListDatabases struct{ DataDir string }

func (t *NotionListDatabases) Name() string        { return "notion_list_databases" }
func (t *NotionListDatabases) Description() string { return "List all Notion databases the integration has access to." }
func (t *NotionListDatabases) Parameters() map[string]interface{} {
	return params(map[string]interface{}{}, nil)
}
func (t *NotionListDatabases) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := notionBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	respBody, err := b.apiCall(ctx, "POST", "https://api.notion.com/v1/search", map[string]interface{}{
		"filter":    map[string]string{"property": "object", "value": "database"},
		"page_size": 50,
	}, token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var result struct {
		Results []map[string]interface{} `json:"results"`
	}
	json.Unmarshal(respBody, &result)
	if len(result.Results) == 0 {
		return "No databases found.", nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d database(s):\n", len(result.Results)))
	for i, item := range result.Results {
		id, _ := item["id"].(string)
		title := extractNotionTitle(item)
		sb.WriteString(fmt.Sprintf("%d. %s — %s\n", i+1, title, id))
	}
	return sb.String(), nil
}

type NotionQueryDatabase struct{ DataDir string }

func (t *NotionQueryDatabase) Name() string        { return "notion_query_database" }
func (t *NotionQueryDatabase) Description() string { return "Query a Notion database with optional filter and sort. Returns matching pages." }
func (t *NotionQueryDatabase) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"database_id": strParam("Database ID to query."),
		"filter_text": strParam("Optional text to filter pages by title."),
		"limit":       intParam("Max results (default 20)."),
	}, []string{"database_id"})
}
func (t *NotionQueryDatabase) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := notionBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	dbID := getStringArg(args, "database_id")
	limit := getIntArg(args, "limit", 20)

	body := map[string]interface{}{"page_size": limit}
	if ft := getStringArg(args, "filter_text"); ft != "" {
		body["filter"] = map[string]interface{}{
			"property": "title",
			"rich_text": map[string]interface{}{
				"contains": ft,
			},
		}
	}
	respBody, err := b.apiCall(ctx, "POST", "https://api.notion.com/v1/databases/"+dbID+"/query", body, token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var result struct {
		Results []map[string]interface{} `json:"results"`
	}
	json.Unmarshal(respBody, &result)
	if len(result.Results) == 0 {
		return "No matching pages.", nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d page(s):\n", len(result.Results)))
	for i, item := range result.Results {
		id, _ := item["id"].(string)
		title := extractNotionTitle(item)
		sb.WriteString(fmt.Sprintf("%d. %s — %s\n", i+1, title, id))
	}
	return sb.String(), nil
}

type NotionGetBlockChildren struct{ DataDir string }

func (t *NotionGetBlockChildren) Name() string        { return "notion_get_block_children" }
func (t *NotionGetBlockChildren) Description() string { return "Get child blocks of a Notion block (page, toggle, etc)." }
func (t *NotionGetBlockChildren) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"block_id": strParam("Block ID to get children of."),
	}, []string{"block_id"})
}
func (t *NotionGetBlockChildren) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := notionBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	blockID := getStringArg(args, "block_id")
	respBody, err := b.apiCall(ctx, "GET", "https://api.notion.com/v1/blocks/"+blockID+"/children?page_size=50", nil, token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var result struct {
		Results []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"results"`
	}
	json.Unmarshal(respBody, &result)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d child block(s):\n", len(result.Results)))
	for i, r := range result.Results {
		sb.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, r.Type, r.ID))
	}
	return sb.String(), nil
}

type NotionAppendBlock struct{ DataDir string }

func (t *NotionAppendBlock) Name() string        { return "notion_append_block" }
func (t *NotionAppendBlock) Description() string { return "Append content blocks to a Notion page or block." }
func (t *NotionAppendBlock) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"block_id": strParam("Parent block or page ID."),
		"content":  strParam("Text content to append."),
	}, []string{"block_id", "content"})
}
func (t *NotionAppendBlock) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := notionBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	blockID := getStringArg(args, "block_id")
	content := getStringArg(args, "content")

	body := map[string]interface{}{
		"children": []map[string]interface{}{
			{
				"object": "block", "type": "paragraph",
				"paragraph": map[string]interface{}{
					"rich_text": []map[string]interface{}{
						{"type": "text", "text": map[string]interface{}{"content": content}},
					},
				},
			},
		},
	}
	respBody, err := b.apiCall(ctx, "PATCH", "https://api.notion.com/v1/blocks/"+blockID+"/children", body, token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	if strings.Contains(string(respBody), `"id"`) {
		return fmt.Sprintf("Appended block to %s.", blockID[:8]+"..."), nil
	}
	return fmt.Sprintf("Response: %s", previewBody(respBody, 200)), nil
}
