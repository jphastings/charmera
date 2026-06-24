// Package daemon is the long-running charmera background process. It watches for
// the camera, imports its media on detection (unless paused), holds live state,
// and serves that state plus pause/resume control over a Unix-domain socket.
//
// The package is deliberately cgo-free and platform-agnostic: the cgo-heavy
// import and unmount steps are injected (an Importer and Unmount funcs), the same
// way the pipeline keeps orientation detection behind an interface. This keeps
// the state machine unit-testable on any platform with fakes.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jphastings/charmera/internal/config"
	"github.com/jphastings/charmera/internal/pipeline"
	"github.com/jphastings/charmera/internal/scan"
)

const (
	// defaultPoll is how often the daemon re-checks for a mounted camera. It also
	// re-evaluates immediately whenever the pause setting changes.
	defaultPoll = 2 * time.Second
	// ejectWindow is how long after an import the result keeps showing once the
	// camera has been unplugged.
	ejectWindow = time.Hour

	defaultUnmountRetries = 5
	defaultUnmountBackoff = time.Second
)

// Importer plans and performs the import for a camera. It's injected by the CLI
// (which owns the cgo-heavy video/photos/orientation wiring) so this package
// stays cgo-free.
type Importer interface {
	// Plan reports which files are new (not already in Photos), changing nothing.
	Plan(ctx context.Context, volumePath string) (pipeline.Plan, error)
	// Execute fixes/converts and imports the planned files, emitting progress.
	Execute(ctx context.Context, volumePath string, plan pipeline.Plan, obs pipeline.Observer) (pipeline.Summary, error)
}

// Options configures a Daemon. Config and Importer are required; the rest have
// defaults.
type Options struct {
	Config   config.Config
	Importer Importer

	// Unmount ejects the camera after an import. Optional; nil skips unmounting
	// (used in tests, and honours the user's --no-unmount preference).
	Unmount func(volumePath string) error
	// ForceUnmount is tried as a last resort when Unmount keeps being refused
	// (e.g. the volume is briefly busy with Spotlight indexing). Optional.
	ForceUnmount func(volumePath string) error

	// Poll overrides the camera re-check interval (defaults to defaultPoll).
	Poll time.Duration
	// UnmountRetries / UnmountBackoff tune the eject retry loop.
	UnmountRetries int
	UnmountBackoff time.Duration
	// Now is the clock, injectable for tests (defaults to time.Now).
	Now func() time.Time
}

// Daemon holds the running state and its subscribers.
type Daemon struct {
	opts Options
	poll time.Duration
	now  func() time.Time

	mu   sync.Mutex
	cur  State
	subs map[int]chan State
	next int

	// Per-mount bookkeeping (only touched by the single watch goroutine):
	plannedVol string        // mount path the cached plan is for
	plan       pipeline.Plan // cached plan for plannedVol
	handled    string        // mount path already imported this session
	lastCount  int           // items imported in the most recent import
	lastAt     time.Time     // when that import finished

	wake chan struct{}
}

