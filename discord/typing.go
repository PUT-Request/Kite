package discord

import (
	"math"
	"math/rand"
	"time"
)

const (
	defaultWPMMin = 40
	defaultWPMMax = 50
)

type TypingModel struct {
	wpm    float64
	remain float64
}

func NewTypingModel(wpmMin, wpmMax int) *TypingModel {
	if wpmMin <= 0 || wpmMax <= 0 {
		wpmMin = defaultWPMMin
		wpmMax = defaultWPMMax
	}
	if wpmMax < wpmMin {
		wpmMax = wpmMin
	}
	wpm := float64(wpmMin) + rand.Float64()*float64(wpmMax-wpmMin)
	return &TypingModel{wpm: wpm}
}

func (m *TypingModel) NextDelay(chunk string, isFirst, isParaBreak bool) time.Duration {
	if len(chunk) == 0 {
		return 0
	}

	charsPerSec := m.wpm * 5.0 / 60.0
	rawSec := float64(len(chunk)) / charsPerSec

	jitter := 0.8 + rand.Float64()*0.4
	sec := rawSec * jitter

	if isParaBreak {
		sec += 0.4 + rand.Float64()*0.4
	}

	ms := sec * 1000
	if ms < 50 {
		ms = 50
	}
	if ms > 5000 {
		ms = 5000
	}

	return time.Duration(math.Round(ms)) * time.Millisecond
}
