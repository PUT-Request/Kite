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
	"strings"
	"time"

	"kite/sandbox"
	"kite/store"
)

var dropboxOAuthClientID, dropboxOAuthClientSecret string

func SetDropboxOAuth(clientID, clientSecret string) {
	dropboxOAuthClientID = clientID
	dropboxOAuthClientSecret = clientSecret
}

type dropboxBase struct{ dataDir string }

func (b *dropboxBase) token(userID string) (string, error) {
	i, err := store.LoadIntegration(b.dataDir, userID, "dropbox")
	if err != nil || !i.Connected {
		return "", fmt.Errorf("dropbox not connected")
	}
	if i.RefreshToken != "" && (i.ExpiresAt.IsZero() || time.Now().After(i.ExpiresAt)) {
		if dropboxOAuthClientID != "" {
			_ = store.RefreshDropboxToken(b.dataDir, userID, dropboxOAuthClientID, dropboxOAuthClientSecret)
			i, err = store.LoadIntegration(b.dataDir, userID, "dropbox")
			if err == nil && i.Connected && i.AccessToken != "" {
				return i.AccessToken, nil
			}
		}
		return "", fmt.Errorf("dropbox token expired and could not refresh")
	}
	return i.AccessToken, nil
}

func (b *dropboxBase) apiCall(ctx context.Context, endpoint string, body interface{}, token string) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.dropboxapi.com/2/"+endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
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
		return nil, fmt.Errorf("dropbox api %d: %s", resp.StatusCode, previewBody(bodyBytes, 200))
	}
	return bodyBytes, nil
}

type DropboxListFiles struct{ DataDir string }

func (t *DropboxListFiles) Name() string        { return "dropbox_list_files" }
func (t *DropboxListFiles) Description() string { return "List files and folders in a Dropbox directory. Returns names, sizes, and types." }
func (t *DropboxListFiles) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"path":    strParam("Directory path (default: empty for root)."),
		"limit":   intParam("Max entries (default 50)."),
	}, []string{})
}
func (t *DropboxListFiles) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := dropboxBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	path := getStringArg(args, "path")
	if path == "" {
		path = ""
	}
	limit := getIntArg(args, "limit", 50)
	respBody, err := b.apiCall(ctx, "files/list_folder", map[string]interface{}{
		"path":  path,
		"limit": limit,
	}, token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var result struct {
		Entries []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
			Tag  string `json:".tag"`
		} `json:"entries"`
	}
	json.Unmarshal(respBody, &result)
	if len(result.Entries) == 0 {
		return fmt.Sprintf("Empty directory: %s", path), nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s (%d entries):\n", path, len(result.Entries)))
	for _, e := range result.Entries {
		tag := e.Tag
		if tag == "" {
			tag = "file"
		}
		sb.WriteString(fmt.Sprintf("  %s %s (%d bytes)\n", tag, e.Name, e.Size))
	}
	return sb.String(), nil
}

type DropboxUploadFile struct{ DataDir string }

func (t *DropboxUploadFile) Name() string        { return "dropbox_upload_file" }
func (t *DropboxUploadFile) Description() string { return "Upload a file to Dropbox. Content must be provided as text (the tool will upload it)." }
func (t *DropboxUploadFile) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"path":    strParam("Dropbox path where to upload (e.g., '/folder/file.txt')."),
		"content": strParam("File content as text."),
	}, []string{"path", "content"})
}
func (t *DropboxUploadFile) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := dropboxBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	path := getStringArg(args, "path")
	content := getStringArg(args, "content")

	body := map[string]interface{}{
		"path": path,
		"mode": map[string]string{".tag": "overwrite"},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Sprintf("Failed to marshal request: %v", err), nil
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://content.dropboxapi.com/2/files/upload", bytes.NewReader([]byte(content)))
	if err != nil {
		return fmt.Sprintf("Failed to create request: %v", err), nil
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Dropbox-API-Arg", string(data))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	json.Unmarshal(respBody, &result)
	if result.Name != "" {
		return fmt.Sprintf("Uploaded %s (%d bytes)", result.Name, result.Size), nil
	}
	return fmt.Sprintf("Response: %s", previewBody(respBody, 200)), nil
}

type DropboxDownloadFile struct {
	DataDir string
	Manager *sandbox.Manager
}

func (t *DropboxDownloadFile) Name() string        { return "dropbox_download_file" }
func (t *DropboxDownloadFile) Description() string { return "Download a file from Dropbox. Returns the file content as text." }
func (t *DropboxDownloadFile) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"path": strParam("Dropbox file path to download."),
	}, []string{"path"})
}
func (t *DropboxDownloadFile) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := dropboxBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	path := getStringArg(args, "path")
	body := map[string]string{"path": path}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Sprintf("Failed to marshal request: %v", err), nil
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://content.dropboxapi.com/2/files/download", nil)
	if err != nil {
		return fmt.Sprintf("Failed to create request: %v", err), nil
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Dropbox-API-Arg", string(data))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	defer resp.Body.Close()

	if t.Manager == nil {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		return fmt.Sprintf("%s (%d bytes):\n%s", path, len(respBody), string(respBody)), nil
	}

	home := t.Manager.HomeDir(userID)
	if err := t.Manager.InitUser(userID); err != nil {
		return fmt.Sprintf("Sandbox init error: %v", err), nil
	}
	dlDir := filepath.Join(home, "downloads")
	os.MkdirAll(dlDir, 0755)
	filename := filepath.Base(path)
	if filename == "" || filename == "." {
		filename = "dropbox_download"
	}
	localPath := filepath.Join(dlDir, filename)
	f, err := os.Create(localPath)
	if err != nil {
		return fmt.Sprintf("File create error: %v", err), nil
	}
	written, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		return fmt.Sprintf("Write error: %v", err), nil
	}
	sandboxPath := "/home/user/downloads/" + filename
	return fmt.Sprintf("Downloaded %s -> %s (%d bytes)\nUse sandbox_read(\"%s\") to read content.", path, sandboxPath, written, sandboxPath), nil
}

