//go:build darwin

// Package volume manages the camera's mounted volume.
package volume

/*
#cgo LDFLAGS: -framework DiskArbitration -framework CoreFoundation
#include <DiskArbitration/DiskArbitration.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
	int   done;
	char *err; // NULL on success; otherwise a malloc'd message
} unmountResult;

static void onUnmount(DADiskRef disk, DADissenterRef dissenter, void *context) {
	unmountResult *res = (unmountResult *)context;
	if (dissenter != NULL) {
		CFStringRef s = DADissenterGetStatusString(dissenter);
		if (s != NULL) {
			CFIndex max = CFStringGetMaximumSizeForEncoding(CFStringGetLength(s), kCFStringEncodingUTF8) + 1;
			res->err = (char *)malloc(max);
			if (!CFStringGetCString(s, res->err, max, kCFStringEncodingUTF8)) {
				free(res->err);
				res->err = strdup("unmount refused");
			}
		} else {
			res->err = strdup("unmount refused");
		}
	}
	res->done = 1;
	CFRunLoopStop(CFRunLoopGetCurrent());
}

// charmera_unmount unmounts the volume at path, returning NULL on success or a
// malloc'd error string that the caller must free. When force is non-zero a
// forced unmount is requested (used as a last resort when a normal unmount keeps
// being refused as "busy" — safe here because the camera is only ever read).
static char *charmera_unmount(const char *path, int force) {
	DASessionRef session = DASessionCreate(kCFAllocatorDefault);
	if (session == NULL) {
		return strdup("could not create DiskArbitration session");
	}

	CFURLRef url = CFURLCreateFromFileSystemRepresentation(
		kCFAllocatorDefault, (const UInt8 *)path, strlen(path), true);
	if (url == NULL) {
		CFRelease(session);
		return strdup("invalid volume path");
	}

	DADiskRef disk = DADiskCreateFromVolumePath(kCFAllocatorDefault, session, url);
	if (disk == NULL) {
		CFRelease(url);
		CFRelease(session);
		return strdup("no disk found at volume path");
	}

	DASessionScheduleWithRunLoop(session, CFRunLoopGetCurrent(), kCFRunLoopDefaultMode);

	DADiskUnmountOptions opts = force ? kDADiskUnmountOptionForce : kDADiskUnmountOptionDefault;
	unmountResult res = {0, NULL};
	DADiskUnmount(disk, opts, onUnmount, &res);

	SInt32 r;
	do {
		r = CFRunLoopRunInMode(kCFRunLoopDefaultMode, 10.0, false);
	} while (!res.done && r != kCFRunLoopRunTimedOut && r != kCFRunLoopRunFinished);

	DASessionUnscheduleFromRunLoop(session, CFRunLoopGetCurrent(), kCFRunLoopDefaultMode);
	CFRelease(disk);
	CFRelease(url);
	CFRelease(session);

	if (!res.done) {
		return strdup("unmount timed out");
	}
	return res.err;
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

// Unmount unmounts the volume at volumePath using the DiskArbitration framework.
func Unmount(volumePath string) error {
	return unmount(volumePath, false)
}

// UnmountForce forcibly unmounts the volume at volumePath. Use only as a last
// resort when a normal Unmount keeps being refused; it's safe here because the
// camera is never written to.
func UnmountForce(volumePath string) error {
	return unmount(volumePath, true)
}

func unmount(volumePath string, force bool) error {
	cPath := C.CString(volumePath)
	defer C.free(unsafe.Pointer(cPath))

	cForce := C.int(0)
	if force {
		cForce = C.int(1)
	}
	cErr := C.charmera_unmount(cPath, cForce)
	if cErr == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(cErr))
	return errors.New(C.GoString(cErr))
}
