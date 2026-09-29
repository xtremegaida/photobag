# PhotoBag — implementation plan

## Context
The repo is empty, so this is a new build. PhotoBag keeps a whole photo library in one SQLite file (the "bag"). That file holds the original image bytes, thumbnails, thumbprints, tags, comparison runs and scores, so the library can be moved as a single file. A single CLI binary points at a bag (new or existing) and can:
- serve an interactive web UI,
- import a folder (optionally recursive),
- export to a folder,
- back up the bag.

The UI can also start imports, exports and backups (VACUUM INTO, then download).

Core features:
- **Dedup:** exact (bit-identical) or perceptual (compare each image to its N nearest by thumbprint, then apply a threshold). Can run automatically or interactively.
- **Tagging:** gallery filtered by tag set; tags also drive export selection.
- **Pairwise scoring:** per metric, all runs are combined, contradictions cancel, and a Bradley–Terry model ranks the images.

It must run on Linux and Windows. The front-end is a SPA that will keep growing.

**User decisions:**
- Formats: common web formats only. JPEG, PNG, GIF, WebP, BMP and TIFF are imported. Anything else is skipped with a warning.
- Thumbprint: a versioned perceptual hash, so it can be upgraded later.

Library versions were checked on 2026-09-28.

## Stack
- **Backend:** Go 1.26, built as a single binary with `CGO_ENABLED=0`, cross-compiled to windows/amd64, linux/amd64 and linux/arm64.
  - `spf13/cobra` for the CLI.
  - `modernc.org/sqlite` v1.59 (SQLite 3.53).
  - `google/uuid` `NewV7`.
  - stdlib `net/http` ServeMux with method patterns.
  - `golang.org/x/{image,text,sys}`.
- **Imaging:** pure Go.
  - Decoders: `image/jpeg|png|gif`, `x/image/{webp,bmp,tiff}`, resizing with `x/image/draw`.
  - EXIF: `evanoberholster/imagemeta` for JPEG, TIFF and PNG. It can't read WebP, so the EXIF chunk is extracted from the WebP RIFF container ourselves and parsed with the same reader.
  - The decoder sits behind an interface, so `gen2brain/jpegn` scaled DCT decoding can be swapped in later as a measured speed-up.
- **Front-end:** Vite 8, React 19.2+, TypeScript, and Mantine 9 (its TagsInput/Combobox handle creatable tags, plus modals and notifications).
  - Also TanStack Query 5, React Router, Zustand (the selection store), and TanStack Virtual, virtualising rows of N columns so range selection, keyboard navigation and justified layouts work later.
  - API types are generated from the Go DTOs with `tygo` into `web/src/api/types.gen.ts` via `go generate`.
- The SPA is embedded via `//go:embed`.

## Project layout
```
cmd/photobag/main.go
internal/cli/          serve, import, export, backup, dedup, info
internal/bag/          Open/Create, pragmas, migrations (embedded .sql), repositories, custom SQL funcs (pb_glob)
internal/imaging/      sniff (magic bytes), decode, EXIF orient (incl. WebP chunk), thumbnail, fingerprint
internal/query/        ImageQuery {tagsAll, tagsAny, tagsNone, nameGlob, ids, scope} -> SQL
internal/importer/     walker, byte-budgeted worker pool, single batched writer
internal/exporter/     query -> files
internal/backup/       VACUUM INTO, temp lifecycle, disk-space check
internal/sysutil/      diskfree_{windows,unix}.go, netfs detection
internal/dedup/        exact.go, knn.go, cluster.go, resolve.go
internal/scoring/      pairs.go (Feistel permutation), bt.go (Bradley–Terry), runs.go
internal/jobs/         sequential job queue, progress, event broker
internal/server/       router, security.go, handlers_*.go, sse.go, static.go, dto/ (tygo source)
internal/webui/        embed.go + dist/ (Vite outDir; committed placeholder index.html so go build/test work without Node)
web/                   Vite React TS app: src/api, src/pages, src/components, src/stores
scripts/build.mjs      web build, then go generate, then go build for all targets into dist/
scripts/genfixtures/   Go program writing a synthetic photo tree (used by tests + manual E2E)
.github/workflows/ci.yml  go test + web typecheck/lint/test on windows-latest and ubuntu-latest
```
`internal/query.ImageQuery` is the single selection abstraction. The gallery, export, scoring-run creation and dedup scope all use it.

