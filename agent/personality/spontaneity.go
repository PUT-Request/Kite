package personality

import "math/rand"

const defaultSpontaneityPercent = 15

func SpontaneityInject(sysPrompt string, targetPercent int) string {
	if targetPercent <= 0 {
		targetPercent = defaultSpontaneityPercent
	}
	if rand.Intn(100) < targetPercent {
		return sysPrompt + "\n\nthis message: include one spontaneous reaction mid-response. 'actually wait', 'hmm', 'hold on', reconsider something you just said. make it natural, one max."
	}
	return sysPrompt + "\n\nthis message: respond directly. no hesitations, no mid-thought corrections."
}
