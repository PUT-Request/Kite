package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"kite/store"
)

type githubBase struct{ dataDir string }

func (b *githubBase) token(userID string) (string, error) {
	i, err := store.LoadIntegration(b.dataDir, userID, "github")
	if err != nil || !i.Connected {
		return "", fmt.Errorf("github not connected")
	}
	return i.AccessToken, nil
}

func (b *githubBase) apiCall(ctx context.Context, method, url string, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "kite/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github api %d: %s", resp.StatusCode, previewBody(bodyBytes, 200))
	}
	return bodyBytes, nil
}

type GitHubSearchRepos struct{ DataDir string }

func (t *GitHubSearchRepos) Name() string        { return "github_search_repos" }
func (t *GitHubSearchRepos) Description() string { return "Search GitHub repositories by name, description, or topic." }
func (t *GitHubSearchRepos) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"query": strParam("Search query."),
		"limit": intParam("Max results (default 10)."),
	}, []string{"query"})
}
func (t *GitHubSearchRepos) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := githubBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	q := getStringArg(args, "query")
	limit := getIntArg(args, "limit", 10)
	respBody, err := b.apiCall(ctx, "GET", fmt.Sprintf("https://api.github.com/search/repositories?q=%s&per_page=%d", escapeQuery(q), limit), token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var result struct {
		TotalCount int `json:"total_count"`
		Items      []struct {
			FullName    string `json:"full_name"`
			Description string `json:"description"`
			HTMLURL     string `json:"html_url"`
			Stars       int    `json:"stargazers_count"`
		} `json:"items"`
	}
	json.Unmarshal(respBody, &result)
	if len(result.Items) == 0 {
		return fmt.Sprintf("No repos found for '%s'.", q), nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d repos (total %d):\n", len(result.Items), result.TotalCount))
	for i, r := range result.Items {
		sb.WriteString(fmt.Sprintf("%d. %s ⭐%d\n   %s\n   %s\n", i+1, r.FullName, r.Stars, r.Description, r.HTMLURL))
	}
	return sb.String(), nil
}

type GitHubListRepos struct{ DataDir string }

func (t *GitHubListRepos) Name() string        { return "github_list_repos" }
func (t *GitHubListRepos) Description() string { return "List the authenticated user's GitHub repositories." }
func (t *GitHubListRepos) Parameters() map[string]interface{} {
	return params(map[string]interface{}{}, nil)
}
func (t *GitHubListRepos) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := githubBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	respBody, err := b.apiCall(ctx, "GET", "https://api.github.com/user/repos?per_page=30&sort=updated", token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var repos []struct {
		FullName string `json:"full_name"`
		Private  bool   `json:"private"`
	}
	json.Unmarshal(respBody, &repos)
	if len(repos) == 0 {
		return "No repos found.", nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d repos:\n", len(repos)))
	for i, r := range repos {
		vis := "public"
		if r.Private {
			vis = "private"
		}
		sb.WriteString(fmt.Sprintf("%d. %s (%s)\n", i+1, r.FullName, vis))
	}
	return sb.String(), nil
}

type GitHubReadFile struct{ DataDir string }

func (t *GitHubReadFile) Name() string        { return "github_read_file" }
func (t *GitHubReadFile) Description() string { return "Read a file from a GitHub repository. Specify owner/repo and file path." }
func (t *GitHubReadFile) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"repo":      strParam("Repository as owner/repo (e.g., 'torvalds/linux')."),
		"path":      strParam("File path in the repo (e.g., 'README.md')."),
	}, []string{"repo", "path"})
}
func (t *GitHubReadFile) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := githubBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	repo := getStringArg(args, "repo")
	path := getStringArg(args, "path")
	respBody, err := b.apiCall(ctx, "GET", fmt.Sprintf("https://api.github.com/repos/%s/contents/%s", escapeQuery(repo), escapeQuery(path)), token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var result struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		Size     int    `json:"size"`
	}
	json.Unmarshal(respBody, &result)
	if result.Content != "" {
		text := result.Content
		if result.Encoding == "base64" {
			decoded, decErr := base64.StdEncoding.DecodeString(result.Content)
			if decErr == nil {
				text = string(decoded)
			}
		}
		return fmt.Sprintf("%s (%d bytes, %s):\n%s", path, result.Size, result.Encoding, text), nil
	}
	return fmt.Sprintf("Response: %s", previewBody(respBody, 300)), nil
}

