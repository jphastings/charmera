# charmera — notes for working in this repo

A macOS CLI that imports Kodak Charmera (Generalplus CBB3) toy-camera photos and
videos into the Photos app, repairing their broken EXIF and converting AVI→MP4.
See `README.md` for user-facing docs; this file is the non-obvious stuff.

Two binaries ship together inside `Charmera.app`: the `charmera` CLI/daemon
(repo root `main.go`) and the `charmera-menu` menu-bar app (`cmd/charmera-menu`).

## Platform & build

- **macOS only, and cgo is mandatory.** `CGO_ENABLED=0` will **not** build —
  `onnxruntime_go` (orientation) and the DiskArbitration unmount both need cgo,
  so a C toolchain (Xcode Command Line Tools) is required. Plain `go build`
  works because cgo is on by default on macOS.
- The binary only *links* macOS system frameworks; onnxruntime is `dlopen`ed
  lazily at runtime, so it runs fine without onnxruntime installed — orientation
  detection just turns off.
- The menu app (`cmd/charmera-menu`) uses `fyne.io/systray`, which links Cocoa —
  another reason cgo is required.

## Daemon & menu app

The auto-import model is a **persistent daemon**, not a one-shot run per mount:

- `charmera daemon` runs continuously (a `KeepAlive` LaunchAgent, label
  `com.charmera.daemon`), watches `/Volumes`, imports on detection unless paused,
  unmounts after, and holds live state. `charmera run` is still the manual
  one-shot path and is unchanged.
- `internal/daemon` is deliberately **cgo-free and platform-agnostic**: the
  cgo-heavy import and unmount are injected as `RunImport` / `Unmount` funcs (the
  CLI wires in the real ones). Keep it that way — it's what makes the state
  machine unit-testable with a fake camera on any platform.
- **IPC** is a Unix-domain socket (`StateDir/daemon.sock`), newline-delimited
  JSON: the daemon streams a `State` on every change (deduped — unchanged states
  aren't re-sent), clients send a `Command` (pause/resume/toggle). The pause
  setting is persisted to `StateDir/state.json` so it survives restarts.
- The menu app does **no importing**; it just connects to the socket, shows
  `State.Label()`, and toggles pause. `State.Label()` is the single source of
  truth for the status wording (CLI `status` uses it too).
- On first launch the menu app installs the daemon LaunchAgent pointing at the
  CLI bundled next to it (`Contents/Resources/charmera`).

## ⚠️ Do NOT start the daemon (or launch Charmera.app) in development

`charmera daemon`, the installed LaunchAgent, and launching `Charmera.app` all
start the persistent daemon watching the **real** `/Volumes`, which will import a
plugged-in camera into the **real Photos library** (and `install` it to run at
login). This trips the same Photos-fingerprint hazard below. Exercise the daemon
through its unit tests instead — they point `VolumesDir` at a temp dir with a
fake camera (`DCIM` + `SPIDCIM`) and inject a fake `RunImport`.

## Runtime dependencies (all optional, via Homebrew)

The tool imports photos with none of these present; each degrades gracefully:

- **ffmpeg** — AVI→MP4. Absent → videos transferred without compression, photos still processed.
- **onnxruntime** — orientation auto-rotate. Absent → feature silently off. The
  ~77 MB model is downloaded once (URL + checksum in `internal/orient/setup.go`)
  into Application Support; it is **never embedded**, which keeps the binary
  small.
- **exiftool** is **not** a runtime dependency — it's only used by tests to
  independently validate the EXIF we write.

## Testing

- `go test ./...`. Tests needing ffmpeg / exiftool / onnxruntime (+ the model)
  skip when those are unavailable, so the suite passes on a bare machine.
- Tests are behavioural. Fixtures are in `internal/exiffix/testdata`
  (`no_exif.jpg` is a real Charmera frame with its EXIF stripped).
- Keep `pipeline` cgo-light: it does **not** import `orient`; the detector is
  injected behind the `OrientationDetector` interface and adapted in the CLI.
- `internal/daemon` tests use a **short** `StateDir` (`os.MkdirTemp`, not
  `t.TempDir`): the daemon socket lives in `StateDir`, and Unix socket paths are
  capped near 104 chars — `t.TempDir`'s long, test-named paths overflow it.

## Key decisions / gotchas

- **EXIF is rebuilt in pure Go, not repaired.** `internal/exiffix` drops the
  camera's corrupt MakerNote and writes a fresh, minimal, spec-complete APP1
  from scratch. Do not reintroduce a runtime exiftool dependency.
- **Camera detection is by content signature, not volume name** — a sibling
  `SPIDCIM` directory or a `GPEncoder` JPEG comment (`internal/scan/detect.go`).
  The SD card can be renamed, so never key off the volume name.
- **Orientation:** the 180° class is treated as impossible (a hand-held camera
  is never upside-down) and dropped before argmax; a rotation is applied only
  above a confidence threshold. Rationale and class→EXIF mapping live in
  `internal/orient`.
- **Unmount uses DiskArbitration via cgo** (`internal/volume`), not `diskutil`.

## Photos integration — read before touching it

Dedup is driven by **Photos itself**, not a local file: the content hash is
embedded in the imported filename, and each run queries the album's existing
filenames (`internal/photos`). There is no ledger.

Hard-won gotchas (these cost a long debugging session):

- **Do NOT test imports against the real Photos library.** Use `--dry-run` or
  `--out <dir>`. Photos keeps a persistent content *fingerprint* that survives
  deleting a photo **and** emptying Recently Deleted, and on re-import it can
  alias the new item to a previously-seen master's filename — silently breaking
  the filename-hash dedup, and very hard to undo.
- **Removing a photo from an album ≠ deleting it from the library.** A copy left
  in the library or in Recently Deleted still blocks re-import.
- The import deliberately uses `skip check duplicates` so our hash is the sole
  authority (Photos' own dedup would otherwise block re-importing a photo the
  user deleted).
- AppleScript **cannot delete** Photos items on current macOS (errors -1700 /
  -10000); cleanup is manual / the user's job.

## Release

- GoReleaser on tag push (`v*`), on a **macOS runner** with **CGO_ENABLED=1**;
  the universal binary cross-compiles amd64 from the arm64 runner via
  `clang -arch`. A Linux runner or `CGO_ENABLED=0` will not work.
- Signing/notarization is via **quill** (`scripts/macos-sign.sh`, a
  `universal_binaries` post-hook). It degrades: no quill → skip, no credentials
  → ad-hoc sign, credentials present → Developer ID sign + notarize. The
  required GitHub secrets are listed in `.github/workflows/release.yml`.
- A bare binary in a `.tar.gz` **cannot be stapled**, so notarization is
  verified online — expected, not a bug.
- `Charmera.app` is built separately by `scripts/make-app.sh` (run after
  GoReleaser in the workflow; its zip is uploaded with `gh release upload`). It
  cross-compiles both binaries universal — note **`GOARCH` and `clang -arch`
  must agree** or cgo passes conflicting `-arch` flags. Unlike the bare binary,
  the `.app` bundle **can** be stapled, so the script staples it. It reuses the
  same `QUILL_*` secrets but via `codesign` + `notarytool` (not quill).
- **The CLI lives in `Contents/Resources/charmera`, not `Contents/MacOS`.** The
  bundle executable is `Charmera`, the filesystem is case-insensitive, and
  `MacOS/charmera` would collide with `MacOS/Charmera` (same file). The menu app
  finds the CLI at `../Resources/charmera`.

## Tunable defaults

Video (CRF/preset/codecs), album name, orientation confidence threshold, and
paths all live in `internal/config/config.go`. Change defaults there.