## Bag file & schema (`internal/bag`, migration 001)
**Creation order:**
1. `page_size=16384`
2. `auto_vacuum=INCREMENTAL`
3. `application_id` = 'PHBG'
4. create tables
5. `journal_mode=WAL`
6. `user_version` = migration number

**Connections:**
- Writer pool: `MaxOpenConns=1` with `_txlock=immediate`. Reader pool: separate.
- Per-connection pragmas are set via the DSN `_pragma`: `foreign_keys=ON`, `busy_timeout=10000`, `journal_size_limit`.
- `pb_glob(pattern, name)` is registered as a case-insensitive glob via modernc `RegisterDeterministicScalarFunction`.

**Opening a bag:**
- A missing file is created.
- A SQLite file with a foreign application_id and non-empty tables is refused.
- A bag with an old user_version gets an automatic VACUUM INTO `<bag>.pre-vN.bak` before migrating. `--no-migration-backup` skips it.
- A network filesystem is detected (UNC or a mapped network drive on Windows, statfs NFS/CIFS/SMB on Linux). On a network filesystem the bag uses `journal_mode=DELETE` plus exclusive locking, with a warning. `--journal` overrides this.
- On shutdown, run `wal_checkpoint(TRUNCATE)` and close cleanly, so no -wal or -shm files are left.
- The server polls `PRAGMA data_version` so changes from another process (e.g. a CLI import) trigger UI refresh events.

**Tables:**
- `meta(key PK, value)`: name, created_at, fingerprint version.
- `blobs(id INTEGER PK, sha256 BLOB(32) UNIQUE, size, data BLOB)`: content-addressed original bytes. Bit-identical imports share one blob. Blobs are stored whole (photos are at most tens of MB); reads are limited by a size cap and a concurrency semaphore. The table is kept apart so metadata scans stay small.
- `images`:
  - Columns: `id INTEGER PK, uid TEXT UNIQUE (UUIDv7), sha256, blob_id NULL, name, original_name, original_path, import_id, format, width, height, orientation, file_mtime, taken_at TEXT (EXIF local), taken_offset NULL, imported_at, deleted_at NULL, merged_into NULL, purged_at NULL`.
  - `uid` is the persistent global id, used in exports and the manifest. The API uses the integer `id`.
  - Name validation: no separators or characters that are reserved on Windows.
  - Purged images stay as tombstones with `blob_id` NULL. This keeps comparison history intact and means removed files can be recognised on re-import.
- `thumbnails(blob_id PK, w, h, data)`: JPEG q80, 400px on the long side, orientation applied, alpha flattened to grey.
- `fingerprints(blob_id PK, version, phash INTEGER, color BLOB(48), aspect REAL, ac_energy REAL)`.
- `tags(id, name, key UNIQUE, created_at)`: `key` is the name with NFC normalisation and Unicode case folding, because NOCASE only folds ASCII. `image_tags(image_id, tag_id, PK) WITHOUT ROWID`, plus an index on tag_id.
- `imports(id, source, recursive, options_json, started_at, finished_at, added, skipped, failed)`.
- `not_duplicates(a, b, PK, CHECK a<b)`.
- `metrics(id, name, key UNIQUE, description, created_at, scores_dirty)`.
- `score_runs(id, metric_id, seed, position, total_pairs, status, selection_json, created_at, finished_at)`, `score_run_images(run_id, ord, image_id)`: the input set is frozen when the run is created.
- `comparisons(id, run_id, pos, metric_id, left_id, right_id, winner NULL=skip, created_at, UNIQUE(run_id,pos))`, plus an index on `(metric_id)`.
- `scores(metric_id, image_id, theta, score, stderr, stderr_approx, n, wins, losses, computed_at)`.

