# PhotoBag

An organising, deduplicating bag for images. A whole photo library lives in
**one SQLite file** (the *bag*). That file holds:

- the original image bytes (bit-exact),
- thumbnails and visual thumbprints,
- tags,
- comparison runs and scores,
- captions, text found in images, Danbooru tags and categories from a vision
  model,
- ComfyUI workflows, generation experiments and how each generated image was
  made,
- ordinary files in folders: notes, documentation, anything that should
  travel with the library.

You can copy, back up or carry the library as a single file.

One self-contained binary (Windows and Linux) points at a bag, new or
existing, and can:

- **serve** an interactive web UI,
- **import** a folder (optionally recursive),
- **export** to a folder,
- **back up** the bag,
- find **duplicates**, bit-identical or visually similar,
- play **slideshows** of the library or of **slide decks** (images in an
  order you choose),
- **re-encode** images to another format, quality or size (lossless PNG or
  WebP, lossy JPEG or WebP, optionally scaled down), replacing the originals
  at once or after comparing them side by side,
- keep **files** (notes, documentation) in folders, and read text and
  Markdown in the browser,
- **analyse** images with a vision model behind any OpenAI-compatible API,
- **generate** images with ComfyUI: run workflows with overrides, sweep
  settings to compare them, and keep the images that work.

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
- **Filter:** by tags (all or any), excluded tags, untagged images, a name
  glob (`IMG_*.jpg`; plain text matches anywhere in the name), file type and
  smallest file size.
- **Sort:** by import date, capture date (EXIF), name, size, resolution,
  score, shuffle, or **visual similarity**. Similarity sort chains
  look-alikes together, or ranks everything by likeness to one image
  ("Find similar").
- **Select:** click, Ctrl/⌘-click, Shift-click ranges, Ctrl+A, or "select all
  matching".
- **Bulk actions:** tag, untag, add to a slide deck, play as a slideshow,
  export, score, re-encode, and move to the trash.
- **Lightbox:** ← and → to browse, inline rename, a tag editor that picks
  existing tags or creates new ones, metadata, and per-metric scores.
- **Tags page:** rename a tag, merge by renaming onto an existing tag, or
  delete one.

### Slide decks and slideshows

- **Slideshows:** **Slideshow** in the library plays the images shown, with
  the current filters and sort. The arrow beside it holds the settings,
  which are kept in the browser. The lightbox's ▷ starts from the image
  shown, and the selection bar plays just the selected images.
- **Slide decks** (in the sidebar) are images in an order you choose, with
  their own slideshow settings.
  - **Adding images:** select images in the library and use **Add to deck**
    (or start a new deck with them). On the deck's page, **Add images**
    takes everything with some tags or name, or the library selection, in
    date, name or shuffled order.
  - **Ordering:** drag slides, or a selection of them, to where they
    should go. Selected slides can also move to the start or the end.
    **Sort** reorders the whole deck by date, name, visual similarity
    (look-alikes next to each other), score, or at random, and can be
    undone.
  - Images in the trash are hidden from decks, and return to their place if
    restored. A duplicate merged into its keeper is replaced by the keeper.
- **Settings:**
  - advance automatically (seconds per slide) or by hand;
  - cross-fade on or off, and how long it takes;
  - **Fit** (whole image, as large as fits), **Fill** (crops), **Stretch**
    (distorts) or **Actual size** (larger images shrink to fit);
  - background colour, shuffle, loop, and captions (the image's description,
    or its name).

  A small preview shows the result.
- **Playing:** the slideshow fills the screen.
  - → or Space shows the next slide (in timed shows, Space pauses), ← the
    previous one, Home and End the first and last. Presentation clickers
    (Page Up/Down) work too.
  - F switches full screen, C switches captions, and Esc ends the show.
  - A click moves on (the left quarter goes back); on touch screens, swipe.
  - The controls and the pointer hide while the mouse is still. The screen
    is kept awake where the browser allows it.
  - GIFs, PNGs and WebPs are shown as they are, so they animate and
    transparent parts show the background. Other images use a preview sized
    for the screen, prepared ahead of time.

