package memory

import (
	"encoding/json"
	"os"
	"path/filepath"

	"kite/llm"
	"kite/store"
)

func AppendMessage(dataDir, userID string, msg llm.Message) error {
	return store.AppendJSONL(filepath.Join(store.UserDir(dataDir, userID), "brain.jsonl"), msg)
}

func GetRecent(dataDir, userID string, n int) ([]llm.Message, error) {
	rawMessages, err := store.ReadJSONL(filepath.Join(store.UserDir(dataDir, userID), "brain.jsonl"))
	if err != nil {
		return nil, err
	}
	var msgs []llm.Message
	for _, raw := range rawMessages {
		var m llm.Message
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		msgs = append(msgs, m)
	}
	if n > 0 && len(msgs) > n {
		msgs = msgs[len(msgs)-n:]
	}
	return msgs, nil
}

func CountMessages(dataDir, userID string) (int, error) {
	rawMessages, err := store.ReadJSONL(filepath.Join(store.UserDir(dataDir, userID), "brain.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	count := 0
	for _, line := range rawMessages {
		if len(line) > 0 {
			count++
		}
	}
	return count, nil
}
