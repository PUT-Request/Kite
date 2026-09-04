package tool

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kite/sandbox"
)

type SandboxExec struct {
	Manager *sandbox.Manager
}

func (t *SandboxExec) Name() string        { return "sandbox_exec" }
func (t *SandboxExec) Description() string { return "Execute a shell command inside the Alpine Linux sandbox. Output is captured and returned. Long output is truncated — use sandbox_read to get the rest from /home/user/.last_output. Commands run as 'user' in /home/user." }
func (t *SandboxExec) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"command": strParam("Shell command to execute."),
	}, []string{"command"})
}

func (t *SandboxExec) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	cmd := getStringArg(args, "command")
	if cmd == "" {
		return "Error: command is required", nil
	}
	result, err := t.Manager.Exec(ctx, userID, cmd)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	sb := strings.Builder{}
	if result.Signal != "" {
		sb.WriteString(fmt.Sprintf("signal: %s\n", result.Signal))
	}
	sb.WriteString(fmt.Sprintf("exit_code: %d\n", result.ExitCode))
	if result.Stdout != "" {
		sb.WriteString(fmt.Sprintf("stdout:\n%s\n", result.Stdout))
	}
	if result.Stderr != "" {
		sb.WriteString(fmt.Sprintf("stderr:\n%s\n", result.Stderr))
	}
	if result.Truncated {
		sb.WriteString(fmt.Sprintf("\ntruncated: true (full output at /home/user/.last_output)\n"))
	}
	return sb.String(), nil
}

type SandboxRead struct {
	Manager   *sandbox.Manager
	ChunkSize int
}

func (t *SandboxRead) Name() string        { return "sandbox_read" }
func (t *SandboxRead) Description() string { return "Read a file from the sandbox workspace at /home/user. Supports pagination via offset and limit. Returns has_more=true when more content is available." }
func (t *SandboxRead) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"path":   strParam("File path relative to /home/user (e.g., 'data.txt' or '.last_output')."),
		"offset": intParam("Byte offset to start reading from (default 0)."),
		"limit":  intParam("Maximum bytes to read (default chunk size from config)."),
	}, []string{"path"})
}

func (t *SandboxRead) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	path := getStringArg(args, "path")
	if path == "" {
		return "Error: path is required", nil
	}
	offset := getIntArg(args, "offset", 0)
	limit := getIntArg(args, "limit", t.ChunkSize)
	if limit <= 0 {
		limit = t.ChunkSize
	}

	home := t.Manager.HomeDir(userID)
	fullPath := filepath.Join(home, path)
	cleanHome := filepath.Clean(home) + string(filepath.Separator)
	cleanPath := filepath.Clean(fullPath)
	if cleanPath != filepath.Clean(home) && !strings.HasPrefix(cleanPath, cleanHome) {
		return "Error: path escapes sandbox", nil
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	totalSize := info.Size()

	f, err := os.Open(fullPath)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	defer f.Close()

	if offset > 0 {
		if _, err := f.Seek(int64(offset), 0); err != nil {
			return fmt.Sprintf("Error seeking: %v", err), nil
		}
	}

	buf := make([]byte, limit)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return fmt.Sprintf("Error reading: %v", err), nil
	}

	nextOffset := offset + n
	hasMore := int64(nextOffset) < totalSize
	content := string(buf[:n])

	return fmt.Sprintf(`content: |
  %s
bytes_read: %d
total_size: %d
has_more: %v
next_offset: %d`, indent(content), n, totalSize, hasMore, nextOffset), nil
}

func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}

type SandboxWrite struct {
	Manager   *sandbox.Manager
	ChunkSize int
}

func (t *SandboxWrite) Name() string        { return "sandbox_write" }
func (t *SandboxWrite) Description() string { return "Write content to a file in the sandbox workspace. Use mode='append' for appending (useful for writing large files in chunks). Returns bytes written. Max content per call is limited — for larger files, chain multiple calls with append mode." }
func (t *SandboxWrite) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"path":    strParam("File path relative to /home/user."),
		"content": strParam("Content to write."),
		"mode":    strEnumParam("Write mode: 'overwrite' (default) or 'append'.", []string{"overwrite", "append"}),
	}, []string{"path", "content"})
}

