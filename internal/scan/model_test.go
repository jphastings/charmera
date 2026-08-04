package scan

import (
	"path/filepath"
	"testing"
)

func TestReadModel_TrimsAndLowercases(t *testing.T) {
	_, volume, _ := testVolume(t, "CARD")
	write(t, filepath.Join(volume, modelFileName), "Red\n")

	if got := ReadModel(volume); got != "red" {
		t.Errorf("ReadModel = %q, want %q", got, "red")
	}
}

func TestReadModel_MissingFileReturnsEmpty(t *testing.T) {
	_, volume, _ := testVolume(t, "CARD")

	if got := ReadModel(volume); got != "" {
		t.Errorf("ReadModel with no charmera.model = %q, want empty", got)
	}
}
