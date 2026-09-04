package discord

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"kite/agent"
	"kite/config"
	"kite/memory"
	"kite/sandbox"
	"kite/store"
)

type msgState struct {
	ChannelID       string
	UserMsgID       string
	BotMsgID        string
	LastBotMsgAt    time.Time
}

type pendingMessage struct {
	Content     string
	UserMsgID   string
	Attachments []agent.Attachment
	ImageBase64s []string
}

type pendingReset struct {
	MsgID     string
	ChannelID string
	ExpiresAt time.Time
}

type Bot struct {
	session          *discordgo.Session
	interaction      *agent.InteractionAgent
	userChannels     map[string]string
	userMsgs         map[string]*msgState
	pendingResets    map[string]*pendingReset
	mu               sync.RWMutex
	config           *config.Config
	sandboxMgr       *sandbox.Manager
	dashboardGen     func(userID, channelID string) string
	processedMsgs    sync.Map
	activeCancel     map[string]context.CancelFunc
	pendingMsg       map[string]*pendingMessage
	statusMu         sync.Mutex
	userAliases      map[string]string
	dmChannels       map[string]string
}

func NewBot(token string, interaction *agent.InteractionAgent, cfg *config.Config, sandboxMgr *sandbox.Manager) (*Bot, error) {
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	session.Identify.Intents = discordgo.IntentsDirectMessages |
		discordgo.IntentMessageContent |
		discordgo.IntentsGuildMessages |
		discordgo.IntentsGuildMessageReactions |
		discordgo.IntentsDirectMessageReactions
	b := &Bot{
		session:      session,
		interaction:  interaction,
		userChannels: make(map[string]string),
		userMsgs:      make(map[string]*msgState),
		pendingResets: make(map[string]*pendingReset),
		activeCancel: make(map[string]context.CancelFunc),
		pendingMsg:   make(map[string]*pendingMessage),
		config:       cfg,
		sandboxMgr:   sandboxMgr,
		userAliases:  make(map[string]string),
		dmChannels:   make(map[string]string),
	}
	b.loadChannels()
	b.loadDMChannels()
	session.AddHandler(b.onMessageCreate)
	session.AddHandler(b.onInteractionCreate)
	if cfg.ReactionsEnabled {
		session.AddHandler(b.onReactionAdd)
	}
	interaction.SetReactor(b)
	interaction.SetFileSender(b)
	return b, nil
}

func (b *Bot) SetDashboardGen(fn func(userID, channelID string) string) {
	b.dashboardGen = fn
}

func (b *Bot) channelsPath() string {
	return filepath.Join(b.config.DataDir, "channels.json")
}

func (b *Bot) channelsDMPath() string {
	return filepath.Join(b.config.DataDir, "channels_dm.json")
}

func (b *Bot) saveChannels() {
	b.mu.RLock()
	channels := make(map[string]string, len(b.userChannels))
	for k, v := range b.userChannels {
		channels[k] = v
	}
	b.mu.RUnlock()
	if err := store.WriteJSON(b.channelsPath(), channels); err != nil {
		log.Printf("Failed to save channels: %v", err)
	}
}

func (b *Bot) loadChannels() {
	var channels map[string]string
	if err := store.ReadJSON(b.channelsPath(), &channels); err != nil {
		return
	}
	b.mu.Lock()
	for k, v := range channels {
		b.userChannels[k] = v
	}
	b.mu.Unlock()
	log.Printf("Loaded %d saved channels", len(channels))
}

func (b *Bot) saveDMChannels() {
	b.mu.RLock()
	channels := make(map[string]string, len(b.dmChannels))
	for k, v := range b.dmChannels {
		channels[k] = v
	}
	b.mu.RUnlock()
	if err := store.WriteJSON(b.channelsDMPath(), channels); err != nil {
		log.Printf("Failed to save DM channels: %v", err)
	}
}

func (b *Bot) loadDMChannels() {
	var channels map[string]string
	if err := store.ReadJSON(b.channelsDMPath(), &channels); err != nil {
		return
	}
	b.mu.Lock()
	for k, v := range channels {
		b.dmChannels[k] = v
	}
	b.mu.Unlock()
	log.Printf("Loaded %d saved DM channels", len(channels))
}

