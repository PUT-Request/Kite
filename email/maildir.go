package email

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Email struct {
	ID        string   `json:"id"`
	From      string   `json:"from"`
	To        []string `json:"to"`
	Subject   string   `json:"subject"`
	Body      string   `json:"body"`
	Date      string   `json:"date"`
	Read      bool     `json:"read"`
	ThreadID  string   `json:"thread_id,omitempty"`
	MessageID string   `json:"message_id"`
}

type InboxIndex struct {
	Emails []Email `json:"emails"`
}

type Maildir struct {
	dataDir string
	mu      sync.Mutex
}

func NewMaildir(dataDir string) *Maildir {
	return &Maildir{dataDir: dataDir}
}

func (m *Maildir) inboxDir(userID string) string {
	return filepath.Join(m.dataDir, userID, "inbox")
}

func (m *Maildir) inboxIndexPath(userID string) string {
	return filepath.Join(m.inboxDir(userID), "index.json")
}

func (m *Maildir) getIndex(userID string) (*InboxIndex, error) {
	path := m.inboxIndexPath(userID)
	var idx InboxIndex
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &InboxIndex{}, nil
		}
		return nil, err
	}
	json.Unmarshal(data, &idx)
	return &idx, nil
}

func (m *Maildir) saveIndex(userID string, idx *InboxIndex) error {
	dir := m.inboxDir(userID)
	os.MkdirAll(dir, 0755)
	data, _ := json.MarshalIndent(idx, "", "  ")
	return os.WriteFile(m.inboxIndexPath(userID), data, 0644)
}

func (m *Maildir) Add(userID string, eml *Email) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	dir := m.inboxDir(userID)
	os.MkdirAll(dir, 0755)

	eml.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	emlPath := filepath.Join(dir, eml.ID+".eml")
	content := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: %s\r\n\r\n%s",
		eml.From, strings.Join(eml.To, ", "), eml.Subject, eml.Date, eml.MessageID, eml.Body)
	os.WriteFile(emlPath, []byte(content), 0644)

	idx, _ := m.getIndex(userID)
	idx.Emails = append(idx.Emails, *eml)
	return m.saveIndex(userID, idx)
}

func (m *Maildir) Get(userID, id string) (*Email, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx, err := m.getIndex(userID)
	if err != nil {
		return nil, err
	}
	for i := range idx.Emails {
		if idx.Emails[i].ID == id {
			if !idx.Emails[i].Read {
				idx.Emails[i].Read = true
				m.saveIndex(userID, idx)
			}
			return &idx.Emails[i], nil
		}
	}
	return nil, fmt.Errorf("email not found")
}

func (m *Maildir) List(userID string, unreadOnly bool, limit int) ([]Email, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx, err := m.getIndex(userID)
	if err != nil {
		return nil, err
	}
	var results []Email
	for _, e := range idx.Emails {
		if unreadOnly && e.Read {
			continue
		}
		results = append(results, e)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].ID > results[j].ID })
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func (m *Maildir) Delete(userID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx, err := m.getIndex(userID)
	if err != nil {
		return err
	}
	for i, e := range idx.Emails {
		if e.ID == id {
			idx.Emails = append(idx.Emails[:i], idx.Emails[i+1:]...)
			os.Remove(filepath.Join(m.inboxDir(userID), id+".eml"))
			return m.saveIndex(userID, idx)
		}
	}
	return fmt.Errorf("email not found")
}

func (m *Maildir) Search(userID, query string) ([]Email, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx, err := m.getIndex(userID)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(query)
	var results []Email
	for _, e := range idx.Emails {
		if strings.Contains(strings.ToLower(e.Subject), lower) ||
			strings.Contains(strings.ToLower(e.From), lower) ||
			strings.Contains(strings.ToLower(e.Body), lower) {
			results = append(results, e)
		}
	}
	return results, nil
}

func (m *Maildir) CountUnread(userID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx, _ := m.getIndex(userID)
	count := 0
	for _, e := range idx.Emails {
		if !e.Read {
			count++
		}
	}
	return count
}

func (m *Maildir) ListUnreadSince(userID string, since time.Time) ([]Email, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx, err := m.getIndex(userID)
	if err != nil {
		return nil, err
	}
	var results []Email
	for _, e := range idx.Emails {
		if e.Read {
			continue
		}
		ed, parseErr := time.Parse(time.RFC3339, e.Date)
		if parseErr != nil {
			continue
		}
		if ed.After(since) || ed.Equal(since) {
			results = append(results, e)
		}
	}
	return results, nil
}
