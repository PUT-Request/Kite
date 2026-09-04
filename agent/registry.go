package agent

import (
	"crypto/sha512"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"kite/llm"
	"kite/store"
)

type AgentState struct {
	ID         string         `json:"id"`
	Task       string         `json:"task"`
	CreatedAt  time.Time      `json:"created_at"`
	LastActive time.Time      `json:"last_active"`
	Status     string         `json:"status"`
	History    []llm.Message  `json:"history"`
}

type Registry struct {
	dataDir string
	mu      sync.RWMutex
}

func NewRegistry(dataDir string) *Registry {
	return &Registry{dataDir: dataDir}
}

func (r *Registry) List(userID string) ([]AgentState, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agentsDir := filepath.Join(store.UserDir(r.dataDir, userID), "agents")
	entries, err := filepath.Glob(filepath.Join(agentsDir, "*.json"))
	if err != nil {
		return nil, err
	}

	var agents []AgentState
	for _, entry := range entries {
		var a AgentState
		if store.ReadJSON(entry, &a) != nil {
			continue
		}
		agents = append(agents, a)
	}
	return agents, nil
}

func (r *Registry) Load(userID, agentID string) (*AgentState, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	path := filepath.Join(store.UserDir(r.dataDir, userID), "agents", safeFilename(agentID)+".json")
	var a AgentState
	if err := store.ReadJSON(path, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *Registry) Save(userID string, a *AgentState) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	path := filepath.Join(store.UserDir(r.dataDir, userID), "agents", safeFilename(a.ID)+".json")
	a.LastActive = time.Now()
	return store.WriteJSON(path, a)
}

func (r *Registry) Delete(userID, agentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	path := filepath.Join(store.UserDir(r.dataDir, userID), "agents", safeFilename(agentID)+".json")
	return os.Remove(path)
}

func safeFilename(s string) string {
	h := sha512Hash(s)
	cleaned := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(s, "_")
	if len(cleaned) > 16 {
		cleaned = cleaned[:16]
	}
	cleaned = strings.Trim(cleaned, "_")
	if cleaned == "" {
		cleaned = "agent"
	}
	return cleaned + "_" + h[:16]
}

var _ = sha512Hash

func sha512Hash(s string) string {
	h := sha512.Sum512_256([]byte(s))
	return hex.EncodeToString(h[:])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
