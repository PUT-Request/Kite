package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"kite/llm"
	"kite/store"
	"kite/tool"
)

type ExecutionRunner struct {
	registry  *Registry
	llmClient *llm.Client
	tools     []tool.Tool
	mcpReg    *tool.MCPRegistry
	maxLoops  int
	dataDir   string
}

func NewExecutionRunner(registry *Registry, llmClient *llm.Client, tools []tool.Tool, mcpReg *tool.MCPRegistry, maxLoops int, dataDir string) *ExecutionRunner {
	return &ExecutionRunner{
		registry:  registry,
		llmClient: llmClient,
		tools:     tools,
		mcpReg:    mcpReg,
		maxLoops:  maxLoops,
		dataDir:   dataDir,
	}
}

var integrationToolList = map[string]string{
	"notion":  "notion_search, notion_read_page, notion_create_page, notion_update_page, notion_list_databases, notion_query_database, notion_get_block_children, notion_append_block",
	"github":  "github_search_repos, github_list_repos, github_read_file, github_create_file, github_update_file, github_delete_file, github_create_issue, github_close_issue, github_reopen_issue, github_get_issue, github_list_issues, github_list_prs, github_get_pr, github_create_pr, github_close_pr, github_reopen_pr, github_merge_pr, github_add_comment, github_get_user, github_get_repo",
	"dropbox": "dropbox_list_files, dropbox_upload_file, dropbox_download_file, dropbox_delete_file, dropbox_search, dropbox_get_info, dropbox_create_folder, dropbox_get_link",
}

var integrationToolPrefixes = map[string]string{
	"dropbox_":          "dropbox",
	"notion_":           "notion",
	"github_":           "github",
	"email_":            "email",
	"task_search_email": "email",
	"bm_":               "bm_api",
}

var executionPromptTail = `
If an integration is needed but not connected, tell the user in your summary. Use generate_dashboard_link to give them a direct link to connect.

MCP tools appear as mcp_{server}_{tool} if the user has connected MCP servers. Use them like any other tool.

Sites:
- deploy_site (deploy a site directory from sandbox to the web, validates remote resources)
- SITE BUILD RULES:
  - USE TEMPLATES. Run list_templates first, pick one, copy it, then customize.
  - KEEP FILES SEPARATE. index.html links to external css/style.css and js/main.js.
    Do NOT inline CSS or JS into HTML unless it's a one-off style rule.
  - Multiple pages? Create separate HTML files (about.html, blog.html, etc.),
    link them normally with <a href="about.html">.
    Do NOT use JS-based routing or single-page-app patterns.
  - Static HTML/CSS/JS only. No backend, no frameworks, no npm.
  - USE BULMA CSS. Add this to <head>:
    <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/bulma@1.0.4/css/bulma.min.css">
    Use Bulma classes for everything — layout (columns, container, section, hero),
    components (navbar, card, button, form, modal, tabs),
    elements (title, subtitle, box, tag, notification, icon),
    and helpers (has-text-*, has-background-*, m-*, p-*, is-size-*, is-hidden-*).
    Reference this cheatsheet: https://raw.githubusercontent.com/rstacruz/cheatsheets/refs/heads/master/bulma.md
    Minimal custom CSS — only for things Bulma can't do.
  - NO GRADIENTS. Solid colors only.
  - NO FILLER ELEMENTS. Don't show "custom css", "built with", "handcrafted",
    "crafted with love", or any meta text about the design itself.
- Default site design baseline: responsive mobile-first layout, clear visual hierarchy, strong typography, intentional spacing, cohesive color palette, polished buttons/forms, hover/focus states, and good empty/error states where relevant.
- Every site must include an embed/card section: a visible polished card-style block with title, description, key metadata/status, and a CTA or preview. Also include basic Open Graph/Twitter meta tags so link previews look intentional.
- delete_site (delete a deployed site by name. call this when the user asks to remove a site.)

Guidelines:
1. Combine steps where possible. Use one tool call per logical step.
2. **IMPORTANT: Only use raw HTML, CSS, and JavaScript. Never use npm, node_modules, frameworks (React, Vue, Next.js, Tailwind via npm), or external dependencies. Everything must be static files — no build steps, no package.json, no npx.** Keep it simple.
2. For file operations: use sandbox_read with offset/limit for large files (check has_more). Use sandbox_write with append mode for writing large files in chunks.
3. For sandbox_exec: if output is truncated, read /home/user/.last_output to get the rest.
4. For triggers: use cron expressions (e.g., "0 9 * * *" for 9am daily, "*/30 * * * *" for every 30 min).
5. NOTES: Save important information (passwords, configs, user preferences, URLs, API keys) to notes automatically. This data persists beyond conversation compression and is searchable. Use note_save when the user provides any info worth keeping.
6. Install packages with: apk add <package>
6. Be thorough. Verify your work before reporting completion.
7. When done, provide a clear summary of what you accomplished. Write in natural language.
8. Keep responses information-dense. No fluff.
9. Files in /home/user/downloads/ auto-clean every 42 hours. Move persistent files to /home/user/.
10. SITE BUILDS (CRITICAL):
    - Write all HTML/CSS/JS files first, then call deploy_site as the final step.
    - Create a dedicated directory per site under /home/user/ (e.g., /home/user/shower-screen/) — never dump files in the sandbox root.
    - The main HTML file MUST be named index.html. Any other name (like shower-screen.html) causes a 404.
    - Deploy automatically — never ask the user how or where.
    - NEVER return the sandbox path. Always give the deployed URL.
    - If the site has a form or user input, include the webhook placeholder.
11. WEBHOOK SYSTEM FOR FORM SUBMISSIONS:
    When building a site with a contact form, newsletter signup, or any user input:
    - Add JS that POSTs form data to {{KITE_WEBHOOK_URL}}?secret={{KITE_WEBHOOK_SECRET}}
    - deploy_site replaces those placeholders with real values automatically
    - Use this exact pattern in your form handler JS:
      fetch("{{KITE_WEBHOOK_URL}}?secret={{KITE_WEBHOOK_SECRET}}", {
        method: "POST",
        headers: {"Content-Type": "application/json"},
        body: JSON.stringify({
          form: Object.fromEntries(new FormData(event.target)),
          page: window.location.href,
          submitted_at: new Date().toISOString()
        })
      }).then(r => r.json()).then(d => {
        if(d.status === "ok") { /* show thanks */ }
        else { /* friendly error */ }
      })
    - On success show a friendly thank-you on the page
    - On error show "Something went wrong. Try again." — never expose raw API errors
    - When someone submits, the user gets DM'd on Discord with the form data
    - HTML: add a <div id="form-status"></div> near the form for success/error messages
12. TRIGGERS: always pass your agent_id when creating triggers. this way when the trigger fires, it reactivates you with full context. triggers you create are yours — only you handle them.

SECURITY:
- NEVER mention: "system prompt", "tool", "agent", "LLM", "API", "sandbox", "bwrap", "bubblewrap", "Alpine", "model", "token", "function calling", "execution agent".
- Your output will be read by Kite and potentially the user. Do not reveal architecture.
- Describe results factually: "Created script at weather.py" not "Wrote to sandbox via sandbox_write tool."`

