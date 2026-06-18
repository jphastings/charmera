package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jphastings/charmera/internal/config"
)

// fakeConverter records conversions and writes a stub output file.
type fakeConverter struct {
	available error
	converted []string
}

func (f *fakeConverter) Available() error { return f.available }
func (f *fakeConverter) Convert(_ context.Context, _, output string, _ time.Time, _ func(float64)) error {
	f.converted = append(f.converted, output)
	return os.WriteFile(output, []byte("fake-mp4"), 0o644)
}

// fakeImporter models Photos: imported files become album members (by basename),
// which is what AlbumFilenames reports back as the dedup source of truth.
type fakeImporter struct {
	available error
	imported  [][]string
	album     []string
}

func (f *fakeImporter) Available() error { return f.available }
func (f *fakeImporter) AlbumFilenames(_ context.Context) ([]string, error) {
	return append([]string(nil), f.album...), nil
}
func (f *fakeImporter) Import(_ context.Context, paths []string) error {
	f.imported = append(f.imported, append([]string(nil), paths...))
	for _, p := range paths {
		f.album = append(f.album, filepath.Base(p))
	}
	return nil
}

// realJPEG is a minimal valid JPEG (the no-EXIF Charmera fixture).
func realJPEG(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../exiffix/testdata/no_exif.jpg")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// setup returns a config, the camera's volume path, and its DCIM directory.
func setup(t *testing.T) (config.Config, string, string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.StateDir = filepath.Join(root, "state")
	volume := filepath.Join(root, "volume")
	dcim := filepath.Join(volume, cfg.DCIMSubdir)
	if err := os.MkdirAll(dcim, 0o755); err != nil {
		t.Fatal(err)
	}
	return cfg, volume, dcim
}

func TestRun_ImportsAndKeepsNoOnDiskCopy(t *testing.T) {
	cfg, volume, dcim := setup(t)
	if err := os.WriteFile(filepath.Join(dcim, "PICT0001.jpg"), realJPEG(t), 0o644); err != nil {
		t.Fatal(err)
	}

	imp := &fakeImporter{}
	sum, err := New(cfg, volume, &fakeConverter{}, imp, Options{}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if sum.Scanned != 1 || sum.PhotosFixed != 1 || sum.Imported != 1 {
		t.Errorf("unexpected summary: %+v", sum)
	}
	if len(imp.imported) != 1 || len(imp.imported[0]) != 1 {
		t.Fatalf("expected one batched import of one file, got %v", imp.imported)
	}
	// Nothing is left on disk: the staging area is gone after the run.
	if exists(cfg.StagingDir()) {
		t.Errorf("staging dir should be removed after import: %s", cfg.StagingDir())
	}
	// The imported filename carries an extractable hash key (the dedup record).
	if _, ok := keyFromName(imp.album[0]); !ok {
		t.Errorf("imported filename %q lacks a hash key", imp.album[0])
	}
}

func TestRun_SkipsAlreadyInPhotos(t *testing.T) {
	cfg, volume, dcim := setup(t)
	if err := os.WriteFile(filepath.Join(dcim, "PICT0001.jpg"), realJPEG(t), 0o644); err != nil {
		t.Fatal(err)
	}

	imp := &fakeImporter{} // shared across runs: the album persists
	if _, err := New(cfg, volume, &fakeConverter{}, imp, Options{}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	sum, err := New(cfg, volume, &fakeConverter{}, imp, Options{}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sum.Skipped != 1 || sum.PhotosFixed != 0 {
		t.Errorf("second run should skip what Photos already holds: %+v", sum)
	}
	if len(imp.imported) != 1 {
		t.Errorf("file should only have been imported once, got %d batches", len(imp.imported))
	}
}

func TestRun_ReimportsAfterDeletedFromPhotos(t *testing.T) {
	cfg, volume, dcim := setup(t)
	if err := os.WriteFile(filepath.Join(dcim, "PICT0001.jpg"), realJPEG(t), 0o644); err != nil {
		t.Fatal(err)
	}

	imp := &fakeImporter{}
	if _, err := New(cfg, volume, &fakeConverter{}, imp, Options{}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Simulate the user deleting everything from the Photos album.
	imp.album = nil

	sum, err := New(cfg, volume, &fakeConverter{}, imp, Options{}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sum.Skipped != 0 || sum.Imported != 1 {
		t.Errorf("removing the photo from Photos should re-enable import: %+v", sum)
	}
	if len(imp.imported) != 2 {
		t.Errorf("expected a second import after deletion, got %d batches", len(imp.imported))
	}
}

func TestRun_RenamedDuplicateContentIsSkipped(t *testing.T) {
	cfg, volume, dcim := setup(t)
	img := realJPEG(t)
	if err := os.WriteFile(filepath.Join(dcim, "PICT0001.jpg"), img, 0o644); err != nil {
		t.Fatal(err)
	}
	imp := &fakeImporter{}
	if _, err := New(cfg, volume, &fakeConverter{}, imp, Options{}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Same content, different filename (camera reused/renumbered) — must dedup.
	if err := os.WriteFile(filepath.Join(dcim, "PICT9999.jpg"), img, 0o644); err != nil {
		t.Fatal(err)
	}
	sum, err := New(cfg, volume, &fakeConverter{}, imp, Options{}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Both the original and the renamed copy hash-match what Photos holds.
	if sum.Skipped != 2 || sum.PhotosFixed != 0 {
		t.Errorf("identical content under a new name should be skipped by hash: %+v", sum)
	}
}

func TestRun_VideoSkippedWhenFFmpegMissing(t *testing.T) {
	cfg, volume, dcim := setup(t)
	if err := os.WriteFile(filepath.Join(dcim, "MOV0001.avi"), []byte("avi-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	conv := &fakeConverter{available: errMissing("ffmpeg")}
	imp := &fakeImporter{}

	sum, err := New(cfg, volume, conv, imp, Options{}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sum.VideosConverted != 0 {
		t.Errorf("video should be skipped without ffmpeg: %+v", sum)
	}
	if len(imp.imported) != 0 {
		t.Errorf("nothing should be imported, got %v", imp.imported)
	}
}

func TestRun_ConvertsVideoAndImports(t *testing.T) {
	cfg, volume, dcim := setup(t)
	if err := os.WriteFile(filepath.Join(dcim, "MOV0001.avi"), []byte("avi-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	imp := &fakeImporter{}

	sum, err := New(cfg, volume, &fakeConverter{}, imp, Options{}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sum.VideosConverted != 1 || sum.Imported != 1 {
		t.Errorf("expected one converted+imported video: %+v", sum)
	}
	if got := imp.imported[0][0]; filepath.Ext(got) != ".mp4" {
		t.Errorf("expected an .mp4 to be imported, got %q", got)
	}
}

func TestRun_OutDirWritesFilesWithoutImporting(t *testing.T) {
	cfg, volume, dcim := setup(t)
	if err := os.WriteFile(filepath.Join(dcim, "PICT0001.jpg"), realJPEG(t), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(t.TempDir(), "out")
	imp := &fakeImporter{}

	sum, err := New(cfg, volume, &fakeConverter{}, imp, Options{OutDir: outDir}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sum.PhotosFixed != 1 {
		t.Errorf("expected the photo to be fixed: %+v", sum)
	}
	if len(imp.imported) != 0 {
		t.Error("--out must not import into Photos")
	}
	if files, _ := filepath.Glob(filepath.Join(outDir, "IMG_*.jpg")); len(files) != 1 {
		t.Errorf("expected one fixed file in OutDir, got %v", files)
	}
}

func TestRun_DryRunChangesNothing(t *testing.T) {
	cfg, volume, dcim := setup(t)
	if err := os.WriteFile(filepath.Join(dcim, "PICT0001.jpg"), realJPEG(t), 0o644); err != nil {
		t.Fatal(err)
	}
	imp := &fakeImporter{}
	sum, err := New(cfg, volume, &fakeConverter{}, imp, Options{DryRun: true}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sum.PhotosFixed != 1 {
		t.Errorf("dry run should still plan the photo: %+v", sum)
	}
	if len(imp.imported) != 0 {
		t.Error("dry run must not import")
	}
	if exists(cfg.StagingDir()) {
		t.Error("dry run must not create the staging directory")
	}
}

type errMissing string

func (e errMissing) Error() string { return string(e) + " missing" }

// fakeDetector returns a fixed orientation/confidence for every photo.
type fakeDetector struct {
	orientation int
	confidence  float64
}

func (f fakeDetector) DetectJPEG([]byte) (int, float64, error) {
	return f.orientation, f.confidence, nil
}

func fixOrientation(t *testing.T, det OrientationDetector, minConf float64) string {
	t.Helper()
	cfg, volume, dcim := setup(t)
	if err := os.WriteFile(filepath.Join(dcim, "PICT0001.jpg"), realJPEG(t), 0o644); err != nil {
		t.Fatal(err)
	}
	var fixDetail string
	obs := func(e Event) {
		if e.Phase == "fix" {
			fixDetail = e.Detail
		}
	}
	_, err := New(cfg, volume, &fakeConverter{}, &fakeImporter{}, Options{
		OutDir: filepath.Join(t.TempDir(), "out"), Observer: obs,
		Detector: det, MinConfidence: minConf,
	}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return fixDetail
}

func TestRun_AppliesConfidentRotation(t *testing.T) {
	if got := fixOrientation(t, fakeDetector{orientation: 8, confidence: 0.95}, 0.90); !strings.Contains(got, "orient=8") {
		t.Errorf("expected detected orientation 8 to be applied, got %q", got)
	}
}

func TestRun_IgnoresLowConfidenceRotation(t *testing.T) {
	if got := fixOrientation(t, fakeDetector{orientation: 8, confidence: 0.50}, 0.90); !strings.Contains(got, "orient=1") {
		t.Errorf("low-confidence rotation should be ignored (orient=1), got %q", got)
	}
}

func TestRun_IgnoresUprightPrediction(t *testing.T) {
	if got := fixOrientation(t, fakeDetector{orientation: 1, confidence: 0.99}, 0.90); !strings.Contains(got, "orient=1") {
		t.Errorf("upright prediction should leave orient=1, got %q", got)
	}
}
