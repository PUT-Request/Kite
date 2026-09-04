package store

import (
	"fmt"
	"os"
	"path/filepath"
)

type MCPServerConfig struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	URL          string      `json:"url"`
	AuthType     string      `json:"auth_type"`
	AuthToken    string      `json:"auth_token,omitempty"`
	Enabled      bool        `json:"enabled"`
	ConnectedAt  string      `json:"connected_at,omitempty"`
	DiscoveredTools []MCPToolRef `json:"tools,omitempty"`
}

type MCPToolRef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type MCPServersFile struct {
	Servers []MCPServerConfig `json:"servers"`
}

func LoadMCPServers(dataDir, userID string) (*MCPServersFile, error) {
	path := filepath.Join(UserDir(dataDir, userID), "mcp_servers.json")
	var sf MCPServersFile
	if err := ReadJSON(path, &sf); err != nil {
		if os.IsNotExist(err) {
			return &MCPServersFile{}, nil
		}
		return nil, err
	}
	return &sf, nil
}

func SaveMCPServers(dataDir, userID string, sf *MCPServersFile) error {
	path := filepath.Join(UserDir(dataDir, userID), "mcp_servers.json")
	return WriteJSON(path, sf)
}

func AddMCPServer(dataDir, userID string, srv MCPServerConfig) error {
	if len(srv.Name) > 64 || len(srv.URL) > 512 {
		return fmt.Errorf("name or URL too long")
	}
	sf, _ := LoadMCPServers(dataDir, userID)
	if len(sf.Servers) >= 10 {
		return fmt.Errorf("max 10 MCP servers per user")
	}
	for _, s := range sf.Servers {
		if s.URL == srv.URL {
			return fmt.Errorf("server with this URL already exists")
		}
	}
	sf.Servers = append(sf.Servers, srv)
	return SaveMCPServers(dataDir, userID, sf)
}

func RemoveMCPServer(dataDir, userID, serverID string) error {
	sf, _ := LoadMCPServers(dataDir, userID)
	for i, s := range sf.Servers {
		if s.ID == serverID {
			sf.Servers = append(sf.Servers[:i], sf.Servers[i+1:]...)
			return SaveMCPServers(dataDir, userID, sf)
		}
	}
	return fmt.Errorf("server not found")
}

func UpdateMCPServer(dataDir, userID string, srv MCPServerConfig) error {
	sf, _ := LoadMCPServers(dataDir, userID)
	for i, s := range sf.Servers {
		if s.ID == srv.ID {
			sf.Servers[i] = srv
			return SaveMCPServers(dataDir, userID, sf)
		}
	}
	return fmt.Errorf("server not found")
}

func GetMCPServer(dataDir, userID, serverID string) (*MCPServerConfig, error) {
	sf, _ := LoadMCPServers(dataDir, userID)
	for _, s := range sf.Servers {
		if s.ID == serverID {
			return &s, nil
		}
	}
	return nil, fmt.Errorf("server not found")
}

func AllMCPServersForUser(dataDir, userID string) []MCPServerConfig {
	sf, _ := LoadMCPServers(dataDir, userID)
	if sf == nil {
		return nil
	}
	return sf.Servers
}
