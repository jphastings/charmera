// Package daemon is the long-running charmera background process. It watches for
// the camera, imports its media on detection (unless paused), holds live state,
// and serves that state plus pause/resume control over a Unix-domain socket.
//
// The package is deliberately cgo-free and platform-agnostic: the cgo-heavy
// import and unmount steps are injected (RunImport, Unmount), the same way the
// pipeline keeps orientation detection behind an interface. This keeps the state
// machine unit-testable on any platform with fakes.
package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jphastings/charmera/internal/config"
	"github.com/jphastings/charmera/internal/pipeline"
	"github.com/jphastings/charmera/internal/scan"
)

// defaultPoll is how often the daemon re-checks for a mounted camera. It also
// re-evaluates immediately whenever the pause setting changes.
const defaultPoll = 2 * time.Second

// Options configures a Daemon. RunImport is required; the rest have defaults.
type Options struct {
	Config config.Config

	// RunImport fixes/converts and imports the camera's media. It is injected by
	// the CLI (which owns the cgo-heavy video/photos/orientation wiring) so this
	// package stays cgo-free. The observer streams progress for live status.
	RunImport func(ctx context.Context, volumePath string, obs pipeline.Observer) (pipeline.Summary, error)

	// Unmount ejects the camera after a successful import. Optional; nil skips
	// unmounting (used in tests, and honours the user's --no-unmount preference).
	Unmount func(volumePath string) error

	// Poll overrides the camera re-check interval (defaults to defaultPoll).
	Poll time.Duration
}

// Daemon holds the running state and its subscribers.
type Daemon struct {
	opts Options
	poll time.Duration

	mu   sync.Mutex
	cur  State
	subs map[int]chan State
	next int

	// handled is the mount path of a camera whose import has already completed
	// this mount session; it prevents re-importing a camera that stays mounted
	// (e.g. when unmount is disabled or fails). Cleared when the camera leaves.
	handled string

	wake chan struct{}
}

// New builds a Daemon, restoring the persisted pause setting.
func New(opts Options) *Daemon {
	if opts.RunImport == nil {
		panic("daemon: RunImport is required")
	}
	if opts.Poll == 0 {
		opts.Poll = defaultPoll
	}
	d := &Daemon{
		opts: opts,
		poll: opts.Poll,
		subs: map[int]chan State{},
		wake: make(chan struct{}, 1),
	}
	d.cur = State{Status: StatusIdle, Paused: loadPaused(opts.Config)}
	return d
}

// Run starts the socket server and the watch loop, blocking until ctx is done
// or the server fails to start (e.g. the socket can't be bound).
func (d *Daemon) Run(ctx context.Context) error {
	if err := os.MkdirAll(d.opts.Config.StateDir, 0o755); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	srvErr := make(chan error, 1)
	go func() { srvErr <- d.serve(ctx) }()

	watchDone := make(chan struct{})
	go func() { d.watch(ctx); close(watchDone) }()

	select {
	case err := <-srvErr:
		cancel()
		<-watchDone
		return err
	case <-watchDone:
		return nil
	}
}

// watch is the detection/import loop: re-evaluate on a timer and whenever the
// pause setting changes.
func (d *Daemon) watch(ctx context.Context) {
	t := time.NewTicker(d.poll)
	defer t.Stop()

	d.evaluate(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.evaluate(ctx)
		case <-d.wake:
			d.evaluate(ctx)
		}
	}
}

// evaluate inspects the current camera situation and acts on it: import when a
// fresh camera appears and we're not paused, otherwise just reflect the state.
func (d *Daemon) evaluate(ctx context.Context) {
	volPath, found := scan.FindCamera(d.opts.Config)
	if !found {
		d.handled = ""
		d.setStatus(StatusIdle, "", "")
		return
	}

	volName := filepath.Base(volPath)

	if d.isPaused() {
		d.setStatus(StatusDetected, volName, "")
		return
	}
	if d.handled == volPath {
		d.setStatus(StatusDetected, volName, "")
		return
	}

	d.importMedia(ctx, volPath, volName)
}

