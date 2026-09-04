package personality

import (
	"fmt"
	"time"

	"kite/store"
)

type Drive struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
	Status    string `json:"status"`
}

type DriveState struct {
	Drives           []Drive `json:"drives"`
	LastInitiativeAt string  `json:"last_initiative_at"`
	NextDriveID      int     `json:"next_drive_id"`

	filePath string
}

func (d *DriveState) Inject() string {
	if len(d.Drives) == 0 {
		return ""
	}
	var pending []Drive
	for _, dr := range d.Drives {
		if dr.Status == "pending" {
			pending = append(pending, dr)
		}
	}
	if len(pending) == 0 {
		return ""
	}
	s := "\npending drives:"
	for _, dr := range pending {
		s += "\n- " + dr.ID + ": " + dr.Text
	}
	s += "\nyou can fulfill a pending drive if it fits the conversation."
	return s
}

func (d *DriveState) CanTakeInitiative() bool {
	if d.LastInitiativeAt == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, d.LastInitiativeAt)
	if err != nil {
		return true
	}
	return time.Since(t).Hours() >= 1
}

func (d *DriveState) Apply(upd *StateUpdate) {
	if upd.DriveClear {
		var active []Drive
		for _, dr := range d.Drives {
			if dr.Status != "fulfilled" {
				active = append(active, dr)
			}
		}
		d.Drives = active
	}
	for _, id := range upd.DriveDoneIDs {
		for i := range d.Drives {
			if d.Drives[i].ID == id {
				d.Drives[i].Status = "fulfilled"
			}
		}
	}
	for _, text := range upd.Drives {
		d.NextDriveID++
		if d.NextDriveID <= 0 {
			d.NextDriveID = 1
		}
		id := fmt.Sprintf("d%d", d.NextDriveID)
		d.Drives = append(d.Drives, Drive{
			ID:        id,
			Text:      text,
			CreatedAt: time.Now().Format(time.RFC3339),
			Status:    "pending",
		})
	}
}

func (d *DriveState) Cleanup() {
	var kept []Drive
	for _, dr := range d.Drives {
		if dr.Status == "fulfilled" {
			t, err := time.Parse(time.RFC3339, dr.CreatedAt)
			if err == nil && time.Since(t).Hours() < 48 {
				kept = append(kept, dr)
			}
		} else {
			kept = append(kept, dr)
		}
	}
	d.Drives = kept
}

func LoadDrives(dataDir, ctxID, speakerID string) *DriveState {
	d := &DriveState{}
	if ctxID == speakerID {
		d.filePath = store.UserDir(dataDir, ctxID) + "/personality/drives.json"
	} else {
		d.filePath = store.GuildDir(dataDir, ctxID) + "/personality/drives/" + speakerID + ".json"
	}
	store.ReadJSON(d.filePath, d)
	return d
}

func SaveDrives(d *DriveState) error {
	return store.WriteJSON(d.filePath, d)
}
