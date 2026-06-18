//go:build !darwin

package volume

import "errors"

// Unmount is only implemented on macOS.
func Unmount(volumePath string) error {
	return errors.New("volume unmount is only supported on macOS")
}