func (b *Bot) Start() error {
	if err := b.session.Open(); err != nil {
		return fmt.Errorf("open session: %w", err)
	}
	go b.cleanExpiredResetsLoop()
	go b.registerSlashCommands()
	log.Println("Discord bot connected")
	return nil
}

func (b *Bot) registerSlashCommands() {
	time.Sleep(2 * time.Second)
	userID := b.session.State.User.ID
	oldCmds, _ := b.session.ApplicationCommands(userID, "")
	for _, cmd := range oldCmds {
		b.session.ApplicationCommandDelete(userID, "", cmd.ID)
	}
	commands := []*discordgo.ApplicationCommand{
		{Name: "dashboard", Description: "Get a link to your Kite dashboard"},
		{Name: "reset", Description: "Wipe all your Kite data — profile, memory, agents, files"},
	}
	_, err := b.session.ApplicationCommandBulkOverwrite(userID, "", commands)
	if err != nil {
		log.Printf("Failed to register slash commands: %v", err)
	} else {
		log.Println("Registered 2 global slash commands: /dashboard, /reset")
	}
}

func (b *Bot) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Interaction handler panic: %v", r)
		}
	}()

	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	data := i.ApplicationCommandData()

	userID := ""
	if i.Member != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}
	if userID == "" {
		return
	}
	channelID := i.ChannelID

	b.mu.Lock()
	b.userChannels[userID] = channelID
	b.mu.Unlock()
	b.saveChannels()

	switch data.Name {
	case "dashboard":
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
		})
		go func() {
			link := "dashboard not available"
			if b.dashboardGen != nil {
				link = b.dashboardGen(userID, channelID)
			}
			content := fmt.Sprintf("Dashboard: %s\n\nLink expires in 5 minutes.", link)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content})
		}()
	case "reset":
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
		})
		go func() {
			b.handleResetInteraction(s, i, userID, channelID)
		}()
	}
}

func (b *Bot) Close() error {
	return b.session.Close()
}

func (b *Bot) React(channelID, messageID, emoji string) error {
	return b.session.MessageReactionAdd(channelID, messageID, emoji)
}

func (b *Bot) SendFile(channelID, filePath, caption string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = b.session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		File: &discordgo.File{
			Name:   filepath.Base(filePath),
			Reader: f,
		},
		Content: caption,
	})
	return err
}

func (b *Bot) ChannelForUser(userID string) (string, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	ch, ok := b.userChannels[userID]
	return ch, ok
}

func (b *Bot) guildContextID() string {
	if b.config.GuildID != "" {
		return "guild_" + b.config.GuildID
	}
	return ""
}

func (b *Bot) isGuildMode() bool {
	return b.config.GuildID != "" && b.config.ChannelID != ""
}

func (b *Bot) stateForUser(userID string) *msgState {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.userMsgs[userID]
	if !ok {
		s = &msgState{}
		b.userMsgs[userID] = s
	}
	return s
}

func (b *Bot) NotifyUser(userID, message string) {
	b.mu.RLock()
	ch, ok := b.dmChannels[userID]
	if !ok {
		ch, ok = b.userChannels[userID]
	}
	b.mu.RUnlock()
	if !ok {
		if b.isGuildMode() {
			ch = b.config.ChannelID
		}
		if ch == "" {
			log.Printf("No channel for user %s, cannot notify", userID)
			return
		}
	}
	b.sendChunked(b.session, ch, message)
}

func (b *Bot) NotifyChannel(channelID, message string) {
	if channelID == "" {
		return
	}
	b.sendChunked(b.session, channelID, message)
}

