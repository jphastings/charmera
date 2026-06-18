package exiffix

import (
	"encoding/binary"
	"errors"
)

var errNotJPEG = errors.New("not a JPEG (missing SOI marker)")

// jpegLayout describes where the EXIF segment is (or should go) and the image's
// true pixel dimensions read from the Start-Of-Frame marker.
type jpegLayout struct {
	width  int
	height int
	// exifStart..exifEnd delimit an existing APP1/Exif segment. When they are
	// equal, no Exif segment exists and that offset is the insertion point.
	exifStart int
	exifEnd   int
	exifFound bool
}

// scanJPEG walks the marker segments up to the start of scan data. It returns
// the image dimensions and the location of any existing Exif APP1 segment, or
// the correct place to insert one.
func scanJPEG(data []byte) (jpegLayout, error) {
	var l jpegLayout
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		return l, errNotJPEG
	}

	// Default insertion point: immediately after SOI.
	l.exifStart, l.exifEnd = 2, 2

	pos := 2
	for pos+1 < len(data) {
		if data[pos] != 0xFF {
			break
		}
		marker := data[pos+1]

		// Standalone markers (no length field).
		if marker == 0xD9 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			pos += 2
			continue
		}
		// Start of scan: entropy-coded data follows; stop walking.
		if marker == 0xDA {
			break
		}
		if pos+4 > len(data) {
			break
		}
		segLen := int(binary.BigEndian.Uint16(data[pos+2 : pos+4]))
		segStart := pos
		segEnd := pos + 2 + segLen
		if segEnd > len(data) {
			break
		}
		payload := data[pos+4 : segEnd]

		switch {
		case marker == 0xE1 && len(payload) >= 6 && string(payload[0:6]) == "Exif\x00\x00":
			if !l.exifFound {
				l.exifStart, l.exifEnd = segStart, segEnd
				l.exifFound = true
			}
		case marker == 0xE0 && !l.exifFound:
			// Leading APP0/JFIF: prefer to insert Exif right after it.
			l.exifStart, l.exifEnd = segEnd, segEnd
		case isSOF(marker) && len(payload) >= 5:
			l.height = int(binary.BigEndian.Uint16(payload[1:3]))
			l.width = int(binary.BigEndian.Uint16(payload[3:5]))
		}

		pos = segEnd
	}

	return l, nil
}

// isSOF reports whether a marker is a Start-Of-Frame carrying dimensions.
// Excludes DHT (C4), JPG (C8) and DAC (CC).
func isSOF(marker byte) bool {
	if marker < 0xC0 || marker > 0xCF {
		return false
	}
	return marker != 0xC4 && marker != 0xC8 && marker != 0xCC
}

// replaceEXIF returns a copy of data with its Exif APP1 segment replaced by (or,
// if absent, inserted with) one wrapping the given payload ("Exif\0\0"+TIFF).
func replaceEXIF(data []byte, l jpegLayout, payload []byte) []byte {
	seg := make([]byte, 0, len(payload)+4)
	seg = append(seg, 0xFF, 0xE1)
	seg = append(seg, byte((len(payload)+2)>>8), byte(len(payload)+2))
	seg = append(seg, payload...)

	out := make([]byte, 0, len(data)-(l.exifEnd-l.exifStart)+len(seg))
	out = append(out, data[:l.exifStart]...)
	out = append(out, seg...)
	out = append(out, data[l.exifEnd:]...)
	return out
}
