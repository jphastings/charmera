package daemon

// Status is the daemon's current activity, independent of the pause setting.
type Status string

const (
	// StatusIdle: no camera is mounted.
	StatusIdle Status = "idle"
	// StatusDetected: a camera is mounted but no import is in progress (at rest,
	// or held back because the daemon is paused).
	StatusDetected Status = "detected"
	// StatusImporting: a camera is mounted and its media is being imported.
	StatusImporting Status = "importing"
)

// State is the snapshot the daemon broadcasts to connected clients. Paused is
// the persistent setting (drives the menu toggle); Status reflects live
// activity. A client composes its label from both.
type State struct {
	Status Status `json:"status"`
	Paused bool   `json:"paused"`
	Volume string `json:"volume,omitempty"` // detected camera's volume name
	Detail string `json:"detail,omitempty"` // live progress, e.g. "importing 3 file(s)"
	Error  string `json:"error,omitempty"`  // last import error, if any
}

// Label is the human-readable status line shown in the menu and by
// `charmera status`. It is the single source of truth for wording so the CLI
// and the menu app stay in sync.
func (s State) Label() string {
	switch s.Status {
	case StatusImporting:
		if s.Detail != "" {
			return "Charmera detected: " + s.Detail
		}
		return "Charmera detected: importing…"
	case StatusDetected:
		if s.Paused {
			return "Charmera detected: paused"
		}
		return "Charmera detected"
	default:
		if s.Paused {
			return "Paused — no Charmera detected"
		}
		return "No Charmera detected"
	}
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
