package video

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jphastings/charmera/internal/config"
)

func TestBuildArgs_UsesConfigAndCreationTime(t *testing.T) {
	cfg := config.Default()
	c := New(cfg)
	when := time.Date(2026, 3, 3, 12, 16, 29, 0, time.UTC)

	args := c.buildArgs("in.avi", "out.mp4", when)
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"-i in.avi",
		"-c:v libx264",
		"-crf 18",
		"-preset medium",
		"-c:a aac",
		"-b:a 128k",
		"-ar 44100",
		"creation_time=2026-03-03T12:16:29",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q\ngot: %s", want, joined)
		}
	}
	if args[len(args)-1] != "out.mp4" {
		t.Errorf("output path should be last arg, got %q", args[len(args)-1])
	}
}

func TestAvailable_ReportsMissingBinaries(t *testing.T) {
	c := &Converter{FFmpeg: "definitely-not-ffmpeg-xyz", FFprobe: "definitely-not-ffprobe-xyz"}
	err := c.Available()
	if err == nil {
		t.Fatal("expected error when binaries are missing")
	}
	if !strings.Contains(err.Error(), "brew install ffmpeg") {
		t.Errorf("error should suggest installation, got: %v", err)
	}
}

func TestConvert_RealTranscode(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	cfg := config.Default()
	cfg.FFmpegPreset = "ultrafast" // keep the test quick
	c := New(cfg)
	if err := c.Available(); err != nil {
		t.Skip(err)
	}

	dir := t.TempDir()
	input := filepath.Join(dir, "in.avi")
	output := filepath.Join(dir, "out.mp4")
	ctx := context.Background()

	// Synthesize a Charmera-like AVI: Motion JPEG video + PCM audio.
	gen := exec.CommandContext(ctx, "ffmpeg", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:v", "mjpeg", "-c:a", "pcm_s16le", input)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generating test AVI: %v\n%s", err, out)
	}

	// Use a local-zone time: file mtimes are local, and ffmpeg stores tz-naive
	// metadata as UTC, so the instant must round-trip regardless of machine tz.
	when := time.Date(2026, 3, 3, 12, 16, 29, 0, time.Local)
	if err := c.Convert(ctx, input, output, when, nil); err != nil {
		t.Fatalf("convert: %v", err)
	}

	// Verify the output codecs and creation_time.
	probe, err := exec.CommandContext(ctx, "ffprobe", "-v", "quiet",
		"-show_entries", "stream=codec_name:format_tags=creation_time",
		"-of", "default=noprint_wrappers=1", output).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe output: %v\n%s", err, probe)
	}
	got := string(probe)
	if !strings.Contains(got, "h264") {
		t.Errorf("expected H.264 video stream, got:\n%s", got)
	}
	if !strings.Contains(got, "aac") {
		t.Errorf("expected AAC audio stream, got:\n%s", got)
	}

	stored, ok := extractCreationTime(got)
	if !ok {
		t.Fatalf("no creation_time metadata in:\n%s", got)
	}
	parsed, err := time.Parse("2006-01-02T15:04:05.999999Z07:00", stored)
	if err != nil {
		t.Fatalf("parse creation_time %q: %v", stored, err)
	}
	if !parsed.Equal(when) {
		t.Errorf("creation_time = %v, want instant %v", parsed, when)
	}
}

func extractCreationTime(probeOutput string) (string, bool) {
	for _, line := range strings.Split(probeOutput, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "TAG:creation_time="); ok {
			return v, true
		}
	}
	return "", false
}