### Re-encoding

Store images in another format, at another quality or at a smaller size.
Select images in the library and use **Re-encode**, or start one on the
**Re-encode** page and choose images by type, size, tags or name (for
example every PNG of at least 5 MB).

- **Formats:** lossless (PNG, or lossless WebP) or lossy (JPEG or WebP, with
  a quality from 1 to 100). WebP and PNG have an effort setting (smaller
  files take longer); JPEG can be progressive and keep colour at full
  resolution (4:4:4). The encoders are pure Go
  ([jpegn](https://github.com/gen2brain/jpegn),
  [vpx](https://github.com/gen2brain/vpx)'s WebP, and Go's PNG).
- **Scaling down:** a largest width and/or height. Images that already fit
  keep their size; proportions are kept.
- **What carries over:** pixels are turned upright (the EXIF orientation is
  applied, and reset to 1). The colour profile is always kept; EXIF and XMP
  are kept unless you untick *Keep the camera metadata*. Transparency is
  kept, except in JPEG, where it is filled with white. Tags, scores, decks
  and analysis results stay with the image, whose name gets the new
  extension.
- **Left alone:** animated GIF, PNG and WebP (only the first frame would
  survive) and CMYK JPEGs. With *Only when the new file is smaller* (on by
  default), an image whose new file would not be smaller keeps its
  original.
- **Try:** the dialog encodes three of the images and shows the size change
  and how close the result is (PSNR), before you commit to all of them.
- **Replace straight away** (the default for lossless re-encodes at full
  size): each original is deleted as soon as its new file is made. Every new
  file is decoded again first, and lossless ones must reproduce every pixel.
- **Compare first** (the default otherwise): results wait beside their
  originals. Open one to see both side by side, or flip between them (hold
  Space). Zoom with the wheel or to the original's actual pixels (Z), and
  drag to look around; both sides follow. Then **R** replaces and **K**
  keeps the original, and the next one comes up. *Replace all* and *Keep
  all* decide the rest. Sort by biggest saving or by most changed.
- **Running:** a batch runs as a background job, on half the processor cores.
  Stopped or interrupted batches carry on where they left off. The image's
  details list each re-encode with the sizes before and after.
- **Imports** skip files identical to an original that was replaced
  (*include removed images* imports them anyway).
- Results awaiting review are stored in the bag until you decide; deleting
  the batch drops them and keeps the originals.

### Files

The **Files** page keeps ordinary files in the bag, in folders: notes,
documentation, licences, anything that should travel with the library.

- **Adding:** upload files or whole folders (drag them onto the page, or
  onto a folder to put them there), or import a file or folder from the
  machine running PhotoBag. When names are taken you choose to replace,
  keep both (` (2)`) or skip.
- **Organising:** new folders, rename (F2), move (drag onto a folder or a
  part of the path, or *Move to…*), delete (Del). Names follow the same
  rules as image names, so they export cleanly to Windows and Linux; they
  are unique within a folder ignoring case.
- **Viewing:** text and code open in the browser (UTF-8, UTF-16 or
  Windows-1252), Markdown is shown formatted (tables, task lists; relative
  links and images point into the bag), and images, PDFs, audio and video
  play in place. Files are served sandboxed: an HTML page or SVG never runs
  scripts, and HTML is shown as source.
- **Getting them out:** download a file, or anything as a zip; or export to a
  folder on the machine, keeping folders and modification times.
- Identical files are stored once; contents are kept in 1 MB pieces, so large
  files stream in and out.

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
| **Danbooru tags** | Danbooru-style tags (`1girl`, `outdoors`, `long_hair`…), from the vision model or a [WD tagger](#danbooru-tags-from-a-wd-tagger); optionally also added as PhotoBag tags, with a prefix and spaces for underscores |
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

#### Danbooru tags from a WD tagger

The Danbooru pipeline can use a **WD tagger** (SmilingWolf's
[WD v3 ONNX models](https://huggingface.co/SmilingWolf/wd-eva02-large-tagger-v3))
instead of the vision model. It knows the real Danbooru vocabulary, gives
every tag a confidence, and takes a fraction of a second per image on a GPU.
Choose it under *Analysis → Pipelines & prompts → Danbooru tags → A WD tagger*:

- **Where:** *This computer* uses the tagger installed with
  `photobag tagger install`, which PhotoBag starts when needed and stops
  after 10 idle minutes (freeing the GPU memory). *A tagger server* uses one
  running elsewhere: give its `host:port`.
- **Which tags:** general, character and rating tags can each be kept or
  left out, with a minimum confidence (defaults 0.35, 0.85 and 0) and their
  own tag prefix (e.g. `rating:` gives `rating:general`). The scores are
  stored, so *Update tags on analysed images* can apply stricter thresholds,
  fewer categories or new prefixes without tagging again.
- The vision model is not needed for tagger tags, so a bag can use the
  tagger alone.

Setting up the local tagger:

```text
photobag tagger install --download-model     # Python env, ONNX Runtime, the 1.26 GB model, and a test run
photobag tagger status [--check]             # what is installed; --check starts it and tags a test image
photobag tagger serve --host 0.0.0.0 --port 8000   # run it for other computers
photobag tagger script <dir>                 # write the server script, requirements and README to host elsewhere
photobag tagger uninstall
```

`install` finds Python 3.10 or later (python.org builds are preferred:
Anaconda's older C++ runtime crashes ONNX Runtime on Windows), creates a
virtual environment in `%LOCALAPPDATA%\PhotoBag\tagger` (Windows) or
`~/.local/share/photobag/tagger` (Linux; `PHOTOBAG_TAGGER_DIR` or `--dir`
elsewhere), and installs `onnxruntime-gpu` with CUDA and cuDNN from pip when
`nvidia-smi` finds an NVIDIA GPU (about 1.3 GB; `--system-cuda` uses installed
ones), otherwise `onnxruntime` for the CPU (`--device` chooses). Downloads
resume and are checked against Hugging Face's checksums. Without
`--download-model` it asks, or you can put `model.onnx` and
`selected_tags.csv` from the model's page into the folder yourself;
`--model SmilingWolf/wd-vit-tagger-v3` picks a smaller WD v3 model. Running
`install` again repairs or updates the installation.

The server is `internal/tagger/python/tagger_server.py` (FastAPI, `/health`,
`/tag` and `/tag/details`). Environment variables: `MODEL_PATH`,
`TAGS_PATH`, `MODEL_NAME`, `DEVICE` (`cuda`, `cpu` or `auto`), `GPU_DEVICE`,
`MAX_CONCURRENCY`, `ORT_PRELOAD_DLLS`.

### Generating images with ComfyUI

Under **Generate**, PhotoBag runs [ComfyUI](https://github.com/comfyanonymous/ComfyUI)
workflows on a ComfyUI server (set its address under Generate → ComfyUI) and
receives the images over ComfyUI's websocket.

- **Workflows:** templates are workflows exported from ComfyUI in the API
  format (Workflow → Export (API)). Paste one or load the file. PhotoBag
  lists the nodes and the literal inputs that can be overridden, and warns
  about duplicate node titles or a missing image output.
  - A "Send Image (WebSocket)" node (from
    [comfyui-tooling-nodes](https://github.com/Acly/comfyui-tooling-nodes))
    hands images straight over. Workflows ending in "Save Image" work too:
    PhotoBag then downloads what ComfyUI saved.
  - Every edit is kept as a version. Images made with an older version still
    point at it.
- **Experiments** hold generated images apart from the library. They don't
  appear in the library, duplicates, scoring, exports or statistics until
  moved.
  - **Overrides** name a node by its title (or `#id` when titles repeat) and
    change one of its inputs: `Width › value = 2048`,
    `Checkpoint › ckpt_name`, the sampler, the prompt, anything literal.
    ComfyUI's node definitions supply the choices (installed models,
    samplers) and ranges.
  - **N images:** each gets its own seed. The seed policy can be random,
    counting up from the workflow's seed, or kept as it is.
  - **Sweeps:** an override takes several values, as a list (model names,
    prompts) or a range (steps 25 to 30). Every combination is made, and the
    n-th image of each combination shares a seed, so only the swept values
    differ. Model sweeps run model by model, to load each model once.
  - **Results:** sweeps show as tables, with the swept values across and
    down. Failed prompts (say, a model ComfyUI doesn't have) show ComfyUI's
    error in their cell.
- **Keeping images:** select images and **move them to the library**,
  optionally tagging them. They are named after the experiment. Or
  **delete** them for good.
- **Metadata:** each image keeps its workflow version and the exact values
  applied. From an image, in the experiment or in the library, you can:
  - load its settings back into the form, with or without its seed, to make
    variations or to see what one change does to that very image;
  - save its effective workflow as a new template;
  - download it in the API format.

  PNGs also carry the prompt the way ComfyUI's Save Image writes it, so a
  file dropped on ComfyUI opens the workflow.
- **Running:** generation runs as a background job. Two prompts are kept
  queued, so ComfyUI never waits. You see live progress, and sampler
  previews when ComfyUI sends them. Stopping it removes PhotoBag's waiting
  prompts from ComfyUI's queue and interrupts the running one.

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
                       [--concurrency N] [--tagger local|host:port] [--dry-run] [selection flags]
photobag tagger  install [--download-model] [--device auto|cuda|cpu] [--model REPO] | status | serve | script | uninstall
photobag files   ls <bag> [path] [-r] | import <bag> <file-or-folder> [--to path] [--replace | --skip-existing]
                 | export <bag> <folder> [--from path] [--overwrite | --skip-existing]
photobag backup  <bag> <file-or-folder>
photobag compact <bag>
photobag info    <bag> [--json]

Global: --journal auto|wal|delete   --no-migration-backup   -v
```

`dedup` is a dry run unless you pass `--apply`. With `--apply` it trashes the
proposed duplicates, and they can still be restored.

`analyze` uses the bag's analysis settings (set them in the UI); the flags
override the endpoint, model, concurrency and tagger for one run.

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

**Requirements:** Go 1.26.4+ (an older Go downloads it by itself) and Node
24+. No C compiler is needed: the SQLite driver and the JPEG and WebP
encoders are pure Go.

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
| `internal/imaging` | format sniffing, EXIF, decoding, area-filter downscale, orientation, thumbnails, previews, thumbprints; re-encoding with metadata carried over |
| `internal/query` | `ImageQuery` (the one selection abstraction) and sort → SQL |
| `internal/library` | image, tag and trash operations |
| `internal/importer`, `internal/exporter`, `internal/backup` | transfer |
| `internal/similar`, `internal/dedup` | k-nearest-neighbour search, similarity ordering, duplicate clusters and resolution |
| `internal/scoring` | runs, the Feistel pair scheduler and the Bradley–Terry fit |
| `internal/llm`, `internal/analysis` | OpenAI-compatible client (retries, parameter fallbacks, fake server for tests); pipelines, prompts, reply parsing, analysis jobs |
| `internal/comfy` | ComfyUI API-format workflows (titles, overrides, canonical JSON), sweep and seed expansion, HTTP and websocket client, prompt runner, PNG prompt chunks, fake server for tests |
| `internal/experiments` | workflow templates and versions, experiments, generation jobs, moving images to the library |
| `internal/decks` | slide decks: membership, order and moves, slideshow settings |
| `internal/files` | the files space: folders, chunked contents, uploads, import/export to disk, zips |
| `internal/reencode` | re-encode batches: running them, results awaiting review, replacing originals |
| `internal/tagger` | WD tagger client, the embedded Python server, its installer (Python discovery, venv, pip, Hugging Face downloads) and the on-demand local process |
| `internal/jobs`, `internal/events`, `internal/server` | background jobs, SSE, HTTP API, security |
| `internal/webui` | embedded build of `web/` |
| `web/` | React + TypeScript + Mantine SPA; `src/api/gen` is generated by [tygo](https://github.com/gzuidhof/tygo) from the Go types |
