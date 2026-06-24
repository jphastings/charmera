package daemon

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jphastings/charmera/internal/config"
	"github.com/jphastings/charmera/internal/pipeline"
	"github.com/jphastings/charmera/internal/scan"
)

func TestStateLabels(t *testing.T) {
	cases := []struct {
		name    string
		state   State
		primary string
		detail  string
	}{
		{"idle", State{Status: StatusIdle}, "No Charmera detected", ""},
		{"idle paused", State{Status: StatusIdle, Paused: true}, "No Charmera detected", "Paused"},
		{"pending", State{Status: StatusPending, Total: 3}, "Charmera detected", "3 items to import"},
		{"pending one", State{Status: StatusPending, Total: 1}, "Charmera detected", "1 item to import"},
		{"pending paused", State{Status: StatusPending, Total: 3, Paused: true}, "Charmera detected", "3 items to import (paused)"},
		{"pending nothing", State{Status: StatusPending, Total: 0}, "Charmera detected", "Up to date"},
		{"importing", State{Status: StatusImporting, Total: 5, Done: 2}, "Charmera detected", "Importing 5 items"},
		{"imported", State{Status: StatusImported, Total: 4, Done: 4}, "Charmera detected", "4 items imported"},
		{"imported none", State{Status: StatusImported}, "Charmera detected", "Up to date"},
		{"eject failed", State{Status: StatusImported, Total: 4, Done: 4, EjectFailed: true}, "Charmera detected", "4 items imported — couldn't eject"},
		{"ejected", State{Status: StatusEjected, Done: 7}, "Charmera ejected", "7 items imported"},
		{"plan error", State{Status: StatusPending, Error: "waiting for Photos…"}, "Charmera detected", "waiting for Photos…"},
	}
	for _, c := range cases {
		if got := c.state.PrimaryLabel(); got != c.primary {
			t.Errorf("%s: PrimaryLabel = %q, want %q", c.name, got, c.primary)
		}
		if got := c.state.DetailLabel(); got != c.detail {
			t.Errorf("%s: DetailLabel = %q, want %q", c.name, got, c.detail)
		}
	}
}

func TestProgress(t *testing.T) {
	if _, show := (State{Status: StatusImported, Total: 3, Done: 3}).Progress(); show {
		t.Error("progress should only show while importing")
	}
	frac, show := State{Status: StatusImporting, Total: 4, Done: 1}.Progress()
	if !show || frac != 0.25 {
		t.Errorf("progress = %v, %v; want 0.25, true", frac, show)
	}
}

// fakeImporter plans planTotal new files and records each Execute.
type fakeImporter struct {
	mu        sync.Mutex
	planTotal int
	planErr   error
	execErr   error
	executed  []string
}

func (f *fakeImporter) Plan(_ context.Context, _ string) (pipeline.Plan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.planErr != nil {
		return pipeline.Plan{}, f.planErr
	}
	return pipeline.Plan{Files: make([]scan.File, f.planTotal), Scanned: f.planTotal, Photos: f.planTotal}, nil
}

func (f *fakeImporter) Execute(_ context.Context, vol string, plan pipeline.Plan, obs pipeline.Observer) (pipeline.Summary, error) {
	f.mu.Lock()
	f.executed = append(f.executed, vol)
	f.mu.Unlock()
	if obs != nil {
		for range plan.Files {
			obs(pipeline.Event{Phase: "fix"})
		}
	}
	if f.execErr != nil {
		return pipeline.Summary{}, f.execErr
	}
	return pipeline.Summary{Imported: plan.Total()}, nil
}

