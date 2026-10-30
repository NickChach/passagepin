# PDF recovery and file reports

Normal parsing runs first. Recognized cross-reference failures can trigger one
bounded, in-memory recovery attempt. The original PDF is never rewritten and
no external executable or network service is used.

Recovery handles incorrect `startxref`, `/Prev`, and object byte offsets in
classic-table PDFs. Object bodies, table entries and revision trailers must
remain readable, with intact object syntax and direct stream lengths. A
structural scan records actual object boundaries,
skips binary stream payloads by their declared lengths, and reconstructs offsets
using the original tables' object identities and generations. Incremental
revisions are selected using the final `startxref` and the trailers' `/Prev`
links. A broken `/Prev` is repaired only when exactly one earlier table's own
`startxref` declared that offset. Missing, ambiguous, cyclic and forward history
links are rejected. Physically present revisions outside the selected chain
are excluded, including when searching for objects with damaged byte offsets.
Free entries remain free. Valid declared object offsets take precedence;
ambiguous duplicate objects are rejected.
The recovered catalog and page tree must validate, including physical page
counts. A late recovery during text extraction must preserve page identities,
page dictionaries and inherited resource associations already observed.

The recovery attempt is limited to 128 MiB of source data, 100,000 objects,
128 revisions and three seconds, with cooperative cancellation. Cross-reference
streams, object streams, hybrid references, encryption, missing object bodies,
damaged stream lengths and incomplete revisions are outside this recovery path.
Healthy files with these supported normal-reading features still use normal
parsing. Recovery can fix structural bookkeeping without resolving unrelated
font, content-stream or text-extraction limitations.

Index format 9 requires rebuilding older indexes for page hashes. Format 8 retried pages rejected
for missing publisher symbol mappings, explicitly undefined Type 1 glyphs and
redundant `ET` operators. Verified proprietary glyph aliases are scoped to their
font families, and ZapfDingbats uses Adobe's separate glyph list. A partial
ToUnicode map can fall back to the supported embedded Type 1 encoding; explicit
`.notdef` slots produce U+FFFD without guessing a character. Unknown named glyphs
still fail extraction. Version 7 applied corrected
spacing for transformed text and exclude whole documents when cross-reference
errors persist after a recovery retry. Version 6 added font/inline-image fixes
and partial indexing; version 5 added marked-content `ActualText` support and
explicit rejection of inherited ToUnicode maps. Version 4 introduced
revision-chain-safe recovery.

Document-level structural failures skip the complete PDF, including unresolved
cross-reference errors on the first recovery retry or any later read. Queued work
for that PDF is discarded, running embedding calls are joined, and no vectors
from it are published. Page-local extraction failures retain independently
readable pages and produce a partial report with physical page numbers. When a
changed PDF becomes unreadable, its old vectors are excluded from the new index.
Deleted PDFs disappear normally; skipped files are retried on the next update.
Recovered and partially indexed documents can be reused by their original source
hash, with their reports preserved; use `--rebuild` to retry cached failed pages.

The app saves the usable documents and file report atomically. The report
contains filenames, outcomes and reasons, with physical page numbers when
available. Skipped PDFs contribute no searchable pages; partial reports list the
retained page count and each failed page. Known unsupported features also have a stable
`feature` identifier (for example `encryption`, `inline-image-filter`, `stream-filter`,
`composite-font-encoding`, `tounicode-inheritance` or `missing-unicode-map`).
Other errors retain their parser or filesystem reason without a guessed feature.
Blank/image-only pages return empty text and are counted separately from failed
PDFs; OCR applies to image-only pages, not to font-decoding errors.
The report survives application restarts and is refreshed on every update.
If every PDF is unreadable, the resulting index is empty and the report lists
the skipped files. Source PDFs remain available outside PassagePin.

Cancellation, model failures, directory traversal failures, process/disk resource
errors and index-write failures still abort the operation, preserving the
previous saved and in-memory index. File-specific permission errors are reported
as skipped files. Recovery limits apply to recovery itself; normal extraction
retains its existing stream/object bounds.

## Local verification

```sh
go test -race ./internal/pdf ./internal/index ./internal/worker

# Read-only checks against optional independent files. The library check uses
# a fake embedder to validate extraction and failure isolation without inference.
PDFSEARCH_TEST_RECOVERY_PDF=/path/to/damaged.pdf \
  go test -v ./internal/pdf -run '^TestExternalRecoveryStructure$'
PDFSEARCH_TEST_LIBRARY_DIR=/path/to/pdfs \
  go test -v ./internal/index -run '^TestExternalLibraryPartialIndexing$'
```
