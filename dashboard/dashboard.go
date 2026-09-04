package dashboard

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"kite/config"
	"kite/store"
)

//go:embed templates/*
var tmplFS embed.FS

var tmpl *template.Template

func init() {
	tmpl = template.Must(template.ParseFS(tmplFS, "templates/*.html"))
}

type pageData struct {
	Page            string
	Token           string
	UserName        string
	Domain          string
	Expired         bool
	Connected       string
	NotionStatus    string
	NotionClass     string
	NotionConnected bool
	GitHubStatus    string
	GitHubClass     string
	GitHubConnected bool
	DropboxStatus   string
	DropboxClass    string
	DropboxConnected bool
	Servers         []serverCard
	RSSFeeds        []store.RSSFeed
	EmailConfigured bool
	EmailAddress    string
	EmailUnread     int
	IntegrationCount int
	MCPCount         int
	ThirdPartyCount  int
	RSSCount         int
	EmailConnected   int
}

type serverCard struct {
	ID              string
	Name            string
	URL             string
	DiscoveredTools []store.MCPToolRef
}

type tokenEntry struct {
	UserID    string
	ChannelID string
	ExpiresAt time.Time
}

type oauthState struct {
	UserID  string
	Expires time.Time
}

type Server struct {
	port      string
	domain    string
	dataDir   string
	cfg       *config.Config
	tokens    map[string]*tokenEntry
	states    map[string]*oauthState
	mu        sync.RWMutex
	notify    func(userID, message string)
	mcpReg    MCPRefresher
	maildir   EMAilDir
	sitePath  string
	siteURL   string
}

type EMAilDir interface {
	CountUnread(userID string) int
}

type MCPRefresher interface {
	RefreshServer(userID, serverID string) error
	ConnectAndDiscover(userID string, srv store.MCPServerConfig) ([]store.MCPToolRef, error)
}

func NewServer(port int, domain, dataDir string, cfg *config.Config) *Server {
	return &Server{
		port:     fmt.Sprintf(":%d", port),
		domain:   domain,
		dataDir:  dataDir,
		cfg:      cfg,
		tokens:   make(map[string]*tokenEntry),
		states:   make(map[string]*oauthState),
		sitePath: cfg.SitePublicPath,
		siteURL:  "https://" + domain + "/site",
	}
}

func (s *Server) SetMaildir(md EMAilDir) {
	s.maildir = md
}

func (s *Server) SetNotifier(fn func(userID, message string)) {
	s.notify = fn
}

func (s *Server) SetMCPRefresher(r MCPRefresher) {
	s.mcpReg = r
}

func tokenPreview(token string) string {
	if len(token) >= 8 {
		return token[:8] + "..."
	}
	return token + "..."
}

func (s *Server) GenerateToken(userID, channelID string) string {
	b := make([]byte, 16)
	rand.Read(b)
	token := hex.EncodeToString(b)

	s.mu.Lock()
	s.tokens[token] = &tokenEntry{
		UserID:    userID,
		ChannelID: channelID,
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	s.mu.Unlock()

	log.Printf("[token] generated %s for %s (expires %s)", tokenPreview(token), userID, time.Now().Add(5*time.Minute).Format(time.RFC3339))
	return fmt.Sprintf("https://%s/dashboard?token=%s", s.domain, token)
}
func (s *Server) genToken(userID string) string {
	b := make([]byte, 16)
	rand.Read(b)
	token := hex.EncodeToString(b)
	s.mu.Lock()
	s.tokens[token] = &tokenEntry{UserID: userID, ExpiresAt: time.Now().Add(5 * time.Minute)}
	s.mu.Unlock()
	return token
}

func (s *Server) validateToken(token string) *tokenEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.tokens[token]
	if !ok || time.Now().After(entry.ExpiresAt) {
		log.Printf("[token] validate %s... → invalid", tokenPreview(token))
		return nil
	}
	log.Printf("[token] validate %s... → valid for %s", tokenPreview(token), entry.UserID)
	return entry
}

