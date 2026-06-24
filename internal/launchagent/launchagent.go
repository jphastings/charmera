// Package launchagent installs and removes the macOS LaunchAgent that runs the
// charmera daemon. The daemon is kept alive by launchd (relaunched if it exits)
// and started at login; it watches for the camera itself, so no WatchPaths
// trigger is needed.
package launchagent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Label is the launchd job label and plist basename.
const Label = "com.charmera.daemon"

// legacyLabel is the pre-daemon run-once agent; Install removes it so the two
// don't coexist after an upgrade.
const legacyLabel = "com.charmera.import"

// plistData is the data needed to render the agent plist.
type plistData struct {
	ExePath    string
	VolumeName string // optional; pins the daemon to a specific volume
	OutLog     string
	ErrLog     string
}

func plistPathFor(label string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), nil
}

func plistPath() (string, error) { return plistPathFor(Label) }

func logPaths() (out, errLog string) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "Library", "Logs")
	return filepath.Join(dir, "charmera.out.log"), filepath.Join(dir, "charmera.err.log")
}

// renderPlist builds the LaunchAgent plist for the long-running daemon. It runs
// `charmera daemon`, is kept alive and started at load, and includes Homebrew's
// bin on PATH so ffmpeg is found under launchd.
func renderPlist(d plistData) string {
	args := "\t\t<string>" + xmlEscape(d.ExePath) + "</string>\n\t\t<string>daemon</string>\n"
	if d.VolumeName != "" {
		args += "\t\t<string>--volume</string>\n\t\t<string>" + xmlEscape(d.VolumeName) + "</string>\n"
	}

	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + Label + `</string>

	<key>ProgramArguments</key>
	<array>
` + args + `	</array>

	<key>RunAtLoad</key>
	<true/>

	<key>KeepAlive</key>
	<true/>

	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
	</dict>

	<key>StandardOutPath</key>
	<string>` + xmlEscape(d.OutLog) + `</string>
	<key>StandardErrorPath</key>
	<string>` + xmlEscape(d.ErrLog) + `</string>

	<key>ProcessType</key>
	<string>Background</string>
</dict>
</plist>
`
}

// Install writes the daemon plist for the given executable (optionally pinned to
// a volume) and (re)loads it via launchctl. exePath should be an absolute,
// symlink-resolved path to the charmera binary to run.
func Install(exePath, volumeName string) (string, error) {
	removeLegacy()

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

	content := renderPlist(plistData{ExePath: exePath, VolumeName: volumeName, OutLog: out, ErrLog: errLog})
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

// IsInstalled reports whether the daemon LaunchAgent plist is present.
func IsInstalled() bool {
	dst, err := plistPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(dst)
	return err == nil
}

// CurrentExe resolves the path of the running binary, following symlinks — the
// value to pass to Install when the running binary is the one to be kept alive.
func CurrentExe() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// Uninstall unloads and removes the LaunchAgent plist.
func Uninstall() (string, error) {
	removeLegacy()

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

// removeLegacy unloads and deletes the old run-once agent if present.
func removeLegacy() {
	old, err := plistPathFor(legacyLabel)
	if err != nil {
		return
	}
	if _, err := os.Stat(old); err == nil {
		_ = exec.Command("launchctl", "unload", old).Run()
		_ = os.Remove(old)
	}
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
