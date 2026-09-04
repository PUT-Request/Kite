package tool

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type skillInfo struct {
	Name        string
	Description string
	Body        string
	IsUser      bool
}

func parseSkillMD(content string) (name, description, body string, err error) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return "", "", "", fmt.Errorf("missing frontmatter")
	}
	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		return "", "", "", fmt.Errorf("malformed frontmatter")
	}
	frontmatter := strings.TrimSpace(parts[1])
	body = strings.TrimSpace(parts[2])

	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		} else if strings.HasPrefix(line, "description:") {
			description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		}
	}

	if name == "" || description == "" {
		return "", "", "", fmt.Errorf("missing required fields: name and description")
	}

	return name, description, body, nil
}

func scanSkills(dataDir, userID string) []skillInfo {
	var skills []skillInfo
	seen := map[string]bool{}

	userDir := filepath.Join(dataDir, userID, "skills")
	sysDir := filepath.Join(dataDir, "skills")

	for _, base := range []string{userDir, sysDir} {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillPath := filepath.Join(base, entry.Name(), "SKILL.md")
			raw, err := os.ReadFile(skillPath)
			if err != nil {
				continue
			}
			name, desc, _, err := parseSkillMD(string(raw))
			if err != nil {
				continue
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			skills = append(skills, skillInfo{
				Name:        name,
				Description: desc,
				Body:        string(raw),
				IsUser:      base == userDir,
			})
		}
	}

	return skills
}

func FormatSkillDescriptions(dataDir, userID string) string {
	skills := scanSkills(dataDir, userID)
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n# Installed Skills (loaded on demand)\n")
	b.WriteString("Use skill_view to load a skill's full instructions when the task matches.\n")
	for _, s := range skills {
		b.WriteString(fmt.Sprintf("- %s: %s\n", s.Name, s.Description))
	}
	return b.String()
}

type SkillInstall struct {
	DataDir string
}

func (t *SkillInstall) Name() string { return "install_skill" }
func (t *SkillInstall) Description() string {
	return "Install a skill from a raw SKILL.md URL. Fetches the file, validates it, and stores it for the user."
}
func (t *SkillInstall) Parameters() map[string]interface{} {
	return ObjectParams(map[string]interface{}{
		"url":  StrParam("Direct URL to a raw SKILL.md file (e.g., https://raw.githubusercontent.com/.../SKILL.md)."),
		"name": StrParam("Optional name override. If not set, uses the name from the SKILL.md frontmatter."),
	}, []string{"url"})
}
func (t *SkillInstall) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	url := ExtractArgString(args, "url")
	if url == "" {
		return "Error: url is required", nil
	}

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Sprintf("Error creating request: %v", err), nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("Error fetching URL: %v", err), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("Error: server returned %s", resp.Status), nil
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("Error reading response: %v", err), nil
	}

	name, desc, body, err := parseSkillMD(string(raw))
	if err != nil {
		return fmt.Sprintf("Invalid SKILL.md: %v", err), nil
	}

	overrideName := ExtractArgString(args, "name")
	if overrideName != "" {
		name = overrideName
	}

	skillDir := filepath.Join(t.DataDir, userID, "skills", name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return fmt.Sprintf("Error creating skill directory: %v", err), nil
	}

	// Reconstruct the full SKILL.md
	skillContent := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s", name, desc, body)
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		return fmt.Sprintf("Error writing skill file: %v", err), nil
	}

	return fmt.Sprintf("Installed skill '%s': %s", name, desc), nil
}

type SkillList struct {
	DataDir string
}

func (t *SkillList) Name() string { return "list_skills" }
func (t *SkillList) Description() string {
	return "List all installed skills with names and descriptions."
}
func (t *SkillList) Parameters() map[string]interface{} {
	return ObjectParams(map[string]interface{}{}, nil)
}
func (t *SkillList) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	skills := scanSkills(t.DataDir, userID)
	if len(skills) == 0 {
		return "No skills installed.", nil
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d skill(s) installed:\n\n", len(skills)))
	for _, s := range skills {
		scope := "user"
		if !s.IsUser {
			scope = "system"
		}
		b.WriteString(fmt.Sprintf("  - %s (%s): %s\n", s.Name, scope, s.Description))
	}
	return b.String(), nil
}

type SkillRemove struct {
	DataDir string
}

func (t *SkillRemove) Name() string { return "remove_skill" }
func (t *SkillRemove) Description() string {
	return "Remove an installed skill by name."
}
func (t *SkillRemove) Parameters() map[string]interface{} {
	return ObjectParams(map[string]interface{}{
		"name": StrParam("Name of the skill to remove."),
	}, []string{"name"})
}
func (t *SkillRemove) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	name := ExtractArgString(args, "name")
	if name == "" {
		return "Error: name is required", nil
	}

	skillDir := filepath.Join(t.DataDir, userID, "skills", name)
	if _, err := os.Stat(skillDir); os.IsNotExist(err) {
		return fmt.Sprintf("Skill '%s' not found.", name), nil
	}

	if err := os.RemoveAll(skillDir); err != nil {
		return fmt.Sprintf("Error removing skill: %v", err), nil
	}
	return fmt.Sprintf("Removed skill '%s'.", name), nil
}

type SkillView struct {
	DataDir string
}

func (t *SkillView) Name() string { return "skill_view" }
func (t *SkillView) Description() string {
	return "Load the full SKILL.md instructions for an installed skill."
}
func (t *SkillView) Parameters() map[string]interface{} {
	return ObjectParams(map[string]interface{}{
		"name": StrParam("Name of the skill to load."),
	}, []string{"name"})
}
func (t *SkillView) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	name := ExtractArgString(args, "name")
	if name == "" {
		return "Error: name is required", nil
	}

	// Check user skills first, then system
	paths := []string{
		filepath.Join(t.DataDir, userID, "skills", name, "SKILL.md"),
		filepath.Join(t.DataDir, "skills", name, "SKILL.md"),
	}

	var content string
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err == nil {
			content = string(raw)
			break
		}
	}

	if content == "" {
		return fmt.Sprintf("Skill '%s' not found.", name), nil
	}

	return content, nil
}