type GitHubCreateIssue struct{ DataDir string }

func (t *GitHubCreateIssue) Name() string        { return "github_create_issue" }
func (t *GitHubCreateIssue) Description() string { return "Create a GitHub issue in a repository." }
func (t *GitHubCreateIssue) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"repo":  strParam("Repository as owner/repo."),
		"title": strParam("Issue title."),
		"body":  strParam("Issue body/description."),
	}, []string{"repo", "title"})
}
func (t *GitHubCreateIssue) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := githubBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	repo := getStringArg(args, "repo")
	title := getStringArg(args, "title")
	body := getStringArg(args, "body")
	bodyJSON, _ := json.Marshal(map[string]string{"title": title, "body": body})
	req, _ := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("https://api.github.com/repos/%s/issues", repo), strings.NewReader(string(bodyJSON)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	json.Unmarshal(respBody, &result)
	if result.Number > 0 {
		return fmt.Sprintf("Issue #%d created: %s", result.Number, result.HTMLURL), nil
	}
	return fmt.Sprintf("Response: %s", previewBody(respBody, 200)), nil
}

type GitHubListIssues struct{ DataDir string }

func (t *GitHubListIssues) Name() string        { return "github_list_issues" }
func (t *GitHubListIssues) Description() string { return "List issues in a GitHub repository." }
func (t *GitHubListIssues) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"repo":  strParam("Repository as owner/repo."),
		"state": strParam("Issue state: 'open' (default), 'closed', or 'all'."),
	}, []string{"repo"})
}
func (t *GitHubListIssues) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := githubBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	repo := getStringArg(args, "repo")
	state := getStringArg(args, "state")
	if state == "" {
		state = "open"
	}
	respBody, err := b.apiCall(ctx, "GET", fmt.Sprintf("https://api.github.com/repos/%s/issues?state=%s&per_page=20", escapeQuery(repo), escapeQuery(state)), token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var issues []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		State  string `json:"state"`
	}
	json.Unmarshal(respBody, &issues)
	if len(issues) == 0 {
		return fmt.Sprintf("No %s issues in %s.", state, repo), nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d %s issues in %s:\n", len(issues), state, repo))
	for _, iss := range issues {
		sb.WriteString(fmt.Sprintf("  #%d [%s] %s\n", iss.Number, iss.State, iss.Title))
	}
	return sb.String(), nil
}

type GitHubListPRs struct{ DataDir string }

func (t *GitHubListPRs) Name() string        { return "github_list_prs" }
func (t *GitHubListPRs) Description() string { return "List pull requests in a GitHub repository." }
func (t *GitHubListPRs) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"repo":  strParam("Repository as owner/repo."),
		"state": strParam("PR state: 'open' (default), 'closed', or 'all'."),
	}, []string{"repo"})
}
func (t *GitHubListPRs) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := githubBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	repo := getStringArg(args, "repo")
	state := getStringArg(args, "state")
	if state == "" {
		state = "open"
	}
	respBody, err := b.apiCall(ctx, "GET", fmt.Sprintf("https://api.github.com/repos/%s/pulls?state=%s&per_page=20", escapeQuery(repo), escapeQuery(state)), token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var prs []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		State  string `json:"state"`
	}
	json.Unmarshal(respBody, &prs)
	if len(prs) == 0 {
		return fmt.Sprintf("No %s PRs in %s.", state, repo), nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d %s PRs:\n", len(prs), state))
	for _, pr := range prs {
		sb.WriteString(fmt.Sprintf("  #%d %s\n", pr.Number, pr.Title))
	}
	return sb.String(), nil
}

type GitHubGetUser struct{ DataDir string }

func (t *GitHubGetUser) Name() string        { return "github_get_user" }
func (t *GitHubGetUser) Description() string { return "Get GitHub user profile info (login, name, bio, followers, repos)." }
func (t *GitHubGetUser) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"username": strParam("GitHub username (leave empty for authenticated user)."),
	}, []string{})
}
func (t *GitHubGetUser) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := githubBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	username := getStringArg(args, "username")
	url := "https://api.github.com/user"
	if username != "" {
		url = "https://api.github.com/users/" + username
	}
	respBody, err := b.apiCall(ctx, "GET", url, token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var result struct {
		Login     string `json:"login"`
		Name      string `json:"name"`
		Bio       string `json:"bio"`
		Followers int    `json:"followers"`
		PublicRepos int  `json:"public_repos"`
	}
	json.Unmarshal(respBody, &result)
	n := result.Name
	if n == "" {
		n = result.Login
	}
	return fmt.Sprintf("%s (%s)\nbio: %s\nfollowers: %d | public repos: %d", n, result.Login, result.Bio, result.Followers, result.PublicRepos), nil
}