func (b *Bot) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.Bot {
		return
	}

	discordUserID := m.Author.ID

	// Always accept DMs, plus optionally accept a specific guild channel
	isValidChannel := false
	var contextID string
	var contextChannelID string

	ch, chErr := s.Channel(m.ChannelID)
	if chErr == nil && ch.Type == discordgo.ChannelTypeDM {
		isValidChannel = true
		contextID = discordUserID
		contextChannelID = m.ChannelID
	} else if b.isGuildMode() && m.GuildID == b.config.GuildID && m.ChannelID == b.config.ChannelID {
		isValidChannel = true
		contextID = b.guildContextID()
		contextChannelID = m.ChannelID
	}
	if !isValidChannel {
		return
	}

	if _, loaded := b.processedMsgs.LoadOrStore(m.ID, true); loaded {
		return
	}
	go func() { time.Sleep(30 * time.Second); b.processedMsgs.Delete(m.ID) }()

	// Per-user state
	b.mu.Lock()
	b.userChannels[discordUserID] = contextChannelID
	if contextID == discordUserID {
		b.dmChannels[discordUserID] = contextChannelID
	}
	if m.Author.Username != "" {
		b.userAliases[strings.ToLower(m.Author.Username)] = discordUserID
	}
	aliases := make(map[string]string, len(b.userAliases))
	for k, v := range b.userAliases {
		aliases[k] = v
	}
	b.mu.Unlock()
	b.saveChannels()
	if contextID == discordUserID {
		b.saveDMChannels()
	}
	b.interaction.SetUserAliases(aliases)

	state := b.stateForUser(discordUserID)
	state.ChannelID = contextChannelID
	state.UserMsgID = m.ID

	userMsgID := m.ID
	channelID := contextChannelID

	b.interaction.SetUserState(contextID, channelID, userMsgID, "")

	userMsg := m.Content

	// Inject reply context so LLM knows what they're replying to
	if m.MessageReference != nil {
		refMsg, refErr := s.ChannelMessage(m.ChannelID, m.MessageReference.MessageID)
		if refErr == nil && refMsg.Author.ID != "" {
			refAuthor := refMsg.Author.Username
			if refMsg.Author.ID == s.State.User.ID {
				refAuthor = "Kite"
			}
			replyCtx := truncate(refMsg.Content, 200)
			userMsg = fmt.Sprintf("(replying to @%s: \"%s\") %s", refAuthor, replyCtx, userMsg)
		}
	}

	// Tag message with username in guild mode so LLM knows who spoke
	if contextID != discordUserID && b.isGuildMode() {
		userMsg = fmt.Sprintf("@%s: %s", m.Author.Username, userMsg)
	}
	var atts []agent.Attachment

	if shouldHumanPause(userMsg) && !state.LastBotMsgAt.IsZero() && time.Since(state.LastBotMsgAt) < 8*time.Second {
		jitter := time.Duration(1500+rand.Intn(1500)) * time.Millisecond
		time.Sleep(jitter)
	}

	if len(m.Attachments) > 0 {
		sandboxID := contextID
		for _, a := range m.Attachments {
			isImage := strings.HasPrefix(a.ContentType, "image/")
			dlCtx, dlCancel := context.WithTimeout(context.Background(), 30*time.Second)
			localPath, dlErr := b.sandboxMgr.DownloadAttachment(dlCtx, sandboxID, a.URL, a.Filename)
			dlCancel()
			sandboxPath := "/home/user/downloads/" + a.Filename
			atts = append(atts, agent.Attachment{
				URL:         a.URL,
				Filename:    a.Filename,
				LocalPath:   localPath,
				SandboxPath: sandboxPath,
				IsImage:     isImage,
				Width:       a.Width,
				Height:      a.Height,
			})
			if dlErr != nil {
				log.Printf("Download attachment failed: %v", dlErr)
			}
			tag := "File"
			if isImage {
				tag = "Image"
			}
			if userMsg == "" {
				userMsg = fmt.Sprintf("%s attached: %s", tag, sandboxPath)
			} else {
				userMsg += fmt.Sprintf("\n%s attached: %s", tag, sandboxPath)
			}
			log.Printf("[%s] %s: attachment %s -> %s", discordUserID, m.Author.Username, a.Filename, sandboxPath)
		}
	}

	log.Printf("[%s] %s: %s", discordUserID, m.Author.Username, userMsg)

	var imageBase64s []string
	if b.config.VisionEnabled {
		for _, att := range atts {
			if !att.IsImage || att.LocalPath == "" {
				continue
			}
			b64, err := fileToBase64(att.LocalPath)
			if err != nil {
				log.Printf("Failed to encode image %s: %v", att.Filename, err)
				continue
			}
			imageBase64s = append(imageBase64s, b64)
		}
	}

	// Per-context coalescing — keyed by contextID to prevent DM↔guild cross-contamination
	b.statusMu.Lock()
	if cancel, ok := b.activeCancel[contextID]; ok {
		b.pendingMsg[contextID] = &pendingMessage{
			Content:      userMsg,
			UserMsgID:    userMsgID,
			Attachments:  atts,
			ImageBase64s: imageBase64s,
		}
		cancel()
		b.statusMu.Unlock()
		return
	}
	b.statusMu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Message handler panic: %v", r)
			}
		}()
		done := make(chan struct{})
		go typingLoop(s, channelID, done)
		defer close(done)
		b.handleMessage(discordUserID, m.Author.Username, contextID, channelID, userMsgID, userMsg, atts, imageBase64s)
	}()
}

