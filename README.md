# charmera

A macOS command-line tool that imports [Kodak Charmera](https://www.kodak.com/en/consumer/product/cameras/digital/charmera-keychain-digital-camera/)
toy-camera photos and videos into the **Photos** app — repairing their broken
EXIF metadata (in pure Go) and converting AVI clips to MP4 along the way. It can
auto-run whenever the camera is plugged in, and never imports the same shot
twice.

Install & configure auto-run with:

```bash
$ go install github.com/jphastings/charmera-go@latest
$ charmera-go install
```

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
3. **Import** the fixed files into a Photos album (default: *Kodak Charmera*).
4. **Delete** the staged copy — Photos holds the only copy. There's no separate
   database, so deleting a photo from the album re-enables its import next run.

## Install

Requires [Go](https://go.dev) to build and [`ffmpeg`](https://ffmpeg.org)
(`brew install ffmpeg`) only if you have AVI videos to convert. EXIF fixing and
Photos import have no external dependencies.

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

| Flag            | Default          | Description                                             |
| --------------- | ---------------- | ------------------------------------------------------- |
| `--volume NAME` | _auto-detect_    | Pin to a specific volume name (override; see Detection) |
| `--album NAME`  | `Kodak Charmera` | Photos album to import into                             |
| `--out DIR`     |                  | Write fixed/converted files to DIR instead of importing |
| `--dry-run`     |                  | Show planned actions without changing anything          |
| `--auto`        |                  | Non-interactive; exit quietly if no camera is mounted   |

## Auto-launch when the camera is plugged in

```bash
charmera install     # registers a LaunchAgent that watches /Volumes
charmera uninstall   # removes it
```

The agent watches `/Volumes` (not a fixed name), so it fires whenever *any*
volume is mounted; each time, charmera detects the Charmera by signature and
exits quietly if it isn't the one. Pin it to a single volume with
`charmera install --volume NAME` if you prefer.

`install` records the path of the `charmera` binary, so install it to a stable
location first (e.g. `go install`, or copy it to `/usr/local/bin`) and re-run
`install` if you move it. Logs are written to `~/Library/Logs/charmera.*.log`.

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
- **Video.** `ffmpeg` transcodes to H.264 (CRF 18) + AAC, stamping
  `creation_time` from the file's modification time (the embedded date is wrong).
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
