package store

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Integration struct {
	Provider     string    `json:"provider"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	Connected    bool      `json:"connected"`
	ConnectedAt  time.Time `json:"connected_at"`
}

func integrationsDir(dataDir, userID string) string {
	return filepath.Join(UserDir(dataDir, userID), "integrations")
}

func LoadIntegration(dataDir, userID, provider string) (*Integration, error) {
	path := filepath.Join(integrationsDir(dataDir, userID), provider+".json")
	var i Integration
	if err := ReadJSON(path, &i); err != nil {
		if os.IsNotExist(err) {
			return &Integration{Provider: provider, Connected: false}, nil
		}
		return nil, err
	}
	return &i, nil
}

func SaveIntegration(dataDir, userID string, i *Integration) error {
	dir := integrationsDir(dataDir, userID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, i.Provider+".json")
	return WriteJSON(path, i)
}

func DeleteIntegration(dataDir, userID, provider string) error {
	path := filepath.Join(integrationsDir(dataDir, userID), provider+".json")
	return os.Remove(path)
}

func IsConnected(dataDir, userID, provider string) bool {
	i, err := LoadIntegration(dataDir, userID, provider)
	if err != nil {
		return false
	}
	if !i.Connected {
		return false
	}
	if !i.ExpiresAt.IsZero() && time.Now().After(i.ExpiresAt) && i.RefreshToken == "" {
		return false
	}
	return true
}

func ConnectedProviders(dataDir, userID string) map[string]bool {
	result := make(map[string]bool)
	for _, p := range []string{"notion", "github", "dropbox"} {
		result[p] = IsConnected(dataDir, userID, p)
	}
	ec, err := LoadEmailConfig(dataDir, userID)
	result["email"] = err == nil && ec.Enabled
	tp := ConnectedServersForType(dataDir, userID, "bm_api")
	result["bm_api"] = len(tp) > 0
	feeds, _ := LoadRSSFeeds(dataDir, userID)
	result["rss"] = len(feeds) > 0
	return result
}

func AllIntegrations(dataDir, userID string) map[string]*Integration {
	result := make(map[string]*Integration)
	for _, provider := range []string{"notion", "github", "dropbox"} {
		i, err := LoadIntegration(dataDir, userID, provider)
		if err != nil {
			i = &Integration{Provider: provider, Connected: false}
		}
		result[provider] = i
	}
	return result
}

func RefreshDropboxToken(dataDir, userID, clientID, clientSecret string) error {
	i, err := LoadIntegration(dataDir, userID, "dropbox")
	if err != nil || !i.Connected || i.RefreshToken == "" {
		return nil
	}
	if !i.ExpiresAt.IsZero() && time.Now().Before(i.ExpiresAt) {
		return nil
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", i.RefreshToken)
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)

	req, err := http.NewRequest("POST", "https://api.dropboxapi.com/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken  string  `json:"access_token"`
		ExpiresIn    float64 `json:"expires_in"`
		TokenType    string  `json:"token_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	if result.AccessToken == "" {
		return fmt.Errorf("refresh returned empty access token")
	}

	i.AccessToken = result.AccessToken
	i.ExpiresAt = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	return SaveIntegration(dataDir, userID, i)
}