## Imaging & thumbprint (`internal/imaging`)
- **Sniff:** by magic bytes, not the extension.
- **Decode:** run `DecodeConfig` first so memory can be budgeted.
  - GIF and animated WebP: first frame only. `x/image/webp` rejects animated files, so the first ANMF frame's bitstream is extracted into a still container.
  - EXIF orientation is applied for JPEG, TIFF, PNG (eXIf chunk) and WebP.
- **Fixed pipeline, regardless of decoder:**
  1. Decode.
  2. Area-downsample to a working image of ≤1024px.
  3. Build the thumbnail (CatmullRom to 400px) and the fingerprint from that working image.
- **pHash v1:**
  1. 32×32 luma with an area filter.
  2. 2-D DCT-II.
  3. Take the 8×8 low-frequency block and threshold each coefficient against the median (DC excluded). This gives 64 bits.
- **Extra fingerprint data:**
  - A colour signature: a 4×4 grid of mean CIELAB values, quantised to 48 bytes.
  - The aspect ratio.
  - `ac_energy`: pHash is unstable on flat images such as sky, black frames or scans. When the energy is low, dedup requires a close colour match and a stricter Hamming limit.
- **Distance:** `d = 0.75·min(1,ham/32) + 0.2·min(1,meanΔE/50) + 0.05·aspectPenalty`, and similarity = 1−d. Hamming is computed first; the rest only for candidates.
- **Versioning:** `fingerprints.version` plus a "recompute fingerprints" job keep upgrades possible.

## Import (`internal/importer`, CLI + UI job)
- **Pipeline:** `WalkDir` (recursive optional; hidden and system entries skipped), then workers, then a single writer.
- **Workers:** read, SHA-256, sniff, decode, working image, thumbnail and fingerprint.
  - In-flight memory is capped by bytes (file bytes + w×h×4, about 1.5 GB total), so panoramas can't OOM.
- **Writer:**
  - One transaction per batch of about 128 MB or 100 images, with a WAL checkpoint between batches.
  - `INSERT OR IGNORE` the blob by sha. Thumbnail and fingerprint rows are written only for new blobs. Then the image row and tags.
- **Options:**
  - `-r`
  - `--tag t` (repeatable)
  - `--tag-folders` (each relative directory component becomes a tag)
  - `--skip-identical` (skip files whose sha is already held by an active image)
  - `--include-removed` (see below)
- By default everything is imported, so exact duplicates show up in the dedup step at no storage cost. A file whose sha matches a trashed or purged image is skipped as "previously removed" unless `--include-removed` is given.
- A per-file report lists files that were unsupported, corrupt or too big (the SQLite blob limit is 1e9). Nothing aborts the whole import.

## Export (`internal/exporter`, CLI + UI job)
- **Selection:** an `ImageQuery` (tags all/any/none, glob, explicit ids from the UI selection).
- **Writing:** the original bytes go to a temp file, which is then renamed. The original mtime is restored.
- **Names:**
  - Sanitised for Windows reserved names (CON, COM¹…), illegal characters, and trailing dots or spaces.
  - A missing extension is added from the format.
  - Collisions are detected case-insensitively and become `name (2).jpg`.
- **Options:**
  - `--keep-structure` (uses `original_path` directories)
  - `--overwrite` or `--skip-existing` (the default is to add a suffix)
  - `--manifest` (writes `photobag-manifest.json` with uid, name, tags and per-metric scores)

