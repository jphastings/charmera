# charmera

A macOS command-line tool, background daemon, and menu-bar app that import
[Kodak Charmera](https://www.kodak.com/en/consumer/product/cameras/digital/charmera-keychain-digital-camera/)
toy-camera photos and videos into the **Photos** app — repairing their broken
EXIF metadata (in pure Go) plus optionally converting AVI clips to MP4 along the way. It can
auto-run whenever the camera is plugged in, and never imports the same shot
twice.

A small **menu-bar app** (shipped as `Charmera.app`, with the CLI bundled
inside) shows whether the camera is connected and importing, and lets you pause
importing with one click. The actual work is done by a background **daemon**, so
the app needn't stay open.

See [install notes](#install) below.

This is a Go reimplementation of the Python
[`kodak-charmera-exif-fixer`](https://github.com/RAIT-09/kodak-charmera-exif-fixer), extended to import into Photos, handle different SD card disk names, and deduplicate against previous imports.

## The problem

The Charmera's Generalplus chipset writes JPEGs with several EXIF defects, and
the exact symptoms vary by unit/firmware:

| Issue                                              | Fix                                            |
| -------------------------------------------------- | ---------------------------------------------- |
| Malformed date `2026:03:03:12:16:29` (extra colon) | Normalised to `2026:03:03 12:16:29`            |
| Missing capture date entirely                      | Filled from the file's modification time       |
| Wrong / missing `ExifImageWidth`/`Height`          | Set to the image's true pixel size             |
| Corrupt MakerNote / bad IFD offsets                | Dropped — a fresh, clean EXIF block is written |

AVI videos store an uncompressed Motion-JPEG + PCM stream and a hard-coded wrong
date (2010). They're transcoded to H.264 + AAC with a correct `creation_time`.

In every case the **image (pixel) data is preserved byte-for-byte** — only the
metadata is rewritten — and **the camera card is never modified** (read-only).

## What it does

For each file on the camera, in one pass:

1. **Skip** anything already in the Photos album (matched by a content hash
   embedded in the imported filename, so it works even after the camera
   renumbers files).
2. **Fix** photo EXIF (pure Go) or **convert** AVI → MP4 (via `ffmpeg`) into a
   temporary staging area, named `IMG_YYYYMMDD_HHMMSS_<hash>.jpg` / `VID_…​.mp4`.
3. **Auto-rotate** (optional) — detect each photo's orientation with a local ML
   model and set the EXIF Orientation tag so it shows upright in Photos.
4. **Import** the fixed files into a Photos album (default: *Kodak Charmera*).
5. **Delete** the staged copy — Photos holds the only copy. There's no separate
   database, so deleting a photo from the album re-enables its import next run.
6. **Unmount** the camera when finished, so you can unplug it safely.

## Install

Optional, all via Homebrew:

- [`ffmpeg`](https://ffmpeg.org) (`brew install ffmpeg`) — only if you have AVI
  videos to convert.
- [`onnxruntime`](https://onnxruntime.ai) (`brew install onnxruntime`) — enables
  the orientation auto-rotate feature; the model itself (~77 MB) is downloaded
  automatically on first use.

EXIF fixing and Photos import need neither.

**The menu-bar app (recommended).** Download `Charmera_*_darwin_universal.zip`
from the [Releases](https://github.com/jphastings/charmera/releases) page, unzip
it, and drag **Charmera.app** to `/Applications`. Launch it once: it puts a
camera icon in the menu bar and sets itself up to import automatically (at login
and whenever the camera is plugged in). The CLI is bundled inside the app, so
there's nothing else to install. The app can be quit at any time — importing
continues in the background.

**Download the CLI on its own** (no Go toolchain required) — grab the macOS
archive from the [Releases](https://github.com/jphastings/charmera/releases)
page (a single universal binary for Intel and Apple Silicon), then:

```bash
tar -xzf charmera_*_darwin_all.tar.gz
xattr -d com.apple.quarantine charmera   # the binary is unsigned; clear Gatekeeper
sudo mv charmera /usr/local/bin/
```

**Or build from source.** Requires [Go](https://go.dev) **and a C toolchain**
(Xcode Command Line Tools — run `xcode-select --install`), because the binary
uses cgo for onnxruntime and the DiskArbitration framework. `CGO_ENABLED=0`
won't build.

```bash
go install github.com/jphastings/charmera@latest   # installs to $(go env GOPATH)/bin
# or, from a checkout:
go build -o charmera .
```

## Usage

```bash
charmera                   # detect the camera and import everything new
charmera run --dry-run     # preview what would happen, change nothing
charmera run --out ./fixed # write fixed files to a folder instead of importing
```

Flags (for `run`):

| Flag               | Default          | Description                                                             |
| ------------------ | ---------------- | ----------------------------------------------------------------------- |
| `--volume NAME`    | _auto-detect_    | Pin to a specific volume name (override; see Detection)                 |
| `--album NAME`     | `Kodak Charmera` | Photos album to import into                                             |
| `--out DIR`        |                  | Write fixed/converted files to DIR instead of importing                 |
| `--dry-run`        |                  | Show planned actions without changing anything                          |
| `--auto`           |                  | Non-interactive; exit quietly if no camera is mounted                   |
| `--no-auto-rotate` |                  | Disable orientation detection (on when onnxruntime + model are present) |
| `--no-unmount`     |                  | Leave the camera mounted when finished                                  |

## Background daemon

Instead of running by hand, charmera can run as a background daemon that watches
for the camera, imports automatically on detection, and exposes its state to the
menu-bar app:

```bash
charmera daemon      # run in the foreground (normally started by the LaunchAgent)
charmera status      # print the running daemon's state
charmera pause       # stop importing when the camera is plugged in
charmera resume      # import on detection again
```

`pause` is remembered across restarts. The menu-bar app is just a friendlier
front-end for `status` and `pause`/`resume`.

## Auto-launch at login

```bash
charmera install     # start the daemon now and at every login (LaunchAgent)
charmera uninstall   # stop it and remove the LaunchAgent
```

`install` registers a `KeepAlive` LaunchAgent (`com.charmera.daemon`) that keeps
the daemon running and relaunches it if it exits. The daemon watches `/Volumes`
itself and detects the Charmera by signature, so it works no matter what the
card is named; pin it to one volume with `charmera install --volume NAME` if you
prefer. (Installing **Charmera.app** does this for you on first launch.)

`install` records the path of the running `charmera` binary, so install it to a
stable location first (e.g. `go install`, copy it to `/usr/local/bin`, or use the
copy inside Charmera.app) and re-run `install` if you move it. Logs are written
to `~/Library/Logs/charmera.*.log`.

### Permissions

- The first import asks macOS for permission to **control Photos** (Automation).
  Grant it; under the LaunchAgent the prompt appears in your GUI session.
- Keep Photos' *"Copy items to the Photos library"* setting enabled (the
  default). The tool deletes its staged copy after import, which is safe only
  when Photos has copied the file into its own library.

## How it works

- **Detection (name-independent).** The camera is found by *content signature*,
  not its volume name (which you can rename): a volume qualifies if it has a
  `DCIM` directory plus either a sibling `SPIDCIM` directory or a JPEG bearing
  the Generalplus `GPEncoder` comment. `--volume NAME` overrides this to pin a
  specific volume.
- **EXIF (pure Go).** The JPEG is parsed into its marker segments; the true size
  is read from the Start-Of-Frame header. A brand-new little-endian EXIF block is
  written with the corrected date, dimensions and preserved Orientation, and
  spliced in as the `APP1` segment. Because the EXIF is rebuilt from scratch, the
  camera's corrupt MakerNote/IFD is simply discarded rather than repaired.
- **Camera identity.** The rebuilt EXIF also stamps the camera's published
  details: Make *Kodak*, Model *Charmera*, and the fixed lens (35 mm-equivalent,
  f/2.4).
- **Orientation (optional, local ML).** When onnxruntime is installed, each
  photo is run through the [deep-image-orientation-detection](https://huggingface.co/DuarteBarbosa/deep-image-orientation-detection)
  EfficientNetV2 model (downloaded once, run locally via onnxruntime) to predict
  0°/90°/180°/270°. The 180° case is treated as impossible — a hand-held camera
  is never upside-down — so its probability is dropped and the rest renormalised.
  A rotation is written to the EXIF Orientation tag only when it then wins an
  outright majority (≥ 0.50), so an already-upright photo is never flipped.
- **Video.** `ffmpeg` transcodes to H.264 (CRF 26) + AAC, stamping
  `creation_time` from the file's modification time (the embedded date is wrong).
- **Unmount.** When finished, the camera volume is unmounted via the
  DiskArbitration framework (`--no-unmount` to skip).
- **Dedup (Photos is the source of truth).** Each camera file is hashed
  (SHA-256) and the first 16 hex chars are embedded in the imported filename.
  Before importing, charmera asks Photos for the album's existing filenames and
  skips any whose hash is already present — so identical content is never
  imported twice (even across renames), and deleting a photo from the album
  re-enables its import. The import uses `skip check duplicates` so our filename
  hash is the sole authority: Photos' own duplicate memory (which persists even
  after deletion) can't silently block a re-import. There is no separate ledger;
  the only on-disk state is the staging area
  (`~/Library/Application Support/charmera/staging`), cleared after each run.

## Development

```bash
go test ./...
```

The EXIF tests validate the rebuilt metadata against `exiftool` when it's
installed (skipped otherwise), and the video test performs a real `ffmpeg`
round-trip when `ffmpeg` is available.

## License

MIT
