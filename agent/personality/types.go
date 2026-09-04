package personality

import (
	"regexp"
	"strconv"
	"strings"
)

var stateTagRe = regexp.MustCompile(`\{__state__\s+([^}]+)\}`)

type StateUpdate struct {
	EmotionLabel    *string
	EmotionValence  *float64
	EmotionArousal  *float64
	Familiarity     *int
	Banter          *int
	InsideJoke      *string
	Energy          *float64
	Drives          []string
	DriveDoneIDs    []string
	DriveClear      bool
}

type PersonaState struct {
	Emotion      EmotionState
	Relationship RelationshipState
	Energy       EnergyState
	Drives       DriveState
}

func ParseStateTag(content string) (update *StateUpdate, clean string) {
	var updates []*StateUpdate
	clean = stateTagRe.ReplaceAllStringFunc(content, func(match string) string {
		parts := stateTagRe.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}
		updates = append(updates, parseTagBody(parts[1]))
		return ""
	})
	clean = strings.TrimSpace(clean)
	if len(updates) > 0 {
		merged := &StateUpdate{}
		for _, u := range updates {
			if u.EmotionLabel != nil {
				merged.EmotionLabel = u.EmotionLabel
			}
			if u.EmotionValence != nil {
				merged.EmotionValence = u.EmotionValence
			}
			if u.EmotionArousal != nil {
				merged.EmotionArousal = u.EmotionArousal
			}
			if u.Familiarity != nil {
				merged.Familiarity = u.Familiarity
			}
			if u.Banter != nil {
				merged.Banter = u.Banter
			}
			if u.InsideJoke != nil {
				merged.InsideJoke = u.InsideJoke
			}
			if u.Energy != nil {
				merged.Energy = u.Energy
			}
			if u.DriveClear {
				merged.DriveClear = true
			}
			merged.Drives = append(merged.Drives, u.Drives...)
			merged.DriveDoneIDs = append(merged.DriveDoneIDs, u.DriveDoneIDs...)
		}
		update = merged
	}
	return
}

func parseTagBody(body string) *StateUpdate {
	u := &StateUpdate{}
	tokens := tokenizeTag(body)
	for _, tok := range tokens {
		colon := strings.IndexByte(tok, ':')
		if colon < 0 {
			continue
		}
		key := tok[:colon]
		val := tok[colon+1:]

		switch key {
		case "emotion":
			v := strings.ToLower(strings.TrimSpace(val))
			u.EmotionLabel = &v
		case "valence":
			f, err := strconv.ParseFloat(val, 64)
			if err == nil {
				u.EmotionValence = &f
			}
		case "arousal":
			f, err := strconv.ParseFloat(val, 64)
			if err == nil {
				u.EmotionArousal = &f
			}
		case "familiarity":
			n, err := strconv.Atoi(val)
			if err == nil {
				u.Familiarity = &n
			}
		case "banter":
			n, err := strconv.Atoi(val)
			if err == nil {
				u.Banter = &n
			}
		case "inside_joke":
			u.InsideJoke = &val
		case "energy":
			f, err := strconv.ParseFloat(val, 64)
			if err == nil {
				u.Energy = &f
			}
		case "drive":
			u.Drives = append(u.Drives, val)
		case "drive_done":
			u.DriveDoneIDs = append(u.DriveDoneIDs, val)
		case "drive_clear":
			u.DriveClear = true
		}
	}
	return u
}

func tokenizeTag(body string) []string {
	var tokens []string
	var cur strings.Builder
	inQuote := false
	for _, r := range body {
		switch {
		case r == '"':
			inQuote = !inQuote
			if !inQuote {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
		case r == ' ' && !inQuote:
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
