package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModelIconPath(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"red.png", "grey.png"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fake png"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if got := modelIconPath(dir, "red"); got != filepath.Join(dir, "red.png") {
		t.Errorf("modelIconPath(red) = %q, want the red.png path", got)
	}
	if got := modelIconPath(dir, " Red\n"); got != filepath.Join(dir, "red.png") {
		t.Errorf("modelIconPath(%q) = %q, want the red.png path (trimmed/lower-cased)", " Red\n", got)
	}
	if got := modelIconPath(dir, "gray"); got != filepath.Join(dir, "grey.png") {
		t.Errorf(`modelIconPath("gray") = %q, want the grey.png path`, got)
	}
	if got := modelIconPath(dir, "purple"); got != "" {
		t.Errorf("modelIconPath(unrecognised colour) = %q, want empty", got)
	}
	if got := modelIconPath(dir, ""); got != "" {
		t.Errorf("modelIconPath(empty model) = %q, want empty", got)
	}
	if got := modelIconPath(dir, "blue"); got != "" {
		t.Errorf("modelIconPath(colour with no matching file) = %q, want empty", got)
	}
}
