package tool

import (
	"context"
	"fmt"
	"strings"

	"kite/store"
)

type NoteSave struct {
	DataDir string
}

func (t *NoteSave) Name() string { return "note_save" }
func (t *NoteSave) Description() string { return "Save a persistent note by title and content. If a note with the same title exists, it is updated. Use for passwords, configs, links, preferences, API keys, or any info that should persist beyond conversation compression." }
func (t *NoteSave) Parameters() map[string]interface{} {
	return ObjectParams(map[string]interface{}{
		"title":   StrParam("Note title (used as unique identifier)."),
		"content": StrParam("Note content — the actual information to save."),
		"tags":    StrParam("Optional comma-separated tags for organization."),
	}, []string{"title", "content"})
}

func (t *NoteSave) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	title := ExtractArgString(args, "title")
	content := ExtractArgString(args, "content")
	tagsStr := ExtractArgString(args, "tags")
	if title == "" || content == "" {
		return "Error: title and content are required", nil
	}
	var tags []string
	if tagsStr != "" {
		for _, tag := range strings.Split(tagsStr, ",") {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				tags = append(tags, tag)
			}
		}
	}
	note, err := store.SaveNote(t.DataDir, userID, title, content, tags)
	if err != nil {
		return fmt.Sprintf("Error saving note: %v", err), nil
	}
	return fmt.Sprintf("Saved note '%s' (id: %s) with %d tag(s).", note.Title, note.ID, len(note.Tags)), nil
}

type NoteGet struct {
	DataDir string
}

func (t *NoteGet) Name() string { return "note_get" }
func (t *NoteGet) Description() string { return "Get a note by ID or title." }
func (t *NoteGet) Parameters() map[string]interface{} {
	return ObjectParams(map[string]interface{}{
		"id": StrParam("Note ID or title to look up."),
	}, []string{"id"})
}

func (t *NoteGet) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	id := ExtractArgString(args, "id")
	if id == "" {
		return "Error: id is required", nil
	}
	note, err := store.GetNote(t.DataDir, userID, id)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	if note == nil {
		return fmt.Sprintf("Note '%s' not found.", id), nil
	}
	return fmt.Sprintf("Title: %s\nTags: %s\nCreated: %s\nUpdated: %s\n\n%s",
		note.Title, strings.Join(note.Tags, ", "), note.CreatedAt, note.UpdatedAt, note.Content), nil
}

type NoteSearch struct {
	DataDir string
}

func (t *NoteSearch) Name() string { return "note_search" }
func (t *NoteSearch) Description() string { return "Search notes by title, content, or tag. Returns matching note titles and IDs." }
func (t *NoteSearch) Parameters() map[string]interface{} {
	return ObjectParams(map[string]interface{}{
		"query": StrParam("Search query — matches against title, content, and tags."),
	}, []string{"query"})
}

func (t *NoteSearch) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	query := ExtractArgString(args, "query")
	if query == "" {
		return "Error: query is required", nil
	}
	results, err := store.SearchNotes(t.DataDir, userID, query)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	if len(results) == 0 {
		return fmt.Sprintf("No notes matching '%s'.", query), nil
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d note(s) matching '%s':\n\n", len(results), query))
	for _, n := range results {
		b.WriteString(fmt.Sprintf("  - %s (id: %s)\n    tags: %s\n", n.Title, n.ID, strings.Join(n.Tags, ", ")))
	}
	return b.String(), nil
}

type NoteList struct {
	DataDir string
}

func (t *NoteList) Name() string { return "note_list" }
func (t *NoteList) Description() string { return "List all saved notes with titles and tags." }
func (t *NoteList) Parameters() map[string]interface{} {
	return ObjectParams(map[string]interface{}{}, nil)
}

func (t *NoteList) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	notes, err := store.ListNotes(t.DataDir, userID)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	if len(notes) == 0 {
		return "No notes saved.", nil
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d note(s):\n\n", len(notes)))
	for _, n := range notes {
		preview := n.Content
		if len(preview) > 80 {
			preview = preview[:80] + "..."
		}
		b.WriteString(fmt.Sprintf("  %s (id: %s)\n", n.Title, n.ID))
		if len(n.Tags) > 0 {
			b.WriteString(fmt.Sprintf("    tags: %s\n", strings.Join(n.Tags, ", ")))
		}
		b.WriteString(fmt.Sprintf("    %s\n\n", preview))
	}
	return b.String(), nil
}

type NoteDelete struct {
	DataDir string
}

func (t *NoteDelete) Name() string { return "note_delete" }
func (t *NoteDelete) Description() string { return "Delete a note by ID or title." }
func (t *NoteDelete) Parameters() map[string]interface{} {
	return ObjectParams(map[string]interface{}{
		"id": StrParam("ID or title of the note to delete."),
	}, []string{"id"})
}

func (t *NoteDelete) Execute(ctx context.Context, userID string, args map[string]interface{}) (string, error) {
	id := ExtractArgString(args, "id")
	if id == "" {
		return "Error: id is required", nil
	}
	if err := store.DeleteNote(t.DataDir, userID, id); err != nil {
		return fmt.Sprintf("Error deleting note: %v", err), nil
	}
	return fmt.Sprintf("Deleted note '%s'.", id), nil
}