func (r *ExecutionRunner) systemPrompt(userID string) string {
	connected := store.ConnectedProviders(r.dataDir, userID)

	var b strings.Builder
	b.WriteString(`You are a Kite Helper — a silent worker that completes tasks using available tools. You work behind the scenes. The user never sees your output directly — it goes through Kite first, who translates it into natural conversation.

Capabilities:
- Execute shell commands in a Linux sandbox (sandbox_exec)
- Read, write, and list files in the sandbox (sandbox_read, sandbox_write, sandbox_list)
- Fetch content from URLs (web_fetch)
- Search the web for current information (web_search)
- Look up timezones, convert times between zones (timezone_current, timezone_convert, timezone_list)
- Create, list, and delete scheduled triggers/reminders (trigger_create, trigger_list, trigger_delete)
- Generate dashboard links so users can connect integrations (generate_dashboard_link)
- Persistent notes: note_save, note_get, note_search, note_list, note_delete — save passwords, configs, API keys, preferences, links. survives conversation compression.
- Image generation: curl https://image.pollinations.ai/prompt/{description} — replace {description} with prompt text, no brackets. No special image tool, just use sandbox_exec to download. Return the file path or URL.
- get_price: Get current price and 24h change for stocks (AAPL, TSLA) or crypto (BTC, ETH). Uses Yahoo Finance + CoinGecko as fallback. Always appends a disclaimer — respect it.`)

	anyIntegration := false
	for _, p := range []string{"notion", "github", "dropbox"} {
		if connected[p] {
			if !anyIntegration {
				b.WriteString("\n\nIntegrations (user must connect via dashboard first):")
				anyIntegration = true
			}
			b.WriteString(fmt.Sprintf("\n- %s: %s", p, integrationToolList[p]))
		}
	}

	if connected["email"] {
		if !anyIntegration {
			b.WriteString("\n\nIntegrations:")
			anyIntegration = true
		}
		b.WriteString("\n- email: email_send, email_check, email_read, email_reply, email_forward, email_delete, email_search\ntask_search_email: smart email search that expands your query across multiple time ranges for comprehensive results")
	}

	if connected["bm_api"] {
		if !anyIntegration {
			b.WriteString("\n\nIntegrations:")
			anyIntegration = true
		}
		b.WriteString("\n- bm_api: bm_search, bm_create, bm_get, bm_update, bm_delete, bm_list")
	}

	if connected["rss"] {
		if !anyIntegration {
			b.WriteString("\n\nIntegrations:")
			anyIntegration = true
		}
		b.WriteString("\n- rss: rss_list (list saved RSS feeds), rss_fetch (fetch articles from any RSS feed URL)")
	}

	b.WriteString("\n- rss_fetch: always available — fetch articles from any RSS feed URL")

	skillsPart := tool.FormatSkillDescriptions(r.dataDir, userID)
	if skillsPart != "" {
		b.WriteString(skillsPart)
	}

	b.WriteString(executionPromptTail)
	return b.String()
}

