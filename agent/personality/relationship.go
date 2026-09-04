package personality

import (
	"time"

	"kite/store"
)

type RelationshipState struct {
	Familiarity      int      `json:"familiarity"`
	BanterLevel      int      `json:"banter_level"`
	InteractionCount int      `json:"interaction_count"`
	FirstSeen        string   `json:"first_seen"`
	LastAt           string   `json:"last_at"`
	InsideJokes      []string `json:"inside_jokes"`
	HypeCount        int      `json:"hype_count"`
	RoastCount       int      `json:"roast_count"`

	filePath string
}

func (r *RelationshipState) Inject(username string) string {
	stage := relationshipStage(r.Familiarity)
	return "relationship with " + username + ": " + stage + " (familiarity " + itoa(r.Familiarity) + "/100, banter " + itoa(r.BanterLevel) + "/100)"
}

func (r *RelationshipState) Apply(upd *StateUpdate) {
	if upd.Familiarity != nil {
		r.Familiarity = clampInt(*upd.Familiarity, 0, 100)
	}
	if upd.Banter != nil {
		r.BanterLevel = clampInt(*upd.Banter, 0, 100)
	}
	if upd.InsideJoke != nil {
		addInsideJoke(r, *upd.InsideJoke)
	}
	r.LastAt = time.Now().Format(time.RFC3339)
	if r.FirstSeen == "" {
		r.FirstSeen = r.LastAt
	}
}

func (r *RelationshipState) RecordInteraction() {
	r.InteractionCount++
}

func (r *RelationshipState) Tick() {
	hours := idleHours(r.LastAt)
	if hours > 24 {
		decay := int(hours / 24)
		r.Familiarity = clampInt(r.Familiarity-decay, 0, 100)
	}
}

func addInsideJoke(r *RelationshipState, joke string) {
	for _, j := range r.InsideJokes {
		if j == joke {
			return
		}
	}
	r.InsideJokes = append(r.InsideJokes, joke)
	if len(r.InsideJokes) > 10 {
		r.InsideJokes = r.InsideJokes[len(r.InsideJokes)-10:]
	}
}

func relationshipStage(familiarity int) string {
	switch {
	case familiarity < 5:
		return "barely know em"
	case familiarity < 20:
		return "getting familiar"
	case familiarity < 50:
		return "comfortable"
	case familiarity < 80:
		return "close, can banter"
	default:
		return "ride or die, zero filter"
	}
}

func idleHours(lastAt string) float64 {
	t, err := time.Parse(time.RFC3339, lastAt)
	if err != nil {
		return 0
	}
	return time.Since(t).Hours()
}

func LoadRelationship(dataDir, ctxID, speakerID string) *RelationshipState {
	r := &RelationshipState{}
	if ctxID == speakerID {
		r.filePath = store.UserDir(dataDir, ctxID) + "/personality/relationship.json"
	} else {
		r.filePath = store.GuildDir(dataDir, ctxID) + "/personality/relationships/" + speakerID + ".json"
	}
	store.ReadJSON(r.filePath, r)
	return r
}

func SaveRelationship(r *RelationshipState) error {
	return store.WriteJSON(r.filePath, r)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