func (s *Server) consumeToken(token string) *tokenEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.tokens[token]
	if !ok {
		log.Printf("[token] consume %s... → NOT FOUND in map (len=%d)", tokenPreview(token), len(s.tokens))
		return nil
	}
	if time.Now().After(entry.ExpiresAt) {
		log.Printf("[token] consume %s... → EXPIRED (expired at %s)", tokenPreview(token), entry.ExpiresAt.Format(time.RFC3339))
		delete(s.tokens, token)
		return nil
	}
	entry.ExpiresAt = time.Now().Add(30 * time.Minute)
	log.Printf("[token] consume %s... → valid for %s, extended to 30min", tokenPreview(token), entry.UserID)
	return entry
}

func (s *Server) generateOAuthState(userID string) string {
	b := make([]byte, 16)
	rand.Read(b)
	state := hex.EncodeToString(b)

	h := hmac.New(sha256.New, []byte(s.cfg.OAuth.StateSecret))
	h.Write([]byte(state + ":" + userID))
	sig := hex.EncodeToString(h.Sum(nil))

	fullState := state + "." + sig

	s.mu.Lock()
	s.states[fullState] = &oauthState{UserID: userID, Expires: time.Now().Add(10 * time.Minute)}
	s.mu.Unlock()

	return fullState
}

func (s *Server) verifyOAuthState(fullState string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored, ok := s.states[fullState]
	if !ok || time.Now().After(stored.Expires) {
		delete(s.states, fullState)
		return "", false
	}
	delete(s.states, fullState)

	parts := strings.SplitN(fullState, ".", 2)
	if len(parts) != 2 {
		return "", false
	}
	state, sig := parts[0], parts[1]

	h := hmac.New(sha256.New, []byte(s.cfg.OAuth.StateSecret))
	h.Write([]byte(state + ":" + stored.UserID))
	expected := hex.EncodeToString(h.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return "", false
	}
	return stored.UserID, true
}