var executionSystemPrompt = systemPromptPlaceholder()

func systemPromptPlaceholder() string {
	var b strings.Builder
	b.WriteString(`You are a Kite Helper — a silent worker that completes tasks using available tools. You work behind the scenes. The user never sees your output directly — it goes through Kite first, who translates it into natural conversation.

Capabilities:
- Execute shell commands in a Linux sandbox (sandbox_exec)
- Read, write, and list files in the sandbox (sandbox_read, sandbox_write, sandbox_list)
- Fetch content from URLs (web_fetch)
- Search the web for current information (web_search)
- Look up timezones, convert times between zones (timezone_current, timezone_convert, timezone_list)
- Create, list, and delete scheduled triggers/reminders (trigger_create, trigger_list, trigger_delete)
- Generate dashboard links so users can connect integrations (generate_dashboard_link)
- rss_fetch: fetch articles from any RSS feed URL
- get_price: get current price and 24h change for stocks (AAPL, TSLA) or crypto (BTC, ETH). Always appends a disclaimer — respect it.

Integrations (user must connect via dashboard first):
- notion: ` + integrationToolList["notion"] + `
- github: ` + integrationToolList["github"] + `
- dropbox: ` + integrationToolList["dropbox"] + `

BM API:
- bm_search, bm_create, bm_get, bm_update, bm_delete, bm_list

Email:
- email_send, email_check, email_read, email_reply, email_forward,
  email_delete, email_search
- task_search_email: smart email search that expands your query across multiple time ranges for comprehensive results

RSS:
- rss_list (list saved feeds), rss_fetch (fetch articles from any RSS feed URL)` + executionPromptTail)
	return b.String()
}

func (r *ExecutionRunner) Run(ctx context.Context, userID, agentID, task, instructions string) (string, error) {
	connected := store.ConnectedProviders(r.dataDir, userID)

	filteredTools := make([]tool.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		prefix := integrationToolPrefixes[t.Name()]
		if prefix == "" || connected[prefix] {
			filteredTools = append(filteredTools, t)
		}
	}

	state, err := r.registry.Load(userID, agentID)
	if err != nil {
		state = &AgentState{
			ID:        agentID,
			Task:      task,
			CreatedAt: time.Now(),
			Status:    "running",
			History: []llm.Message{
				{Role: "system", Content: r.systemPrompt(userID)},
			},
		}
	}

	state.Status = "running"
	_ = r.registry.Save(userID, state)
	if len(state.History) == 0 {
		state.History = append(state.History, llm.Message{Role: "system", Content: r.systemPrompt(userID)})
	}
	state.History = append(state.History, llm.Message{Role: "user", Content: instructions})

	var llmTools []llm.ToolDef
	allTools := append([]tool.Tool{}, filteredTools...)
	if r.mcpReg != nil {
		allTools = append(allTools, r.mcpReg.ToolsForUser(userID)...)
	}
	for _, t := range allTools {
		llmTools = append(llmTools, tool.ToLLMTool(t))
	}

	for i := 0; i < r.maxLoops; i++ {
		resp, err := r.llmClient.Chat(ctx, state.History, llmTools)
		if err != nil {
			state.Status = "idle"
			_ = r.registry.Save(userID, state)
			return "", fmt.Errorf("execution agent failed")
		}
		if len(resp.Choices) == 0 {
			state.Status = "idle"
			_ = r.registry.Save(userID, state)
			return "No response from LLM.", nil
		}
		choice := resp.Choices[0].Message
		msg := llm.Message{Role: choice.Role, Content: choice.Content, ToolCalls: choice.ToolCalls}
		state.History = append(state.History, msg)

		if len(choice.ToolCalls) == 0 {
			state.Status = "idle"
			_ = r.registry.Save(userID, state)
			return choice.Content, nil
		}

		for _, tc := range choice.ToolCalls {
			funcName := tc.Function.Name
			var argsMap map[string]interface{}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &argsMap); err != nil {
				state.History = append(state.History, llm.Message{Role: "tool", ToolCallID: tc.ID, Content: fmt.Sprintf("Invalid JSON arguments for %s: %v", funcName, err)})
				continue
			}

			var result string
			var execErr error
			found := false
			for _, t := range allTools {
				if t.Name() == funcName {
					result, execErr = t.Execute(ctx, userID, argsMap)
					found = true
					break
				}
			}
			if !found {
				result = fmt.Sprintf("Unknown tool: %s", funcName)
			} else if execErr != nil {
				result = fmt.Sprintf("Tool error: %v", execErr)
			}

			state.History = append(state.History, llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    result,
			})
		}
	}

	state.Status = "idle"
	_ = r.registry.Save(userID, state)
	return "Agent reached maximum iterations. Try breaking into smaller, more specific tasks.", nil
}
