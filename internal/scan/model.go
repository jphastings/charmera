package scan

import (
	"os"
	"path/filepath"
	"strings"
)

// modelFileName is a user-authored file at the root of the SD card naming the
// physical camera's colour (e.g. "red"), so the menu-bar app can show a
// matching icon. It is not written by the camera itself — the user creates it.
const modelFileName = "charmera.model"

// ReadModel reads the camera's colour from charmera.model at the root of
// volPath, trimmed of whitespace and lower-cased. It returns "" if the file is
// missing or unreadable.
func ReadModel(volPath string) string {
	data, err := os.ReadFile(filepath.Join(volPath, modelFileName))
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(string(data)))
}