func (b *Bot) handleMessage(discordUserID, speakerName, contextID, channelID, userMsgID, userMsg string, atts []agent.Attachment, imageBase64s []string) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)

		b.statusMu.Lock()
		b.activeCancel[contextID] = cancel
		b.statusMu.Unlock()

		hc := agent.HandleContext{CtxID: contextID, SpeakerID: discordUserID, SpeakerName: speakerName}
		response, err := b.interaction.Handle(ctx, hc, userMsg, atts, imageBase64s)

		b.statusMu.Lock()
		pending := b.pendingMsg[contextID]
		delete(b.pendingMsg, contextID)
		delete(b.activeCancel, contextID)
		if pending != nil && err != nil {
			userMsg = userMsg + "\n" + pending.Content
			atts = append(atts, pending.Attachments...)
			imageBase64s = append(imageBase64s, pending.ImageBase64s...)
			userMsgID = pending.UserMsgID
			b.statusMu.Unlock()
			cancel()
			continue
		}
		b.statusMu.Unlock()
		cancel()

		if pending != nil {
			go b.handleMessage(discordUserID, speakerName, contextID, channelID, pending.UserMsgID, pending.Content, pending.Attachments, pending.ImageBase64s)
		}

		if err != nil {
			log.Printf("Error handling message: %v", err)
			response = "my brain had a hiccup, try again in a sec"
		}
		response = strings.TrimSpace(response)
		if response == "" || response == "__SILENT__" {
			return
		}

		replyTo := ""
		if b.isGuildMode() {
			replyTo = userMsgID
		}
		firstMsgID := b.sendChunked(b.session, channelID, response, replyTo)
		b.interaction.SetUserState(contextID, channelID, userMsgID, firstMsgID)
		b.interaction.SetLastActive(contextID, time.Now())
		b.mu.Lock()
		if state, ok := b.userMsgs[discordUserID]; ok {
			state.BotMsgID = firstMsgID
			state.LastBotMsgAt = time.Now()
		}
		b.mu.Unlock()
		return
	}
}

