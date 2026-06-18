package exiffix

import (
	"encoding/binary"
	"strings"
)

// exifInfo holds the subset of EXIF we read from a source file. Every field is
// best-effort: a corrupt or out-of-range structure simply leaves fields empty
// rather than producing an error, because we never trust the camera's IFD.
type exifInfo struct {
	present           bool
	orientation       int
	make              string
	model             string
	modifyDate        string
	dateTimeOriginal  string
	dateTimeDigitized string
}

// readEXIF parses the TIFF block that follows the "Exif\0\0" identifier. It only
// extracts the handful of tags we care about and ignores everything else
// (notably the MakerNote, whose offset the Charmera corrupts).
func readEXIF(tiff []byte) exifInfo {
	var info exifInfo
	if len(tiff) < 8 {
		return info
	}

	var bo binary.ByteOrder
	switch string(tiff[0:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return info
	}
	if bo.Uint16(tiff[2:4]) != 0x2A {
		return info
	}
	info.present = true

	ifd0Off := int(bo.Uint32(tiff[4:8]))
	readDir(tiff, bo, ifd0Off, &info, false)
	return info
}

// readDir reads one IFD. When inExif is true it looks for ExifIFD-specific date
// tags; otherwise it reads IFD0 tags and follows the ExifIFD pointer once.
func readDir(tiff []byte, bo binary.ByteOrder, off int, info *exifInfo, inExif bool) {
	if off < 0 || off+2 > len(tiff) {
		return
	}
	count := int(bo.Uint16(tiff[off : off+2]))
	pos := off + 2
	for i := 0; i < count; i++ {
		if pos+12 > len(tiff) {
			return
		}
		tag := bo.Uint16(tiff[pos : pos+2])
		typ := bo.Uint16(tiff[pos+2 : pos+4])
		cnt := int(bo.Uint32(tiff[pos+4 : pos+8]))
		valField := tiff[pos+8 : pos+12]
		pos += 12

		switch {
		case !inExif && tag == tagOrientation && typ == typeShort:
			info.orientation = int(bo.Uint16(valField[0:2]))
		case !inExif && tag == tagMake:
			info.make = readASCII(tiff, bo, valField, typ, cnt)
		case !inExif && tag == tagModel:
			info.model = readASCII(tiff, bo, valField, typ, cnt)
		case !inExif && tag == tagDateTime:
			info.modifyDate = readASCII(tiff, bo, valField, typ, cnt)
		case !inExif && tag == tagExifIFD && typ == typeLong:
			readDir(tiff, bo, int(bo.Uint32(valField)), info, true)
		case inExif && tag == tagDateTimeOrig:
			info.dateTimeOriginal = readASCII(tiff, bo, valField, typ, cnt)
		case inExif && tag == tagDateTimeDigi:
			info.dateTimeDigitized = readASCII(tiff, bo, valField, typ, cnt)
		}
	}
}

func readASCII(tiff []byte, bo binary.ByteOrder, valField []byte, typ uint16, cnt int) string {
	if typ != typeASCII || cnt <= 0 {
		return ""
	}
	var raw []byte
	if cnt <= 4 {
		raw = valField[:cnt]
	} else {
		off := int(bo.Uint32(valField))
		if off < 0 || off+cnt > len(tiff) {
			return ""
		}
		raw = tiff[off : off+cnt]
	}
	return strings.Trim(string(raw), "\x00 ")
}