func (f *fakeImporter) execCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.executed)
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func makeCamera(t *testing.T, volumesDir, name string) string {
	t.Helper()
	vol := filepath.Join(volumesDir, name)
	for _, d := range []string{"DCIM", "SPIDCIM"} {
		if err := os.MkdirAll(filepath.Join(vol, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return vol
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.VolumesDir = t.TempDir()
	// A short StateDir: the daemon socket lives here and Unix socket paths are
	// capped near 104 chars, which t.TempDir's long test-named paths can exceed.
	dir, err := os.MkdirTemp("", "chd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	cfg.StateDir = dir
	return cfg
}

func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

func startDaemon(t *testing.T, d *Daemon) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func removingUnmount(t *testing.T) (func(string) error, func() []string) {
	var mu sync.Mutex
	var calls []string
	fn := func(p string) error {
		mu.Lock()
		calls = append(calls, p)
		mu.Unlock()
		return os.RemoveAll(p) // simulate ejection
	}
	get := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), calls...) }
	return fn, get
}

func TestDetectionImportsThenEjects(t *testing.T) {
	cfg := testConfig(t)
	imp := &fakeImporter{planTotal: 2}
	vol := makeCamera(t, cfg.VolumesDir, "CAM")
	unmount, unmounts := removingUnmount(t)

	d := New(Options{Config: cfg, Importer: imp, Unmount: unmount, Poll: 10 * time.Millisecond})
	startDaemon(t, d)

	if !waitFor(time.Second, func() bool { return d.Snapshot().Status == StatusEjected }) {
		t.Fatalf("expected ejected state, got %+v", d.Snapshot())
	}
	if imp.execCount() != 1 {
		t.Errorf("expected one import, got %d", imp.execCount())
	}
	if u := unmounts(); len(u) != 1 || u[0] != vol {
		t.Errorf("expected one unmount of %q, got %v", vol, u)
	}
	if st := d.Snapshot(); st.Done != 2 {
		t.Errorf("ejected state should remember 2 imported, got %+v", st)
	}
}

func TestUpToDateCameraNotImportedButEjected(t *testing.T) {
	cfg := testConfig(t)
	imp := &fakeImporter{planTotal: 0}
	makeCamera(t, cfg.VolumesDir, "CAM")
	unmount, unmounts := removingUnmount(t)

	d := New(Options{Config: cfg, Importer: imp, Unmount: unmount, Poll: 10 * time.Millisecond})
	startDaemon(t, d)

	// Nothing new → no Execute, but the camera is still ejected, and with nothing
	// imported the daemon returns to idle (no hour-long "ejected" banner).
	if !waitFor(time.Second, func() bool { return len(unmounts()) == 1 }) {
		t.Fatal("expected the up-to-date camera to be ejected")
	}
	if imp.execCount() != 0 {
		t.Errorf("nothing new should not run an import, got %d", imp.execCount())
	}
	if !waitFor(time.Second, func() bool { return d.Snapshot().Status == StatusIdle }) {
		t.Errorf("after ejecting an up-to-date camera, want idle, got %+v", d.Snapshot())
	}
}

func TestLingeringCameraImportedOnce(t *testing.T) {
	cfg := testConfig(t)
	imp := &fakeImporter{planTotal: 1}
	makeCamera(t, cfg.VolumesDir, "CAM")

	// No unmount: the camera stays mounted across many polls.
	d := New(Options{Config: cfg, Importer: imp, Poll: 5 * time.Millisecond})
	startDaemon(t, d)

	waitFor(200*time.Millisecond, func() bool { return imp.execCount() >= 1 })
	time.Sleep(50 * time.Millisecond)
	if imp.execCount() != 1 {
		t.Fatalf("lingering camera imported %d times, want 1", imp.execCount())
	}
	if st := d.Snapshot(); st.Status != StatusImported || st.Done != 1 {
		t.Errorf("after import want imported/1, got %+v", st)
	}
}

func TestPauseShowsCountWithoutImporting(t *testing.T) {
	cfg := testConfig(t)
	imp := &fakeImporter{planTotal: 3}
	makeCamera(t, cfg.VolumesDir, "CAM")

	d := New(Options{Config: cfg, Importer: imp, Poll: 5 * time.Millisecond})
	d.SetPaused(true)
	startDaemon(t, d)

	if !waitFor(300*time.Millisecond, func() bool {
		st := d.Snapshot()
		return st.Status == StatusPending && st.Total == 3
	}) {
		t.Fatalf("paused camera should show pending count, got %+v", d.Snapshot())
	}
	time.Sleep(50 * time.Millisecond)
	if imp.execCount() != 0 {
		t.Fatalf("import ran while paused (%d times)", imp.execCount())
	}

	d.SetPaused(false)
	if !waitFor(time.Second, func() bool { return imp.execCount() == 1 }) {
		t.Fatalf("resume did not trigger import, count=%d", imp.execCount())
	}
}

func TestEjectedExpiresAfterWindow(t *testing.T) {
	cfg := testConfig(t)
	imp := &fakeImporter{planTotal: 1}
	makeCamera(t, cfg.VolumesDir, "CAM")
	unmount, _ := removingUnmount(t)
	clock := &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}

	d := New(Options{Config: cfg, Importer: imp, Unmount: unmount, Poll: 5 * time.Millisecond, Now: clock.now})
	startDaemon(t, d)

	if !waitFor(time.Second, func() bool { return d.Snapshot().Status == StatusEjected }) {
		t.Fatalf("expected ejected, got %+v", d.Snapshot())
	}
	clock.advance(ejectWindow + time.Minute)
	if !waitFor(time.Second, func() bool { return d.Snapshot().Status == StatusIdle }) {
		t.Errorf("ejected banner should expire to idle, got %+v", d.Snapshot())
	}
}

