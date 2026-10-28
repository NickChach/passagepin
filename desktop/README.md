# PassagePin desktop

Electron + React + TypeScript, with a persistent Go search worker. The current
packaged release targets Linux x86-64 with glibc 2.38 or newer. Windows and macOS
UI code paths are present, but their native worker bundling and signed releases
are not implemented or tested yet.

## Run a release

For a local `0.8.0` build, use `releases/PassagePin-0.8.0-x86_64.AppImage` from a file manager, or extract
`releases/PassagePin-0.8.0-x64.tar.gz` and open `passagepin` in the extracted
folder. The AppImage requires an AppImage-capable system (commonly FUSE); the
archive avoids that requirement. Neither needs Node, Go, model downloads or
runtime flags. The app includes Chromium and the bundled Go worker.

1. Choose a folder containing PDFs.
2. Click **Index folder**. Subdirectories are included; progress is per document.
3. Enter a subject and click **Search**.
4. Select a ranked page to read its extracted text. **Open PDF** opens the file
   in the system PDF viewer; it does not promise navigation to a specific page in
   an arbitrary external viewer. The selected physical page is shown in the app.

Each folder is automatically saved as a library. Use **Add library** to save
another folder and select a name in **Your libraries** to switch. Each library
keeps its own index; switching does not require reindexing. **Rename** changes
the saved display name without moving the folder. **Remove** asks for confirmation
and deletes that library's saved entry and cached index, leaving its PDF files
unchanged. Removing the current library clears the search; select another saved
library to continue.

Index files, saved names, and the last selection are kept under Electron's OS
user-data folder, not beside the PDFs. Model/native caches follow the bundled CLI
behavior in the root README. The app reopens the last selected library on launch
and imports the folder remembered by earlier versions automatically. An unavailable
folder remains saved: reconnect it and select its name to retry, or remove it.
Opening PassagePin again shows the existing window, restoring it if minimized,
and requests focus. One app instance owns the profile so saved libraries cannot be
overwritten by a second process.
Use the **Dark mode** switch at the bottom of the sidebar to change appearance.
The first launch follows the system's color preference; an explicit selection
is saved locally and restored when the app is reopened.
PassagePin retains the original `pdfsearch-desktop` user-data directory and
theme preference key so existing indexes, library selections and appearance
settings carry over from PDF Search without reindexing.
Search scores are similarities, not probabilities or definitive topic detection.
The root README describes PDF-parser support and OCR limitations.

## Develop on Linux

From the repository root, build the bundled worker first:

```sh
make bundle
cd desktop
npm ci
npx --no-install install-electron
npm start
```

Node 22.12 or newer is required by the current Electron development package.
Dependencies are locked in `package-lock.json`. Build tools are only needed by
developers, not end users. `npm start` builds local assets and opens Electron;
there is no localhost server or remote frontend content in the shipped app.

`PDFSEARCH_WORKER=/absolute/path/to/pdfsearch` can choose another development
worker. The executable must implement `worker` protocol version 1. The packaged
application always launches its own sidecar from `resources/worker`; it does not
use that development override. A source worker can still use
`PDFSEARCH_MODEL_DIR` and `ONNXRUNTIME_SHARED_LIBRARY_PATH` for external assets.

## Build Linux packages

From the repository root:

```sh
./build.sh
# Launch the rebuilt app:
./desktop/releases/linux-unpacked/passagepin
```

The script rebuilds the Go worker, builds the Electron interface, and creates the
AppImage, tar.gz, and unpacked application under `desktop/releases/`. It resolves
paths relative to itself, so it can also be invoked from another working directory.
It requires Linux x86-64, Go 1.23+, Node 22.12+, npm, make, a C compiler and tar,
plus the model and runtime assets described in the root README. Existing npm and
Electron installations are reused. If `desktop/node_modules` is absent, npm
dependencies are installed automatically; the Electron runtime is installed if
needed. Initial dependency installation may need network access. After changing dependency
versions, run `./build.sh --install-deps` to reinstall from the lockfile.
Optional `MODEL_DIR` and `RUNTIME_DIR` environment variables select other local
assets; relative paths are resolved from the repository root. `./build.sh --help`
shows usage.

The equivalent manual commands are:

```sh
# From the repository root, after installing desktop dependencies:
make desktop-package

# Or from desktop/, after make bundle:
npm run package:linux

# Or create just the unpacked application:
npm run package:dir
```

