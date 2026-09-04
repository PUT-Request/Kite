package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DiscordToken             string  `yaml:"discord_token"`
	LLMEndpoint              string  `yaml:"llm_endpoint"`
	LLMApiKey                string  `yaml:"llm_api_key"`
	LLMModel                 string  `yaml:"llm_model"`
	LLMFallback1Endpoint     string  `yaml:"llm_fallback_1_endpoint"`
	LLMFallback1Key          string  `yaml:"llm_fallback_1_api_key"`
	LLMFallback1Model        string  `yaml:"llm_fallback_1_model"`
	LLMFallback2Endpoint     string  `yaml:"llm_fallback_2_endpoint"`
	LLMFallback2Key          string  `yaml:"llm_fallback_2_api_key"`
	LLMFallback2Model        string  `yaml:"llm_fallback_2_model"`
	ContextWindow            int     `yaml:"context_window"`
	MaxOutputTokens          int     `yaml:"max_output_tokens"`
	ReasoningLevel           string  `yaml:"reasoning_level"`
	Temperature              float64 `yaml:"temperature"`
	TriggerCheckIntervalSec  int     `yaml:"trigger_check_interval_sec"`
	MemoryCompressThreshold  int     `yaml:"memory_compress_threshold"`
	SandboxCommandTimeoutSec int     `yaml:"sandbox_command_timeout_sec"`
	SandboxMaxOutputBytes    int     `yaml:"sandbox_max_output_bytes"`
	SandboxReadChunkSize     int     `yaml:"sandbox_read_chunk_size"`
	SandboxWriteChunkSize    int     `yaml:"sandbox_write_chunk_size"`
	AlpineRootfsPath         string  `yaml:"alpine_rootfs_path"`
	SharedRootfsPath         string  `yaml:"shared_rootfs_path"`
	SandboxMaxDiskMB         int     `yaml:"sandbox_max_disk_mb"`
	SandboxPreinstallPkgs    []string `yaml:"sandbox_preinstall_pkgs"`
	SandboxDNS               []string `yaml:"sandbox_dns"`
	DataDir                  string  `yaml:"data_dir"`
	ExecLoopMaxIterations    int     `yaml:"exec_loop_max_iterations"`
	InteractionLoopsMax      int     `yaml:"interaction_loops_max"`
	SystemPromptExtra        string  `yaml:"system_prompt_extra"`
	TypingWPMMin             int     `yaml:"typing_wpm_min"`
	TypingWPMMax             int     `yaml:"typing_wpm_max"`
	GuildID                  string  `yaml:"guild_id"`
	ChannelID                string  `yaml:"channel_id"`
	AdminUserID              string  `yaml:"admin_user_id"`
	DiscordInviteLink        string  `yaml:"discord_invite_link"`
	StripReasoning           bool    `yaml:"strip_reasoning"`
	SearXNGEndpoints         []string `yaml:"searxng_endpoints"`
	SearchTimeoutSec         int     `yaml:"search_timeout_sec"`
	VisionEnabled            bool    `yaml:"vision_enabled"`
	DownloadCleanupHours     int     `yaml:"download_cleanup_hours"`
	ReactionsEnabled         bool    `yaml:"reactions_enabled"`
	DashboardPort            int    `yaml:"dashboard_port"`
	DashboardDomain          string `yaml:"dashboard_domain"`
	SitePublicPath           string `yaml:"site_public_path"`
	SMTPPort                 int    `yaml:"smtp_port"`
	SMTPDomain               string `yaml:"smtp_domain"`
	OAuth                    OAuthConfig `yaml:"oauth"`
}

type OAuthProvider struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
}

type OAuthConfig struct {
	StateSecret  string        `yaml:"state_secret"`
	RedirectBase string        `yaml:"redirect_base"`
	Notion       OAuthProvider `yaml:"notion"`
	GitHub       OAuthProvider `yaml:"github"`
	Dropbox      OAuthProvider `yaml:"dropbox"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	cfg := &Config{
		TriggerCheckIntervalSec:  60,
		MemoryCompressThreshold:  100,
		SandboxCommandTimeoutSec: 60,
		SandboxMaxOutputBytes:    65536,
		SandboxReadChunkSize:     4096,
		SandboxWriteChunkSize:    8192,
		ExecLoopMaxIterations:    30,
		InteractionLoopsMax:      15,
		TypingWPMMin:              40,
		TypingWPMMax:              50,
		StripReasoning:           true,
		SearchTimeoutSec:         5,
		VisionEnabled:            false,
		DownloadCleanupHours:     42,
		ReactionsEnabled:         true,
		DashboardPort:            8080,
		SMTPPort:                 25,
		AlpineRootfsPath:         "alpine-rootfs.tar.gz",
		SharedRootfsPath:         "shared-rootfs",
		SandboxMaxDiskMB:         500,
		SandboxDNS:               []string{"8.8.8.8", "1.1.1.1"},
		SandboxPreinstallPkgs:    []string{"python3", "nodejs", "npm", "git", "curl", "openssh-client"},
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if cfg.DiscordToken == "" {
		return nil, fmt.Errorf("discord_token is required")
	}
	if cfg.LLMEndpoint == "" {
		return nil, fmt.Errorf("llm_endpoint is required")
	}
	if cfg.LLMApiKey == "" {
		return nil, fmt.Errorf("llm_api_key is required")
	}
	if cfg.LLMModel == "" {
		return nil, fmt.Errorf("llm_model is required")
	}
	if cfg.DashboardDomain == "" {
		return nil, fmt.Errorf("dashboard_domain is required")
	}
	if cfg.SMTPDomain == "" {
		return nil, fmt.Errorf("smtp_domain is required")
	}
	if cfg.DataDir == "" {
		return nil, fmt.Errorf("data_dir is required")
	}
	if cfg.GuildID == "" {
		return nil, fmt.Errorf("guild_id is required")
	}
	if cfg.AdminUserID == "" {
		return nil, fmt.Errorf("admin_user_id is required")
	}
	return cfg, nil
}