func (t *SandboxWrite) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	path := getStringArg(args, "path")
	content := getStringArg(args, "content")
	mode := getStringArg(args, "mode")
	if path == "" {
		return "Error: path is required", nil
	}
	if mode == "" {
		mode = "overwrite"
	}

	home := t.Manager.HomeDir(userID)
	fullPath := filepath.Join(home, path)
	cleanHome := filepath.Clean(home) + string(filepath.Separator)
	cleanPath := filepath.Clean(fullPath)
	if cleanPath != filepath.Clean(home) && !strings.HasPrefix(cleanPath, cleanHome) {
		return "Error: path escapes sandbox", nil
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}

	var flag int
	if mode == "append" {
		flag = os.O_APPEND | os.O_CREATE | os.O_WRONLY
	} else {
		flag = os.O_TRUNC | os.O_CREATE | os.O_WRONLY
	}

	f, err := os.OpenFile(fullPath, flag, 0644)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	defer f.Close()

	n, err := f.WriteString(content)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}

	return fmt.Sprintf("Wrote %d bytes to %s (mode: %s)", n, path, mode), nil
}

type SandboxList struct {
	Manager *sandbox.Manager
}

func (t *SandboxList) Name() string        { return "sandbox_list" }
func (t *SandboxList) Description() string { return "List files and directories in the sandbox workspace. Supports pagination." }
func (t *SandboxList) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"path":   strParam("Directory path relative to /home/user (default: '.')."),
		"offset": intParam("Entry offset for pagination (default 0)."),
		"limit":  intParam("Max entries to return (default 100)."),
	}, []string{})
}

func (t *SandboxList) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	path := getStringArg(args, "path")
	if path == "" {
		path = "."
	}
	offset := getIntArg(args, "offset", 0)
	limit := getIntArg(args, "limit", 100)

	home := t.Manager.HomeDir(userID)
	fullPath := filepath.Join(home, path)
	cleanHome := filepath.Clean(home) + string(filepath.Separator)
	cleanPath := filepath.Clean(fullPath)
	if cleanPath != filepath.Clean(home) && !strings.HasPrefix(cleanPath, cleanHome) {
		return "Error: path escapes sandbox", nil
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}

	total := len(entries)
	if offset >= total {
		return fmt.Sprintf("entries: []\ntotal: %d\nhas_more: false", total), nil
	}

	end := offset + limit
	if end > total {
		end = total
	}
	hasMore := end < total

	var sb strings.Builder
	sb.WriteString("entries:\n")
	for _, e := range entries[offset:end] {
		info, _ := e.Info()
		size := int64(0)
		modTime := ""
		isDir := "d"
		if info != nil {
			size = info.Size()
			modTime = info.ModTime().Format("2006-01-02 15:04")
		}
		if !e.IsDir() {
			isDir = "-"
		}
		sb.WriteString(fmt.Sprintf("  %s %10d %s %s\n", isDir, size, modTime, e.Name()))
	}
	sb.WriteString(fmt.Sprintf("total: %d\nhas_more: %v\nnext_offset: %d", total, hasMore, end))
	return sb.String(), nil
}

type WebFetch struct{}

func (t *WebFetch) Name() string        { return "web_fetch" }
func (t *WebFetch) Description() string { return "Fetch content from a URL. Returns the response body as text. Use for downloading files, checking APIs, or scraping simple pages." }
func (t *WebFetch) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"url": strParam("The URL to fetch (must start with http:// or https://)."),
	}, []string{"url"})
}

func (t *WebFetch) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	url := getStringArg(args, "url")
	if url == "" {
		return "Error: url is required", nil
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "Error: url must start with http:// or https://", nil
	}

	client := &http.Client{Timeout: 30 * time.Second}
	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Sprintf("Invalid URL: %v", err), nil
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return fmt.Sprintf("Error reading body: %v", err), nil
	}

	return fmt.Sprintf("status: %d\ncontent_type: %s\nbody_length: %d\n\n%s",
		resp.StatusCode, resp.Header.Get("Content-Type"), len(body), string(body)), nil
}

func ExtractArgString(args map[string]interface{}, key string) string {
	return getStringArg(args, key)
}

func ExtractArgInt(args map[string]interface{}, key string, defaultVal int) int {
	return getIntArg(args, key, defaultVal)
}