## Backup (`internal/backup`)
- **UI job:**
  1. Check free disk space; about 1× the bag size is needed.
  2. `VACUUM INTO` a fresh temp file next to the bag. The target must not exist, and the output is in DELETE journal mode, so it's a single clean file.
  3. Return a one-time token. `GET /api/backup/{token}` streams the file as `<bag>-YYYYMMDD-HHMMSS.photobag` and then deletes it.
- **Cleanup:** tokens expire. Stale temp files are cleaned up at startup and shutdown.
- **Other ways to back up:** "Save to server path" keeps the file. CLI: `photobag backup <bag> <out>`.
- **Reclaiming space:** "Compact" runs VACUUM; emptying the trash runs `incremental_vacuum`. The UI notes that purged bytes stay in free pages until Compact runs.

## Dedup (`internal/dedup`, CLI `photobag dedup` + UI)
- **Exact:** group active images by sha256.
- **Similar:**
  1. Load the fingerprints of in-scope active images.
  2. Run a parallel brute-force kNN: per-worker top-N heaps by Hamming distance, then the full distance for those candidates. About 200k images takes seconds.
  3. Remove `not_duplicates` pairs and keep pairs above a similarity floor of about 0.5.
  4. Store the result in memory under a scan id.
- **Clustering:** `GET /api/dedup/scans/{id}?threshold=` re-clusters server-side with union-find. The UI threshold slider is debounced and never triggers a rescan.
- **Proposals:**
  - Keeper: most pixels, then largest file, then most tags and comparisons, then earliest import.
  - Only members whose similarity *to the keeper* passes the threshold are pre-marked for deletion. Union-find can chain images together, so other members are shown unmarked.
- **Resolve:** one transaction per cluster.
  - Losers get `deleted_at` and `merged_into = keeper`, and the keeper inherits their tags.
  - Comparisons are **not** rewritten: runs stay intact and a restore stays possible. The score aggregation resolves `merged_into` instead.
  - Affected metrics are marked dirty. "Not duplicates" records pairs.
- **CLI:** `photobag dedup <bag> --mode exact|similar --neighbors N --threshold T [--apply]`. Without `--apply` it's a dry run that prints the clusters.
- **Trash:** restore (clears `merged_into`) or empty. Emptying purges to tombstones, garbage-collects unreferenced blobs, thumbnails and fingerprints, then runs incremental vacuum.
- **Visual similarity sorting:** a gallery sort that chains images greedily through the kNN graph, plus "sort by similarity to this image" from the lightbox.

## Scoring (`internal/scoring`)
- **Run creation:** a metric (pick an existing one or create one) plus an `ImageQuery`, which freezes the image list, plus a random seed. There are M = n(n−1)/2 pairs. The wizard shows M (n=200 already means 19,900 comparisons).
- **Pair order:**
  - A keyed Feistel permutation over [0, M) with cycle-walking, mapped from index k to (i, j) by triangular-number inversion. This needs O(1) memory and can jump straight to `(seed, position)`.
  - Left and right come from hash(seed, pos). Pairs that include an image trashed since the run started are auto-skipped with no row written.
- **API:** `next` returns `{pos, left, right}`. `compare {pos, winner}` and `skip {pos}` are idempotent through `UNIQUE(run_id,pos)`, so two open tabs can't overwrite each other. `undo` deletes the run's highest-pos row and rewinds. Also `stop` and `resume`. Status is active, stopped or complete.
- **Score calculation per metric:**
  1. Take all non-skip comparisons across all runs and map each image through `merged_into` to its final survivor. Drop self-pairs and pairs involving inactive images.
  2. Group the comparisons by unordered pair and compute `net = wins_ij − wins_ji`. Contradicting results cancel one-for-one.
     - If net > 0, i beats j with weight net.
     - If net == 0, the pair is dropped and counted as "cancelled".
  3. Report both raw and after-cancellation win/loss counts.
