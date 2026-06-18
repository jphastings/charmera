// Package exiffix repairs the broken EXIF that Kodak Charmera (Generalplus
// CBB3) cameras produce, entirely in pure Go. It fixes the malformed date
// format, sets the EXIF pixel dimensions to the image's true size, and discards
// the corrupt MakerNote/IFD by rewriting a fresh, minimal EXIF segment. Image
// (pixel) data is preserved byte-for-byte.
package exiffix

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// exifDateLayout is the standard EXIF datetime format.
const exifDateLayout = "2006:01:02 15:04:05"

var (
	validDateRe     = regexp.MustCompile(`^\d{4}:\d{2}:\d{2} \d{2}:\d{2}:\d{2}$`)
	malformedDateRe = regexp.MustCompile(`^(\d{4}):(\d{2}):(\d{2}):(\d{2}):(\d{2}):(\d{2})$`)
)

// Result reports what was written, for logging and dry-run previews.
type Result struct {
	Width          int
	Height         int
	Orientation    int
	DateTime       string
	DateSource     string // "exif" when taken from a usable EXIF tag, else "mtime"
	ExifWasPresent bool
}

// Fix rewrites the EXIF of a JPEG. It reads the true dimensions from the frame
// header, preserves Orientation/Make/Model when present, normalizes the capture
// date (falling back to the supplied time when no usable EXIF date exists), and
// returns the corrected JPEG bytes.
func Fix(data []byte, fallback time.Time) ([]byte, Result, error) {
	return FixWithOrientation(data, fallback, 0)
}

// FixWithOrientation is like Fix but, when orientationOverride is a valid EXIF
// Orientation value (1, 3, 6 or 8), uses it instead of the source's orientation.
// This is how a content-based orientation detector feeds its result in. An
// override of 0 means "no override" (use the source orientation, else normal).
func FixWithOrientation(data []byte, fallback time.Time, orientationOverride int) ([]byte, Result, error) {
	layout, err := scanJPEG(data)
	if err != nil {
		return nil, Result{}, err
	}
	if layout.width == 0 || layout.height == 0 {
		return nil, Result{}, fmt.Errorf("could not read image dimensions from JPEG frame")
	}

	var info exifInfo
	if layout.exifFound {
		payload := data[layout.exifStart+4 : layout.exifEnd]
		if len(payload) >= 6 {
			info = readEXIF(payload[6:]) // skip "Exif\0\0"
		}
	}

	dateTime, source := pickDate(info, fallback)
	orientation := info.orientation
	if orientationOverride > 0 {
		orientation = orientationOverride
	}
	if orientation < 1 {
		orientation = 1
	}

	payload := encodeEXIF(exifData{
		make:          orDefault(info.make, cameraMake),
		model:         orDefault(info.model, cameraModel),
		orientation:   orientation,
		width:         layout.width,
		height:        layout.height,
		dateTime:      dateTime,
		lensMake:      lensMake,
		lensModel:     lensModel,
		fNumberNum:    fNumberNum,
		fNumberDen:    fNumberDen,
		focalLength35: focalLength35mm,
	})
	if len(payload)+2 > 0xFFFF {
		return nil, Result{}, fmt.Errorf("rebuilt EXIF segment too large (%d bytes)", len(payload))
	}

	out := replaceEXIF(data, layout, payload)
	return out, Result{
		Width:          layout.width,
		Height:         layout.height,
		Orientation:    orientation,
		DateTime:       dateTime,
		DateSource:     source,
		ExifWasPresent: info.present,
	}, nil
}

// pickDate chooses the best capture time. It prefers a usable EXIF date
// (DateTimeOriginal, then DateTimeDigitized, then ModifyDate), normalizing the
// Charmera's malformed "YYYY:MM:DD:HH:MM:SS" form. With no usable EXIF date it
// falls back to the file's modification time.
func pickDate(info exifInfo, fallback time.Time) (string, string) {
	for _, raw := range []string{info.dateTimeOriginal, info.dateTimeDigitized, info.modifyDate} {
		if fixed, ok := normalizeExifDate(raw); ok {
			return fixed, "exif"
		}
	}
	return fallback.Format(exifDateLayout), "mtime"
}

// normalizeExifDate returns a standard-form EXIF datetime and whether the input
// was usable. It accepts already-valid dates and repairs the Charmera's
// extra-colon form, rejecting empty/zero dates.
func normalizeExifDate(raw string) (string, bool) {
	raw = strings.Trim(raw, "\x00 ")
	if strings.HasPrefix(raw, "0000") {
		return "", false
	}
	if validDateRe.MatchString(raw) {
		return raw, true
	}
	if m := malformedDateRe.FindStringSubmatch(raw); m != nil {
		return fmt.Sprintf("%s:%s:%s %s:%s:%s", m[1], m[2], m[3], m[4], m[5], m[6]), true
	}
	return "", false
}