// importMedia runs one import for a freshly-detected camera, streaming progress
// into the live state, then unmounts (if configured).
func (d *Daemon) importMedia(ctx context.Context, volPath, volName string) {
	d.setStatus(StatusImporting, volName, "importing…")

	obs := func(e pipeline.Event) {
		if detail := importDetail(e); detail != "" {
			d.setDetail(detail)
		}
	}

	_, err := d.opts.RunImport(ctx, volPath, obs)
	d.handled = volPath // don't re-import this mount even if it lingers

	if err != nil {
		d.setError(volName, err.Error())
		return
	}

	if d.opts.Unmount != nil {
		if uerr := d.opts.Unmount(volPath); uerr == nil {
			d.setStatus(StatusIdle, "", "")
			return
		}
	}
	d.setStatus(StatusDetected, volName, "")
}

// importDetail maps a pipeline event to a short live-status string (empty for
// events not worth surfacing to a glanceable menu).
func importDetail(e pipeline.Event) string {
	switch e.Phase {
	case "import":
		if e.Name == "" {
			return "importing into Photos…"
		}
	case "fix":
		return "fixing " + e.Name
	case "convert":
		return "converting " + e.Name
	}
	return ""
}

// --- pause setting (persisted) ---

func (d *Daemon) isPaused() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cur.Paused
}

// SetPaused changes and persists the pause setting, then nudges the loop so the
// change takes effect promptly.
func (d *Daemon) SetPaused(paused bool) {
	changed := false
	d.apply(func(s *State) {
		if s.Paused != paused {
			s.Paused = paused
			changed = true
		}
	})
	if changed {
		savePaused(d.opts.Config, paused)
		d.nudge()
	}
}

// Toggle flips the pause setting.
func (d *Daemon) Toggle() { d.SetPaused(!d.isPaused()) }

func (d *Daemon) nudge() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// --- state updates + broadcast ---

func (d *Daemon) setStatus(status Status, volume, detail string) {
	d.apply(func(s *State) {
		s.Status = status
		s.Volume = volume
		s.Detail = detail
		s.Error = ""
	})
}

func (d *Daemon) setDetail(detail string) {
	d.apply(func(s *State) { s.Detail = detail })
}

func (d *Daemon) setError(volume, msg string) {
	d.apply(func(s *State) {
		s.Status = StatusDetected
		s.Volume = volume
		s.Detail = ""
		s.Error = msg
	})
}

// apply mutates the current state under the lock and broadcasts the result, but
// only when it actually changed — so an idle re-evaluation each poll doesn't
// spam subscribers.
func (d *Daemon) apply(mutate func(*State)) {
	d.mu.Lock()
	next := d.cur
	mutate(&next)
	if next == d.cur {
		d.mu.Unlock()
		return
	}
	d.cur = next
	d.mu.Unlock()
	d.broadcast(next)
}

// Snapshot returns the current state.
func (d *Daemon) Snapshot() State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cur
}

// subscribe registers a channel that receives the current state immediately and
// every subsequent change. The returned func unsubscribes.
func (d *Daemon) subscribe() (<-chan State, func()) {
	ch := make(chan State, 8)
	d.mu.Lock()
	id := d.next
	d.next++
	d.subs[id] = ch
	ch <- d.cur // prime with current state
	d.mu.Unlock()

	return ch, func() {
		d.mu.Lock()
		if c, ok := d.subs[id]; ok {
			delete(d.subs, id)
			close(c)
		}
		d.mu.Unlock()
	}
}

func (d *Daemon) broadcast(s State) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, ch := range d.subs {
		select {
		case ch <- s:
		default: // a slow client never blocks the daemon; it'll get the next one
		}
	}
}

// --- pause persistence ---

type persisted struct {
	Paused bool `json:"paused"`
}

func loadPaused(cfg config.Config) bool {
	data, err := os.ReadFile(cfg.StatePath())
	if err != nil {
		return false
	}
	var p persisted
	if json.Unmarshal(data, &p) != nil {
		return false
	}
	return p.Paused
}

func savePaused(cfg config.Config, paused bool) {
	_ = os.MkdirAll(filepath.Dir(cfg.StatePath()), 0o755)
	data, err := json.Marshal(persisted{Paused: paused})
	if err != nil {
		return
	}
	tmp := cfg.StatePath() + ".tmp"
	if os.WriteFile(tmp, data, 0o644) != nil {
		return
	}
	if err := os.Rename(tmp, cfg.StatePath()); err != nil {
		_ = os.Remove(tmp)
	}
}