type GitHubGetRepo struct{ DataDir string }

func (t *GitHubGetRepo) Name() string        { return "github_get_repo" }
func (t *GitHubGetRepo) Description() string { return "Get GitHub repository details (description, stars, language, topics)." }
func (t *GitHubGetRepo) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"repo": strParam("Repository as owner/repo."),
	}, []string{"repo"})
}
func (t *GitHubGetRepo) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := githubBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	repo := getStringArg(args, "repo")
	respBody, err := b.apiCall(ctx, "GET", "https://api.github.com/repos/"+repo, token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var result struct {
		FullName    string   `json:"full_name"`
		Description string   `json:"description"`
		Stars       int      `json:"stargazers_count"`
		Forks       int      `json:"forks_count"`
		Language    string   `json:"language"`
		Topics      []string `json:"topics"`
		HTMLURL     string   `json:"html_url"`
	}
	json.Unmarshal(respBody, &result)
	return fmt.Sprintf("%s\n%s\n⭐ %d | forks %d | lang: %s\ntopics: %v\n%s",
		result.FullName, result.Description, result.Stars, result.Forks,
		result.Language, result.Topics, result.HTMLURL), nil
}

type GitHubCreateFile struct{ DataDir string }

func (t *GitHubCreateFile) Name() string        { return "github_create_file" }
func (t *GitHubCreateFile) Description() string { return "Create a file in a GitHub repository. Commits directly to the specified branch." }
func (t *GitHubCreateFile) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"repo":    strParam("Repository as owner/repo."),
		"path":    strParam("File path (e.g., src/main.go)."),
		"content": strParam("File content."),
		"message": strParam("Commit message."),
		"branch":  strParam("Branch to commit to (default 'main')."),
	}, []string{"repo", "path", "content", "message"})
}
func (t *GitHubCreateFile) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := githubBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	repo := getStringArg(args, "repo")
	path := getStringArg(args, "path")
	content := getStringArg(args, "content")
	msg := getStringArg(args, "message")
	branch := getStringArg(args, "branch")
	if branch == "" {
		branch = "main"
	}
	payload, _ := json.Marshal(map[string]string{
		"message": msg,
		"content": base64.StdEncoding.EncodeToString([]byte(content)),
		"branch":  branch,
	})
	req, _ := http.NewRequestWithContext(ctx, "PUT", fmt.Sprintf("https://api.github.com/repos/%s/contents/%s", repo, path), strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		Commit struct {
			SHA string `json:"sha"`
			URL string `json:"html_url"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(respBody, &result); err == nil && result.Commit.SHA != "" {
		return fmt.Sprintf("Created %s at %s\nCommit: %s", path, result.Commit.URL, result.Commit.SHA), nil
	}
	return fmt.Sprintf("Response: %s", previewBody(respBody, 300)), nil
}

type GitHubUpdateFile struct{ DataDir string }

func (t *GitHubUpdateFile) Name() string        { return "github_update_file" }
func (t *GitHubUpdateFile) Description() string { return "Update an existing file in a GitHub repository. Requires the current file SHA." }
func (t *GitHubUpdateFile) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"repo":    strParam("Repository as owner/repo."),
		"path":    strParam("File path (e.g., src/main.go)."),
		"content": strParam("New file content."),
		"message": strParam("Commit message."),
		"sha":     strParam("Current file SHA (get from github_read_file)."),
		"branch":  strParam("Branch (default 'main')."),
	}, []string{"repo", "path", "content", "message", "sha"})
}
func (t *GitHubUpdateFile) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := githubBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	repo := getStringArg(args, "repo")
	path := getStringArg(args, "path")
	content := getStringArg(args, "content")
	msg := getStringArg(args, "message")
	sha := getStringArg(args, "sha")
	branch := getStringArg(args, "branch")
	if branch == "" {
		branch = "main"
	}
	payload, _ := json.Marshal(map[string]string{
		"message": msg,
		"content": base64.StdEncoding.EncodeToString([]byte(content)),
		"sha":     sha,
		"branch":  branch,
	})
	req, _ := http.NewRequestWithContext(ctx, "PUT", fmt.Sprintf("https://api.github.com/repos/%s/contents/%s", repo, path), strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		Commit struct {
			SHA string `json:"sha"`
			URL string `json:"html_url"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(respBody, &result); err == nil && result.Commit.SHA != "" {
		return fmt.Sprintf("Updated %s\nCommit: %s", path, result.Commit.URL), nil
	}
	return fmt.Sprintf("Response: %s", previewBody(respBody, 300)), nil
}

type GitHubCreatePR struct{ DataDir string }

func (t *GitHubCreatePR) Name() string        { return "github_create_pr" }
func (t *GitHubCreatePR) Description() string { return "Create a pull request on a GitHub repository." }
func (t *GitHubCreatePR) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"repo":   strParam("Repository as owner/repo."),
		"title":  strParam("PR title."),
		"body":   strParam("PR description."),
		"head":   strParam("Source branch (the branch with changes)."),
		"base":   strParam("Target branch (default 'main')."),
	}, []string{"repo", "title", "head"})
}
func (t *GitHubCreatePR) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := githubBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	repo := getStringArg(args, "repo")
	title := getStringArg(args, "title")
	body := getStringArg(args, "body")
	head := getStringArg(args, "head")
	base := getStringArg(args, "base")
	if base == "" {
		base = "main"
	}
	payload, _ := json.Marshal(map[string]string{
		"title": title,
		"body":  body,
		"head":  head,
		"base":  base,
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("https://api.github.com/repos/%s/pulls", repo), strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	json.Unmarshal(respBody, &result)
	if result.Number > 0 {
		return fmt.Sprintf("PR #%d created: %s", result.Number, result.HTMLURL), nil
	}
	return fmt.Sprintf("Response: %s", previewBody(respBody, 300)), nil
}

type GitHubAddComment struct{ DataDir string }

func (t *GitHubAddComment) Name() string        { return "github_add_comment" }
func (t *GitHubAddComment) Description() string { return "Add a comment to an issue or pull request on a GitHub repository." }
func (t *GitHubAddComment) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"repo":    strParam("Repository as owner/repo."),
		"issue":   intParam("Issue or PR number."),
		"body":    strParam("Comment body text."),
	}, []string{"repo", "issue", "body"})
}
func (t *GitHubAddComment) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := githubBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	repo := getStringArg(args, "repo")
	issue := getIntArg(args, "issue", 0)
	body := getStringArg(args, "body")
	payload, _ := json.Marshal(map[string]string{"body": body})
	req, _ := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("https://api.github.com/repos/%s/issues/%d/comments", repo, issue), strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		ID      int    `json:"id"`
		HTMLURL string `json:"html_url"`
	}
	json.Unmarshal(respBody, &result)
	if result.ID > 0 {
		return fmt.Sprintf("Comment added: %s", result.HTMLURL), nil
	}
	return fmt.Sprintf("Response: %s", previewBody(respBody, 300)), nil
}

