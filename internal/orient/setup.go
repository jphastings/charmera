package orient

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const (
	// ModelFilename is the on-disk name of the orientation model.
	ModelFilename = "orientation_model_v2_0.9882.onnx"
	modelURL      = "https://huggingface.co/DuarteBarbosa/deep-image-orientation-detection/resolve/main/" + ModelFilename
	modelSHA256   = "cffe911c1dff47fbfbbd90110aaab9c07134645c460d35b3ae8832079bea91ba"
)

// libCandidates are the usual Homebrew locations for libonnxruntime.
var libCandidates = []string{
	"/opt/homebrew/lib/libonnxruntime.dylib",
	"/usr/local/lib/libonnxruntime.dylib",
}

// LibraryPath returns the path to libonnxruntime, or "" if it isn't installed.
// The CHARMERA_ONNXRUNTIME_LIB environment variable overrides the search.
func LibraryPath() string {
	if env := os.Getenv("CHARMERA_ONNXRUNTIME_LIB"); env != "" {
		if fileExists(env) {
			return env
		}
		return ""
	}
	for _, p := range libCandidates {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

// EnsureModel returns the path to the model in dir, downloading it (once,
// checksum-verified) if it isn't already there. progress, if non-nil, is called
// with a human-readable note when a download is needed.
func EnsureModel(dir string, progress func(string)) (string, error) {
	path := filepath.Join(dir, ModelFilename)
	if ok, _ := verifyChecksum(path, modelSHA256); ok {
		return path, nil
	}
	if progress != nil {
		progress("downloading orientation model (~77 MB, one-time)")
	}
	if err := download(modelURL, path); err != nil {
		return "", fmt.Errorf("downloading model: %w", err)
	}
	if ok, got := verifyChecksum(path, modelSHA256); !ok {
		os.Remove(path)
		return "", fmt.Errorf("model checksum mismatch: got %s", got)
	}
	return path, nil
}

func download(url, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dest), ModelFilename+".*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, dest)
}

func verifyChecksum(path, want string) (bool, string) {
	f, err := os.Open(path)
	if err != nil {
		return false, ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, ""
	}
	got := hex.EncodeToString(h.Sum(nil))
	return got == want, got
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