type DropboxDeleteFile struct{ DataDir string }

func (t *DropboxDeleteFile) Name() string        { return "dropbox_delete_file" }
func (t *DropboxDeleteFile) Description() string { return "Delete a file or folder from Dropbox." }
func (t *DropboxDeleteFile) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"path": strParam("Dropbox path to delete."),
	}, []string{"path"})
}
func (t *DropboxDeleteFile) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := dropboxBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	path := getStringArg(args, "path")
	respBody, err := b.apiCall(ctx, "files/delete_v2", map[string]string{"path": path}, token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	if strings.Contains(string(respBody), `"deleted"`) {
		return fmt.Sprintf("Deleted: %s", path), nil
	}
	return fmt.Sprintf("Response: %s", previewBody(respBody, 200)), nil
}

type DropboxSearch struct{ DataDir string }

func (t *DropboxSearch) Name() string        { return "dropbox_search" }
func (t *DropboxSearch) Description() string { return "Search files and folders in Dropbox by name." }
func (t *DropboxSearch) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"query": strParam("Search query."),
		"limit": intParam("Max results (default 20)."),
	}, []string{"query"})
}
func (t *DropboxSearch) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := dropboxBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	query := getStringArg(args, "query")
	limit := getIntArg(args, "limit", 20)
	respBody, err := b.apiCall(ctx, "files/search_v2", map[string]interface{}{
		"query":        query,
		"max_results":  limit,
	}, token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var result struct {
		Matches []struct {
			Metadata struct {
				Name string `json:"name"`
				Size int64  `json:"size"`
				Tag  string `json:".tag"`
			} `json:"metadata"`
		} `json:"matches"`
	}
	json.Unmarshal(respBody, &result)
	if len(result.Matches) == 0 {
		return fmt.Sprintf("No Dropbox results for '%s'.", query), nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Dropbox search for '%s' (%d results):\n", query, len(result.Matches)))
	for i, m := range result.Matches {
		sb.WriteString(fmt.Sprintf("%d. %s %s (%d bytes)\n", i+1, m.Metadata.Tag, m.Metadata.Name, m.Metadata.Size))
	}
	return sb.String(), nil
}

type DropboxGetInfo struct{ DataDir string }

func (t *DropboxGetInfo) Name() string        { return "dropbox_get_info" }
func (t *DropboxGetInfo) Description() string { return "Get metadata for a Dropbox file or folder (size, modified date, type)." }
func (t *DropboxGetInfo) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"path": strParam("Dropbox path."),
	}, []string{"path"})
}
func (t *DropboxGetInfo) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := dropboxBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	path := getStringArg(args, "path")
	respBody, err := b.apiCall(ctx, "files/get_metadata", map[string]string{"path": path}, token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var result struct {
		Name         string `json:"name"`
		Size         int64  `json:"size"`
		Tag          string `json:".tag"`
		ClientModified string `json:"client_modified"`
	}
	json.Unmarshal(respBody, &result)
	return fmt.Sprintf("%s: %s | size: %d bytes | modified: %s", result.Tag, result.Name, result.Size, result.ClientModified), nil
}

type DropboxCreateFolder struct{ DataDir string }

func (t *DropboxCreateFolder) Name() string        { return "dropbox_create_folder" }
func (t *DropboxCreateFolder) Description() string { return "Create a new folder in Dropbox." }
func (t *DropboxCreateFolder) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"path": strParam("Folder path to create."),
	}, []string{"path"})
}
func (t *DropboxCreateFolder) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := dropboxBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	path := getStringArg(args, "path")
	respBody, err := b.apiCall(ctx, "files/create_folder_v2", map[string]string{"path": path}, token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	if strings.Contains(string(respBody), `"folder"`) {
		return fmt.Sprintf("Created folder: %s", path), nil
	}
	return fmt.Sprintf("Response: %s", previewBody(respBody, 200)), nil
}

type DropboxGetLink struct{ DataDir string }

func (t *DropboxGetLink) Name() string        { return "dropbox_get_link" }
func (t *DropboxGetLink) Description() string { return "Create a shared link for a Dropbox file or folder." }
func (t *DropboxGetLink) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"path": strParam("Dropbox path to create link for."),
	}, []string{"path"})
}
func (t *DropboxGetLink) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := dropboxBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	path := getStringArg(args, "path")
	respBody, err := b.apiCall(ctx, "sharing/create_shared_link_with_settings", map[string]interface{}{
		"path": path,
		"settings": map[string]string{
			"requested_visibility": "public",
		},
	}, token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var result struct {
		URL string `json:"url"`
	}
	json.Unmarshal(respBody, &result)
	if result.URL != "" {
		return fmt.Sprintf("Shared link: %s", result.URL), nil
	}
	return fmt.Sprintf("Response: %s", previewBody(respBody, 200)), nil
}