type GitHubClosePR struct{ DataDir string }

func (t *GitHubClosePR) Name() string        { return "github_close_pr" }
func (t *GitHubClosePR) Description() string { return "Close a pull request without merging." }
func (t *GitHubClosePR) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"repo":   strParam("Repository as owner/repo."),
		"number": intParam("PR number to close."),
	}, []string{"repo", "number"})
}
func (t *GitHubClosePR) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := githubBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	repo := getStringArg(args, "repo")
	number := getIntArg(args, "number", 0)
	payload, _ := json.Marshal(map[string]string{"state": "closed"})
	req, _ := http.NewRequestWithContext(ctx, "PATCH", fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d", repo, number), strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
	}
	json.Unmarshal(respBody, &result)
	if result.State == "closed" {
		return fmt.Sprintf("PR #%d closed: %s", result.Number, result.HTMLURL), nil
	}
	return fmt.Sprintf("Response: %s", previewBody(respBody, 300)), nil
}

type GitHubReopenPR struct{ DataDir string }

func (t *GitHubReopenPR) Name() string        { return "github_reopen_pr" }
func (t *GitHubReopenPR) Description() string { return "Reopen a closed pull request." }
func (t *GitHubReopenPR) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"repo":   strParam("Repository as owner/repo."),
		"number": intParam("PR number to reopen."),
	}, []string{"repo", "number"})
}
func (t *GitHubReopenPR) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := githubBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	repo := getStringArg(args, "repo")
	number := getIntArg(args, "number", 0)
	payload, _ := json.Marshal(map[string]string{"state": "open"})
	req, _ := http.NewRequestWithContext(ctx, "PATCH", fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d", repo, number), strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
	}
	json.Unmarshal(respBody, &result)
	if result.State == "open" {
		return fmt.Sprintf("PR #%d reopened: %s", result.Number, result.HTMLURL), nil
	}
	return fmt.Sprintf("Response: %s", previewBody(respBody, 300)), nil
}

