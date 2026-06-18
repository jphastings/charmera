package photos

import (
	"strings"
	"testing"
)

func TestArgs_PassesAlbumThenPaths(t *testing.T) {
	i := New("Kodak Charmera")
	args := i.args([]string{"/tmp/a.jpg", "/tmp/b.mp4"})

	if args[0] != "-e" {
		t.Fatalf("first arg should be -e, got %q", args[0])
	}
	// After the script come the run-handler arguments: album name, then paths.
	if args[2] != "Kodak Charmera" {
		t.Errorf("album name should follow script, got %q", args[2])
	}
	if args[3] != "/tmp/a.jpg" || args[4] != "/tmp/b.mp4" {
		t.Errorf("file paths not passed in order: %v", args[3:])
	}
}

func TestImportScript_CreatesAlbumAndImports(t *testing.T) {
	for _, want := range []string{
		"if not (exists album albumName)",
		"make new album named albumName",
		"import mediaFiles into album albumName with skip check duplicates",
		"POSIX file",
	} {
		if !strings.Contains(importScript, want) {
			t.Errorf("import script missing %q", want)
		}
	}
}

func TestAlbumFilenamesScript_ReadsFilenames(t *testing.T) {
	for _, want := range []string{
		"if not (exists album albumName) then return",
		"media items of album albumName",
		"filename of mi",
	} {
		if !strings.Contains(albumFilenamesScript, want) {
			t.Errorf("album-filenames script missing %q", want)
		}
	}
}
