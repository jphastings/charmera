// Package photos imports media into the macOS Photos app via AppleScript
// (osascript), placing it in a named album that is created on first use.
package photos

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// importScript imports the files passed as argv (after the album name) into the
// named album, creating the album if needed.
//
// "skip check duplicates" is essential: Photos remembers the fingerprints of
// images it has imported and refuses to re-import them even after they are
// deleted and purged from Recently Deleted. That persistent memory would
// override the user deleting a photo to re-import it. Our content-hash filename
// query is the sole dedup authority, so we bypass Photos' own check entirely —
// only files we have decided are new ever reach this import.
const importScript = `on run argv
	set albumName to item 1 of argv
	set mediaFiles to {}
	repeat with idx from 2 to (count of argv)
		set end of mediaFiles to (POSIX file (item idx of argv) as alias)
	end repeat
	tell application "Photos"
		if not (exists album albumName) then
			make new album named albumName
		end if
		import mediaFiles into album albumName with skip check duplicates
	end tell
end run`

// albumFilenamesScript returns the original filename of every media item in the
// album, one per line (empty if the album does not exist). These filenames are
// what makes Photos itself the dedup source of truth.
const albumFilenamesScript = `on run argv
	set albumName to item 1 of argv
	tell application "Photos"
		if not (exists album albumName) then return ""
		set out to ""
		repeat with mi in (get media items of album albumName)
			set out to out & (filename of mi) & linefeed
		end repeat
		return out
	end tell
end run`

// Importer adds files to a Photos album.
type Importer struct {
	Album     string
	osascript string
}

// New returns an Importer targeting the given album name.
func New(album string) *Importer {
	return &Importer{Album: album, osascript: "osascript"}
}

// Available reports whether the AppleScript runtime is present.
func (i *Importer) Available() error {
	if _, err := exec.LookPath(i.osascript); err != nil {
		return fmt.Errorf("osascript not found; Photos import requires macOS")
	}
	return nil
}

// args builds the osascript invocation: the script followed by the album name
// and each file path as run-handler arguments.
func (i *Importer) args(paths []string) []string {
	args := make([]string, 0, len(paths)+3)
	args = append(args, "-e", importScript, i.Album)
	args = append(args, paths...)
	return args
}

// AlbumFilenames returns the original filenames of the media items currently in
// the album (empty if the album does not exist yet).
func (i *Importer) AlbumFilenames(ctx context.Context) ([]string, error) {
	out, err := exec.CommandContext(ctx, i.osascript, "-e", albumFilenamesScript, i.Album).Output()
	if err != nil {
		return nil, fmt.Errorf("querying Photos album %q: %w", i.Album, err)
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

// Import adds the given files to the album. The first run may trigger a macOS
// permission prompt to allow controlling Photos.
func (i *Importer) Import(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	cmd := exec.CommandContext(ctx, i.osascript, i.args(paths)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("photos import failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