func TestEjectFailedSurfacedWhenUnmountKeepsFailing(t *testing.T) {
	cfg := testConfig(t)
	imp := &fakeImporter{planTotal: 2}
	makeCamera(t, cfg.VolumesDir, "CAM")
	unmount := func(string) error { return errBusy }

	d := New(Options{
		Config: cfg, Importer: imp, Unmount: unmount,
		Poll: 5 * time.Millisecond, UnmountRetries: 2, UnmountBackoff: time.Millisecond,
	})
	startDaemon(t, d)

	if !waitFor(time.Second, func() bool {
		st := d.Snapshot()
		return st.Status == StatusImported && st.EjectFailed
	}) {
		t.Fatalf("expected eject-failed state, got %+v", d.Snapshot())
	}
}

func TestForceUnmountFallback(t *testing.T) {
	cfg := testConfig(t)
	imp := &fakeImporter{planTotal: 1}
	vol := makeCamera(t, cfg.VolumesDir, "CAM")
	var forced int
	var mu sync.Mutex

	d := New(Options{
		Config: cfg, Importer: imp,
		Unmount: func(string) error { return errBusy },
		ForceUnmount: func(p string) error {
			mu.Lock()
			forced++
			mu.Unlock()
			return os.RemoveAll(p)
		},
		Poll: 5 * time.Millisecond, UnmountRetries: 2, UnmountBackoff: time.Millisecond,
	})
	startDaemon(t, d)

	if !waitFor(time.Second, func() bool { return d.Snapshot().Status == StatusEjected }) {
		t.Fatalf("force unmount should let the camera eject, got %+v", d.Snapshot())
	}
	mu.Lock()
	got := forced
	mu.Unlock()
	if got != 1 {
		t.Errorf("expected one forced unmount, got %d", got)
	}
	if st := d.Snapshot(); st.EjectFailed {
		t.Error("force unmount succeeded; EjectFailed should be false")
	}
	_ = vol
}

func TestPlanErrorIsSurfacedAndRetried(t *testing.T) {
	cfg := testConfig(t)
	imp := &fakeImporter{planErr: pipeline.ErrImporterUnavailable}
	makeCamera(t, cfg.VolumesDir, "CAM")

	d := New(Options{Config: cfg, Importer: imp, Poll: 5 * time.Millisecond})
	startDaemon(t, d)

	if !waitFor(time.Second, func() bool { return d.Snapshot().Error != "" }) {
		t.Fatalf("plan error should be surfaced, got %+v", d.Snapshot())
	}
	if imp.execCount() != 0 {
		t.Errorf("a failed plan must not import, got %d", imp.execCount())
	}
}

func TestPausePersistsAcrossRestart(t *testing.T) {
	cfg := testConfig(t)
	imp := &fakeImporter{}
	New(Options{Config: cfg, Importer: imp}).SetPaused(true)

	d2 := New(Options{Config: cfg, Importer: imp})
	if !d2.Snapshot().Paused {
		t.Error("pause setting did not persist to a fresh daemon")
	}
}

func TestSocketStreamsStateAndAcceptsCommands(t *testing.T) {
	cfg := testConfig(t) // empty VolumesDir: stays idle, never imports
	imp := &fakeImporter{}
	d := New(Options{Config: cfg, Importer: imp, Poll: 20 * time.Millisecond})
	startDaemon(t, d)

	var c *Client
	if !waitFor(time.Second, func() bool {
		cl, err := Dial(cfg)
		if err != nil {
			return false
		}
		c = cl
		return true
	}) {
		t.Fatal("daemon socket never became reachable")
	}
	defer c.Close()

	first, err := c.ReadState()
	if err != nil {
		t.Fatalf("read initial state: %v", err)
	}
	if first.Status != StatusIdle || first.Paused {
		t.Errorf("initial state = %+v, want idle & not paused", first)
	}

	if err := c.Send(CmdPause); err != nil {
		t.Fatalf("send pause: %v", err)
	}
	next, err := c.ReadState()
	if err != nil {
		t.Fatalf("read state after pause: %v", err)
	}
	if !next.Paused {
		t.Errorf("state after pause = %+v, want Paused", next)
	}
}

func TestSendCommandReturnsResultingState(t *testing.T) {
	cfg := testConfig(t)
	imp := &fakeImporter{}
	d := New(Options{Config: cfg, Importer: imp, Poll: 20 * time.Millisecond})
	startDaemon(t, d)

	if !waitFor(time.Second, func() bool { _, err := Snapshot(cfg); return err == nil }) {
		t.Fatal("daemon socket never became reachable")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	st, err := SendCommand(ctx, cfg, CmdPause)
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	if !st.Paused {
		t.Errorf("SendCommand(pause) returned %+v, want Paused", st)
	}
}

type busyError struct{}

func (busyError) Error() string { return "Resource busy" }

var errBusy = busyError{}