func (s *Server) Start() {
	mux := http.NewServeMux()
	mux.HandleFunc("/dashboard", s.handleDashboard)
	mux.HandleFunc("/dashboard/integrations", s.handleIntegrationsPage)
	mux.HandleFunc("/dashboard/mcp", s.handleMCPPage)
	mux.HandleFunc("/dashboard/third-party", s.handleThirdPartyPage)
	mux.HandleFunc("/dashboard/email", s.handleEmailPage)
	mux.HandleFunc("/dashboard/rss", s.handleRSSPage)
	mux.HandleFunc("/api/rss/add", s.handleRSSAdd)
	mux.HandleFunc("/api/rss/", s.handleRSSAction)
	mux.HandleFunc("/api/auth", s.handleAuth)
	mux.HandleFunc("/api/profile", s.handleProfile)
	mux.HandleFunc("/api/integrations/status", s.handleIntegrationStatus)
	mux.HandleFunc("/api/integrations/", s.handleIntegrationAction)
	mux.HandleFunc("/api/mcp/add", s.handleMCPAdd)
	mux.HandleFunc("/api/mcp/", s.handleMCPAction)
	mux.HandleFunc("/api/third-party/add", s.handleThirdPartyAdd)
	mux.HandleFunc("/api/third-party/", s.handleThirdPartyAction)
	mux.HandleFunc("/api/email/configure", s.handleEmailConfigure)
	mux.HandleFunc("/api/email/disable", s.handleEmailDisable)
	mux.HandleFunc("/api/webhook/site", s.handleSiteWebhook)
	mux.HandleFunc("/oauth/notion/login", s.handleOAuthLogin("notion"))
	mux.HandleFunc("/oauth/github/login", s.handleOAuthLogin("github"))
	mux.HandleFunc("/oauth/dropbox/login", s.handleOAuthLogin("dropbox"))
	mux.HandleFunc("/oauth/notion/callback", s.handleOAuthCallback("notion"))
	mux.HandleFunc("/oauth/github/callback", s.handleOAuthCallback("github"))
	mux.HandleFunc("/oauth/dropbox/callback", s.handleOAuthCallback("dropbox"))

	go s.cleanExpiredLoop()

	if s.sitePath != "" {
		siteFS := http.FileServer(http.Dir(s.sitePath))
		mux.Handle("/site/", http.StripPrefix("/site/", siteFS))
	}

	log.Printf("Dashboard listening on %s", s.port)
	if err := http.ListenAndServe(s.port, mux); err != nil {
		log.Printf("Dashboard server error: %v", err)
	}
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Kite Dashboard</title><link href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.5.1/css/all.min.css" rel="stylesheet"><style>body{background:#000;color:#f5f5f5;font-family:'Inter',-apple-system,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;-webkit-font-smoothing:antialiased}div{text-align:center}i{font-size:2.5rem;color:#555;margin-bottom:16px;display:block}h1{font-size:1.25rem;color:#f5f5f5;font-weight:500;margin-bottom:8px}p{color:#888;font-size:.875rem;margin-bottom:24px}code{background:#141414;border:1px solid #2a2a2a;padding:4px 10px;font-size:.875rem;color:#f5f5f5;font-family:'SF Mono','IBM Plex Mono',monospace;border-radius:6px}</style></head><body><div><i class="fas fa-lock"></i><h1>Not authenticated</h1><p>Type <code>/dashboard</code> in your Discord DMs with Kite to get a login link.</p></div></body></html>`)
		return
	}
	entry := s.consumeToken(token)
	if entry == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Kite Dashboard</title><link href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.5.1/css/all.min.css" rel="stylesheet"><style>body{background:#000;color:#f5f5f5;font-family:'Inter',-apple-system,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;-webkit-font-smoothing:antialiased}div{text-align:center}i{font-size:2.5rem;color:#888;margin-bottom:16px;display:block}h1{font-size:1.25rem;color:#f5f5f5;font-weight:500;margin-bottom:8px}p{color:#888;font-size:.875rem;margin-bottom:24px}code{background:#141414;border:1px solid #2a2a2a;padding:4px 10px;font-size:.875rem;color:#f5f5f5;font-family:'SF Mono','IBM Plex Mono',monospace;border-radius:6px}</style></head><body><div><i class="fas fa-exclamation-triangle"></i><h1>Link expired or invalid</h1><p>Type <code>/dashboard</code> in your Discord DMs with Kite to get a new link.</p></div></body></html>`)
		return
	}

	pd := pageData{Page: "home", Token: token, UserName: truncateID(entry.UserID)}

	integrations := store.AllIntegrations(s.dataDir, entry.UserID)
	connCount := 0
	for _, v := range integrations {
		if v.Connected {
			connCount++
		}
	}
	pd.IntegrationCount = connCount

	feeds, _ := store.LoadRSSFeeds(s.dataDir, entry.UserID)
	pd.RSSCount = len(feeds)

	mcpServers := store.AllMCPServersForUser(s.dataDir, entry.UserID)
	pd.MCPCount = len(mcpServers)

	thirdParty := store.ConnectedServersForType(s.dataDir, entry.UserID, "bm_api")
	pd.ThirdPartyCount = len(thirdParty)

	ecfg, _ := store.LoadEmailConfig(s.dataDir, entry.UserID)
	if ecfg != nil && ecfg.Enabled {
		pd.EmailConnected = 1
	}

	s.render(w, "home", pd)
}

