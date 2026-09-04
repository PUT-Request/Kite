package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type DeployedSite struct {
	SiteName      string    `json:"site_name"`
	UserID        string    `json:"user_id"`
	WebhookSecret string    `json:"webhook_secret,omitempty"`
	DeployedAt    time.Time `json:"deployed_at"`
}

func sitesDir(dataDir, userID string) string {
	return filepath.Join(UserDir(dataDir, userID), "sites")
}

func SaveSite(dataDir, userID string, site *DeployedSite) error {
	dir := sitesDir(dataDir, userID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return WriteJSON(filepath.Join(dir, site.SiteName+".json"), site)
}

func LoadSite(dataDir, userID, siteName string) (*DeployedSite, error) {
	path := filepath.Join(sitesDir(dataDir, userID), siteName+".json")
	var site DeployedSite
	if err := ReadJSON(path, &site); err != nil {
		return nil, err
	}
	return &site, nil
}

func GenerateWebhookSecret() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func LookupSiteBySecret(dataDir, secret string) (*DeployedSite, error) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		userID := entry.Name()
		sd := sitesDir(dataDir, userID)
		siteEntries, err := os.ReadDir(sd)
		if err != nil {
			continue
		}
		for _, se := range siteEntries {
			if se.IsDir() || !strings.HasSuffix(se.Name(), ".json") {
				continue
			}
			site, err := LoadSite(dataDir, userID, strings.TrimSuffix(se.Name(), ".json"))
			if err != nil {
				continue
			}
			if site.WebhookSecret == secret {
				return site, nil
			}
		}
	}
	return nil, fmt.Errorf("no site found with that secret")
}
