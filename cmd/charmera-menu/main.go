// Command charmera-menu is the menu-bar companion for charmera. It shows the
// daemon's current state and offers a Pause toggle.
//
// It does no importing itself: the charmera daemon (a LaunchAgent) does that in
// the background, and this app just connects to it over the daemon socket. It
// can be quit at any time without affecting the daemon.
package main

import (
	"context"
	_ "embed"
	"os"
	"path/filepath"
	"time"

	"fyne.io/systray"

	"github.com/jphastings/charmera/internal/config"
	"github.com/jphastings/charmera/internal/daemon"
	"github.com/jphastings/charmera/internal/launchagent"
)

//go:embed icon.png
var iconPNG []byte

var (
	cfg        = config.Default()
	statusItem *systray.MenuItem
	pauseItem  *systray.MenuItem
)

func main() {
	systray.Run(onReady, nil)
}

func onReady() {
	systray.SetTemplateIcon(iconPNG, iconPNG)
	systray.SetTooltip("Charmera")

	statusItem = systray.AddMenuItem("Connecting…", "")
	statusItem.Disable()
	systray.AddSeparator()
	pauseItem = systray.AddMenuItem("Pause", "Stop importing when the camera is plugged in")
	systray.AddSeparator()
	quitItem := systray.AddMenuItem("Quit Charmera", "Quit this menu; importing continues in the background")

	ensureDaemonInstalled()
	go connectLoop()

	go func() {
		for {
			select {
			case <-pauseItem.ClickedCh:
				togglePause()
			case <-quitItem.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

// connectLoop keeps a live connection to the daemon, streaming state into the
// menu and reconnecting whenever the daemon is unavailable.
func connectLoop() {
	for {
		c, err := daemon.Dial(cfg)
		if err != nil {
			setDisconnected()
			time.Sleep(2 * time.Second)
			continue
		}
		streamStates(c)
		c.Close()
		setDisconnected()
	}
}

func streamStates(c *daemon.Client) {
	for {
		st, err := c.ReadState()
		if err != nil {
			return
		}
		applyState(st)
	}
}

func applyState(st daemon.State) {
	statusItem.SetTitle(st.Label())
	if st.Paused {
		pauseItem.SetTitle("Resume importing")
		pauseItem.Check()
	} else {
		pauseItem.SetTitle("Pause importing")
		pauseItem.Uncheck()
	}
}

func setDisconnected() {
	statusItem.SetTitle("Daemon not running")
	pauseItem.SetTitle("Pause importing")
	pauseItem.Uncheck()
}

func togglePause() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The resulting state arrives over the streaming connection, which updates
	// the menu; we don't need the return value here.
	_, _ = daemon.SendCommand(ctx, cfg, daemon.CmdToggle)
}

// ensureDaemonInstalled installs the daemon LaunchAgent on first launch when a
// bundled CLI is present (i.e. when running from Charmera.app). In a bare dev
// build there's no embedded CLI, so this is a no-op and the user runs
// `charmera install` themselves.
func ensureDaemonInstalled() {
	if launchagent.IsInstalled() {
		return
	}
	if cli := embeddedCLI(); cli != "" {
		_, _ = launchagent.Install(cli, "")
	}
}

// embeddedCLI returns the path to the charmera CLI shipped inside this app
// (Contents/Resources/charmera, a sibling of the Contents/MacOS executable), or
// "" if it isn't present (e.g. a bare dev build).
func embeddedCLI() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	cli := filepath.Join(filepath.Dir(exe), "..", "Resources", "charmera")
	if fi, err := os.Stat(cli); err == nil && !fi.IsDir() {
		return cli
	}
	return ""
}