- **Bradley–Terry MAP fit:**
  - Hunter's MM algorithm, with a fixed virtual anchor (θ=0) that gets 1 virtual win and 1 virtual loss against each image. This acts as the prior: undefeated images and disconnected groups stay finite and comparable, and no renormalisation is needed.
  - Stop when max|Δθ| < 1e-6, with an iteration cap. When n ≤ 1500, finish with Newton steps, since that Hessian is needed anyway.
- **Standard error:** from the diagonal of the *inverse* Hessian, computed with a Cholesky factorisation, when n ≤ 1500. Above that, use a diagonal approximation and flag it `stderr_approx`.
- **Display:** `1000 + 400·θ/ln10`.
- **Recompute:** lazy on `scores_dirty`. It runs when a run stops or completes and when rankings are requested. There is also a "Recalculate" button.

## Server, security, jobs, API
- **serve:** `photobag serve <bag> [--addr 127.0.0.1:7474] [--open] [--dev] [--allow-remote]`.
- **Security** (`internal/server/security.go`):
  - A Host allow-list against DNS rebinding.
  - `http.CrossOriginProtection` against cross-site requests.
  - A random per-launch token. The printed or opened URL carries `?token=`, which is swapped for an HttpOnly cookie. This protects against other local users.
- **Jobs:**
  - A sequential queue for import, export, backup, dedup-scan, empty-trash, compact and fingerprint-recompute.
  - Progress goes out on SSE `/api/events`, streamed with `http.ResponseController` and no WriteTimeout. It also carries "data changed" events, which the UI turns into TanStack Query invalidations.
  - `GET /api/jobs/{id}` can be polled.
  - Graceful shutdown: cancel jobs, then checkpoint, then close.
- **Endpoints:**
  - `GET /api/stats`
  - `POST /api/images/ids` (ImageQuery + sort → ordered id list plus total; the grid virtualises over it)
  - `POST /api/images/batch` (metadata for the visible range)
  - `GET|PATCH /api/images/{id}` (rename)
  - `GET /api/thumbs/{sha}` (immutable cache)
  - `GET /api/images/{id}/preview?size=` (oriented JPEG, LRU cache, used by the lightbox and compare screens)
  - `GET /api/images/{id}/original`
  - `POST /api/images/tags` (bulk add/remove), `/trash`, `/restore`, `DELETE /api/trash`
  - `/api/tags` CRUD, with merge-on-rename
  - `/api/fs/list` (folder picker)
  - `/api/jobs/{import|export|backup|compact}`
  - `/api/dedup/scans`, `/api/dedup/resolve`
  - `/api/metrics` (+ `/rankings`, `/recalc`)
  - `/api/runs` (+ `/next`, `/compare`, `/skip`, `/undo`, `/stop`, `/resume`)

## Front-end pages (`web/src/pages`)
- **Gallery:**
  - Filters live in the URL: tag include/exclude chips, all/any mode, name glob, and sort by imported, taken, name, size, score:<metric> or visual similarity. Also a thumbnail size slider.
  - Virtualised grid.
  - Selection supports click, ctrl, shift and select-all-matching. A bulk bar offers tag, untag, export, trash and "score these".
  - The lightbox has keyboard navigation, inline rename, a creatable tag editor, metadata, per-metric scores, "find similar" and "open original".
- **Tags:** counts, rename or merge, delete, and click-through to a filtered gallery.
- **Dedup:**
  - Scan form: mode, N, scope.
  - Results: a threshold slider and a cluster queue. Each cluster card shows images side by side with a metadata diff and the proposed keeper.
  - Actions: keep/delete toggles, "Not duplicates", Apply & next, and Apply all proposed. Keyboard shortcuts.
- **Scoring:**
  - Metrics list, and runs per metric with progress, resume and stop.
  - New-run wizard: metric, source (all, tags, glob or current gallery selection), and a preview of n and M.
  - Compare screen: two previews and a progress bar. Keys: ←/→ pick, ↓ skip, Backspace undo, Esc stop. The next pairs are prefetched.
  - Rankings: ranked grid with score ± stderr, W/L, n and cancelled pairs.
