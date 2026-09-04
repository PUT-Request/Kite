package trigger

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"kite/store"
)

type Trigger struct {
	ID        string `json:"id"`
	AgentID   string `json:"agent_id"`
	Schedule  string `json:"schedule"`
	NextFire  string `json:"next_fire"`
	LastFire  string `json:"last_fire"`
	Prompt    string `json:"prompt"`
	OneShot   bool   `json:"one_shot"`
	CreatedAt string `json:"created_at"`
}

type TriggerStore struct {
	Triggers []Trigger `json:"triggers"`
}

type Scheduler struct {
	dataDir   string
	interval  time.Duration
	OnFire    func(userID string, trigger Trigger)
	stop      chan struct{}
	mu        sync.Mutex
	cronParser cron.ScheduleParser
}

func NewScheduler(dataDir string, interval time.Duration) *Scheduler {
	return &Scheduler{
		dataDir:    dataDir,
		interval:   interval,
		stop:       make(chan struct{}),
		cronParser: cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
	}
}

func (s *Scheduler) Start() {
	go s.loop()
}

func (s *Scheduler) Stop() {
	close(s.stop)
}

func (s *Scheduler) loop() {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.checkAll()
		}
	}
}

func (s *Scheduler) checkAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	dirs, err := filepath.Glob(filepath.Join(s.dataDir, "*"))
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, dir := range dirs {
		userID := filepath.Base(dir)
		triggers, err := s.loadTriggers(userID)
		if err != nil {
			continue
		}
		changed := false
		for i := len(triggers.Triggers) - 1; i >= 0; i-- {
			t := &triggers.Triggers[i]
			nextFire, parseErr := time.Parse(time.RFC3339, t.NextFire)
			if parseErr != nil {
				continue
			}
			if !now.Before(nextFire) {
				if s.OnFire != nil {
					go s.OnFire(userID, *t)
				}
				t.LastFire = now.Format(time.RFC3339)
				if t.OneShot {
					triggers.Triggers = append(triggers.Triggers[:i], triggers.Triggers[i+1:]...)
				} else {
					schedule, parseErr := s.cronParser.Parse(t.Schedule)
					if parseErr == nil {
						t.NextFire = schedule.Next(now).Format(time.RFC3339)
					} else {
						t.NextFire = now.Add(1 * time.Hour).Format(time.RFC3339)
					}
				}
				changed = true
			}
		}
		if changed {
			_ = s.saveTriggers(userID, triggers)
		}
	}
}

func (s *Scheduler) AddTrigger(userID, agentID, schedule, prompt string, oneShot bool) (*Trigger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sched, err := s.cronParser.Parse(schedule)
	if err != nil {
		return nil, fmt.Errorf("invalid cron schedule '%s': %w", schedule, err)
	}

	now := time.Now().UTC()
	t := Trigger{
		ID:        uuid.New().String()[:8],
		AgentID:   agentID,
		Schedule:  schedule,
		NextFire:  sched.Next(now).Format(time.RFC3339),
		Prompt:    prompt,
		OneShot:   oneShot,
		CreatedAt: now.Format(time.RFC3339),
	}

	triggers, _ := s.loadTriggers(userID)
	triggers.Triggers = append(triggers.Triggers, t)
	if err := s.saveTriggers(userID, triggers); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Scheduler) ListTriggers(userID string) ([]Trigger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	triggers, err := s.loadTriggers(userID)
	if err != nil {
		return nil, err
	}
	return triggers.Triggers, nil
}

func (s *Scheduler) DeleteTrigger(userID, triggerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	triggers, err := s.loadTriggers(userID)
	if err != nil {
		return err
	}
	for i, t := range triggers.Triggers {
		if t.ID == triggerID {
			triggers.Triggers = append(triggers.Triggers[:i], triggers.Triggers[i+1:]...)
			return s.saveTriggers(userID, triggers)
		}
	}
	return fmt.Errorf("trigger '%s' not found", triggerID)
}

func (s *Scheduler) triggersPath(userID string) string {
	return filepath.Join(store.UserDir(s.dataDir, userID), "triggers.json")
}

func (s *Scheduler) loadTriggers(userID string) (*TriggerStore, error) {
	var ts TriggerStore
	if err := store.ReadJSON(s.triggersPath(userID), &ts); err != nil {
		return &TriggerStore{}, nil
	}
	return &ts, nil
}

func (s *Scheduler) saveTriggers(userID string, ts *TriggerStore) error {
	return store.WriteJSON(s.triggersPath(userID), ts)
}
