package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"kite/agent"
	"kite/config"
	"kite/dashboard"
	"kite/discord"
	"kite/email"
	"kite/llm"
	"kite/memory"
	"kite/sandbox"
	"kite/store"
	"kite/tool"
	"kite/trigger"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("Config: %v", err)
	}

	llmClient := llm.NewClient(
		cfg.LLMEndpoint,
		cfg.LLMApiKey,
		cfg.LLMModel,
		cfg.MaxOutputTokens,
		cfg.Temperature,
		cfg.StripReasoning,
		cfg.DataDir,
	)
	eaLLMClient := llm.NewClient(
		cfg.LLMEndpoint,
		cfg.LLMApiKey,
		cfg.LLMModel,
		cfg.MaxOutputTokens,
		0.3,
		cfg.StripReasoning,
		cfg.DataDir,
	)
	var fb1, fb2 *llm.Client
	if cfg.LLMFallback1Endpoint != "" {
		fb1 = llm.NewClient(cfg.LLMFallback1Endpoint, cfg.LLMFallback1Key, cfg.LLMFallback1Model, cfg.MaxOutputTokens, cfg.Temperature, cfg.StripReasoning, cfg.DataDir)
	}
	if cfg.LLMFallback2Endpoint != "" {
		fb2 = llm.NewClient(cfg.LLMFallback2Endpoint, cfg.LLMFallback2Key, cfg.LLMFallback2Model, cfg.MaxOutputTokens, cfg.Temperature, cfg.StripReasoning, cfg.DataDir)
	}
	for _, c := range []*llm.Client{llmClient, eaLLMClient} {
		if fb1 != nil {
			c.AddFallback(fb1)
		}
		if fb2 != nil {
			c.AddFallback(fb2)
		}
	}

	sandboxMgr := sandbox.NewManager(
		cfg.DataDir,
		cfg.AlpineRootfsPath,
		cfg.SharedRootfsPath,
		cfg.SandboxMaxOutputBytes,
		cfg.SandboxCommandTimeoutSec,
		cfg.SandboxMaxDiskMB,
		cfg.SandboxPreinstallPkgs,
		cfg.SandboxDNS,
	)

	if err := sandboxMgr.InitSharedRootfs(); err != nil {
		log.Printf("Sandbox init error: %v", err)
	} else {
		log.Println("Shared sandbox rootfs ready")
	}

	sched := trigger.NewScheduler(cfg.DataDir, time.Duration(cfg.TriggerCheckIntervalSec)*time.Second)

	maildir := email.NewMaildir(cfg.DataDir)
	dashboardLinkTool := &tool.DashboardLink{Generate: tool.GenerateDashboardLink}

	execTools := []tool.Tool{
		&tool.SandboxExec{Manager: sandboxMgr},
		&tool.SandboxRead{Manager: sandboxMgr, ChunkSize: cfg.SandboxReadChunkSize},
		&tool.SandboxWrite{Manager: sandboxMgr, ChunkSize: cfg.SandboxWriteChunkSize},
		&tool.SandboxList{Manager: sandboxMgr},
		&tool.WebFetch{},
		&tool.WebSearch{Config: tool.SearXNGConfig{
			Endpoints: cfg.SearXNGEndpoints,
			Timeout:   time.Duration(cfg.SearchTimeoutSec) * time.Second,
			Limit:     5,
		}},
		&tool.TimezoneCurrent{},

		&tool.TimezoneConvert{},

		&tool.TimezoneList{},

		&tool.GetPrice{},
		&tool.TriggerCreate{DataDir: cfg.DataDir, Scheduler: sched},
		&tool.TriggerList{DataDir: cfg.DataDir, Scheduler: sched},
		&tool.TriggerDelete{DataDir: cfg.DataDir, Scheduler: sched},
		dashboardLinkTool,
		&tool.NotionSearch{DataDir: cfg.DataDir},
		&tool.NotionReadPage{DataDir: cfg.DataDir},
		&tool.NotionCreatePage{DataDir: cfg.DataDir},
		&tool.NotionUpdatePage{DataDir: cfg.DataDir},
		&tool.NotionListDatabases{DataDir: cfg.DataDir},
		&tool.NotionQueryDatabase{DataDir: cfg.DataDir},
		&tool.NotionGetBlockChildren{DataDir: cfg.DataDir},
		&tool.NotionAppendBlock{DataDir: cfg.DataDir},
		&tool.GitHubSearchRepos{DataDir: cfg.DataDir},
		&tool.GitHubListRepos{DataDir: cfg.DataDir},
		&tool.GitHubReadFile{DataDir: cfg.DataDir},
		&tool.GitHubCreateFile{DataDir: cfg.DataDir},
		&tool.GitHubUpdateFile{DataDir: cfg.DataDir},
		&tool.GitHubCreateIssue{DataDir: cfg.DataDir},
		&tool.GitHubCreatePR{DataDir: cfg.DataDir},
		&tool.GitHubMergePR{DataDir: cfg.DataDir},
		&tool.GitHubAddComment{DataDir: cfg.DataDir},
		&tool.GitHubListIssues{DataDir: cfg.DataDir},
		&tool.GitHubListPRs{DataDir: cfg.DataDir},
		&tool.GitHubGetUser{DataDir: cfg.DataDir},
		&tool.GitHubGetRepo{DataDir: cfg.DataDir},
		&tool.GitHubClosePR{DataDir: cfg.DataDir},
		&tool.GitHubReopenPR{DataDir: cfg.DataDir},
		&tool.GitHubCloseIssue{DataDir: cfg.DataDir},
		&tool.GitHubReopenIssue{DataDir: cfg.DataDir},
		&tool.GitHubGetPR{DataDir: cfg.DataDir},
		&tool.GitHubGetIssue{DataDir: cfg.DataDir},
		&tool.GitHubDeleteFile{DataDir: cfg.DataDir},
		&tool.DropboxListFiles{DataDir: cfg.DataDir},
		&tool.DropboxUploadFile{DataDir: cfg.DataDir},
		&tool.DropboxDownloadFile{DataDir: cfg.DataDir, Manager: sandboxMgr},
		&tool.DropboxDeleteFile{DataDir: cfg.DataDir},
		&tool.DropboxSearch{DataDir: cfg.DataDir},
		&tool.DropboxGetInfo{DataDir: cfg.DataDir},
		&tool.DropboxCreateFolder{DataDir: cfg.DataDir},
		&tool.DropboxGetLink{DataDir: cfg.DataDir},
		&tool.BMSearch{DataDir: cfg.DataDir},
		&tool.BMCreate{DataDir: cfg.DataDir},
		&tool.BMGet{DataDir: cfg.DataDir},
		&tool.BMUpdate{DataDir: cfg.DataDir},
		&tool.BMDelete{DataDir: cfg.DataDir},
		&tool.BMList{DataDir: cfg.DataDir},
		&tool.DeploySite{DataDir: cfg.DataDir, PublicPath: cfg.SitePublicPath, PublicURL: "https://" + cfg.DashboardDomain + "/site", DashboardDomain: cfg.DashboardDomain},
		&tool.DeleteSite{DataDir: cfg.DataDir, PublicPath: cfg.SitePublicPath},
		&tool.TaskSearchEmail{DataDir: cfg.DataDir, Maildir: maildir},
		&tool.EmailSend{Provider: tool.EmailConfigProvider{DataDir: cfg.DataDir, Domain: cfg.SMTPDomain}},
		&tool.EmailCheck{Maildir: maildir, Provider: tool.EmailConfigProvider{DataDir: cfg.DataDir, Domain: cfg.SMTPDomain}},
		&tool.EmailRead{Maildir: maildir, Provider: tool.EmailConfigProvider{DataDir: cfg.DataDir, Domain: cfg.SMTPDomain}},
		&tool.EmailReply{Maildir: maildir, Provider: tool.EmailConfigProvider{DataDir: cfg.DataDir, Domain: cfg.SMTPDomain}},
		&tool.EmailForward{Maildir: maildir, Provider: tool.EmailConfigProvider{DataDir: cfg.DataDir, Domain: cfg.SMTPDomain}},
		&tool.EmailDelete{Maildir: maildir, Provider: tool.EmailConfigProvider{DataDir: cfg.DataDir, Domain: cfg.SMTPDomain}},
		&tool.EmailSearch{Maildir: maildir, Provider: tool.EmailConfigProvider{DataDir: cfg.DataDir, Domain: cfg.SMTPDomain}},
		&tool.RSSFetch{DataDir: cfg.DataDir},
		&tool.RSSList{DataDir: cfg.DataDir},
		&tool.RSSArticles{DataDir: cfg.DataDir},
		&tool.NoteSave{DataDir: cfg.DataDir},
		&tool.NoteGet{DataDir: cfg.DataDir},
		&tool.NoteSearch{DataDir: cfg.DataDir},
		&tool.NoteList{DataDir: cfg.DataDir},
		&tool.NoteDelete{DataDir: cfg.DataDir},
		&tool.SkillInstall{DataDir: cfg.DataDir},
		&tool.SkillList{DataDir: cfg.DataDir},
		&tool.SkillRemove{DataDir: cfg.DataDir},
		&tool.SkillView{DataDir: cfg.DataDir},
	}

	registry := agent.NewRegistry(cfg.DataDir)
	mcpReg := tool.NewMCPRegistry(cfg.DataDir)
	execRunner := agent.NewExecutionRunner(registry, eaLLMClient, execTools, mcpReg, cfg.ExecLoopMaxIterations, cfg.DataDir)
	interactionAgent := agent.NewInteractionAgent(llmClient, execRunner, registry, cfg)

	sched.OnFire = makeOnFire(execRunner, interactionAgent, cfg.DataDir, nil)

	bot, err := discord.NewBot(cfg.DiscordToken, interactionAgent, cfg, sandboxMgr)
	if err != nil {
		log.Fatalf("Discord: %v", err)
	}
	dashboardLinkTool.ChannelLookup = bot.ChannelForUser

	interactionAgent.SetAgentDoneCallback(func(userID, agentName, result string) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		response, err := interactionAgent.Handle(ctx, agent.MakeHandleContext(userID),
			fmt.Sprintf("Your helper '%s' finished with: %s. Tell the user what happened in 1 sentence.", agentName, truncateStr(result, 1000)),
			nil, nil)
		ch := interactionAgent.ChannelForUser(userID)
		send := func(msg string) {
			if ch != "" {
				bot.NotifyChannel(ch, msg)
			} else {
				bot.NotifyUser(userID, msg)
			}
		}
		if err != nil {
			retryCtx, retryCancel := context.WithTimeout(context.Background(), 30*time.Second)
			retryPrompt := fmt.Sprintf("'%s' finished: %s. tell user in 1 sentence.", agentName, truncateStr(result, 1000))
			r2, e2 := interactionAgent.Handle(retryCtx, agent.MakeHandleContext(userID), retryPrompt, nil, nil)
			retryCancel()
			if e2 == nil && r2 != "" && r2 != "__SILENT__" {
				send(r2)
				return
			}
			send(fmt.Sprintf("your helper '%s' finished: %s", agentName, truncateStr(result, 500)))
			return
		}
		if response != "" && response != "__SILENT__" {
			send(response)
		}
	})

	sched.OnFire = makeOnFire(execRunner, interactionAgent, cfg.DataDir, bot)

	tool.SetDropboxOAuth(cfg.OAuth.Dropbox.ClientID, cfg.OAuth.Dropbox.ClientSecret)

	dashServer := dashboard.NewServer(cfg.DashboardPort, cfg.DashboardDomain, cfg.DataDir, cfg)
	dashServer.SetNotifier(func(userID, message string) {
		bot.NotifyUser(userID, message)
	})
	dashServer.SetMCPRefresher(mcpReg)
	dashServer.SetMaildir(maildir)
	bot.SetDashboardGen(dashServer.GenerateToken)
	tool.SetDashboardGenerator(dashServer.GenerateToken)
	go dashServer.Start()

	smtpSrv := email.NewSMTPServer(cfg.SMTPPort, cfg.SMTPDomain, cfg.DataDir, maildir)
	smtpSrv.SetOnEmail(func(userID string, eml *email.Email) {
		log.Printf("New email for %s: %s", userID, eml.Subject)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		result, err := execRunner.Run(ctx, userID, "email_"+eml.ID, "Email handler", fmt.Sprintf(
			"New email received. From: %s. Subject: %s. Use email_read to read it (id: %s), then decide if the user needs to know. If important, summarize for the user.",
			eml.From, eml.Subject, eml.ID))
		if err != nil {
			log.Printf("Email agent error: %v", err)
			return
		}
		if result != "" {
			iaCtx, iaCancel := context.WithTimeout(context.Background(), 15*time.Second)
			summary, sumErr := interactionAgent.Handle(iaCtx, agent.MakeHandleContext(userID),
				fmt.Sprintf("Email result: %s. Tell the user in 1 sentence in your voice.", result), nil, nil)
			iaCancel()
			if sumErr == nil && summary != "" && summary != "__SILENT__" {
				bot.NotifyUser(userID, summary)
				memory.AppendMessage(cfg.DataDir, userID, llm.NewTextMessage("system", summary))
			}
		}
	})
	go smtpSrv.Start()

	if err := bot.Start(); err != nil {
		log.Fatalf("Bot start: %v", err)
	}
	sched.Start()

	// Downloads cleanup goroutine
	if cfg.DownloadCleanupHours > 0 {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("Download cleanup panic: %v", r)
				}
			}()
			interval := time.Duration(cfg.DownloadCleanupHours) * time.Hour
			for {
				time.Sleep(interval)
				log.Println("Running downloads cleanup...")
				if err := sandboxMgr.CleanupDownloads(cfg.DataDir); err != nil {
					log.Printf("Cleanup error: %v", err)
				}
			}
		}()
	}

	// Email monitoring goroutine — polls inbox every 5 min for important messages
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Email monitoring panic: %v", r)
			}
		}()
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		lastChecked := make(map[string]time.Time)
		for range ticker.C {
			entries, _ := os.ReadDir(cfg.DataDir)
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				userID := entry.Name()
				ec, _ := store.LoadEmailConfig(cfg.DataDir, userID)
				if ec == nil || !ec.Enabled {
					continue
				}
				lcb, ok := lastChecked[userID]
				if !ok {
					lastChecked[userID] = time.Now().Add(-24 * time.Hour)
					continue
				}
				emails, err := maildir.ListUnreadSince(userID, lcb)
				if err != nil {
					continue
				}
				processed := 0
				for _, eml := range emails {
					if processed >= 5 {
						break
					}
					processed++
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					classify, classifyErr := llmClient.Chat(ctx, []llm.Message{
						{Role: "system", Content: "Classify this email as urgent, important, or normal. Reply with exactly one word."},
						{Role: "user", Content: fmt.Sprintf("From: %s\nSubject: %s\nBody: %s", eml.From, eml.Subject, eml.Body)},
					}, nil)
					cancel()
					if classifyErr == nil && len(classify.Choices) > 0 {
						label := strings.TrimSpace(classify.Choices[0].Message.Content)
						if label == "urgent" || label == "important" {
							iaCtx, iaCancel := context.WithTimeout(context.Background(), 15*time.Second)
							summary, sumErr := interactionAgent.Handle(iaCtx, agent.MakeHandleContext(userID),
								fmt.Sprintf("New email from %s: %s. Tell the user in 1 sentence in your voice.", eml.From, eml.Subject), nil, nil)
							iaCancel()
							if sumErr == nil && summary != "" && summary != "__SILENT__" {
								bot.NotifyUser(userID, summary)
								memory.AppendMessage(cfg.DataDir, userID, llm.NewTextMessage("system", summary))
							}
						}
					}
				}
				lastChecked[userID] = time.Now()
			}
		}
	}()

	// RSS auto-fetch goroutine — fetches all feeds every 30 minutes
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("RSS fetch panic: %v", r)
			}
		}()
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			log.Println("Running RSS auto-fetch...")
			store.RefreshAllFeeds(cfg.DataDir)
		}
	}()

	log.Println("Kite is running. Press Ctrl+C to stop.")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Println("Shutting down...")
	sched.Stop()
	bot.Close()
}

func makeOnFire(execRunner *agent.ExecutionRunner, interactionAgent *agent.InteractionAgent, dataDir string, bot *discord.Bot) func(string, trigger.Trigger) {
	return func(userID string, t trigger.Trigger) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		log.Printf("Trigger %s firing for user %s: %s", t.ID, userID, t.Prompt)

		agentID := t.AgentID
		if agentID == "" {
			agentID = "trigger_" + t.ID
		}
		result, err := execRunner.Run(ctx, userID, agentID, t.Prompt, t.Prompt)
		if err != nil {
			log.Printf("Trigger execution error: %v", err)
			result = "Failed to execute trigger. Check logs."
		}

		if bot != nil && result != "" && result != "__SILENT__" {
			bot.NotifyUser(userID, result)
			memory.AppendMessage(dataDir, userID, llm.NewTextMessage("system", "[Trigger "+t.ID+"] "+result))
		}
	}
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