func (s *Server) handleIntegrationsPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	connected := r.URL.Query().Get("connected")

	if token == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Kite Dashboard</title><link href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.5.1/css/all.min.css" rel="stylesheet"><style>body{background:#000;color:#f5f5f5;font-family:'Inter',-apple-system,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;-webkit-font-smoothing:antialiased}div{text-align:center}i{font-size:2.5rem;color:#555;margin-bottom:16px;display:block}h1{font-size:1.25rem;color:#f5f5f5;font-weight:500;margin-bottom:8px}p{color:#888;font-size:.875rem;margin-bottom:24px}code{background:#141414;border:1px solid #2a2a2a;padding:4px 10px;font-size:.875rem;color:#f5f5f5;font-family:'SF Mono','IBM Plex Mono',monospace;border-radius:6px}</style></head><body><div><i class="fas fa-lock"></i><h1>Not authenticated</h1><p>Type <code>/dashboard</code> in your Discord DMs with Kite to get a login link.</p></div></body></html>`)
		return
	}
	entry := s.consumeToken(token)
	if entry == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Kite Dashboard</title><link href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.5.1/css/all.min.css" rel="stylesheet"><style>body{background:#000;color:#f5f5f5;font-family:'Inter',-apple-system,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;-webkit-font-smoothing:antialiased}div{text-align:center}i{font-size:2.5rem;color:#888;margin-bottom:16px;display:block}h1{font-size:1.25rem;color:#f5f5f5;font-weight:500;margin-bottom:8px}p{color:#888;font-size:.875rem;margin-bottom:24px}code{background:#141414;border:1px solid #2a2a2a;padding:4px 10px;font-size:.875rem;color:#f5f5f5;font-family:'SF Mono','IBM Plex Mono',monospace;border-radius:6px}</style></head><body><div><i class="fas fa-exclamation-triangle"></i><h1>Link expired or invalid</h1><p>Type <code>/dashboard</code> in your Discord DMs with Kite to get a new link.</p></div></body></html>`)
		return
	}

	pd := pageData{Page: "integrations", Token: token, UserName: truncateID(entry.UserID), Connected: connected}

	integrations := store.AllIntegrations(s.dataDir, entry.UserID)
	if i := integrations["notion"]; i != nil && i.Connected {
		pd.NotionStatus = "Connected"
		pd.NotionClass = "status-on"
		pd.NotionConnected = true
	} else {
		pd.NotionStatus = "Not connected"
		pd.NotionClass = "status-off"
	}
	if i := integrations["github"]; i != nil && i.Connected {
		pd.GitHubStatus = "Connected"
		pd.GitHubClass = "status-on"
		pd.GitHubConnected = true
	} else {
		pd.GitHubStatus = "Not connected"
		pd.GitHubClass = "status-off"
	}
	if i := integrations["dropbox"]; i != nil && i.Connected {
		pd.DropboxStatus = "Connected"
		pd.DropboxClass = "status-on"
		pd.DropboxConnected = true
	} else {
		pd.DropboxStatus = "Not connected"
		pd.DropboxClass = "status-off"
	}

	s.render(w, "dashboard", pd)
}

func (s *Server) handleMCPPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	pd := pageData{Page: "mcp", UserName: ""}

	if token == "" {
		pd.Expired = true
		s.render(w, "mcp", pd)
		return
	}

	entry := s.consumeToken(token)
	if entry == nil {
		pd.Expired = true
		s.render(w, "mcp", pd)
		return
	}
	pd.Token = token
	pd.UserName = truncateID(entry.UserID)

	servers := store.AllMCPServersForUser(s.dataDir, entry.UserID)
	for _, srv := range servers {
		pd.Servers = append(pd.Servers, serverCard{
			ID:              srv.ID,
			Name:            srv.Name,
			URL:             srv.URL,
			DiscoveredTools: srv.DiscoveredTools,
		})
	}

	s.render(w, "mcp", pd)
}

func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		jsonError(w, "missing token", 400)
		return
	}
	entry := s.consumeToken(token)
	if entry == nil {
		jsonError(w, "invalid token", 401)
		return
	}
	profile, _ := store.LoadProfile(s.dataDir, entry.UserID)
	jsonOK(w, map[string]interface{}{
		"user_id":       entry.UserID,
		"name":          safeStr(profile, "name"),
		"authenticated": true,
	})
}

func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		jsonError(w, "missing token", 400)
		return
	}
	entry := s.consumeToken(token)
	if entry == nil {
		jsonError(w, "invalid token", 401)
		return
	}
	profile, _ := store.LoadProfile(s.dataDir, entry.UserID)
	jsonOK(w, map[string]interface{}{
		"user_id":  entry.UserID,
		"name":     safeStr(profile, "name"),
		"timezone": safeStr(profile, "timezone"),
		"vibe":     safeStr(profile, "vibe"),
	})
}

func (s *Server) handleIntegrationStatus(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		jsonError(w, "missing token", 400)
		return
	}
	entry := s.consumeToken(token)
	if entry == nil {
		jsonError(w, "invalid token", 401)
		return
	}
	integrations := store.AllIntegrations(s.dataDir, entry.UserID)
	result := make(map[string]bool)
	for p, i := range integrations {
		result[p] = i.Connected
	}
	jsonOK(w, result)
}

