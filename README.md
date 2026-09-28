# PhotoBag

An organising, deduplicating bag for images. A whole photo library lives in
**one SQLite file** (the *bag*). That file holds:

- the original image bytes (bit-exact),
- thumbnails and visual thumbprints,
- tags,
- comparison runs and scores,
- captions, text found in images, Danbooru tags and categories from a vision
  model.

You can copy, back up or carry the library as a single file.

One self-contained binary (Windows and Linux) points at a bag, new or
existing, and can:

- **serve** an interactive web UI,
- **import** a folder (optionally recursive),
- **export** to a folder,
- **back up** the bag,
- find **duplicates**, bit-identical or visually similar,
- **analyse** images with a vision model behind any OpenAI-compatible API.

## Quick start

```bash
photobag import  photos.photobag ~/Pictures/2019 -r --tag-folders
photobag serve   photos.photobag --open
```

`serve` prints a link like `http://127.0.0.1:7474/?token=…`. The token is new
on every launch and protects the API from other local users and from web
pages.

## Features

### Library and tags

- Every image has a persistent id (UUIDv7).
- Duplicate file names are fine, and images can be renamed in the UI.
- The gallery is virtualised, so it stays fast with large libraries.
- **Filter:** by tags (all or any), excluded tags, untagged images, and a
  name glob (`IMG_*.jpg`; plain text matches anywhere in the name).
- **Sort:** by import date, capture date (EXIF), name, size, resolution,
  score, shuffle, or **visual similarity**. Similarity sort chains
  look-alikes together, or ranks everything by likeness to one image
  ("Find similar").
- **Select:** click, Ctrl/⌘-click, Shift-click ranges, Ctrl+A, or "select all
  matching".
- **Bulk actions:** tag, untag, export, score, and move to the trash.
- **Lightbox:** ← and → to browse, inline rename, a tag editor that picks
  existing tags or creates new ones, metadata, and per-metric scores.
- **Tags page:** rename a tag, merge by renaming onto an existing tag, or
  delete one.

### Import

- Files are recognised by content, not by extension. Supported formats:
  **JPEG, PNG, GIF, WebP, BMP, TIFF**.
  - Animated GIF and WebP use their first frame for thumbnails.
  - EXIF orientation is applied for JPEG, PNG, WebP and TIFF.
- Other files (HEIC, RAW, video, documents…) are skipped with a reason in the
  import report. Corrupt files are reported and never abort the import.
- Bit-identical files share storage, so importing a duplicate costs no space.
- Options:
  - include sub-folders,
  - tag images with their folder names,
  - add extra tags,
  - skip files already in the bag,
  - *include removed*. By default, files identical to images you trashed or
    purged are skipped, so deleted duplicates don't come back.

### Duplicates

- **Bit-identical** mode groups files with the same SHA-256.
- **Visually similar** mode compares each image with its **N nearest images
  by thumbprint**, then groups pairs whose similarity reaches a threshold.
  The UI slider re-groups instantly, without rescanning.
  - The thumbprint combines a 64-bit DCT perceptual hash, a 4×4 CIELAB colour
    layout and the aspect ratio.
  - Flat images (sky, black frames) are judged on colour layout only, because
    their hash bits are noise.
- **Proposed keeper:** the image with the most pixels, then the largest file,
  then the most tags and comparisons, then the earliest import. Only members
  similar to the *keeper itself* are pre-marked for removal.
- **Review:** for each group you can:
  - star a different keeper,
  - mark each member keep or trash,
  - compare two members side by side,
  - mark the group as **not duplicates**, which is remembered for future
    scans,
  - or apply all proposals at once.
- **Merging:** a trashed duplicate is *merged* into its keeper. The keeper
  gets the duplicate's tags, and the duplicate's comparisons count for the
  keeper. Restoring from the trash undoes the merge.
- **Emptying the trash:**
  - It purges the image bytes. Tombstones stay behind, so comparison history
    and "previously removed" detection keep working.
  - The file itself only shrinks after **Compact**.

### Scoring

- **Start a run:** pick or create a *metric* (for example "Composition"),
  then choose the images: all, by tags or name, or the current selection.
