// The Charmera comes in several colours, and Charmera.app's Finder icon
// changes to match the last camera seen (see internal/scan.ReadModel), while
// the menu-bar glyph itself stays the fixed monochrome template.
//
// The mechanism is NSWorkspace's -setIcon:forFile:options:, the same API
// Finder uses for "paste custom icon" in Get Info: it writes a custom-icon
// resource ("Icon\r") into the bundle root. Gatekeeper only assesses an app at
// first launch, so this doesn't stop the app from opening, but it does mean
// `codesign --verify --strict` will report the added file once a camera has
// been seen, and a fresh app update resets the icon to the default yellow
// until the next camera is plugged in.
package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>

static bool setFinderIcon(const char *bundlePath, const char *iconPath) {
    NSString *bp = [NSString stringWithUTF8String:bundlePath];
    NSImage *img = nil;
    if (iconPath != NULL) {
        img = [[NSImage alloc] initWithContentsOfFile:[NSString stringWithUTF8String:iconPath]];
        if (img == nil) return false;
    }
    __block BOOL ok = NO;
    void (^apply)(void) = ^{ ok = [[NSWorkspace sharedWorkspace] setIcon:img forFile:bp options:0]; };
    if ([NSThread isMainThread]) { apply(); } else { dispatch_sync(dispatch_get_main_queue(), apply); }
    return ok;
}
*/
import "C"

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

// modelIconPath returns the path to the icon PNG for a camera's colour under
// iconsDir, or "" if model is empty or unrecognised — meaning: clear the
// custom icon so the bundle's default yellow .icns shows again.
func modelIconPath(iconsDir, model string) string {
	switch model = strings.ToLower(strings.TrimSpace(model)); model {
	case "":
		return ""
	case "gray":
		model = "grey"
	}
	path := filepath.Join(iconsDir, model+".png")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// bundlePaths returns Charmera.app's own path and its Resources/icons
// directory, or "", "" if this binary isn't running from inside a .app (e.g. a
// bare dev build) — guarding against calling setIcon on some unrelated parent
// directory.
func bundlePaths() (bundle, iconsDir string) {
	exe, err := os.Executable()
	if err != nil {
		return "", ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	// exe is Contents/MacOS/Charmera, so the bundle is two directories up.
	b := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	if filepath.Ext(b) != ".app" {
		return "", ""
	}
	icons := filepath.Join(b, "Contents", "Resources", "icons")
	if fi, err := os.Stat(icons); err != nil || !fi.IsDir() {
		return "", ""
	}
	return b, icons
}

// applyBundleIcon sets Charmera.app's Finder icon to match a camera's colour,
// or clears it back to the default yellow .icns for an empty/unrecognised
// model. It's a no-op outside a real .app bundle, and failures (read-only
// /Applications, translocation, etc.) are ignored — the icon is cosmetic.
func applyBundleIcon(model string) {
	bundle, iconsDir := bundlePaths()
	if bundle == "" {
		return
	}

	cBundle := C.CString(bundle)
	defer C.free(unsafe.Pointer(cBundle))

	path := modelIconPath(iconsDir, model)
	if path == "" {
		C.setFinderIcon(cBundle, nil)
		return
	}

	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	C.setFinderIcon(cBundle, cPath)
}
