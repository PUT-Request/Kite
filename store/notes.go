package store

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Note struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Content   string   `json:"content"`
	Tags      []string `json:"tags"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

type NoteStore struct {
	Notes []Note `json:"notes"`
}

var notesMu sync.Mutex

func notesPath(dataDir, userID string) string {
	return filepath.Join(dataDir, userID, "notes.json")
}

func LoadNotes(dataDir, userID string) ([]Note, error) {
	notesMu.Lock()
	defer notesMu.Unlock()
	return loadNotesUnsafe(dataDir, userID)
}

func loadNotesUnsafe(dataDir, userID string) ([]Note, error) {
	path := notesPath(dataDir, userID)
	var ns NoteStore
	if err := ReadJSON(path, &ns); err != nil {
		return nil, nil
	}
	return ns.Notes, nil
}

func saveNotesUnsafe(dataDir, userID string, notes []Note) error {
	path := notesPath(dataDir, userID)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return WriteJSON(path, NoteStore{Notes: notes})
}

func SaveNote(dataDir, userID, title, content string, tags []string) (*Note, error) {
	notesMu.Lock()
	defer notesMu.Unlock()

	notes, err := loadNotesUnsafe(dataDir, userID)
	if err != nil {
		return nil, err
	}
	if notes == nil {
		notes = []Note{}
	}

	now := time.Now().UTC().Format(time.RFC3339)

	for i, n := range notes {
		if n.Title == title {
			notes[i].Content = content
			notes[i].Tags = tags
			notes[i].UpdatedAt = now
			if err := saveNotesUnsafe(dataDir, userID, notes); err != nil {
				return nil, err
			}
			return &notes[i], nil
		}
	}

	note := Note{
		ID:        uuid.New().String()[:8],
		Title:     title,
		Content:   content,
		Tags:      tags,
		CreatedAt: now,
		UpdatedAt: now,
	}
	notes = append(notes, note)
	if err := saveNotesUnsafe(dataDir, userID, notes); err != nil {
		return nil, err
	}
	return &note, nil
}

func GetNote(dataDir, userID, id string) (*Note, error) {
	notesMu.Lock()
	defer notesMu.Unlock()

	notes, err := loadNotesUnsafe(dataDir, userID)
	if err != nil {
		return nil, err
	}
	for _, n := range notes {
		if n.ID == id || n.Title == id {
			return &n, nil
		}
	}
	return nil, nil
}

func SearchNotes(dataDir, userID, query string) ([]Note, error) {
	notesMu.Lock()
	defer notesMu.Unlock()

	notes, err := loadNotesUnsafe(dataDir, userID)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	var results []Note
	for _, n := range notes {
		if strings.Contains(strings.ToLower(n.Title), q) ||
			strings.Contains(strings.ToLower(n.Content), q) {
			results = append(results, n)
			continue
		}
		for _, tag := range n.Tags {
			if strings.Contains(strings.ToLower(tag), q) {
				results = append(results, n)
				break
			}
		}
	}
	return results, nil
}

func ListNotes(dataDir, userID string) ([]Note, error) {
	notesMu.Lock()
	defer notesMu.Unlock()
	return loadNotesUnsafe(dataDir, userID)
}

func DeleteNote(dataDir, userID, id string) error {
	notesMu.Lock()
	defer notesMu.Unlock()

	notes, err := loadNotesUnsafe(dataDir, userID)
	if err != nil {
		return err
	}
	var kept []Note
	for _, n := range notes {
		if n.ID != id && n.Title != id {
			kept = append(kept, n)
		}
	}
	return saveNotesUnsafe(dataDir, userID, kept)
}