- **Compare:** two images are shown at a time, and you pick the better one.
  - Keys: ← or → to choose, ↓ or Space to skip, Backspace to undo, Esc to
    stop.
  - Pairs cover every combination of the set, in a seeded random order.
    Left and right are randomised.
  - A run can be stopped and resumed at any time.
- **Combining runs:**
  - Every run is kept, and a metric's scores use all of its runs.
  - For each pair, contradicting answers **cancel out** one for one: A>B and
    B>A net to nothing.
  - The net results feed a **Bradley–Terry** model. A weak prior keeps
    unbeaten images and disconnected groups finite and on one scale.
- **Results:**
  - Scores use an Elo-like scale where 1000 is average, and come with
    standard errors.
  - The rankings page shows scores, net and raw win–loss counts, and how many
    pairs cancelled.
  - The gallery can sort by any metric.

### Analysis (vision model)

Point PhotoBag at an **OpenAI-compatible endpoint** whose model accepts images
(llama.cpp server, LM Studio, Ollama, vLLM, OpenAI, OpenRouter…), choose a set
of images (all, tags/name, or the selection), and run one or more pipelines:

| Pipeline | Stored as |
| --- | --- |
| **Caption** | a one- or two-sentence description for people who cannot see the image; also the image's alt text in the UI |
| **Text in image (OCR)** | the legible text, keeping line breaks; blank when there is none |
| **Danbooru tags** | Danbooru-style tags (`1girl`, `outdoors`, `long_hair`…); optionally also added as PhotoBag tags, with a prefix and spaces for underscores |
| **Category** | one category, or a main and a sub category, added as tags (`Nature`, `Nature / Skies`) |

- **Categories** come from your list (`Main: Sub, Sub` per line). The answer
  must match the list; a wrong answer gets one corrective follow-up. With no
  list the model names categories itself and is shown the ones already in use,
  so they stay consistent.
- **Search:** the library's *Search descriptions & text* box finds images by
  caption, text in the image, Danbooru tag or category (`"quoted phrases"`
  work; `long hair` finds `long_hair`).
- **Re-running:** a job sends only images without a result by default, or
  also those made with another model or prompt, or all. A re-run replaces the
  tags its pipeline added, never tags you added yourself. Corrections you make
  to captions and OCR text are kept.
- **Tuning:** every prompt can be replaced, and *Try on a random image* shows
  the parsed result, the raw reply and any reasoning without storing anything.
  *Check connection* asks the model to read a number from a test image.
- **Server quirks:** extra request parameters (a JSON object) are added to
  every request, e.g. `{"chat_template_kwargs": {"enable_thinking": false}}`
  to switch off thinking for Qwen models on llama.cpp. Rate limits and server
  errors are retried; `max_tokens`/`temperature` are adapted for models that
  reject them; a job stops early if the key is wrong or every request fails.
- **Privacy:** images (scaled to 1024 px by default) are sent to the endpoint,
  so a local server keeps everything on the machine. The **API key is not
  stored in the bag**: it lives in `%AppData%\PhotoBag\credentials.json`
  (Windows) or `~/.config/PhotoBag/credentials.json` (Linux), or comes from
  `PHOTOBAG_API_KEY`.
- Analysis jobs run beside imports and exports, and results appear in the
  lightbox as they are stored, where they can be corrected, re-run or removed.

### Export and backup

- **Export:**
  - It writes the original bytes and restores file times.
  - Names are made safe for both Windows and Linux (reserved names, illegal
    characters, trailing dots).
  - Names that clash, even only by case, get ` (2)`.
  - Optional: keep the original folder structure, and write
    `photobag-manifest.json` with each file's id, tags, scores and analysis
    results.
- **Backup:** `VACUUM INTO` writes a compacted, standalone copy.
  - In the UI you can download it or save it to a path on the machine.
  - It never overwrites an existing file, and checks free disk space first.

## Command line

