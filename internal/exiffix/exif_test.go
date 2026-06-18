package exiffix

import (
	"bytes"
	"image"
	_ "image/jpeg"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestNormalizeExifDate(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"2026:03:03:12:16:29", "2026:03:03 12:16:29", true}, // Charmera malformed form
		{"2026:03:03 12:16:29", "2026:03:03 12:16:29", true}, // already valid
		{"0000:00:00 00:00:00", "", false},                   // empty/zero
		{"", "", false},
		{"garbage", "", false},
	}
	for _, c := range cases {
		got, ok := normalizeExifDate(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("normalizeExifDate(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// fallbackTime is a fixed capture time used for date-less fixtures.
var fallbackTime = time.Date(2026, 3, 3, 12, 16, 29, 0, time.UTC)

func TestFix_NoExifFile_AddsDateFromFallbackAndKeepsDimensions(t *testing.T) {
	data := readFixture(t, "no_exif.jpg")

	out, res, err := Fix(data, fallbackTime)
	if err != nil {
		t.Fatal(err)
	}

	if res.ExifWasPresent {
		t.Error("fixture was expected to have no EXIF")
	}
	if res.DateSource != "mtime" {
		t.Errorf("DateSource = %q, want mtime", res.DateSource)
	}
	if res.DateTime != "2026:03:03 12:16:29" {
		t.Errorf("DateTime = %q", res.DateTime)
	}
	if res.Width != 1440 || res.Height != 1080 {
		t.Errorf("dimensions = %dx%d, want 1440x1080", res.Width, res.Height)
	}

	assertDecodableSameSize(t, data, out)
	assertRoundTrips(t, out, "2026:03:03 12:16:29", 1)
}

func TestFix_PreservesOrientation(t *testing.T) {
	// A normal Charmera file has no Orientation, but if one is present (e.g. the
	// shot was rotated in-camera) Fix must preserve it. Construct such a file
	// in-code rather than ship an unrepresentative fixture.
	base := readFixture(t, "no_exif.jpg")
	layout, err := scanJPEG(base)
	if err != nil {
		t.Fatal(err)
	}
	withOrientation := replaceEXIF(base, layout, encodeEXIF(exifData{
		orientation: 6, width: layout.width, height: layout.height,
		dateTime: "2026:03:03 12:16:29",
	}))

	out, res, err := Fix(withOrientation, fallbackTime)
	if err != nil {
		t.Fatal(err)
	}
	if res.Orientation != 6 {
		t.Errorf("Orientation = %d, want 6 (preserved)", res.Orientation)
	}
	assertRoundTrips(t, out, "2026:03:03 12:16:29", 6)
}

func TestFix_MalformedDateIsRepaired(t *testing.T) {
	// Build a JPEG carrying the Charmera's malformed date, then fix it.
	base := readFixture(t, "no_exif.jpg")
	layout, err := scanJPEG(base)
	if err != nil {
		t.Fatal(err)
	}
	withBadDate := replaceEXIF(base, layout, encodeEXIF(exifData{
		orientation: 1, width: layout.width, height: layout.height,
		dateTime: "2026:03:03:12:16:29", // malformed on purpose
	}))

	_, res, err := Fix(withBadDate, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.DateSource != "exif" {
		t.Errorf("DateSource = %q, want exif (date should be read, not fallback)", res.DateSource)
	}
	if res.DateTime != "2026:03:03 12:16:29" {
		t.Errorf("DateTime = %q, want repaired", res.DateTime)
	}
}

func TestFix_EmbedsCameraIdentity(t *testing.T) {
	out, _, err := Fix(readFixture(t, "no_exif.jpg"), fallbackTime)
	if err != nil {
		t.Fatal(err)
	}

	// Re-read Make/Model with our own parser.
	layout, _ := scanJPEG(out)
	info := readEXIF(out[layout.exifStart+4+6 : layout.exifEnd])
	if info.make != "Kodak" || info.model != "Charmera" {
		t.Errorf("Make/Model = %q/%q, want Kodak/Charmera", info.make, info.model)
	}

	exiftool, err := exec.LookPath("exiftool")
	if err != nil {
		t.Skip("exiftool not found")
	}
	tmp, _ := os.CreateTemp(t.TempDir(), "*.jpg")
	tmp.Write(out)
	tmp.Close()

	got, err := exec.Command(exiftool, "-s3",
		"-FNumber", "-FocalLengthIn35mmFormat", "-LensModel", tmp.Name()).CombinedOutput()
	if err != nil {
		t.Fatalf("exiftool: %v\n%s", err, got)
	}
	for _, want := range []string{"2.4", "35", "Charmera"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("expected %q in lens metadata:\n%s", want, got)
		}
	}
}

func TestFix_RejectsNonJPEG(t *testing.T) {
	if _, _, err := Fix([]byte("not a jpeg"), time.Now()); err == nil {
		t.Error("expected error for non-JPEG input")
	}
}

// --- helpers ---

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func assertDecodableSameSize(t *testing.T, before, after []byte) {
	t.Helper()
	cfgBefore, _, err := image.DecodeConfig(bytes.NewReader(before))
	if err != nil {
		t.Fatalf("decode original: %v", err)
	}
	cfgAfter, _, err := image.DecodeConfig(bytes.NewReader(after))
	if err != nil {
		t.Fatalf("decode fixed output: %v", err)
	}
	if cfgBefore != cfgAfter {
		t.Errorf("dimensions changed: %v -> %v", cfgBefore, cfgAfter)
	}
	if _, _, err := image.Decode(bytes.NewReader(after)); err != nil {
		t.Errorf("fixed output is not a decodable JPEG: %v", err)
	}
}

// assertRoundTrips re-parses our own output and, when exiftool is available,
// validates the EXIF with an independent tool.
func assertRoundTrips(t *testing.T, out []byte, wantDate string, wantOrientation int) {
	t.Helper()
	layout, err := scanJPEG(out)
	if err != nil || !layout.exifFound {
		t.Fatalf("scan output: err=%v found=%v", err, layout.exifFound)
	}
	info := readEXIF(out[layout.exifStart+4+6 : layout.exifEnd])
	if info.dateTimeOriginal != wantDate {
		t.Errorf("re-read DateTimeOriginal = %q, want %q", info.dateTimeOriginal, wantDate)
	}
	if info.orientation != wantOrientation {
		t.Errorf("re-read Orientation = %d, want %d", info.orientation, wantOrientation)
	}

	exiftool, err := exec.LookPath("exiftool")
	if err != nil {
		t.Log("exiftool not found; skipping independent EXIF validation")
		return
	}
	tmp, err := os.CreateTemp(t.TempDir(), "*.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmp.Write(out); err != nil {
		t.Fatal(err)
	}
	tmp.Close()

	got, err := exec.Command(exiftool, "-validate", "-warning", "-error", "-s3", tmp.Name()).CombinedOutput()
	if err != nil {
		t.Fatalf("exiftool: %v\n%s", err, got)
	}
	// Our rebuilt EXIF should be spec-complete: no warnings or errors.
	if !strings.Contains(string(got), "OK") || strings.Contains(string(got), "Warning") {
		t.Errorf("exiftool validation was not clean:\n%s", got)
	}
}
