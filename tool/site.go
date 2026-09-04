package tool

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"kite/store"
)

type DeploySite struct {
	DataDir         string
	PublicPath      string
	PublicURL       string
	DashboardDomain string
}

func (t *DeploySite) Name() string        { return "deploy_site" }
func (t *DeploySite) Description() string { return "Deploy a site directory from the sandbox to the web. Validates remote resources in HTML files before deploying. Pass unsafe=true to skip validation." }
func (t *DeploySite) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"site_path": strParam("Path to the site directory in sandbox (e.g., '/site/myportfolio')."),
		"site_name": strParam("URL-safe name for the site (e.g., 'myportfolio'). Appears in the URL."),
		"unsafe":    strParam("Set to 'true' to skip remote resource validation and deploy anyway."),
	}, []string{"site_path", "site_name"})
}

func (t *DeploySite) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	sitePath := getStringArg(args, "site_path")
	siteName := getStringArg(args, "site_name")
	unsafe := getStringArg(args, "unsafe") == "true"
	if sitePath == "" || siteName == "" {
		return "Error: site_path and site_name are required", nil
	}
	siteName = safeFilename(siteName)

	home := filepath.Join(t.DataDir, userID, "sandbox", "home")
	src := filepath.Clean(filepath.Join(home, strings.TrimPrefix(sitePath, "/home/user/")))
	if !strings.HasPrefix(src, filepath.Clean(home)+string(filepath.Separator)) {
		return "Error: site path escapes sandbox", nil
	}
	if _, err := os.Stat(src); err != nil {
		return fmt.Sprintf("Site directory not found: %s", sitePath), nil
	}

	if !unsafe {
		hasIndex := false
		filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if strings.ToLower(filepath.Base(path)) == "index.html" {
				hasIndex = true
			}
			return nil
		})
		if !hasIndex {
			return "Error: no index.html found. The main HTML file must be named index.html. Pass unsafe=true to skip this check.", nil
		}
		issues := validateSite(src)
		if len(issues) > 0 {
			return fmt.Sprintf("Validation found %d issue(s):\n%s\n\nPass unsafe=true to bypass.", len(issues), strings.Join(issues, "\n")), nil
		}
	}

	dstDir := filepath.Join(t.PublicPath, userID, siteName)
	os.RemoveAll(dstDir)
	if err := copyDir(src, dstDir); err != nil {
		return fmt.Sprintf("Deploy error: %v", err), nil
	}

	// Generate webhook secret and replace placeholders in deployed files
	webhookURL := ""
	if t.DashboardDomain != "" {
		webhookURL = fmt.Sprintf("https://%s/api/webhook/site", t.DashboardDomain)
	}
	secret := store.GenerateWebhookSecret()
	filepath.Walk(dstDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".html") && !strings.HasSuffix(path, ".htm") && !strings.HasSuffix(path, ".js") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		content := string(data)
		changed := false
		if strings.Contains(content, "{{KITE_WEBHOOK_URL}}") {
			content = strings.ReplaceAll(content, "{{KITE_WEBHOOK_URL}}", webhookURL)
			changed = true
		}
		if strings.Contains(content, "{{KITE_WEBHOOK_SECRET}}") {
			content = strings.ReplaceAll(content, "{{KITE_WEBHOOK_SECRET}}", secret)
			changed = true
		}
		if changed {
			os.WriteFile(path, []byte(content), 0644)
		}
		return nil
	})

	if webhookURL != "" {
		store.SaveSite(t.DataDir, userID, &store.DeployedSite{
			SiteName:      siteName,
			UserID:        userID,
			WebhookSecret: secret,
			DeployedAt:    time.Now(),
		})
	}

	url := fmt.Sprintf("%s/%s/%s", strings.TrimRight(t.PublicURL, "/"), userID, siteName)
	result := fmt.Sprintf("Site deployed: %s", url)
	if webhookURL != "" {
		result += "\nWebhook live — when someone submits a form, I'll DM you their data."
	}
	return result, nil
}

var remoteRefRe = regexp.MustCompile(`(?:src|href)\s*=\s*["'](https?://[^"']+)["']`)

func validateSite(dir string) []string {
	var issues []string
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}

	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".html") && !strings.HasSuffix(path, ".htm") {
			return nil
		}
		data, _ := os.ReadFile(path)
		matches := remoteRefRe.FindAllStringSubmatch(string(data), -1)
		seen := map[string]bool{}
		for _, m := range matches {
			url := m[1]
			if seen[url] {
				continue
			}
			seen[url] = true
			resp, err := client.Head(url)
			if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 400 {
				rel, _ := filepath.Rel(dir, path)
				issues = append(issues, fmt.Sprintf("  %s → %s (unreachable)", rel, url))
			}
			if resp != nil {
				resp.Body.Close()
			}
		}
		return nil
	})
	return issues
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()
	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer d.Close()
	_, err = io.Copy(d, s)
	return err
}

func safeFilename(s string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(s) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			b.WriteRune(c)
		} else {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

type DeleteSite struct {
	DataDir    string
	PublicPath string
}

func (t *DeleteSite) Name() string        { return "delete_site" }
func (t *DeleteSite) Description() string { return "Delete a deployed site by name. Removes the site files and its URL." }
func (t *DeleteSite) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"site_name": strParam("Name of the site to delete."),
	}, []string{"site_name"})
}
func (t *DeleteSite) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	siteName := safeFilename(getStringArg(args, "site_name"))
	if siteName == "" {
		return "Error: site_name is required", nil
	}

	dstDir := filepath.Join(t.PublicPath, userID, siteName)
	if _, err := os.Stat(dstDir); err != nil {
		return fmt.Sprintf("Site '%s' not found.", siteName), nil
	}

	os.RemoveAll(dstDir)

	metaPath := filepath.Join(t.DataDir, userID, "sites", siteName+".json")
	os.Remove(metaPath)

	return fmt.Sprintf("Site '%s' deleted. The URL is no longer accessible.", siteName), nil
}