```text
photobag serve   <bag> [--addr 127.0.0.1:7474] [--open] [--allow-remote] [--no-token] [--dev]
photobag import  <bag> <folder|file> [-r] [--tag T]... [--tag-folders] [--skip-identical] [--include-removed] [--report r.json]
photobag export  <bag> <folder> [--tag T]... [--any-tag T]... [--not-tag T]... [--glob G] [--untagged]
                                [--keep-structure] [--overwrite | --skip-existing] [--manifest]
photobag dedup   <bag> [--mode similar|exact] [--neighbors 8] [--threshold 0.9] [--apply] [selection flags]
photobag analyze <bag> -p caption,ocr,danbooru,category [--mode missing|changed|all] [--endpoint URL] [--model M]
                       [--concurrency N] [--dry-run] [selection flags]
photobag backup  <bag> <file-or-folder>
photobag compact <bag>
photobag info    <bag> [--json]

Global: --journal auto|wal|delete   --no-migration-backup   -v
```

`dedup` is a dry run unless you pass `--apply`. With `--apply` it trashes the
proposed duplicates, and they can still be restored.

`analyze` uses the bag's analysis settings (set them in the UI); the flags
override the endpoint, model and concurrency for one run.

## The bag file

- **Format:** an ordinary SQLite database with `application_id` `PHBG`,
  16 KiB pages and incremental auto-vacuum. PhotoBag refuses to open SQLite
  files that belong to other applications.
- **Upgrades:** before a newer PhotoBag migrates an older bag, it takes a
  safety copy (`<bag>.pre-vN.bak`).
- **Journal:** WAL mode, for concurrent reads while importing.
  - Shortly after each change, and on shutdown, the server folds the WAL back
    into the main file. The `.photobag` file on its own is therefore always
    current, and no `-wal`/`-shm` files are left once PhotoBag exits.
  - On network drives PhotoBag switches to a rollback journal with exclusive
    locking automatically.
  - Prefer **Backup** over copying the file while PhotoBag is running.
- **Concurrency:** another process, such as a CLI import, can write to a bag
  while `serve` is running. The UI notices and refreshes.

## Development

**Requirements:** Go 1.26+ and Node 24+. No C compiler is needed, because the
SQLite driver is pure Go.

```bash
node scripts/build.mjs                  # types + web UI + binaries for windows/amd64, linux/amd64, linux/arm64 → dist/
node scripts/build.mjs --targets linux/amd64 --skip-web
go test ./...                           # Go tests (imaging, bag, import/export, dedup, scoring, analysis, server)
npm --prefix web run typecheck
npm --prefix web run lint
npm --prefix web test
```

**Live reload:** run the API and the Vite dev server side by side, then open
the printed link on port 5173.

```bash
go run ./cmd/photobag serve .scratch/dev.photobag --dev
npm --prefix web run dev
```

`go run ./scripts/genfixtures <dir> [scale] [extra]` writes a synthetic test
library. It includes near-duplicates, an exact copy, EXIF-rotated images,
WebP, TIFF, BMP and GIF files, and junk files.

### Layout

| Path | Purpose |
| --- | --- |
| `cmd/photobag` | entry point |
| `internal/cli` | cobra commands |
| `internal/bag` | open/create/migrate the SQLite bag, pragmas, SQL functions (`pb_glob`), label keys |
| `internal/imaging` | format sniffing, EXIF, decoding, area-filter downscale, orientation, thumbnails, previews, thumbprints |
| `internal/query` | `ImageQuery` (the one selection abstraction) and sort → SQL |
| `internal/library` | image, tag and trash operations |
| `internal/importer`, `internal/exporter`, `internal/backup` | transfer |
| `internal/similar`, `internal/dedup` | k-nearest-neighbour search, similarity ordering, duplicate clusters and resolution |
| `internal/scoring` | runs, the Feistel pair scheduler and the Bradley–Terry fit |
| `internal/llm`, `internal/analysis` | OpenAI-compatible client (retries, parameter fallbacks, fake server for tests); pipelines, prompts, reply parsing, analysis jobs |
| `internal/jobs`, `internal/events`, `internal/server` | background jobs, SSE, HTTP API, security |
| `internal/webui` | embedded build of `web/` |
| `web/` | React + TypeScript + Mantine SPA; `src/api/gen` is generated by [tygo](https://github.com/gzuidhof/tygo) from the Go types |
