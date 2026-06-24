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
)

func TestStateLabel(t *testing.T) {
	cases := []struct {
		state State
		want  string
	}{
		{State{Status: StatusIdle}, "No Charmera detected"},
		{State{Status: StatusIdle, Paused: true}, "Paused — no Charmera detected"},
		{State{Status: StatusDetected, Volume: "CAM"}, "Charmera detected"},
		{State{Status: StatusDetected, Paused: true}, "Charmera detected: paused"},
		{State{Status: StatusImporting}, "Charmera detected: importing…"},
		{State{Status: StatusImporting, Detail: "fixing IMG_1"}, "Charmera detected: fixing IMG_1"},
	}
	for _, c := range cases {
		if got := c.state.Label(); got != c.want {
			t.Errorf("Label(%+v) = %q, want %q", c.state, got, c.want)
		}
	}
}

// makeCamera creates a directory under volumesDir that scan.FindCamera will
// recognise as a Charmera (DCIM + a sibling SPIDCIM).
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
	// capped at ~104 chars, which t.TempDir's long test-named paths can exceed.
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

// recorder is a fake RunImport that records the volumes it was asked to import.
type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) run(_ context.Context, volumePath string, obs pipeline.Observer) (pipeline.Summary, error) {
	r.mu.Lock()
	r.calls = append(r.calls, volumePath)
	r.mu.Unlock()
	if obs != nil {
		obs(pipeline.Event{Phase: "import", Detail: "1 file(s)"})
	}
	return pipeline.Summary{Imported: 1}, nil
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
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

func TestDetectionTriggersImportThenUnmount(t *testing.T) {
	cfg := testConfig(t)
	rec := &recorder{}
	vol := makeCamera(t, cfg.VolumesDir, "CAM")

	var unmounted []string
	var umu sync.Mutex
	unmount := func(p string) error {
		umu.Lock()
		unmounted = append(unmounted, p)
		umu.Unlock()
		return os.RemoveAll(p) // simulate ejection so detection goes idle
	}

	d := New(Options{Config: cfg, RunImport: rec.run, Unmount: unmount, Poll: 10 * time.Millisecond})
	startDaemon(t, d)

	if !waitFor(time.Second, func() bool { return rec.count() == 1 }) {
		t.Fatalf("expected exactly one import, got %d", rec.count())
	}
	if rec.calls[0] != vol {
		t.Errorf("imported %q, want %q", rec.calls[0], vol)
	}
	umu.Lock()
	gotUnmount := len(unmounted) == 1 && unmounted[0] == vol
	umu.Unlock()
	if !gotUnmount {
		t.Errorf("expected unmount of %q, got %v", vol, unmounted)
	}
	if !waitFor(time.Second, func() bool { return d.Snapshot().Status == StatusIdle }) {
		t.Errorf("after ejection state = %q, want idle", d.Snapshot().Status)
	}
}

func TestLingeringCameraImportedOnce(t *testing.T) {
	cfg := testConfig(t)
	rec := &recorder{}
	makeCamera(t, cfg.VolumesDir, "CAM")

	// No unmount: the camera stays mounted across many polls.
	d := New(Options{Config: cfg, RunImport: rec.run, Poll: 5 * time.Millisecond})
	startDaemon(t, d)

	waitFor(200*time.Millisecond, func() bool { return rec.count() >= 1 })
	if got := rec.count(); got != 1 {
		t.Fatalf("lingering camera imported %d times, want 1", got)
	}
	if st := d.Snapshot(); st.Status != StatusDetected {
		t.Errorf("after import state = %q, want detected", st.Status)
	}
}

func TestPausePreventsImportResumeAllows(t *testing.T) {
	cfg := testConfig(t)
	rec := &recorder{}
	makeCamera(t, cfg.VolumesDir, "CAM")

	d := New(Options{Config: cfg, RunImport: rec.run, Poll: 5 * time.Millisecond})
	d.SetPaused(true)
	startDaemon(t, d)

	// Paused: camera is detected but never imported.
	if !waitFor(200*time.Millisecond, func() bool { return d.Snapshot().Status == StatusDetected }) {
		t.Fatal("camera should be detected while paused")
	}
	time.Sleep(50 * time.Millisecond)
	if rec.count() != 0 {
		t.Fatalf("import ran while paused (%d times)", rec.count())
	}

	d.SetPaused(false)
	if !waitFor(time.Second, func() bool { return rec.count() == 1 }) {
		t.Fatalf("resume did not trigger import, count=%d", rec.count())
	}
}

func TestPausePersistsAcrossRestart(t *testing.T) {
	cfg := testConfig(t)
	rec := &recorder{}

	d1 := New(Options{Config: cfg, RunImport: rec.run})
	d1.SetPaused(true)

	d2 := New(Options{Config: cfg, RunImport: rec.run})
	if !d2.Snapshot().Paused {
		t.Error("pause setting did not persist to a fresh daemon")
	}
}

func TestSocketStreamsStateAndAcceptsCommands(t *testing.T) {
	cfg := testConfig(t) // empty VolumesDir: stays idle, never imports
	rec := &recorder{}
	d := New(Options{Config: cfg, RunImport: rec.run, Poll: 20 * time.Millisecond})
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
	rec := &recorder{}
	d := New(Options{Config: cfg, RunImport: rec.run, Poll: 20 * time.Millisecond})
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
