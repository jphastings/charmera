package daemon

import "fmt"

// Status is the daemon's current activity, independent of the pause setting.
type Status string

const (
	// StatusIdle: no camera is mounted (and none was recently imported).
	StatusIdle Status = "idle"
	// StatusPending: a camera is mounted with new items not yet imported (at
	// rest before an import, or held back because the daemon is paused).
	StatusPending Status = "pending"
	// StatusImporting: a camera is mounted and its media is being imported.
	StatusImporting Status = "importing"
	// StatusImported: a camera is still mounted and its import has finished.
	StatusImported Status = "imported"
	// StatusEjected: a camera was unmounted after a recent import (shown for a
	// while so the result is still visible after unplugging).
	StatusEjected Status = "ejected"
)

// State is the snapshot the daemon broadcasts to connected clients. Paused is
// the persistent setting (drives the menu toggle); the rest describe live
// activity. Clients render PrimaryLabel/DetailLabel/Progress.
type State struct {
	Status      Status `json:"status"`
	Paused      bool   `json:"paused"`
	Model       string `json:"model,omitempty"`        // last-seen camera's colour (from charmera.model on the card)
	Volume      string `json:"volume,omitempty"`       // detected camera's volume name
	Total       int    `json:"total,omitempty"`        // items in the current/most-recent import
	Done        int    `json:"done,omitempty"`         // items imported so far / in total
	EjectFailed bool   `json:"eject_failed,omitempty"` // import done but the camera wouldn't eject
	Error       string `json:"error,omitempty"`        // import/plan error, if any
	// NeedsPermission: macOS denied access to the camera volume (TCC "Removable
	// Volumes"); the user must grant it in System Settings.
	NeedsPermission bool `json:"needs_permission,omitempty"`
}

// PrimaryLabel is the headline status line ("Charmera detected" etc.).
func (s State) PrimaryLabel() string {
	switch s.Status {
	case StatusEjected:
		return "Charmera ejected"
	case StatusIdle:
		return "No Charmera detected"
	default:
		return "Charmera detected"
	}
}

// DetailLabel is the second line: what's happening with the items, plus a
// "(paused)" suffix when paused. Empty means there's no detail to show.
func (s State) DetailLabel() string {
	base := s.detailBase()
	if s.Paused {
		if base == "" {
			return "Paused"
		}
		return base + " (paused)"
	}
	return base
}

func (s State) detailBase() string {
	if s.NeedsPermission {
		return "Please provide permissions"
	}
	if s.Error != "" {
		return s.Error
	}
	switch s.Status {
	case StatusPending:
		if s.Total > 0 {
			return items(s.Total) + " to import"
		}
		return "Up to date"
	case StatusImporting:
		return "Importing " + items(s.Total)
	case StatusImported, StatusEjected:
		base := "Up to date"
		if s.Done > 0 {
			base = items(s.Done) + " imported"
		}
		if s.EjectFailed {
			base += " — couldn't eject"
		}
		return base
	default:
		return ""
	}
}

// Progress reports the import progress fraction (0..1) and whether a progress
// bar should be shown (only while importing).
func (s State) Progress() (fraction float64, show bool) {
	if s.Status != StatusImporting || s.Total <= 0 {
		return 0, false
	}
	f := float64(s.Done) / float64(s.Total)
	if f > 1 {
		f = 1
	}
	return f, true
}

func items(n int) string {
	if n == 1 {
		return "1 item"
	}
	return fmt.Sprintf("%d items", n)
}

// Command is a request sent by a client to the daemon over the socket.
type Command struct {
	Command string `json:"command"` // "pause" | "resume" | "toggle"
}

const (
	CmdPause  = "pause"
	CmdResume = "resume"
	CmdToggle = "toggle"
)