func (b *Bot) onReactionAdd(s *discordgo.Session, r *discordgo.MessageReactionAdd) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("Reaction handler panic: %v", rec)
		}
	}()
	if r.UserID == s.State.User.ID {
		return
	}

	// Look up pending reset by either Discord userID or guild context ID
	resetKey := r.UserID
	if b.isGuildMode() {
		resetKey = b.guildContextID()
	}
	b.mu.RLock()
	pr, isPending := b.pendingResets[resetKey]
	b.mu.RUnlock()

	if isPending && pr.MsgID == r.MessageID {
		switch r.Emoji.Name {
		case "✅":
			if time.Now().After(pr.ExpiresAt) {
				_ = s.MessageReactionRemove(r.ChannelID, r.MessageID, "✅", r.UserID)
				_ = s.MessageReactionRemove(r.ChannelID, r.MessageID, "❌", r.UserID)
				return
			}
			log.Printf("[%s] confirmed reset", r.UserID)
			s.ChannelMessageSend(r.ChannelID, "gone. like you never existed.")
			b.resetUser(resetKey)
			return
		case "❌":
			log.Printf("[%s] cancelled reset", r.UserID)
			s.ChannelMessageSend(r.ChannelID, "keeping everything.")
			b.mu.Lock()
			delete(b.pendingResets, resetKey)
			b.mu.Unlock()
			return
		}
		return
	}

	msg, err := s.ChannelMessage(r.ChannelID, r.MessageID)
	if err != nil || msg.Author.ID != s.State.User.ID {
		return
	}

	if !b.isGuildMode() {
		b.mu.RLock()
		_, hasDM := b.dmChannels[r.UserID]
		b.mu.RUnlock()
		if !hasDM {
			return
		}
	}

	log.Printf("[%s] reacted %s to bot msg: %s", r.UserID, r.Emoji.Name, truncate(msg.Content, 100))

	// Check if reaction is in DM or guild channel
	reactionUserID := r.UserID
	if ch, err := s.Channel(r.ChannelID); err == nil && ch.Type != discordgo.ChannelTypeDM && b.isGuildMode() {
		reactionUserID = b.guildContextID()
	}

	done := make(chan struct{})
	go typingLoop(s, r.ChannelID, done)
	defer close(done)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	recent, _ := memory.GetRecent(b.config.DataDir, reactionUserID, 10)
	var contextLines []string
	for _, m := range recent {
		content := ""
		switch v := m.Content.(type) {
		case string:
			content = v
		default:
			continue
		}
		contextLines = append(contextLines, fmt.Sprintf("[%s]: %s", m.Role, truncate(content, 200)))
	}

	reactUsername := r.UserID
	if r.Member != nil {
		reactUsername = r.Member.User.Username
	}
	prompt := fmt.Sprintf(
		"User (%s) reacted with %s to your message: \"%s\"\n\nRecent context:\n%s\n\nReply naturally. Keep it short. Match the reaction energy.",
		reactUsername, r.Emoji.Name,
		msg.Content,
		strings.Join(contextLines, "\n"),
	)

	b.interaction.SetUserState(reactionUserID, r.ChannelID, r.MessageID, r.MessageID)
	reactionHC := agent.HandleContext{CtxID: reactionUserID, SpeakerID: r.UserID, SpeakerName: r.UserID}
	if r.Member != nil {
		reactionHC.SpeakerName = r.Member.User.Username
	}
	reply, err := b.interaction.Handle(ctx, reactionHC, prompt, nil, nil)

	if err != nil {
		log.Printf("Reaction reply error: %v", err)
		return
	}
	if reply == "" {
		return
	}

	b.sendChunked(s, r.ChannelID, reply)
	b.mu.Lock()
	if rs, ok := b.userMsgs[reactionUserID]; ok {
		rs.LastBotMsgAt = time.Now()
	}
	b.mu.Unlock()
}

func (b *Bot) sendChunked(s *discordgo.Session, channelID, text string, replyToID ...string) string {
	chunks := strings.Split(text, "\n\n")
	model := NewTypingModel(b.config.TypingWPMMin, b.config.TypingWPMMax)
	const maxChunk = 1900

	var firstID string
	isFirst := true
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		for len(chunk) > maxChunk {
			part := chunk[:maxChunk]
			if cut := strings.LastIndex(part, "\n"); cut > 0 {
				part = chunk[:cut]
			}
			if !isFirst {
				time.Sleep(model.NextDelay(part, false, false))
				_ = s.ChannelTyping(channelID)
			}
			part = strings.TrimSpace(part)
			var msg *discordgo.Message
			var err error
			if isFirst && len(replyToID) > 0 && replyToID[0] != "" {
				msg, err = s.ChannelMessageSendReply(channelID, part, &discordgo.MessageReference{MessageID: replyToID[0]})
			} else {
				msg, err = s.ChannelMessageSend(channelID, part)
			}
			if err != nil {
				log.Printf("Failed to send chunk: %v", err)
				return firstID
			}
			if firstID == "" {
				firstID = msg.ID
			}
			isFirst = false
			chunk = strings.TrimSpace(chunk[len(part):])
		}
		if !isFirst {
			time.Sleep(model.NextDelay(chunk, false, true))
			_ = s.ChannelTyping(channelID)
		}
		if len(chunk) > maxChunk {
			chunk = chunk[:maxChunk]
		}
		var msg *discordgo.Message
		var err error
		if isFirst && len(replyToID) > 0 && replyToID[0] != "" {
			msg, err = s.ChannelMessageSendReply(channelID, chunk, &discordgo.MessageReference{MessageID: replyToID[0]})
		} else {
			msg, err = s.ChannelMessageSend(channelID, chunk)
		}
		if err != nil {
			log.Printf("Failed to send chunk: %v", err)
			continue
		}
		if firstID == "" {
			firstID = msg.ID
		}
		isFirst = false
	}
	return firstID
}

