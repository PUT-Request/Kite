package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type UserProfile struct {
	UserID      string    `json:"user_id"`
	Name        string    `json:"name"`
	Timezone    string    `json:"timezone"`
	Vibe        string    `json:"vibe"`
	TosAccepted bool      `json:"tos_accepted"`
	Onboarded   bool      `json:"onboarded"`
	CreatedAt   time.Time `json:"created_at"`
}

func UserDir(dataDir, userID string) string {
	return filepath.Join(dataDir, userID)
}

// PublicSitesDir must match config.site_public_path.
func PublicSitesDir(publicPath, userID string) string {
	return filepath.Join(publicPath, userID)
}

func UserExists(dataDir, userID string) bool {
	_, err := os.Stat(UserDir(dataDir, userID))
	return err == nil
}

func InitUser(dataDir, userID, name string) error {
	dir := UserDir(dataDir, userID)
	dirs := []string{
		dir,
		filepath.Join(dir, "sandbox", "home"),
		filepath.Join(dir, "agents"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	profile := &UserProfile{
		UserID:    userID,
		Name:      name,
		CreatedAt: time.Now(),
	}
	return SaveProfile(dataDir, userID, profile)
}

func LoadProfile(dataDir, userID string) (*UserProfile, error) {
	var p UserProfile
	if err := ReadJSON(filepath.Join(UserDir(dataDir, userID), "profile.json"), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func SaveProfile(dataDir, userID string, p *UserProfile) error {
	return WriteJSON(filepath.Join(UserDir(dataDir, userID), "profile.json"), p)
}

func ReadJSON(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return err
		}
		return fmt.Errorf("read failed")
	}
	return json.Unmarshal(data, v)
}

func WriteJSON(path string, v interface{}) error {
	os.MkdirAll(filepath.Dir(path), 0755)
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode failed")
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write failed")
	}
	return nil
}

func AppendJSONL(path string, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, string(data))
	return err
}

func ReadJSONL(path string) ([]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := bytesSplit(data, '\n')
	var msgs []json.RawMessage
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		msgs = append(msgs, json.RawMessage(line))
	}
	return msgs, nil
}

func bytesSplit(data []byte, sep byte) [][]byte {
	var result [][]byte
	start := 0
	for i, b := range data {
		if b == sep {
			result = append(result, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		result = append(result, data[start:])
	}
	return result
}

var userLocks sync.Map

func LockUser(userID string) func() {
	mu, _ := userLocks.LoadOrStore(userID, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	return func() { mu.(*sync.Mutex).Unlock() }
}

func DeleteUser(dataDir, userID, publicPath string) error {
	unlock := LockUser(userID)
	defer unlock()
	if err := os.RemoveAll(UserDir(dataDir, userID)); err != nil {
		return err
	}
	return os.RemoveAll(PublicSitesDir(publicPath, userID))
}

func GuildDir(dataDir, guildID string) string {
	return filepath.Join(dataDir, "guild_"+guildID)
}

func DeleteGuild(dataDir, guildID, publicPath string) error {
	if err := os.RemoveAll(GuildDir(dataDir, guildID)); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(publicPath, "guild_"+guildID))
}
