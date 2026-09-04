package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type Message struct {
	Role       string      `json:"role"`
	Content    interface{} `json:"content,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
	Name       string      `json:"name,omitempty"`
}

func NewTextMessage(role, content string) Message {
	return Message{Role: role, Content: content}
}

func NewVisionMessage(role, text string, imageBase64s []string) Message {
	parts := []ContentPart{
		{Type: "text", Text: text},
	}
	for _, b64 := range imageBase64s {
		parts = append(parts, ContentPart{
			Type:     "image_url",
			ImageURL: &ImageURL{URL: "data:image/jpeg;base64," + b64, Detail: "low"},
		})
	}
	return Message{Role: role, Content: parts}
}

type ToolCall struct {
	ID       string           `json:"id"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolDef struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []ToolDef `json:"tools,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
}

type ChatResponse struct {
	Model   string `json:"-"` // set by Chat() — which model actually served
	Choices []struct {
		Message struct {
			Role       string     `json:"role"`
			Content    string     `json:"content,omitempty"`
			ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
			ToolCallID string     `json:"tool_call_id,omitempty"`
		} `json:"message"`
	} `json:"choices"`
}

type Client struct {
	endpoint       string
	apiKey         string
	model          string
	maxTokens      int
	temp           float64
	stripReasoning bool
	httpClient     *http.Client
	llmLogger      *log.Logger
	llmLogFile     *os.File
	llmLogPath     string
	fallbacks      []*Client
}

func NewClient(endpoint, apiKey, model string, maxTokens int, temp float64, stripReasoning bool, dataDir ...string) *Client {
	logPath := "llm.log"
	if len(dataDir) > 0 && dataDir[0] != "" {
		logPath = filepath.Join(dataDir[0], "llm.log")
	}
	llmLog, _ := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	return &Client{
		endpoint:  endpoint,
		apiKey:    apiKey,
		model:     model,
		maxTokens: maxTokens,
		temp:      temp,
		httpClient: &http.Client{
			Timeout: 240 * time.Second,
		},
		stripReasoning: stripReasoning,
		llmLogger:      log.New(llmLog, "", log.LstdFlags),
		llmLogFile:     llmLog,
		llmLogPath:     logPath,
	}
}

func (c *Client) AddFallback(fb *Client) {
	c.fallbacks = append(c.fallbacks, fb)
}

func (c *Client) ModelName() string { return c.model }

var reThink = regexp.MustCompile(`<think>[\s\S]*?</think>`)

func (c *Client) logLLM(tag, msg string) {
	if c.llmLogger == nil {
		return
	}
	c.llmLogger.Printf("[%s] %s", tag, msg)
}

func (c *Client) rotateLog() {
	if c.llmLogger == nil || c.llmLogPath == "" {
		return
	}
	info, err := os.Stat(c.llmLogPath)
	if err != nil || info.Size() < 8*1024*1024 {
		return
	}
	os.Rename(c.llmLogPath, c.llmLogPath+".1")
	f, err := os.OpenFile(c.llmLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	if c.llmLogFile != nil {
		c.llmLogFile.Close()
	}
	c.llmLogFile = f
	c.llmLogger.SetOutput(f)
}

func trunc(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func isTransient(err error) bool {
	msg := err.Error()
	if strings.Contains(msg, "http request") ||
		strings.Contains(msg, "read response") ||
		strings.Contains(msg, "no choices") ||
		strings.Contains(msg, "llm error 5") ||
		strings.Contains(msg, "tls") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "connection") {
		return true
	}
	return false
}

var refusalPatterns = []string{
	"the request was rejected because it was considered high risk",
}

func isRefusal(content string) bool {
	lower := strings.ToLower(content)
	for _, pat := range refusalPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

func (c *Client) Chat(ctx context.Context, msgs []Message, tools []ToolDef) (*ChatResponse, error) {
	allClients := append([]*Client{c}, c.fallbacks...)
	var lastErr error
	for _, cl := range allClients {
		for attempt := 1; attempt <= 3; attempt++ {
			resp, err := cl.chatOnce(ctx, msgs, tools)
			if err == nil {
				if len(resp.Choices) > 0 && isRefusal(resp.Choices[0].Message.Content) {
					c.logLLM("REFUSAL", fmt.Sprintf("model %s refused: %s", cl.model, resp.Choices[0].Message.Content))
					lastErr = errors.New("model refused request")
					break
				}
				resp.Model = cl.model
				return resp, nil
			}
			lastErr = err
			if isTransient(err) && attempt < 3 {
				c.logLLM("RETRY", fmt.Sprintf("attempt %d/3 for %s: %v", attempt, cl.model, err))
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			c.logLLM("FALLBACK", fmt.Sprintf("attempt %d/3 for %s failed: %v", attempt, cl.model, err))
			break
		}
	}
	return nil, lastErr
}

func (c *Client) chatOnce(ctx context.Context, msgs []Message, tools []ToolDef) (*ChatResponse, error) {
	temp := c.temp
	req := ChatRequest{
		Model:       c.model,
		Messages:    msgs,
		MaxTokens:   c.maxTokens,
		Temperature: temp,
	}
	if len(tools) > 0 {
		req.Tools = tools
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	c.logLLM("REQ", trunc(string(body), 2000))
	c.rotateLog()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("llm error %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if c.stripReasoning {
		for i := range chatResp.Choices {
			chatResp.Choices[i].Message.Content = reThink.ReplaceAllString(chatResp.Choices[i].Message.Content, "")
			chatResp.Choices[i].Message.Content = strings.TrimSpace(chatResp.Choices[i].Message.Content)
		}
	}
	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}
	c.logLLM("RES", trunc(string(respBody), 2000))
	c.rotateLog()
	return &chatResp, nil
}
