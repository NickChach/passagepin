# PassagePin

PassagePin finds discussions of a subject in a directory of PDFs, with a desktop
interface and a Go CLI named `pdfsearch`. It embeds
each page with Hugging Face's `sentence-transformers/all-MiniLM-L6-v2`, stores one
384-dimensional vector per nonempty page, and ranks pages by cosine similarity
to the query. Results include the relative filename and the physical PDF page
number, starting at 1.

The CLI, PDF parser, text extractor and BERT tokenizer are written in Go.
PDF extraction is implemented in this repository and needs no
Poppler executables or third-party PDF parsing library. Model inference uses the
native ONNX Runtime C/C++ library through cgo. No Python, model server, API key,
or vector database is required.
After setup, indexing and searching work offline. PDF contents and queries stay
on your machine.

## Desktop application

The PassagePin Electron application with a React/TypeScript interface is in `desktop/`.
It provides folder selection, incremental indexing with progress and
cancellation, ranked page results, extracted-page reading, and opening PDFs in
the system viewer. A persistent Go worker keeps MiniLM and the active index
loaded across searches. The renderer has no Node.js or direct filesystem access.

Page previews show the first 200,000 bytes of extracted text, followed by a
shortening notice for longer pages. Indexing still uses the full extracted page
text. Open the PDF in your system viewer to read the complete page.

