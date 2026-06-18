package pipeline

import (
	"testing"
	"time"

	"github.com/jphastings/charmera/internal/scan"
)

func TestDestName_EmbedsExtractableHashKey(t *testing.T) {
	f := scan.File{
		Name:    "PICT0001.jpg",
		Kind:    scan.Photo,
		ModTime: time.Date(2026, 6, 15, 13, 47, 8, 0, time.UTC),
		Hash:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}

	name := destName(f)
	if name != "IMG_20260615_134708_0123456789abcdef.jpg" {
		t.Fatalf("destName = %q", name)
	}

	// The key must round-trip out of the filename Photos stores.
	key, ok := keyFromName(name)
	if !ok {
		t.Fatal("keyFromName failed to extract a key")
	}
	if key != hashKey(f.Hash) {
		t.Errorf("extracted key %q != hashKey %q", key, hashKey(f.Hash))
	}
}

func TestKeyFromName_IgnoresForeignFilenames(t *testing.T) {
	for _, name := range []string{
		"IMG_4321.JPG",            // a user's own camera file
		"vacation.png",            // arbitrary
		"IMG_20260615_134708.jpg", // our old (pre-hash) scheme
		"VID_20260615_134708_notvalidhexxx.mp4",
	} {
		if _, ok := keyFromName(name); ok {
			t.Errorf("keyFromName(%q) should not match", name)
		}
	}
}

func TestDestName_VideoBecomesMP4(t *testing.T) {
	f := scan.File{
		Name:    "MOV0001.avi",
		Kind:    scan.Video,
		ModTime: time.Date(2026, 6, 15, 13, 47, 8, 0, time.UTC),
		Hash:    "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	}
	name := destName(f)
	if name != "VID_20260615_134708_abcdef0123456789.mp4" {
		t.Errorf("destName = %q, want a VID_*.mp4 name", name)
	}
}
