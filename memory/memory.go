package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"kite/llm"
	"kite/store"
)

type MemoryEntry struct {
	Date    string `json:"date"`
	Summary string `json:"summary"`
}

type Memory struct {
	Summaries    []MemoryEntry      `json:"summaries"`
	ActiveAgents []string           `json:"active_agents"`
	Preferences  map[string]string  `json:"preferences"`
}

func Load(dataDir, userID string) (*Memory, error) {
	path := filepath.Join(store.UserDir(dataDir, userID), "memory.json")
	var m Memory
	if err := store.ReadJSON(path, &m); err != nil {
		if os.IsNotExist(err) {
			return &Memory{
				Preferences: make(map[string]string),
			}, nil
		}
		return nil, err
	}
	if m.Preferences == nil {
		m.Preferences = make(map[string]string)
	}
	return &m, nil
}

func Save(dataDir, userID string, m *Memory) error {
	path := filepath.Join(store.UserDir(dataDir, userID), "memory.json")
	return store.WriteJSON(path, m)
}

func Compress(ctx context.Context, dataDir, userID string, threshold int, llmClient *llm.Client) error {
	brainPath := filepath.Join(store.UserDir(dataDir, userID), "brain.jsonl")
	rawMessages, err := store.ReadJSONL(brainPath)
	if err != nil {
		return fmt.Errorf("read brain: %w", err)
	}
	if len(rawMessages) < threshold {
		return nil
	}

	var msgs []llm.Message
	for _, raw := range rawMessages {
		var m llm.Message
		if json.Unmarshal(raw, &m) == nil {
			msgs = append(msgs, m)
		}
	}
	if len(msgs) < threshold {
		return nil
	}

	splitAt := len(msgs) / 2
	toCompress := msgs[:splitAt]
	keep := msgs[splitAt:]

	var compact []llm.Message
	compact = append(compact, llm.Message{
		Role:    "system",
		Content: "You are a memory compressor. Summarize these messages preserving ALL key facts: names, tasks, decisions, preferences, dates, and relationships. Return ONLY a JSON array of {date, summary} objects. Be information-dense.",
	})

	var toCompressText string
	for _, m := range toCompress {
		toCompressText += fmt.Sprintf("[%s] %s\n", m.Role, m.Content)
	}
	compact = append(compact, llm.Message{
		Role:    "user",
		Content: toCompressText,
	})

	resp, err := llmClient.Chat(ctx, compact, nil)
	if err != nil {
		return fmt.Errorf("compress llm: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil
	}

	content := resp.Choices[0].Message.Content
	var entries []MemoryEntry
	if err := json.Unmarshal([]byte(content), &entries); err != nil {
		entries = []MemoryEntry{{Date: "various", Summary: content}}
	}

	mem, err := Load(dataDir, userID)
	if err != nil {
		return fmt.Errorf("load memory: %w", err)
	}
	mem.Summaries = append(mem.Summaries, entries...)
	if len(mem.Summaries) > 50 {
		mem.Summaries = mem.Summaries[len(mem.Summaries)-50:]
	}
	if err := Save(dataDir, userID, mem); err != nil {
		return fmt.Errorf("save memory: %w", err)
	}

	f, err := os.Create(brainPath)
	if err != nil {
		return fmt.Errorf("truncate brain: %w", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, m := range keep {
		if err := enc.Encode(m); err != nil {
			return fmt.Errorf("encode message: %w", err)
		}
	}
	return nil
}