func (s *Server) handleIntegrationAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/integrations/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[1] != "disconnect" {
		jsonError(w, "unknown action", 404)
		return
	}
	provider := parts[0]

	token := r.URL.Query().Get("token")
	if token == "" || r.Method != "POST" {
		jsonError(w, "invalid request", 400)
		return
	}
	entry := s.consumeToken(token)
	if entry == nil {
		jsonError(w, "invalid token", 401)
		return
	}

	_ = store.DeleteIntegration(s.dataDir, entry.UserID, provider)
	jsonOK(w, map[string]string{"status": "disconnected"})
}

func (s *Server) handleMCPAdd(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" || r.Method != "POST" {
		jsonError(w, "invalid request", 400)
		return
	}
	entry := s.consumeToken(token)
	if entry == nil {
		jsonError(w, "invalid token", 401)
		return
	}

	var req struct {
		Name      string `json:"name"`
		URL       string `json:"url"`
		AuthType  string `json:"auth_type"`
		AuthToken string `json:"auth_token"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	b := make([]byte, 6)
	rand.Read(b)
	serverID := "srv_" + hex.EncodeToString(b)

	srv := store.MCPServerConfig{
		ID:        serverID,
		Name:      req.Name,
		URL:       req.URL,
		AuthType:  req.AuthType,
		AuthToken: req.AuthToken,
		Enabled:   true,
	}

	if s.mcpReg != nil {
		tools, err := s.mcpReg.ConnectAndDiscover(entry.UserID, srv)
		if err != nil {
			jsonError(w, "MCP connect failed: "+err.Error(), 400)
			return
		}
		srv.DiscoveredTools = tools
	}

	if err := store.AddMCPServer(s.dataDir, entry.UserID, srv); err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	jsonOK(w, map[string]interface{}{"status": "added", "id": serverID, "tools": len(srv.DiscoveredTools)})
}

func (s *Server) handleMCPAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/mcp/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		jsonError(w, "unknown action", 404)
		return
	}
	serverID, action := parts[0], parts[1]
	token := r.URL.Query().Get("token")
	if token == "" || r.Method != "POST" {
		jsonError(w, "invalid request", 400)
		return
	}
	entry := s.consumeToken(token)
	if entry == nil {
		jsonError(w, "invalid token", 401)
		return
	}

	switch action {
	case "remove":
		store.RemoveMCPServer(s.dataDir, entry.UserID, serverID)
		jsonOK(w, map[string]string{"status": "removed"})
	case "refresh":
		if s.mcpReg != nil {
			if err := s.mcpReg.RefreshServer(entry.UserID, serverID); err != nil {
				jsonError(w, err.Error(), 400)
				return
			}
		}
		jsonOK(w, map[string]string{"status": "refreshed"})
	default:
		jsonError(w, "unknown action", 404)
	}
}

func (s *Server) handleOAuthLogin(provider string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			jsonError(w, "missing token", 400)
			return
		}
		entry := s.consumeToken(token)
		if entry == nil {
			jsonError(w, "invalid token", 401)
			return
		}

		state := s.generateOAuthState(entry.UserID)
		redirectBase := s.cfg.OAuth.RedirectBase

		var authURL string
		switch provider {
		case "notion":
			authURL = fmt.Sprintf("https://api.notion.com/v1/oauth/authorize?client_id=%s&redirect_uri=%s/oauth/notion/callback&response_type=code&owner=user&state=%s",
				s.cfg.OAuth.Notion.ClientID, redirectBase, state)
		case "github":
			authURL = fmt.Sprintf("https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s/oauth/github/callback&scope=repo,user&state=%s",
				s.cfg.OAuth.GitHub.ClientID, redirectBase, state)
		case "dropbox":
			authURL = fmt.Sprintf("https://www.dropbox.com/oauth2/authorize?client_id=%s&redirect_uri=%s/oauth/dropbox/callback&response_type=code&token_access_type=offline&state=%s",
				s.cfg.OAuth.Dropbox.ClientID, redirectBase, state)
		}
		http.Redirect(w, r, authURL, http.StatusFound)
	}
}

func (s *Server) handleOAuthCallback(provider string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		stateParam := r.URL.Query().Get("state")
		userID, ok := s.verifyOAuthState(stateParam)
		if !ok {
			http.Error(w, "Invalid state", 400)
			return
		}

		redirectBase := s.cfg.OAuth.RedirectBase
		var tokenURL, clientID, clientSecret string
		switch provider {
		case "notion":
			tokenURL = "https://api.notion.com/v1/oauth/token"
			clientID = s.cfg.OAuth.Notion.ClientID
			clientSecret = s.cfg.OAuth.Notion.ClientSecret
		case "github":
			tokenURL = "https://github.com/login/oauth/access_token"
			clientID = s.cfg.OAuth.GitHub.ClientID
			clientSecret = s.cfg.OAuth.GitHub.ClientSecret
		case "dropbox":
			tokenURL = "https://api.dropboxapi.com/oauth2/token"
			clientID = s.cfg.OAuth.Dropbox.ClientID
			clientSecret = s.cfg.OAuth.Dropbox.ClientSecret
		}

		form := url.Values{}
		form.Set("client_id", clientID)
		form.Set("client_secret", clientSecret)
		form.Set("code", code)
		form.Set("redirect_uri", redirectBase+"/oauth/"+provider+"/callback")
		if provider != "github" {
			form.Set("grant_type", "authorization_code")
		}

		req, _ := http.NewRequest("POST", tokenURL, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if provider == "github" {
			req.Header.Set("Accept", "application/json")
		}
		if provider == "notion" {
			req.SetBasicAuth(clientID, clientSecret)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("OAuth token error (%s): %v", provider, err)
			http.Error(w, "Token exchange failed", 500)
			return
		}
		defer resp.Body.Close()
		respBytes, _ := io.ReadAll(resp.Body)

		var tokenResp map[string]interface{}
		json.Unmarshal(respBytes, &tokenResp)

		accessToken, _ := tokenResp["access_token"].(string)
		refreshToken, _ := tokenResp["refresh_token"].(string)
		scope, _ := tokenResp["scope"].(string)
		expiresIn, _ := tokenResp["expires_in"].(float64)

		integration := &store.Integration{
			Provider:    provider,
			AccessToken: accessToken,
			Connected:   true,
			ConnectedAt: time.Now().UTC(),
			Scope:       scope,
		}
		if refreshToken != "" {
			integration.RefreshToken = refreshToken
		}
		if expiresIn > 0 {
			integration.ExpiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)
		}

		if integration.AccessToken == "" {
		log.Printf("OAuth token exchange (%s) returned empty access token", provider)
		http.Error(w, "Token exchange failed: empty access token", 500)
		return
	}
	if err := store.SaveIntegration(s.dataDir, userID, integration); err != nil {
			log.Printf("Failed to save integration (%s): %v", provider, err)
		}
		log.Printf("[%s] OAuth connected: %s", userID, provider)

		dashToken := s.genToken(userID)
		redirectURL := fmt.Sprintf("https://%s/dashboard/integrations?token=%s&connected=%s", s.domain, dashToken, provider)
		http.Redirect(w, r, redirectURL, http.StatusFound)
	}
}

func (s *Server) handleThirdPartyPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	pd := pageData{Page: "third_party", UserName: ""}
	if token == "" {
		pd.Expired = true
		s.render(w, "third_party", pd)
		return
	}
	entry := s.consumeToken(token)
	if entry == nil {
		pd.Expired = true
		s.render(w, "third_party", pd)
		return
	}
	pd.Token = token
	pd.UserName = truncateID(entry.UserID)
	pd.Domain = s.domain
	servers := store.ConnectedServersForType(s.dataDir, entry.UserID, "bm_api")
	for _, srv := range servers {
		pd.Servers = append(pd.Servers, serverCard{ID: srv.ID, Name: srv.Name, URL: srv.URL})
	}
	s.render(w, "third_party", pd)
}

func (s *Server) handleThirdPartyAdd(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" || r.Method != "POST" {
		jsonError(w, "invalid request", 400)
		return
	}
	entry := s.consumeToken(token)
	if entry == nil {
		jsonError(w, "invalid token", 401)
		return
	}
	var req struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		URL    string `json:"url"`
		APIKey string `json:"api_key"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	healthURL := strings.TrimRight(req.URL, "/") + "/healthz"
	resp, err := http.Get(healthURL)
	if err != nil || resp.StatusCode != 200 {
		jsonError(w, "Server unreachable. Check URL and try again.", 400)
		return
	}
	resp.Body.Close()

	srv := store.ThirdPartyServer{
		Type:      req.Type,
		Name:      req.Name,
		URL:       req.URL,
		APIKey:    req.APIKey,
		Connected: true,
		ToolCount: 6,
	}
	if err := store.AddThirdPartyServer(s.dataDir, entry.UserID, srv); err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	jsonOK(w, map[string]interface{}{"status": "connected", "tools": 6})
}

