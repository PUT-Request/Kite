package store

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type ThirdPartyServer struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	APIKey        string `json:"api_key"`
	Connected     bool   `json:"connected"`
	ConnectedAt   string `json:"connected_at,omitempty"`
	ToolCount     int    `json:"tool_count,omitempty"`
}

type ThirdPartyFile struct {
	Servers []ThirdPartyServer `json:"servers"`
}

func LoadThirdParty(dataDir, userID string) (*ThirdPartyFile, error) {
	path := filepath.Join(UserDir(dataDir, userID), "third_party_servers.json")
	var f ThirdPartyFile
	if err := ReadJSON(path, &f); err != nil {
		if os.IsNotExist(err) {
			return &ThirdPartyFile{}, nil
		}
		return nil, err
	}
	return &f, nil
}

func SaveThirdParty(dataDir, userID string, f *ThirdPartyFile) error {
	path := filepath.Join(UserDir(dataDir, userID), "third_party_servers.json")
	return WriteJSON(path, f)
}

func AddThirdPartyServer(dataDir, userID string, srv ThirdPartyServer) error {
	if len(srv.Name) > 64 || len(srv.URL) > 512 {
		return fmt.Errorf("name or URL too long")
	}
	f, _ := LoadThirdParty(dataDir, userID)
	if len(f.Servers) >= 10 {
		return fmt.Errorf("max 10 servers per user")
	}
	srv.ID = "tps_" + fmt.Sprintf("%d", time.Now().UnixNano())[:8]
	srv.ConnectedAt = time.Now().UTC().Format(time.RFC3339)
	f.Servers = append(f.Servers, srv)
	return SaveThirdParty(dataDir, userID, f)
}

func RemoveThirdPartyServer(dataDir, userID, serverID string) error {
	f, _ := LoadThirdParty(dataDir, userID)
	for i, s := range f.Servers {
		if s.ID == serverID {
			f.Servers = append(f.Servers[:i], f.Servers[i+1:]...)
			return SaveThirdParty(dataDir, userID, f)
		}
	}
	return fmt.Errorf("not found")
}

func ConnectedServersForType(dataDir, userID, serverType string) []ThirdPartyServer {
	f, _ := LoadThirdParty(dataDir, userID)
	if f == nil {
		return nil
	}
	var result []ThirdPartyServer
	for _, s := range f.Servers {
		if s.Type == serverType && s.Connected {
			result = append(result, s)
		}
	}
	return result
}
