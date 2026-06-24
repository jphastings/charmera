package launchagent

import (
	"strings"
	"testing"
)

func TestRenderPlist_DaemonFields(t *testing.T) {
	got := renderPlist(plistData{
		ExePath: "/usr/local/bin/charmera",
		OutLog:  "/log/out.log",
		ErrLog:  "/log/err.log",
	})

	for _, want := range []string{
		"<string>" + Label + "</string>",
		"<string>/usr/local/bin/charmera</string>",
		"<string>daemon</string>",
		"<key>KeepAlive</key>",
		"<key>RunAtLoad</key>",
		"/opt/homebrew/bin", // ffmpeg on PATH under launchd
		"<string>/log/out.log</string>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plist missing %q", want)
		}
	}
	if strings.Contains(got, "WatchPaths") {
		t.Error("daemon plist should not use WatchPaths")
	}
}

func TestRenderPlist_PinnedVolume(t *testing.T) {
	got := renderPlist(plistData{ExePath: "/bin/charmera", VolumeName: "CHARMERA"})
	for _, want := range []string{"<string>--volume</string>", "<string>CHARMERA</string>"} {
		if !strings.Contains(got, want) {
			t.Errorf("plist missing %q", want)
		}
	}
}

func TestXMLEscape(t *testing.T) {
	if got := xmlEscape("/Vol/A&B<C>"); got != "/Vol/A&amp;B&lt;C&gt;" {
		t.Errorf("xmlEscape = %q", got)
	}
}