func (s *Server) handleThirdPartyAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/third-party/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		jsonError(w, "unknown action", 404)
		return
	}
	serverID, action := parts[0], parts[1]
	token := r.URL.Query().Get("token")
	if token == "" || r.Method != "POST" {
		jsonError(w, "invalid request", 400)
		return
	}
	entry := s.consumeToken(token)
	if entry == nil {
		jsonError(w, "invalid token", 401)
		return
	}
	if action == "remove" {
		store.RemoveThirdPartyServer(s.dataDir, entry.UserID, serverID)
		jsonOK(w, map[string]string{"status": "removed"})
		return
	}
	jsonError(w, "unknown action", 404)
}

func (s *Server) render(w http.ResponseWriter, templateName string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		log.Printf("Template error: %v", err)
	}
}

func (s *Server) handleEmailPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	pd := pageData{Page: "email", UserName: ""}
	if token == "" {
		pd.Expired = true
		s.render(w, "email", pd)
		return
	}
	entry := s.consumeToken(token)
	if entry == nil {
		pd.Expired = true
		s.render(w, "email", pd)
		return
	}
	pd.Token = token
	pd.UserName = truncateID(entry.UserID)
	pd.Domain = s.domain
	ec, _ := store.LoadEmailConfig(s.dataDir, entry.UserID)
	if ec != nil && ec.Enabled {
		pd.EmailConfigured = true
		pd.EmailAddress = ec.Address
		if s.maildir != nil {
			pd.EmailUnread = s.maildir.CountUnread(entry.UserID)
		}
	}
	s.render(w, "email", pd)
}