type GitHubCloseIssue struct{ DataDir string }

func (t *GitHubCloseIssue) Name() string        { return "github_close_issue" }
func (t *GitHubCloseIssue) Description() string { return "Close a GitHub issue." }
func (t *GitHubCloseIssue) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"repo":   strParam("Repository as owner/repo."),
		"number": intParam("Issue number to close."),
	}, []string{"repo", "number"})
}
func (t *GitHubCloseIssue) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := githubBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	repo := getStringArg(args, "repo")
	number := getIntArg(args, "number", 0)
	payload, _ := json.Marshal(map[string]string{"state": "closed"})
	req, _ := http.NewRequestWithContext(ctx, "PATCH", fmt.Sprintf("https://api.github.com/repos/%s/issues/%d", repo, number), strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
	}
	json.Unmarshal(respBody, &result)
	if result.State == "closed" {
		return fmt.Sprintf("Issue #%d closed: %s", result.Number, result.HTMLURL), nil
	}
	return fmt.Sprintf("Response: %s", previewBody(respBody, 300)), nil
}

type GitHubReopenIssue struct{ DataDir string }

func (t *GitHubReopenIssue) Name() string        { return "github_reopen_issue" }
func (t *GitHubReopenIssue) Description() string { return "Reopen a closed GitHub issue." }
func (t *GitHubReopenIssue) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"repo":   strParam("Repository as owner/repo."),
		"number": intParam("Issue number to reopen."),
	}, []string{"repo", "number"})
}
func (t *GitHubReopenIssue) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := githubBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	repo := getStringArg(args, "repo")
	number := getIntArg(args, "number", 0)
	payload, _ := json.Marshal(map[string]string{"state": "open"})
	req, _ := http.NewRequestWithContext(ctx, "PATCH", fmt.Sprintf("https://api.github.com/repos/%s/issues/%d", repo, number), strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
	}
	json.Unmarshal(respBody, &result)
	if result.State == "open" {
		return fmt.Sprintf("Issue #%d reopened: %s", result.Number, result.HTMLURL), nil
	}
	return fmt.Sprintf("Response: %s", previewBody(respBody, 300)), nil
}

type GitHubGetPR struct{ DataDir string }

