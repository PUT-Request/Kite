package personality

import (
	"time"

	"kite/store"
)

type EnergyState struct {
	Level     float64   `json:"level"`
	UpdatedAt time.Time `json:"updated_at"`

	filePath string
}

func (e *EnergyState) Inject() string {
	desc := describeEnergy(e.Level)
	return "energy level: " + ftos(e.Level, 1) + "/1.0 — " + desc
}

func (e *EnergyState) EffectiveLevel() float64 {
	level := e.Level
	hour := time.Now().Hour()
	switch {
	case hour >= 2 && hour < 6:
		level *= 0.5
	case hour >= 23 || hour < 2:
		level *= 0.8
	}
	return clamp(level, 0, 1)
}

func (e *EnergyState) Apply(upd *StateUpdate) {
	if upd.Energy != nil {
		e.Level = clamp(*upd.Energy, 0, 1)
		e.UpdatedAt = time.Now()
	}
}

func (e *EnergyState) Tick(conversationIntensity int) {
	hours := time.Since(e.UpdatedAt).Hours()
	if hours > 1 {
		recovery := hours * 0.1
		e.Level = clamp(e.Level+recovery, 0, 1)
	}
	if conversationIntensity > 10 && hours < 0.5 {
		drain := 0.05 * float64(conversationIntensity/10)
		e.Level = clamp(e.Level-drain, 0, 1)
	}
}

func (e *EnergyState) TypingWPM(baseMin, baseMax int) (int, int) {
	eff := e.EffectiveLevel()
	factor := 0.5 + eff*0.5
	return int(float64(baseMin) * factor), int(float64(baseMax) * factor)
}

func LoadEnergy(dataDir, ctxID, speakerID string) *EnergyState {
	s := &EnergyState{Level: 0.6, UpdatedAt: time.Now()}
	if ctxID == speakerID {
		s.filePath = store.UserDir(dataDir, ctxID) + "/personality/energy.json"
	} else {
		s.filePath = store.GuildDir(dataDir, ctxID) + "/personality/energy.json"
	}
	store.ReadJSON(s.filePath, s)
	return s
}

func SaveEnergy(s *EnergyState) error {
	return store.WriteJSON(s.filePath, s)
}

func ftos(f float64, prec int) string {
	if prec == 0 {
		return itoa(int(f))
	}
	mul := 1
	for i := 0; i < prec; i++ {
		mul *= 10
	}
	n := int(f * float64(mul))
	intPart := n / mul
	fracPart := n % mul
	if fracPart < 0 {
		fracPart = -fracPart
	}
	s := itoa(intPart) + "."
	digits := itoa(fracPart)
	for len(digits) < prec {
		digits = "0" + digits
	}
	return s + digits
}