- **Tools:**
  - Import: server folder picker, recursive, tags, tag-folders, skip-identical, include-removed.
  - Export: folder, query or selection, collision policy, keep-structure, manifest.
  - Backup: download or save to path.
  - Trash, Compact, and job history with per-file import reports.

## Build order
1. **Skeleton:** go module, cobra, `internal/bag` (creation pragmas, migrations, app-id, netfs), `info`, security middleware, the Vite+React+Mantine shell embedded plus `/api/stats`, tygo, `scripts/build.mjs`, CI.
2. **Imaging + CLI import:** `genfixtures` (including all 8 EXIF orientations, WebP with EXIF, animated GIF and WebP), thumbnails, fingerprints, unit tests.
3. **Gallery:** ids and batch API, grid, lightbox, previews, rename, trash and restore.
4. **Tags:** API, editor, filters, Tags page, bulk actions.
5. **Jobs, import and export:** job queue and SSE, UI import (folder picker), export (CLI + UI), backup (CLI + UI), Compact, `data_version` watcher.
6. **Dedup:** exact then similar, clustering, resolve/merge, review UI, CLI, similarity sorts.
7. **Scoring:** metrics, runs, Feistel scheduler, compare UI, BT solver, rankings, score sort.
8. **Polish:** README (usage, dev workflow), cross-compiled binaries, end-to-end pass.

## Verification
- **`go test ./...`:**
  - **Imaging:**
    - pHash stays close for resized, re-encoded (q50) and brightness-shifted copies, and is far apart for different images.
    - Low-energy (flat) images don't produce false matches.
    - All 8 EXIF orientations give correct oriented dimensions and pixels.
    - WebP EXIF is parsed, and animated WebP and GIF yield their first frame.
  - **Scoring:**
    - The Feistel permutation is a bijection over [0, M) for many M, and pair index ↔ (i, j) round-trips.
    - BT recovers a planted ranking from noisy synthetic comparisons (Kendall τ is high).
    - A>B plus B>A gives no edge. Undefeated images stay finite. Disconnected groups are handled.
    - The inverse-Hessian stderr matches a brute-force inverse on a small case.
    - `merged_into` resolution works.
  - **Dedup:** planted near-duplicates are found, `not_duplicates` is respected, pre-marking only covers images within the threshold of the keeper, and resolve plus restore round-trips.
  - **Bag:**
    - A new bag gets the right creation pragmas.
    - An existing old bag migrates with a pre-migration backup.
    - Foreign SQLite files are refused.
    - `pb_glob`, the tag key normalisation, and the `ImageQuery` SQL all behave correctly.
  - **Export:** Windows-reserved and colliding names are handled.
- **Integration test:** genfixtures (nested dirs, duplicate names, an exact duplicate, a resized duplicate, a corrupt file, an unsupported `.heic`).
  1. `import -r`, then check the counts and the report.
  2. Import again: identical files are skipped as expected.
  3. Export: every exported file's sha256 must equal its source.
  4. Backup: the backup opens with the same counts and in DELETE journal mode.
- **Web:** `npm run typecheck`, `npm run lint`, Vitest for utilities (selection ranges, query↔URL).
- **Manual/E2E:**
  1. Run `node scripts/build.mjs` and check the binaries.
  2. Add a `.claude/launch.json` entry running `photobag serve <tmp>.photobag` and drive it in the browser pane:
     1. Import the fixtures from Tools.
     2. Tag images and filter by tag, then rename one.
     3. Run a similar-dedup scan, move the threshold slider, and resolve one cluster (then restore it from the trash).
     4. Start a scoring run over ~6 images: compare, undo, then stop.
     5. Check the rankings.
     6. Export by tag and download a backup.
  3. Confirm no -wal or -shm files remain after shutdown.