func (t *GitHubGetPR) Name() string        { return "github_get_pr" }
func (t *GitHubGetPR) Description() string { return "Get detailed information about a specific pull request." }
func (t *GitHubGetPR) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"repo":   strParam("Repository as owner/repo."),
		"number": intParam("PR number."),
	}, []string{"repo", "number"})
}
func (t *GitHubGetPR) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := githubBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	repo := getStringArg(args, "repo")
	number := getIntArg(args, "number", 0)
	respBody, err := b.apiCall(ctx, "GET", fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d", repo, number), token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var result struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		State   string `json:"state"`
		HTMLURL string `json:"html_url"`
		Head    struct {
			Ref  string `json:"ref"`
			SHA  string `json:"sha"`
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
		Base struct {
			Ref  string `json:"ref"`
			SHA  string `json:"sha"`
		} `json:"base"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		Merged bool `json:"merged"`
	}
	json.Unmarshal(respBody, &result)
	body := result.Body
	if len(body) > 500 {
		body = body[:500] + "..."
	}
	return fmt.Sprintf("PR #%d: %s\nby: %s | state: %s | merged: %v\n%s ← %s\n\n%s\n%s",
		result.Number, result.Title, result.User.Login, result.State, result.Merged,
		result.Base.Ref, result.Head.Ref,
		body, result.HTMLURL), nil
}

type GitHubGetIssue struct{ DataDir string }

func (t *GitHubGetIssue) Name() string        { return "github_get_issue" }
func (t *GitHubGetIssue) Description() string { return "Get detailed information about a specific issue." }
func (t *GitHubGetIssue) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"repo":   strParam("Repository as owner/repo."),
		"number": intParam("Issue number."),
	}, []string{"repo", "number"})
}
func (t *GitHubGetIssue) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := githubBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	repo := getStringArg(args, "repo")
	number := getIntArg(args, "number", 0)
	respBody, err := b.apiCall(ctx, "GET", fmt.Sprintf("https://api.github.com/repos/%s/issues/%d", repo, number), token)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	var result struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		State   string `json:"state"`
		HTMLURL string `json:"html_url"`
		User    struct {
			Login string `json:"login"`
		} `json:"user"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	json.Unmarshal(respBody, &result)
	body := result.Body
	if len(body) > 500 {
		body = body[:500] + "..."
	}
	var labels string
	for i, l := range result.Labels {
		if i > 0 {
			labels += ", "
		}
		labels += l.Name
	}
	if labels == "" {
		labels = "none"
	}
	return fmt.Sprintf("Issue #%d: %s\nby: %s | state: %s\nlabels: %s\n\n%s\n%s",
		result.Number, result.Title, result.User.Login, result.State,
		labels, body, result.HTMLURL), nil
}

type GitHubDeleteFile struct{ DataDir string }

func (t *GitHubDeleteFile) Name() string        { return "github_delete_file" }
func (t *GitHubDeleteFile) Description() string { return "Delete a file from a GitHub repository." }
func (t *GitHubDeleteFile) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"repo":    strParam("Repository as owner/repo."),
		"path":    strParam("File path to delete."),
		"message": strParam("Commit message."),
		"sha":     strParam("Current file SHA (get from github_read_file)."),
		"branch":  strParam("Branch (default 'main')."),
	}, []string{"repo", "path", "message", "sha"})
}
func (t *GitHubDeleteFile) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := githubBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	repo := getStringArg(args, "repo")
	path := getStringArg(args, "path")
	msg := getStringArg(args, "message")
	sha := getStringArg(args, "sha")
	branch := getStringArg(args, "branch")
	if branch == "" {
		branch = "main"
	}
	payload, _ := json.Marshal(map[string]string{
		"message": msg,
		"sha":     sha,
		"branch":  branch,
	})
	req, _ := http.NewRequestWithContext(ctx, "DELETE", fmt.Sprintf("https://api.github.com/repos/%s/contents/%s", repo, path), strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		Commit struct {
			SHA string `json:"sha"`
			URL string `json:"html_url"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(respBody, &result); err == nil && result.Commit.SHA != "" {
		return fmt.Sprintf("Deleted %s\nCommit: %s", path, result.Commit.URL), nil
	}
	return fmt.Sprintf("Response: %s", previewBody(respBody, 300)), nil
}

type GitHubMergePR struct{ DataDir string }

func (t *GitHubMergePR) Name() string        { return "github_merge_pr" }
func (t *GitHubMergePR) Description() string { return "Merge a pull request on a GitHub repository." }
func (t *GitHubMergePR) Parameters() map[string]interface{} {
	return params(map[string]interface{}{
		"repo":      strParam("Repository as owner/repo."),
		"number":    intParam("PR number to merge."),
		"method":    strParam("Merge method: 'merge' (default), 'squash', or 'rebase'."),
	}, []string{"repo", "number"})
}
func (t *GitHubMergePR) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	b := githubBase{dataDir: t.DataDir}
	token, err := b.token(userID)
	if err != nil {
		return err.Error(), nil
	}
	repo := getStringArg(args, "repo")
	number := getIntArg(args, "number", 0)
	method := getStringArg(args, "method")
	if method == "" {
		method = "merge"
	}
	payload, _ := json.Marshal(map[string]string{"merge_method": method})
	req, _ := http.NewRequestWithContext(ctx, "PUT", fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d/merge", repo, number), strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf("API error: %v", err), nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		SHA     string `json:"sha"`
		Message string `json:"message"`
		Merged  bool   `json:"merged"`
	}
	json.Unmarshal(respBody, &result)
	if result.Merged {
		return fmt.Sprintf("PR #%d merged. SHA: %s", number, result.SHA), nil
	}
	msg := result.Message
	if msg == "" {
		msg = string(respBody)
	}
	return fmt.Sprintf("Merge failed: %s", previewBody([]byte(msg), 300)), nil
}