func (s *Server) handleEmailConfigure(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" || r.Method != "POST" {
		jsonError(w, "invalid request", 400)
		return
	}
	entry := s.consumeToken(token)
	if entry == nil {
		jsonError(w, "invalid token", 401)
		return
	}
	var req struct{ Username string `json:"username"` }
	json.NewDecoder(r.Body).Decode(&req)
	if req.Username == "" || len(req.Username) > 32 {
		jsonError(w, "Invalid username (max 32 chars)", 400)
		return
	}
	if strings.ContainsAny(req.Username, "@<>\"' ") {
		jsonError(w, "Username has invalid characters", 400)
		return
	}
	_, existing := store.UserByEmail(s.dataDir, s.domain, req.Username+"@"+s.domain)
	if existing != nil && existing.Enabled {
		jsonError(w, "That username is already taken", 400)
		return
	}
	ec := &store.EmailConfig{
		Enabled:     true,
		Username:    req.Username,
		Address:     req.Username + "@" + s.domain,
		ConfiguredAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := store.SaveEmailConfig(s.dataDir, entry.UserID, ec); err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	jsonOK(w, map[string]string{"status": "configured", "address": ec.Address})
}

func (s *Server) handleEmailDisable(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" || r.Method != "POST" {
		jsonError(w, "invalid request", 400)
		return
	}
	entry := s.consumeToken(token)
	if entry == nil {
		jsonError(w, "invalid token", 401)
		return
	}
	ec, _ := store.LoadEmailConfig(s.dataDir, entry.UserID)
	if ec == nil {
		jsonOK(w, map[string]string{"status": "already disabled"})
		return
	}
	ec.Enabled = false
	store.SaveEmailConfig(s.dataDir, entry.UserID, ec)
	jsonOK(w, map[string]string{"status": "disabled"})
}