The packager copies the bundled CLI outside `app.asar`, includes the Electron
runtime, and creates AppImage and tar.gz releases. Releases are local artifacts;
there is no publishing or automatic update service. Before public releases, add
appropriate signing, versioning and clean-machine tests for each supported OS.
The Go worker's embedded notices are available with its `licenses` command;
Electron's license files are included by the packager.

The packaging dependency `global-agent` is overridden to 4.1.3 to avoid the
vulnerable legacy `roarr` / `sprintf-js` chain pulled in by the packager's older
downloader. The current locked dependency set passes `npm audit`.

## Architecture

```text
React renderer
  -> isolated preload API
  -> Electron main process
  -> one long-lived Go subprocess over JSON lines
     -> one loaded MiniLM / ONNX session
     -> current library's in-memory index
```

The worker loads its model before sending `ready`, retains it across `open`,
`index`, `search`, and `page` requests, and releases it when stdin closes or it
receives `shutdown`. Indexes are loaded into memory when a library is opened.
User operations are serialized: a concurrent request gets `busy`, and
cancellation is read independently of active work. Within indexing, up to two
independent PDF readers overlap extraction with up to two embedding workers on
the resident CPU model session. Electron uses one native inference thread per
operation to avoid competing thread pools. Queued page text is bounded, and
progress events from different PDFs can interleave. The sidebar keeps a separate
filename, page count, and progress bar for each active PDF until its completion
event arrives. Reused PDFs update a count without replacing active rows.
Cancellation takes effect
between bounded model calls and waits for workers to finish. Successful index
updates are saved atomically; fatal failures and cancellation keep the last saved
index. Individual unreadable PDFs are skipped, while supported cross-reference
damage is recovered in memory. A persistent **Indexing report** shows skipped
and recovered filenames and reasons, including after restarting the app. Skipped
documents contribute no partial or stale results. See [PDF recovery](../docs/pdf-recovery.md).
See [indexing performance](../docs/indexing-performance.md) for tuning and
offline measurements. Rebuild the Go worker with `make bundle` from the repository
root when changing core indexing code; `npm run build` alone rebuilds Electron.

The renderer runs with Node integration disabled, context isolation and the
Chromium sandbox enabled. It cannot spawn processes or choose arbitrary files
through IPC. Folder selection happens in the main process. Preview/open requests
are checked against the active indexed library and resolved paths. Remote
navigation, new windows, renderer network connections, and permission requests
are blocked. PDF text is rendered as text, never HTML.

If the worker crashes, pending requests fail and the app offers **Restart search
engine**. Normal app quit closes the worker's stdin, waits for cancellation and
cleanup, and kills a stuck worker after five seconds. A fatal Electron main
process exit also closes its pipes; the Go worker then exits on EOF.

## Test

```sh
# Repository root:
go test -race ./...
go vet ./...

# desktop/:
npm test
npm run test:e2e

# Verify the packaged application and its packaged worker:
PDFSEARCH_TEST_PACKAGED_BINARY="$PWD/releases/linux-unpacked/passagepin" \
  npm run test:e2e
```

The Node integration test uses the real bundled Go worker and MiniLM model. The
Electron test uses a generated three-page PDF with a blank middle page, indexes
it, previews the correct page, makes two semantic searches, verifies the worker
PID is unchanged, checks that Node is unavailable in the renderer, and verifies
worker shutdown. It also checks light/dark switching with mouse and keyboard,
retained search results, and the saved appearance after reloading and restarting
the application. It stubs only the native folder chooser in the Electron main
process; search, extraction, IPC, inference, and index persistence are real.
Screenshots are saved to `test-results/desktop-search.png` and
`test-results/desktop-search-dark.png`. GUI tests require a
working desktop display or a suitable virtual-display setup. Linux release tests
use temporary XDG config/cache directories to isolate them from user data.

The library tests cover legacy migration, independent saved indexes, naming,
switching, duplicate folders through symlinks, restart persistence, unavailable
folders, removal without deleting PDFs, and operation/IPC guards. Registry unit
tests also check invalid input, corrupt files, and failed atomic writes.

To verify compatibility with an older PDF Search release, provide its AppImage
alongside the new one (with `APPIMAGE_EXTRACT_AND_RUN=1` if FUSE is unavailable):

```sh
PDFSEARCH_TEST_PREVIOUS_BINARY=/path/to/PDF-Search-0.1.0-x86_64.AppImage \
PDFSEARCH_TEST_PACKAGED_BINARY="$PWD/releases/PassagePin-0.8.0-x86_64.AppImage" \
  npm run test:e2e
```

The compatibility check creates an index and dark-mode preference in the old
app, then verifies both are restored and searchable in PassagePin.