func typingLoop(s *discordgo.Session, channelID string, done chan struct{}) {
	ticker := time.NewTicker(8 * time.Second)
	defer ticker.Stop()
	_ = s.ChannelTyping(channelID)
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			_ = s.ChannelTyping(channelID)
		}
	}
}

func fileToBase64(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func (b *Bot) handleResetInteraction(s *discordgo.Session, i *discordgo.InteractionCreate, userID, channelID string) {
	// In guild mode, reset affects the shared guild channel
	resetID := userID
	if b.isGuildMode() {
		resetID = b.guildContextID()
	}

	b.mu.Lock()
	pr := b.pendingResets[resetID]
	b.mu.Unlock()
	if pr != nil && time.Now().Before(pr.ExpiresAt) {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: strPtr("You already have a pending reset. React ✅ to confirm or ❌ to cancel."),
		})
		return
	}

	msg, err := s.ChannelMessageSend(channelID, "you sure? this will wipe the entire channel sandbox, memory, and agents. no undo.")
	if err != nil {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: strPtr("Failed to send confirmation. Try again."),
		})
		return
	}

	// In DM mode, verify it's a DM channel
	if !b.isGuildMode() {
		if ch, err := s.Channel(channelID); err == nil && ch.Type != discordgo.ChannelTypeDM {
			_ = s.ChannelMessageDelete(channelID, msg.ID)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: strPtr("reset confirmations only work in DMs. open a DM with Kite and try again."),
			})
			return
		}
	}

	s.MessageReactionAdd(channelID, msg.ID, "✅")
	s.MessageReactionAdd(channelID, msg.ID, "❌")

	b.mu.Lock()
	b.pendingResets[resetID] = &pendingReset{
		MsgID:     msg.ID,
		ChannelID: channelID,
		ExpiresAt: time.Now().Add(12 * time.Hour),
	}
	b.mu.Unlock()

	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: strPtr("Confirmation sent. React ✅ to confirm or ❌ to cancel."),
	})
	log.Printf("[%s] reset confirmation sent via slash command", userID)
}

func strPtr(s string) *string { return &s }

func (b *Bot) resetUser(userID string) {
	if b.isGuildMode() {
		guildID := b.guildContextID()
		log.Printf("[%s] resetting guild channel data...", guildID)
		_ = store.DeleteGuild(b.config.DataDir, b.config.GuildID, b.config.SitePublicPath)
		b.sandboxMgr.ResetUser(guildID)
		b.interaction.ResetUser(guildID)
		b.mu.Lock()
		delete(b.userChannels, guildID)
		delete(b.userMsgs, guildID)
		delete(b.pendingResets, guildID)
		b.mu.Unlock()
		b.saveChannels()
		log.Printf("[%s] guild channel data wiped", guildID)
		return
	}

	log.Printf("[%s] resetting user data...", userID)
	_ = store.DeleteUser(b.config.DataDir, userID, b.config.SitePublicPath)
	b.sandboxMgr.ResetUser(userID)
	b.interaction.ResetUser(userID)

	b.mu.Lock()
	delete(b.userChannels, userID)
	delete(b.dmChannels, userID)
	delete(b.userMsgs, userID)
	delete(b.pendingResets, userID)
	b.mu.Unlock()
	b.saveChannels()
	b.saveDMChannels()

	log.Printf("[%s] user data wiped", userID)
}

func (b *Bot) cleanExpiredResetsLoop() {
	for {
		time.Sleep(1 * time.Hour)
		b.mu.Lock()
		now := time.Now()
		for userID, pr := range b.pendingResets {
			if now.After(pr.ExpiresAt) {
				log.Printf("[%s] reset confirmation expired, removing", userID)
				delete(b.pendingResets, userID)
			}
		}
		b.mu.Unlock()
	}
}

func shouldHumanPause(msg string) bool {
	t := strings.TrimSpace(msg)
	if t == "" {
		return false
	}
	lower := strings.ToLower(t)
	if lower == "wait" || strings.HasPrefix(lower, "wait ") || strings.HasPrefix(lower, "wait,") {
		return true
	}
	if strings.HasSuffix(t, "...") || strings.HasSuffix(t, "…") {
		return true
	}
	return false
}
