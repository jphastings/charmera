package launchagent

import (
	"strings"
	"testing"
)

func TestRenderPlist_ContainsKeyFields(t *testing.T) {
	got := renderPlist(plistData{
		ExePath:    "/usr/local/bin/charmera",
		VolumePath: "/Volumes/CHARMERA",
		OutLog:     "/log/out.log",
		ErrLog:     "/log/err.log",
	})

	for _, want := range []string{
		"<string>" + Label + "</string>",
		"<string>/usr/local/bin/charmera</string>",
		"<string>run</string>",
		"<string>--auto</string>",
		"<string>/Volumes/CHARMERA</string>", // WatchPaths target
		"/opt/homebrew/bin",                  // ffmpeg on PATH under launchd
		"<string>/log/out.log</string>",
	} {
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