// New builds a Daemon, restoring the persisted pause setting.
func New(opts Options) *Daemon {
	if opts.Importer == nil {
		panic("daemon: Importer is required")
	}
	if opts.Poll == 0 {
		opts.Poll = defaultPoll
	}
	if opts.UnmountRetries == 0 {
		opts.UnmountRetries = defaultUnmountRetries
	}
	if opts.UnmountBackoff == 0 {
		opts.UnmountBackoff = defaultUnmountBackoff
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	d := &Daemon{
		opts: opts,
		poll: opts.Poll,
		now:  opts.Now,
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
		d.onNoCamera()
		return
	}
	volName := filepath.Base(volPath)

	// Plan once per mount and cache it: reused across polls and on resume, so we
	// don't re-hash the whole card every couple of seconds (or while paused).
	if d.plannedVol != volPath {
		plan, err := d.opts.Importer.Plan(ctx, volPath)
		if err != nil {
			d.plannedVol = "" // retry the plan on the next poll
			d.setError(volName, planErrorMessage(err))
			return
		}
		d.plannedVol = volPath
		d.plan = plan
		d.handled = ""
	}

	if d.handled == volPath {
		d.setImported(volName, d.lastCount)
		return
	}
	if d.isPaused() {
		d.setPending(volName, d.plan.Total())
		return
	}
	d.importMedia(ctx, volPath, volName)
}

// onNoCamera reflects an empty /Volumes: show the result of a recent import for
// a while after the camera is unplugged, otherwise go idle.
func (d *Daemon) onNoCamera() {
	d.plannedVol = ""
	d.handled = ""
	d.plan = pipeline.Plan{}
	if d.lastCount > 0 && d.now().Sub(d.lastAt) < ejectWindow {
		d.setEjected(d.lastCount)
		return
	}
	d.setIdle()
}

// importMedia runs the import for the freshly-detected camera using the cached
// plan, streaming progress into the live state, then unmounts (if configured).
func (d *Daemon) importMedia(ctx context.Context, volPath, volName string) {
	d.handled = volPath // don't re-import this mount even if it lingers
	total := d.plan.Total()

	if total == 0 {
		d.lastCount = 0
		d.setImported(volName, 0)
		d.tryUnmount(volPath, volName, 0)
		return
	}

	d.setImporting(volName, total)
	obs := func(e pipeline.Event) {
		if e.Phase == "fix" || e.Phase == "convert" {
			d.incDone()
		}
	}

	sum, err := d.opts.Importer.Execute(ctx, volPath, d.plan, obs)
	if err != nil {
		d.setError(volName, err.Error())
		return
	}

	d.lastCount = sum.Imported
	d.lastAt = d.now()
	d.setImported(volName, sum.Imported)
	d.tryUnmount(volPath, volName, sum.Imported)
}

// tryUnmount ejects the camera, retrying transient "busy" failures and falling
// back to a forced unmount. On success the volume vanishes and the next poll
// shows the ejected state; on persistent failure the import result is shown with
// an "couldn't eject" note.
func (d *Daemon) tryUnmount(volPath, volName string, count int) {
	if d.opts.Unmount == nil {
		return
	}
	if err := d.unmountWithRetry(volPath); err != nil {
		d.setEjectFailed(volName, count)
	}
}

func (d *Daemon) unmountWithRetry(volPath string) error {
	var err error
	for attempt := 0; attempt < d.opts.UnmountRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(d.opts.UnmountBackoff)
		}
		if err = d.opts.Unmount(volPath); err == nil {
			return nil
		}
	}
	if d.opts.ForceUnmount != nil {
		if ferr := d.opts.ForceUnmount(volPath); ferr == nil {
			return nil
		}
	}
	return err
}

func planErrorMessage(err error) string {
	if errors.Is(err, pipeline.ErrImporterUnavailable) {
		return "waiting for Photos…"
	}
	return err.Error()
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

func (d *Daemon) setIdle() {
	d.apply(func(s *State) { *s = State{Status: StatusIdle, Paused: s.Paused} })
}

func (d *Daemon) setPending(volume string, total int) {
	d.apply(func(s *State) {
		*s = State{Status: StatusPending, Paused: s.Paused, Volume: volume, Total: total}
	})
}

func (d *Daemon) setImporting(volume string, total int) {
	d.apply(func(s *State) {
		*s = State{Status: StatusImporting, Paused: s.Paused, Volume: volume, Total: total}
	})
}

func (d *Daemon) setImported(volume string, count int) {
	d.apply(func(s *State) {
		*s = State{Status: StatusImported, Paused: s.Paused, Volume: volume, Total: count, Done: count}
	})
}

func (d *Daemon) setEjected(count int) {
	d.apply(func(s *State) {
		*s = State{Status: StatusEjected, Paused: s.Paused, Total: count, Done: count}
	})
}

func (d *Daemon) setEjectFailed(volume string, count int) {
	d.apply(func(s *State) {
		*s = State{Status: StatusImported, Paused: s.Paused, Volume: volume, Total: count, Done: count, EjectFailed: true}
	})
}

func (d *Daemon) setError(volume, msg string) {
	d.apply(func(s *State) {
		*s = State{Status: StatusPending, Paused: s.Paused, Volume: volume, Error: msg}
	})
}

func (d *Daemon) incDone() {
	d.apply(func(s *State) { s.Done++ })
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
