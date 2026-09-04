package store

import (
	"os"
	"path/filepath"
)

type EmailConfig struct {
	Enabled     bool   `json:"enabled"`
	Username    string `json:"username"`
	Address     string `json:"address"`
	ConfiguredAt string `json:"configured_at,omitempty"`
}

func LoadEmailConfig(dataDir, userID string) (*EmailConfig, error) {
	path := filepath.Join(UserDir(dataDir, userID), "email.json")
	var ec EmailConfig
	if err := ReadJSON(path, &ec); err != nil {
		if os.IsNotExist(err) {
			return &EmailConfig{}, nil
		}
		return nil, err
	}
	return &ec, nil
}

func SaveEmailConfig(dataDir, userID string, ec *EmailConfig) error {
	path := filepath.Join(UserDir(dataDir, userID), "email.json")
	return WriteJSON(path, ec)
}

func UserByEmail(dataDir, domain, address string) (string, *EmailConfig) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return "", nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ec, err := LoadEmailConfig(dataDir, e.Name())
		if err != nil || !ec.Enabled {
			continue
		}
		if ec.Address == address {
			return e.Name(), ec
		}
	}
	return "", nil
}
