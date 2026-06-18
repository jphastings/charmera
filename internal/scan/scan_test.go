package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jphastings/charmera/internal/config"
)

// testVolume creates a temporary volume with a DCIM directory and returns the
// config (pointing VolumesDir at the temp root) and the volume + DCIM paths.
func testVolume(t *testing.T, name string) (config.Config, string, string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.VolumesDir = root
	volume := filepath.Join(root, name)
	dcim := filepath.Join(volume, cfg.DCIMSubdir)
	if err := os.MkdirAll(dcim, 0o755); err != nil {
		t.Fatal(err)
	}
	return cfg, volume, dcim
}

func write(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScan_ClassifiesAndSkips(t *testing.T) {
	_, volume, dcim := testVolume(t, "CARD")
	cfg := config.Default()
	write(t, filepath.Join(dcim, "PICT0001.jpg"), "jpeg-a")
	write(t, filepath.Join(dcim, "PICT0002.JPG"), "jpeg-b") // uppercase ext
	write(t, filepath.Join(dcim, "MOV0001.avi"), "avi-a")
	write(t, filepath.Join(dcim, "._PICT0001.jpg"), "appledouble") // skipped
	write(t, filepath.Join(dcim, "notes.txt"), "ignored")          // skipped

	files, err := Scan(cfg, volume)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("got %d files, want 3: %+v", len(files), files)
	}

	byName := map[string]File{}
	for _, f := range files {
		byName[f.Name] = f
	}
	if byName["PICT0001.jpg"].Kind != Photo {
		t.Error("jpg should be a photo")
	}
	if byName["PICT0002.JPG"].Kind != Photo {
		t.Error("uppercase JPG should be a photo")
	}
	if byName["MOV0001.avi"].Kind != Video {
		t.Error("avi should be a video")
	}
}

func TestScan_HashesDistinguishContent(t *testing.T) {
	cfg, volume, dcim := testVolume(t, "CARD")
	write(t, filepath.Join(dcim, "PICT0001.jpg"), "same")
	write(t, filepath.Join(dcim, "PICT0002.jpg"), "same")
	write(t, filepath.Join(dcim, "PICT0003.jpg"), "different")

	files, err := Scan(cfg, volume)
	if err != nil {
		t.Fatal(err)
	}
	if files[0].Hash != files[1].Hash {
		t.Error("identical content should hash equal (enables dedup across renamed files)")
	}
	if files[0].Hash == files[2].Hash {
		t.Error("different content should hash differently")
	}
}

func TestFindCamera_DetectsBySPIDCIMRegardlessOfName(t *testing.T) {
	cfg, volume, _ := testVolume(t, "RENAMED-BY-USER")
	if err := os.MkdirAll(filepath.Join(volume, "SPIDCIM"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, ok := FindCamera(cfg)
	if !ok || got != volume {
		t.Errorf("FindCamera = (%q,%v), want (%q,true)", got, ok, volume)
	}
}

func TestFindCamera_DetectsByGPEncoderComment(t *testing.T) {
	cfg, volume, dcim := testVolume(t, "ANYTHING")
	// A JPEG carrying the Generalplus marker, no SPIDCIM present.
	write(t, filepath.Join(dcim, "PICT0001.jpg"), "\xff\xd8\xff\xfe\x00\x0bGPEncoder rest-of-file")

	got, ok := FindCamera(cfg)
	if !ok || got != volume {
		t.Errorf("FindCamera = (%q,%v), want (%q,true)", got, ok, volume)
	}
}

func TestFindCamera_IgnoresUnrelatedVolumes(t *testing.T) {
	cfg, _, dcim := testVolume(t, "JUST-AN-SD-CARD")
	// Has DCIM but no Charmera signature.
	write(t, filepath.Join(dcim, "IMG_1234.jpg"), "an ordinary jpeg")

	if got, ok := FindCamera(cfg); ok {
		t.Errorf("non-Charmera DCIM volume should not be detected, got %q", got)
	}
}

func TestFindCamera_HonoursExplicitVolumeName(t *testing.T) {
	cfg, _, _ := testVolume(t, "WEIRD")
	cfg.VolumeName = "WEIRD" // manual override, no signature required

	got, ok := FindCamera(cfg)
	if !ok || filepath.Base(got) != "WEIRD" {
		t.Errorf("explicit --volume should be honoured, got (%q,%v)", got, ok)
	}
}
