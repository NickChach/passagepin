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
revisions are applied in order, and free entries remain free. Valid declared
object offsets take precedence; ambiguous duplicate objects are rejected.
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

Unreadable PDFs are skipped as complete documents. Queued work for a failed PDF
is discarded, running embedding calls are joined, and no vectors from that PDF
are published. When a changed PDF becomes unreadable, its old vectors are
excluded from the new index. Deleted PDFs disappear normally; skipped files are
retried on the next update. Recovered documents can be reused by their original
source hash, and their recovery status stays visible.

The app saves the usable documents and file report atomically. The report
contains filenames, outcomes and reasons, with physical page numbers when
available. It survives application restarts and is refreshed on every update.
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
