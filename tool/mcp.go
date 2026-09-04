package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"kite/store"
)

type MCPRegistry struct {
	dataDir string
	mu      sync.RWMutex
}

func NewMCPRegistry(dataDir string) *MCPRegistry {
	return &MCPRegistry{dataDir: dataDir}
}

func (r *MCPRegistry) ToolsForUser(userID string) []Tool {
	servers := store.AllMCPServersForUser(r.dataDir, userID)
	var tools []Tool
	for _, srv := range servers {
		if !srv.Enabled {
			continue
		}
		for _, tr := range srv.DiscoveredTools {
			tools = append(tools, &mcpToolWrapper{
				registry:  r,
				userID:    userID,
				serverID:  srv.ID,
				serverURL: srv.URL,
				authToken: srv.AuthToken,
				toolRef:   tr,
			})
		}
	}
	return tools
}

func (r *MCPRegistry) AllUserTools() map[string][]Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(map[string][]Tool)
	entries, _ := os.ReadDir(r.dataDir)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		userID := entry.Name()
		if _, err := os.Stat(filepath.Join(r.dataDir, userID, "mcp_servers.json")); err != nil {
			continue
		}
		result[userID] = r.ToolsForUser(userID)
	}
	return result
}

func (r *MCPRegistry) ConnectAndDiscover(userID string, srv store.MCPServerConfig) ([]store.MCPToolRef, error) {
	payload := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]string{
				"name":    "kite",
				"version": "1.0",
			},
		},
	}
	_, err := r.call(srv.URL, srv.AuthToken, payload)
	if err != nil && !strings.Contains(err.Error(), "not allowed") && !strings.Contains(err.Error(), "HTTP 4") {
		return nil, fmt.Errorf("initialize failed: %w", err)
	}
	// Some Streamable HTTP servers don't require initialize — continue anyway

	payload = map[string]interface{}{
		"jsonrpc": "2.0",
		"id":     2,
		"method": "tools/list",
		"params": map[string]interface{}{},
	}
	respBody, err := r.call(srv.URL, srv.AuthToken, payload)
	if err != nil {
		return nil, fmt.Errorf("tools/list failed: %w", err)
	}

	var resp struct {
		Result struct {
			Tools []store.MCPToolRef `json:"tools"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		snippet := string(respBody)
		if len(snippet) > 100 {
			snippet = snippet[:100]
		}
		return nil, fmt.Errorf("parse tools/list: %w (first 100 bytes: %s)", err, strings.TrimSpace(snippet))
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result.Tools, nil
}

func (r *MCPRegistry) CallTool(serverURL, authToken, toolName string, args map[string]interface{}) (string, error) {
	payload := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":     99,
		"method": "tools/call",
		"params": map[string]interface{}{
			"name":      toolName,
			"arguments": args,
		},
	}
	respBody, err := r.call(serverURL, authToken, payload)
	if err != nil {
		return "", err
	}
	var resp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return fmt.Sprintf("raw response: %s", string(respBody)), nil
	}
	if resp.Error != nil {
		return "", fmt.Errorf("MCP error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	if resp.Result.IsError {
		if len(resp.Result.Content) > 0 {
			return resp.Result.Content[0].Text, nil
		}
		return "tool returned error", nil
	}
	var sb strings.Builder
	for _, c := range resp.Result.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String(), nil
}

func (r *MCPRegistry) call(url, authToken string, payload interface{}) ([]byte, error) {
	data, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	mcpPath := strings.TrimRight(url, "/")
	if idx := strings.Index(mcpPath, "://"); idx >= 0 {
		rest := mcpPath[idx+3:]
		if !strings.Contains(rest, "/") {
			mcpPath += "/mcp"
		}
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", mcpPath, bytes.NewReader(data))
	if req == nil {
		return nil, fmt.Errorf("invalid request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := string(body)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, fmt.Errorf("MCP server returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(snippet))
	}

	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		parsed, perr := parseSSE(body)
		if perr != nil {
			return nil, fmt.Errorf("parse SSE: %w", perr)
		}
		body = parsed
	}
	return body, nil
}

func parseSSE(body []byte) ([]byte, error) {
	var parts [][]byte
	inEvent := false
	for _, raw := range bytes.Split(body, []byte("\n")) {
		line := strings.TrimRight(string(raw), "\r")
		if line == "" {
			if inEvent && len(parts) > 0 {
				break
			}
			inEvent = false
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			inEvent = true
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		inEvent = true
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		parts = append(parts, []byte(payload))
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("no data lines in SSE response")
	}
	return bytes.Join(parts, []byte("\n")), nil
}

type mcpToolWrapper struct {
	registry  *MCPRegistry
	userID    string
	serverID  string
	serverURL string
	authToken string
	toolRef   store.MCPToolRef
}

func (t *mcpToolWrapper) Name() string {
	safe := safeMCPName(t.serverID + "_" + t.toolRef.Name)
	return "mcp_" + safe[:min(50, len(safe))]
}

func (t *mcpToolWrapper) Description() string {
	return t.toolRef.Description
}

func (t *mcpToolWrapper) Parameters() map[string]interface{} {
	if t.toolRef.Parameters == nil {
		return params(map[string]interface{}{}, nil)
	}
	return t.toolRef.Parameters
}

func (t *mcpToolWrapper) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	result, err := t.registry.CallTool(t.serverURL, t.authToken, t.toolRef.Name, args)
	if err != nil {
		return "", fmt.Errorf("MCP tool '%s' failed: %w", t.toolRef.Name, err)
	}
	return result, nil
}

var safeMCPRe = regexp.MustCompile(`[^a-z0-9_]+`)

func safeMCPName(s string) string {
	return strings.Trim(safeMCPRe.ReplaceAllString(strings.ToLower(s), "_"), "_")
}

func (r *MCPRegistry) RefreshServer(userID, serverID string) error {
	config, err := store.GetMCPServer(r.dataDir, userID, serverID)
	if err != nil {
		return err
	}
	tools, err := r.ConnectAndDiscover(userID, *config)
	if err != nil {
		config.Enabled = false
		_ = store.UpdateMCPServer(r.dataDir, userID, *config)
		return err
	}
	config.DiscoveredTools = tools
	config.Enabled = true
	return store.UpdateMCPServer(r.dataDir, userID, *config)
}
