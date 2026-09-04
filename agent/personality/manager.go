package personality

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"kite/store"
)

type PersonalityManager struct {
	mu sync.Mutex
}

func New() *PersonalityManager {
	return &PersonalityManager{}
}

type PersonalityContext struct {
	CtxID       string
	SpeakerID   string
	SpeakerName string
}

func (pm *PersonalityManager) LoadState(ctx PersonalityContext, dataDir string) *PersonaState {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	ensureDirs(dataDir, ctx)

	state := &PersonaState{
		Emotion:      *LoadEmotion(dataDir, ctx.CtxID, ctx.SpeakerID),
		Relationship: *LoadRelationship(dataDir, ctx.CtxID, ctx.SpeakerID),
		Energy:       *LoadEnergy(dataDir, ctx.CtxID, ctx.SpeakerID),
		Drives:       *LoadDrives(dataDir, ctx.CtxID, ctx.SpeakerID),
	}

	// Apply time-based decay once per Handle (not per loop iteration)
	state.Emotion.Tick()
	state.Relationship.Tick()
	state.Energy.Tick(0)
	state.Drives.Cleanup()

	SaveEmotion(&state.Emotion)
	SaveRelationship(&state.Relationship)
	SaveEnergy(&state.Energy)
	SaveDrives(&state.Drives)

	return state
}

func (pm *PersonalityManager) InjectIntoPrompt(sysPrompt string, state *PersonaState, ctx PersonalityContext, guildMembers string) string {
	sysPrompt += "\n\n" + state.Emotion.Inject()
	sysPrompt += "\n" + state.Energy.Inject()
	sysPrompt += "\n" + state.Relationship.Inject(ctx.SpeakerName)
	if guildMembers != "" {
		sysPrompt += "\n" + guildMembers
	}
	if drive := state.Drives.Inject(); drive != "" {
		sysPrompt += drive
	}
	return sysPrompt
}

func (pm *PersonalityManager) ProcessResponse(state *PersonaState, response string, ctx PersonalityContext, dataDir string) string {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	upd, clean := ParseStateTag(response)

	if upd != nil {
		state.Emotion.Apply(upd)
		state.Relationship.Apply(upd)
		state.Energy.Apply(upd)
		state.Drives.Apply(upd)
	}

	SaveEmotion(&state.Emotion)
	SaveRelationship(&state.Relationship)
	SaveEnergy(&state.Energy)
	SaveDrives(&state.Drives)

	if state.Drives.CanTakeInitiative() && len(state.Drives.Drives) > 0 {
		for _, d := range state.Drives.Drives {
			if d.Status == "pending" {
				drivesCopy := state.Drives
				drivesCopy.LastInitiativeAt = time.Now().Format(time.RFC3339)
				SaveDrives(&drivesCopy)
				break
			}
		}
	}

	return clean
}

func (pm *PersonalityManager) ApplyEnergyDrain(state *PersonaState, conversationIntensity int) {
	if conversationIntensity <= 0 {
		return
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	drain := 0.05 * float64(conversationIntensity/10)
	state.Energy.Level = clamp(state.Energy.Level-drain, 0, 1)
	SaveEnergy(&state.Energy)
}

func (pm *PersonalityManager) EffectiveWPM(state *PersonaState, baseMin, baseMax int) (int, int) {
	return state.Energy.TypingWPM(baseMin, baseMax)
}

func (pm *PersonalityManager) ResetForUser(dataDir, ctxID, speakerID string) {
	if ctxID == speakerID {
		os.RemoveAll(filepath.Join(store.UserDir(dataDir, ctxID), "personality"))
	} else {
		os.RemoveAll(filepath.Join(store.GuildDir(dataDir, ctxID), "personality", "relationships", speakerID))
		os.RemoveAll(filepath.Join(store.GuildDir(dataDir, ctxID), "personality", "drives", speakerID))
	}
}

func ensureDirs(dataDir string, ctx PersonalityContext) {
	if ctx.CtxID == ctx.SpeakerID {
		os.MkdirAll(store.UserDir(dataDir, ctx.CtxID)+"/personality/", 0755)
	} else {
		guildDir := store.GuildDir(dataDir, ctx.CtxID)
		os.MkdirAll(guildDir+"/personality/", 0755)
		os.MkdirAll(guildDir+"/personality/relationships/", 0755)
		os.MkdirAll(guildDir+"/personality/drives/", 0755)
	}
}
