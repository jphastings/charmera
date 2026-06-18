// Package launchagent installs and removes the macOS LaunchAgent that runs
// charmera automatically when the camera is plugged in. It watches the camera's
// mount path; launchd fires the agent whenever that path appears or disappears.
package launchagent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Label is the launchd job label and plist basename.
const Label = "com.charmera.import"

// plistData is the data needed to render the agent plist.
type plistData struct {
	ExePath    string
	VolumePath string
	OutLog     string
	ErrLog     string
}

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist"), nil
}

func logPaths() (out, errLog string) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "Library", "Logs")
	return filepath.Join(dir, "charmera.out.log"), filepath.Join(dir, "charmera.err.log")
}

// renderPlist builds the LaunchAgent plist. The agent runs `charmera run --auto`
// and includes Homebrew's bin on PATH so ffmpeg is found under launchd.
func renderPlist(d plistData) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + Label + `</string>

	<key>ProgramArguments</key>
	<array>
		<string>` + xmlEscape(d.ExePath) + `</string>
		<string>run</string>
		<string>--auto</string>
	</array>

	<key>WatchPaths</key>
	<array>
		<string>` + xmlEscape(d.VolumePath) + `</string>
	</array>

	<key>RunAtLoad</key>
	<false/>

	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
	</dict>

	<key>StandardOutPath</key>
	<string>` + xmlEscape(d.OutLog) + `</string>
	<key>StandardErrorPath</key>
	<string>` + xmlEscape(d.ErrLog) + `</string>

	<key>ThrottleInterval</key>
	<integer>10</integer>
</dict>
</plist>
`
}

// Install writes the plist watching the given path (typically /Volumes) and
// (re)loads it via launchctl. It records the path of the currently-running
// executable so the agent invokes this same binary.
func Install(watchPath string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}

	dst, err := plistPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}

	out, errLog := logPaths()
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}

	content := renderPlist(plistData{ExePath: exe, VolumePath: watchPath, OutLog: out, ErrLog: errLog})
	if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
		return "", err
	}

	// Reload: unload first (ignore errors), then load.
	_ = exec.Command("launchctl", "unload", dst).Run()
	if err := exec.Command("launchctl", "load", dst).Run(); err != nil {
		return dst, fmt.Errorf("launchctl load failed: %w", err)
	}
	return dst, nil
}

// Uninstall unloads and removes the LaunchAgent plist.
func Uninstall() (string, error) {
	dst, err := plistPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		return dst, os.ErrNotExist
	}
	_ = exec.Command("launchctl", "unload", dst).Run()
	if err := os.Remove(dst); err != nil {
		return dst, err
	}
	return dst, nil
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