func (s *Server) handleRSSPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	pd := pageData{Page: "rss", UserName: ""}
	if token == "" {
		pd.Expired = true
		s.render(w, "rss", pd)
		return
	}
	entry := s.consumeToken(token)
	if entry == nil {
		pd.Expired = true
		s.render(w, "rss", pd)
		return
	}
	pd.Token = token
	pd.UserName = truncateID(entry.UserID)
	feeds, _ := store.LoadRSSFeeds(s.dataDir, entry.UserID)
	pd.RSSFeeds = feeds
	s.render(w, "rss", pd)
}

func (s *Server) handleRSSAdd(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" || r.Method != "POST" {
		jsonError(w, "invalid request", 400)
		return
	}
	entry := s.consumeToken(token)
	if entry == nil {
		jsonError(w, "invalid token", 401)
		return
	}
	var req struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Name == "" || req.URL == "" {
		jsonError(w, "name and url required", 400)
		return
	}
	feed := store.RSSFeed{
		ID:      fmt.Sprintf("feed_%d", time.Now().UnixNano()),
		Name:    req.Name,
		URL:     req.URL,
		AddedAt: time.Now(),
	}
	if err := store.AddRSSFeed(s.dataDir, entry.UserID, feed); err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	jsonOK(w, map[string]string{"status": "added"})
}

func (s *Server) handleRSSAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/rss/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		jsonError(w, "unknown action", 404)
		return
	}
	feedID, action := parts[0], parts[1]
	token := r.URL.Query().Get("token")
	if token == "" || r.Method != "POST" {
		jsonError(w, "invalid request", 400)
		return
	}
	entry := s.consumeToken(token)
	if entry == nil {
		jsonError(w, "invalid token", 401)
		return
	}
	switch action {
	case "remove":
		store.RemoveRSSFeed(s.dataDir, entry.UserID, feedID)
		jsonOK(w, map[string]string{"status": "removed"})
	case "fetch":
		go store.FetchAndSaveFeed(s.dataDir, entry.UserID, feedID)
		jsonOK(w, map[string]string{"status": "fetching"})
	default:
		jsonError(w, "unknown action", 404)
	}
}

func (s *Server) handleSiteWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}
	secret := r.URL.Query().Get("secret")
	if secret == "" {
		jsonError(w, "missing secret", 400)
		return
	}
	site, err := store.LookupSiteBySecret(s.dataDir, secret)
	if err != nil {
		jsonError(w, "invalid secret", 401)
		return
	}

	body, _ := io.ReadAll(r.Body)
	var payload map[string]interface{}
	json.Unmarshal(body, &payload)

	go func() {
		msg := fmt.Sprintf("📩 someone submitted on %s", site.SiteName)
		if f, ok := payload["form"].(map[string]interface{}); ok {
			for k, v := range f {
				msg += fmt.Sprintf("\n  %s: %v", k, v)
			}
		}
		if s.notify != nil {
			s.notify(site.UserID, msg)
		}
	}()

	jsonOK(w, map[string]string{"status": "ok"})
}

func (s *Server) cleanExpiredLoop() {
	for {
		time.Sleep(30 * time.Second)
		s.mu.Lock()
		now := time.Now()
		for t, e := range s.tokens {
			if now.After(e.ExpiresAt) {
				delete(s.tokens, t)
			}
		}
		for st, e := range s.states {
			if now.After(e.Expires) {
				delete(s.states, st)
			}
		}
		s.mu.Unlock()
	}
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func jsonOK(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func safeStr(p *store.UserProfile, field string) string {
	if p == nil {
		return ""
	}
	switch field {
	case "name":
		return p.Name
	case "timezone":
		return p.Timezone
	case "vibe":
		return p.Vibe
	}
	return ""
}

func truncateID(id string) string {
	if len(id) > 12 {
		return id[:12] + "..."
	}
	return id
}
