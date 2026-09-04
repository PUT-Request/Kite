package personality

import (
	"math"
	"math/rand"
	"strings"
	"time"

	"kite/store"
)

var validEmotionLabels = map[string]bool{
	"neutral": true, "happy": true, "hyped": true, "amused": true,
	"playful": true, "excited": true, "chill": true, "relaxed": true,
	"tired": true, "sleepy": true, "annoyed": true, "angry": true,
	"frustrated": true, "sad": true, "melancholic": true, "sarcastic": true,
	"mischievous": true, "grateful": true, "sympathetic": true,
	"curious": true, "confused": true, "smug": true,
}

type EmotionState struct {
	Label           string    `json:"label"`
	Valence         float64   `json:"valence"`
	Arousal         float64   `json:"arousal"`
	UpdatedAt       time.Time `json:"updated_at"`
	BaselineLabel   string    `json:"baseline_label"`
	BaselineValence float64   `json:"baseline_valence"`
	BaselineArousal float64   `json:"baseline_arousal"`

	filePath string
}

func (e *EmotionState) Inject() string {
	desc := describeEnergy(e.Arousal)
	return "rn you feel " + e.Label + " (" + desc + ")"
}

func (e *EmotionState) Apply(upd *StateUpdate) {
	changed := false
	if upd.EmotionLabel != nil {
		l := strings.ToLower(strings.TrimSpace(*upd.EmotionLabel))
		if validEmotionLabels[l] {
			e.Label = l
			changed = true
		}
	}
	if upd.EmotionValence != nil {
		e.Valence = clamp(*upd.EmotionValence, -1.0, 1.0)
		changed = true
	}
	if upd.EmotionArousal != nil {
		e.Arousal = clamp(*upd.EmotionArousal, 0.0, 1.0)
		changed = true
	}
	if changed {
		e.UpdatedAt = time.Now()
	}
}

func (e *EmotionState) Tick() {
	hours := time.Since(e.UpdatedAt).Hours()
	if hours < 1 {
		return
	}
	decay := math.Min(hours*0.1, 0.8)
	e.Valence += (e.BaselineValence - e.Valence) * decay
	e.Arousal += (e.BaselineArousal - e.Arousal) * decay * 0.5
	if math.Abs(e.Valence) < 0.01 {
		e.Valence = 0
	}
	if e.Arousal < 0.01 {
		e.Arousal = 0
	}
	e.Label = e.BaselineLabel
	if rand.Float64() < 0.001 {
		shiftBaseline(e)
	}
}

func shiftBaseline(e *EmotionState) {
	options := []string{"neutral", "chill", "tired", "curious", "playful", "sarcastic"}
	e.BaselineLabel = options[rand.Intn(len(options))]
	baseMoods := map[string]struct{ v, a float64 }{
		"neutral":   {0, 0.4},
		"chill":     {0.2, 0.2},
		"tired":     {-0.1, 0.15},
		"curious":   {0.3, 0.5},
		"playful":   {0.5, 0.6},
		"sarcastic": {-0.1, 0.5},
	}
	if m, ok := baseMoods[e.BaselineLabel]; ok {
		e.BaselineValence = m.v
		e.BaselineArousal = m.a
	}
}

func describeEnergy(arousal float64) string {
	switch {
	case arousal < 0.2:
		return "low energy, kinda blah"
	case arousal < 0.4:
		return "mellow"
	case arousal < 0.6:
		return "alright"
	case arousal < 0.8:
		return "good, feeling it"
	default:
		return "locked the fuck in"
	}
}

func LoadEmotion(dataDir, ctxID, speakerID string) *EmotionState {
	s := &EmotionState{
		Label:           "neutral",
		Valence:         0,
		Arousal:         0.4,
		UpdatedAt:       time.Now(),
		BaselineLabel:   "neutral",
		BaselineValence: 0,
		BaselineArousal: 0.4,
	}
	path := emotionPath(dataDir, ctxID, speakerID)
	s.filePath = path
	store.ReadJSON(path, s)
	s.filePath = path
	return s
}

func SaveEmotion(s *EmotionState) error {
	return store.WriteJSON(s.filePath, s)
}

func emotionPath(dataDir, ctxID, speakerID string) string {
	if ctxID == speakerID {
		return store.UserDir(dataDir, ctxID) + "/personality/emotion.json"
	}
	return store.GuildDir(dataDir, ctxID) + "/personality/emotion.json"
}
