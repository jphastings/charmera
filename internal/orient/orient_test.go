package orient

import (
	"bytes"
	"image"
	"os"
	"path/filepath"
	"testing"
)

func TestArgmaxSoftmax(t *testing.T) {
	idx, conf := argmaxSoftmax([]float32{0, 5, 0, 0})
	if idx != 1 {
		t.Errorf("argmax = %d, want 1", idx)
	}
	if conf < 0.9 {
		t.Errorf("confidence = %.3f, want a dominant class near 1", conf)
	}
}

func TestArgmaxSoftmax_Never180(t *testing.T) {
	// Even when the 180° class dominates the logits, it must never be selected,
	// nor counted in the confidence denominator.
	idx, conf := argmaxSoftmax([]float32{1, 0, 5, 0})
	if idx == class180 || classToOrientation[idx] == 3 {
		t.Errorf("180° must never be selected, got class %d", idx)
	}
	// Renormalised over {0,1,3}: class 0 wins with exp(1)/(exp(1)+exp(0)+exp(0)).
	if conf < 0.5 {
		t.Errorf("confidence should be renormalised over plausible classes, got %.3f", conf)
	}
}

func TestClassMapping(t *testing.T) {
	// 0°->1 (normal), 90°CW->6, 180°->3, 90°CCW->8.
	want := [4]int{1, 6, 3, 8}
	if classToOrientation != want {
		t.Errorf("classToOrientation = %v, want %v", classToOrientation, want)
	}
}

// loadDetector loads the real model, skipping the test when onnxruntime or the
// model file isn't available locally.
func loadDetector(t *testing.T) *Detector {
	t.Helper()
	lib := LibraryPath()
	if lib == "" {
		t.Skip("libonnxruntime not installed")
	}
	home, _ := os.UserHomeDir()
	model := filepath.Join(home, "Library", "Application Support", "charmera", "models", ModelFilename)
	if !fileExists(model) {
		t.Skip("orientation model not downloaded")
	}
	d, err := New(model, lib)
	if err != nil {
		t.Fatalf("load detector: %v", err)
	}
	t.Cleanup(d.Close)
	return d
}

func uprightImage(t *testing.T) image.Image {
	t.Helper()
	data, err := os.ReadFile("../exiffix/testdata/no_exif.jpg")
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// rotate90CW returns the image rotated 90° clockwise.
func rotate90CW(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(h-1-y, x, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func TestDetect_UprightIsNormal(t *testing.T) {
	d := loadDetector(t)
	res, err := d.Detect(uprightImage(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("upright: orientation=%d confidence=%.3f", res.Orientation, res.Confidence)
	if res.Orientation != 1 {
		t.Errorf("upright photo classified as orientation %d (confidence %.3f)", res.Orientation, res.Confidence)
	}
}

func TestDetect_RotatedNeedsCorrection(t *testing.T) {
	d := loadDetector(t)
	rotated := rotate90CW(uprightImage(t))
	res, err := d.Detect(rotated)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("rotated 90° CW: orientation=%d confidence=%.3f", res.Orientation, res.Confidence)
	if !res.Rotated {
		t.Errorf("rotated photo was classified as upright")
	}
	// Correcting a 90°-CW rotation needs a 90°-CCW turn -> EXIF Orientation 8.
	if res.Orientation != 8 {
		t.Errorf("rotated 90° CW -> orientation %d, want 8", res.Orientation)
	}
}