The current desktop packages target Linux x86-64 with glibc 2.38 or newer.
Download the [PassagePin 0.1.0 AppImage](https://github.com/NickChach/passagepin/releases/download/v.0.1.0/PassagePin-0.1.0-x86_64.AppImage),
or visit [GitHub Releases](https://github.com/NickChach/passagepin/releases) for
available versions and assets. From the directory containing the download:

```sh
chmod +x PassagePin-0.1.0-x86_64.AppImage
./PassagePin-0.1.0-x86_64.AppImage
```

After making it executable, you can also launch it from a file manager. The
AppImage requires an AppImage-capable system, commonly using FUSE.

Local packaging writes the AppImage and `PassagePin-0.1.0-x64.tar.gz` under
`desktop/releases/`. The archive provides an alternative without the AppImage
FUSE requirement: extract it and launch `linux-unpacked/passagepin`.

The app includes Electron and the bundled Go worker; end users need no compiler,
Node, Go, model download, or CLI setup. Windows/macOS worker bundling and release
validation remain future work.

See `desktop/README.md` for development, packaging, architecture and GUI tests.
The CLI remains available independently as described below.

## Run the bundled executable (Linux x86-64)

The bundled release is `dist/pdfsearch-linux-amd64`. Distribute this one file;
users do not need Go, a compiler, curl, Poppler, a separate runtime, a model
download, or setup environment variables. From the directory containing it:

```sh
chmod +x pdfsearch-linux-amd64
./pdfsearch-linux-amd64 index --dir /path/to/pdfs
./pdfsearch-linux-amd64 search --top 5 "renewable energy sources"
```

MiniLM weights and the vocabulary are embedded and loaded from memory. On the
first index/search command, the program extracts ONNX Runtime, `libstdc++`, and
`libgcc_s` to a private, versioned directory under the OS user cache directory
(`$XDG_CACHE_HOME/pdfsearch-bundled` or `~/.cache/pdfsearch-bundled` on Linux).
No assets are downloaded. Subsequent runs verify and reuse these files; corrupt
or incomplete copies are replaced atomically. Native files have mode 0600 and
their containing directories have mode 0700. Help and license commands do not
extract the runtime. The model and vocabulary are never extracted.

The release needs a writable user cache that permits loading shared libraries,
and a compatible **glibc-based Linux
x86-64** system. glibc and the system loader remain operating-system
dependencies. It does not run directly on Alpine/musl, macOS, Windows, or ARM.
The build records its minimum glibc version; inspect it and the included licenses
without initializing inference:

```sh
./pdfsearch-linux-amd64 bundle-info
./pdfsearch-linux-amd64 licenses
```

`--model-dir` / `PDFSEARCH_MODEL_DIR` and
`--runtime` / `ONNXRUNTIME_SHARED_LIBRARY_PATH` still override bundled defaults.
When moving from an older manual setup, unset those variables to use the bundle.
The index remains a separate, writable data file and defaults to
`pdfsearch.index.json` in the working directory.

## Build the bundled release

Release builders need Go, a C compiler and make. With the model and official
runtime assets prepared using the source setup below:

```sh
make bundle
# Output: dist/pdfsearch-linux-amd64
```

The builder stages assets in ignored `internal/bundle/assets/`, includes native
dependencies and license notices, then builds with the `bundled` tag. It uses
the existing local assets and does not run asset downloads. This initial builder
targets Debian/Ubuntu-style Linux amd64 build hosts, including their system C++
libraries and license files. The executable's minimum glibc version is computed
from its native dependencies and a probe build of the Go CLI. Build on an older
supported distribution to produce a release with a lower compatibility baseline;
copying a binary built on a newer system does not remove that baseline.

Paths can be customized:

```sh
make bundle MODEL_DIR=/path/to/model \
  RUNTIME_DIR=/path/to/onnxruntime-linux-x64-1.22.0 \
  BUNDLE_OUT=dist/pdfsearch
```

Bundled and external copies of the same model produce identical index
fingerprints, so switching packaging does not require rebuilding an otherwise
compatible index. Native caches are keyed by a manifest hash; different bundles
use separate cache directories. A release's embedded license notices are
available through `licenses` when distributing only its executable.

## Source setup (without bundled assets, Linux x86-64)

Requirements: Go 1.23 or newer, a C compiler, and ONNX Runtime **1.22.0**
(matching the pinned Go binding). The C compiler is for inference, not PDF parsing.

```sh
# Debian / Ubuntu; use the equivalent packages for your distribution.
sudo apt-get install build-essential curl

make build

# Download the native CPU runtime from Microsoft's release.
mkdir -p .runtime
curl -fL --retry 2 \
  https://github.com/microsoft/onnxruntime/releases/download/v1.22.0/onnxruntime-linux-x64-1.22.0.tgz \
  -o .runtime/onnxruntime.tgz
tar -xzf .runtime/onnxruntime.tgz -C .runtime
export ONNXRUNTIME_SHARED_LIBRARY_PATH="$PWD/.runtime/onnxruntime-linux-x64-1.22.0/lib/libonnxruntime.so.1.22.0"

# Download the model (~87 MiB) and BERT vocabulary (~227 KiB) once.
export PDFSEARCH_MODEL_DIR="$PWD/.models"
./bin/pdfsearch download
```

The download command fetches the ONNX model and vocabulary directly from the
[official Hugging Face repository](https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2)
at immutable revision `1110a243fdf4706b3f48f1d95db1a4f5529b4d41`.
It does not convert or train a model. Repeating it replaces the local assets.

For other platforms, install the matching CPU release of
[ONNX Runtime 1.22.0](https://github.com/microsoft/onnxruntime/releases/tag/v1.22.0).
Set `ONNXRUNTIME_SHARED_LIBRARY_PATH` to the actual shared library (`.so`, `.dylib`,
or `.dll`), or pass `--runtime`. Linux x86-64 is tested here; other platforms have
not been verified. Building requires cgo (`CGO_ENABLED=1`), including on Windows.

Without `PDFSEARCH_MODEL_DIR` or `--model-dir`, assets default to
`<OS user cache directory>/pdfsearch/all-MiniLM-L6-v2`.

## Index and search

```sh
# Recursively find PDFs and embed one page at a time.
./bin/pdfsearch index --dir /path/to/pdfs --index library.index.json

# Search by subject. Flags go before the query.
./bin/pdfsearch search --index library.index.json --top 5 \
  "how renewable sources can reduce emissions from electricity generation"

# Restrict matches and emit machine-readable results.
./bin/pdfsearch search --index library.index.json --top 20 --min-score 0.4 --json \
  "protecting crops during periods of drought"
```

Text output has `SCORE`, `PAGE`, and `FILENAME` columns. JSON output is an array:

```json
[
  {"filename": "reports/energy.pdf", "page": 17, "score": 0.6379}
]
```

Filenames are relative to the directory indexed. Page numbers refer to the
physical pages you navigate to in a PDF viewer, including cover and blank pages;
they may differ from printed page labels.

Run `index` again with the same directory and index path to update it. SHA-256
content hashes let it reuse unchanged PDFs; changed PDFs are re-embedded and
deleted PDFs disappear. Use `--rebuild` when changing the directory, model, or
embedding pipeline. Any extraction or inference error aborts the update and
leaves the previous saved index intact. Avoid running multiple index writers
against the same index path at once.

Indexes made by the earlier Poppler-based implementation must be recreated with
`index --rebuild`. The index format version prevents mixing extraction methods.

Additional options:

| Command | Option | Default | Purpose |
| --- | --- | --- | --- |
| index | `--recursive=false` | recursive | Only index PDFs directly in the directory |
| index | `--rebuild` | false | Replace an index without reusing vectors |
| index, search | `--model-dir DIR` | embedded model in bundles; user cache otherwise | Use external local model assets |
| index, search | `--runtime FILE` | bundled runtime / environment | Select the native runtime library |
| index, search | `--threads N` | 0 | Inference CPU threads; 0 lets the runtime choose |
| index, search | `--index FILE` | `pdfsearch.index.json` | Select the saved index |
| search | `--top N` | 10 | Maximum number of returned pages |
| search | `--min-score N` | 0 | Minimum cosine similarity, from -1 to 1 |
| search | `--json` | false | Emit JSON instead of a table |

## Page-sized chunks and MiniLM's token limit

The [model card](https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2)
specifies mean pooling and L2 normalization, and notes that its usual input limit
is 256 wordpieces. Many PDF pages are longer than that.

The Go reader opens each PDF once, reads its cross-reference index and page tree,
then decodes the selected page's content and required font resources on demand.
It tokenizes that page and feeds consecutive windows of up to 254 wordpieces plus
`[CLS]` and `[SEP]` to MiniLM. All unpadded token embeddings are summed across
windows and L2-normalized once. That is a token-weighted mean of the windows
before normalization. Short pages and queries use the model's usual pooling;
long pages retain their text beyond the first 256 tokens. The same embedding
pipeline is applied to the query.

There is exactly **one stored vector and one result per page**. Windows are an
internal inference detail and never span pages. The model never receives a
whole PDF. Only the current page's text and model outputs are held during
embedding. The persistent index contains filenames, hashes, page numbers, and
vectors; it does not store extracted page text. Model asset hashes and a pipeline
version prevent searching an index with incompatible query embeddings.

## Go PDF reader

The reader lives in `internal/pdf` and implements a practical subset of
[ISO 32000-1](https://opensource.adobe.com/dc-acrobat-sdk-docs/standards/pdfstandards/pdf/PDF32000_2008.pdf)
for text extraction. It supports:

- Classic cross-reference tables, cross-reference streams, hybrid references,
  incremental updates, and compressed object streams.
- Page trees with inherited resources; single or array content streams; direct
  and indirect stream lengths. Opening a document reads structure, not the text
  of every page. A test verifies that a broken later content stream does not
  prevent reading an earlier page.
- Flate, LZW (both EarlyChange modes), ASCII85, ASCIIHex and RunLength filters,
  PNG predictors, and TIFF prediction for 8-bit components.
- Literal and hexadecimal strings; text-show, text-position and spacing
  operators; graphics transformations; Form XObjects. Image XObjects are skipped
  without decoding their pixels.
- ToUnicode CMaps with character and range mappings, UTF-16 surrogate pairs and
  multi-character mappings; horizontal Identity-H CID fonts; Standard, WinAnsi
  and MacRoman encodings with Differences; common explicit built-in encodings
  in embedded Type 1 fonts; embedded horizontal metrics for the 14 standard PDF
  fonts when explicit widths are omitted.

Glyph names are mapped using the embedded
[Adobe Glyph List](https://github.com/adobe-type-tools/agl-aglfn), with its
copyright and BSD license retained in `internal/pdf/glyphlist.txt`. Its license
notice must accompany redistributed binaries as well. The parsing and extraction
logic is written here; the glyph list supplies character-mapping data. Adobe's
core font metrics are reduced to character codes, glyph names and widths in
`internal/pdf/standard-fonts.txt`; their notices and distribution terms are
retained in `THIRD_PARTY_NOTICES.md` and included in bundled releases.

This is not a complete PDF rendering or repair engine. Unsupported features
needed for extraction return an error, including encrypted files, inline images,
vertical or custom composite-font encodings, inherited ToUnicode CMaps, and
embedded TrueType/CFF fonts that lack both an explicit encoding and a Unicode
map. Unknown glyph mappings also return errors. Some Type 1 encoding programs
outside the supported declarative forms cannot be read. Image-only pages with
ordinary image XObjects yield no text and still require OCR.

Text follows content-stream order with approximate spaces inferred from glyph
positions. There is no advanced column/table reconstruction, annotation text
extraction, or interpretation of marked-content `ActualText` overrides.
Reading order and extraction fidelity can therefore differ from a full PDF engine.
Streams and a page's combined content are limited to 64 MiB, with additional
object and recursion limits. Decoded page contents are released after extraction;
document structure is kept until the reader advances to the next PDF.

## Practical limits

- This is similarity ranking, not a yes/no determination that a subject appears.
  Scores are not probabilities. The CLI defaults to up to ten pages; the desktop
  app defaults to 20, with options for 10, 20, or 50 results. Both default to a
  minimum similarity of zero. Set `--min-score` in the CLI or adjust Minimum
  similarity in the desktop app's Search options for stricter filtering, and
  calibrate the threshold against your documents.
- One vector averages a page's subjects. A short relevant passage within an
  otherwise unrelated page can rank lower, especially on dense pages. Window
  aggregation covers the full page but differs from a single full-context pass.
- The specified MiniLM model is intended for English. Retrieval quality in
  other languages may be lower.
- Scanned image-only pages need OCR first. Pages with no extractable text are
  skipped with a summary; blank pages still count toward physical numbering.
- Corrupt, password-protected, or unreadable PDFs can stop indexing. The previous
  index remains available. Symlinks to PDFs or directories are not followed.
- The JSON index is loaded into memory and searched exactly, in linear time.
  This is suitable for a local library; very large collections will eventually
  benefit from a disk-backed vector index. Text extraction quality depends on
  the PDF's text layer and reading order.

## Verification

```sh
make test
make vet

# After make bundle has staged the embedded assets:
go test -race -tags bundled ./...

# Test the copied release in an isolated directory with no setup variables.
PDFSEARCH_TEST_BUNDLE_BINARY="$PWD/dist/pdfsearch-linux-amd64" \
  go test -v ./cmd/pdfsearch -run TestReleaseExecutable

# Optional: real offline MiniLM + Go PDF reader integration test after setup.
PDFSEARCH_TEST_MODEL_DIR="$PDFSEARCH_MODEL_DIR" \
  go test -v ./cmd/pdfsearch -run TestSemanticSearchIntegration
```

The regular tests cover tokenization, preservation of long-page tails, actual
PDF extraction with a blank middle page, ranking and filtering, incremental
updates, deleted PDFs, recursion, model compatibility, and extraction failures.
PDF tests also cover compression, Unicode fonts, inherited resources, Form
XObjects, modern object streams, incremental cross-references, and malformed or
unsupported documents. PDF extraction and CLI integration tests run with an
empty PATH so helper programs cannot be used. The optional integration test uses
the actual model and checks the correct physical page and reuse of unchanged
PDFs. No test downloads models automatically.

The PDF package also builds independently without cgo:

```sh
CGO_ENABLED=0 go test ./internal/pdf

# Optional compatibility check against an independent local PDF.
PDFSEARCH_TEST_PDF=/path/to/example.pdf go test -v ./internal/pdf -run TestExternalPDF
```

To also verify real inference over long pages, use the same environment with
`go test -v ./internal/embedding -run TestFullPageEmbeddingIntegration`.

## Persistent worker

`pdfsearch worker [--threads N]` loads MiniLM once and serves JSON-line requests
over stdin/stdout. Electron owns one such process for its lifetime. `open` loads
a library's saved index, `index` builds it with structured progress, `search`
reuses the model and in-memory index, and `page` reads a selected physical page.
`cancel` can interrupt an active operation, and `shutdown` or stdin EOF releases
the session. See `docs/worker-protocol.md` for the versioned protocol.

## License

PassagePin's original source code is licensed under the [MIT License](LICENSE).
Third-party components and assets remain under their respective licenses. See
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) and the bundled license notices
available through the `licenses` command in bundled executables. Ordinary source
builds do not embed these notices; consult the notice file and dependency
licenses directly.
