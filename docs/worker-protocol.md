# Persistent worker protocol, version 1

Start `pdfsearch worker --threads 1`. Model/runtime options are the same as the
CLI. A bundled worker needs no model options. The model loads before readiness;
stdout is exclusively newline-delimited JSON and diagnostics go to stderr.
Indexing also accepts `--index-workers N` and `--embedding-workers N`. Both
default to up to two, respecting Go's CPU limit; 0 selects the default and the
maximum is 32. These control workers within an index request. Native inference
defaults to one thread per operation; tune it together with embedding workers.

```json
{"event":"ready","data":{"protocol":1,"pid":1234,"model_loads":1}}
```

One request per line, with a unique nonempty string ID:

```json
{"id":"1","method":"open","params":{"directory":"/path/to/pdfs","index_path":"/path/to/app-data/library.json"}}
{"id":"2","method":"index","params":{"rebuild":false}}
{"id":"3","method":"search","params":{"query":"renewable energy sources","top":20,"min_score":0}}
{"id":"4","method":"page","params":{"filename":"energy.pdf","page":3}}
```

Send the next operation after the previous result/error. These methods serialize
access to the library. A concurrent operation returns `busy`. Within indexing,
independent PDF readers feed a bounded queue of pages to concurrent calls on
the shared CPU model. The model still loads only once.
`status` returns the current library summary. `open` changes the active library
and loads a saved index without reloading the model. A missing index produces an
unindexed library; an incompatible index produces an `issue` requiring rebuild.
Search and preview do not automatically update an index when PDFs change.

Each request has one terminal `result` or `error` response with the same ID:

```json
{"id":"3","result":{"results":[{"filename":"energy.pdf","page":3,"score":0.64}],"query":"renewable energy sources"}}
{"id":"3","error":{"code":"operation_failed","message":"index this folder before searching"}}
```

Library summaries contain `directory`, `indexed`, `documents`, `pages`,
`empty_pages`, `failed_pages`, `updated_at`, optional `issue` (incompatible index), and optional
`issues` (per-file reports). Index results contain a
`library` summary and `stats` (`indexed`, `reused`, `pages`, `empty_pages`,
`timings`, `recovered`, `partial`, `skipped`, `failed_pages`, and optional `issues`). Each file issue has
`filename`, `kind` (`recovered`, `partial`, or `skipped`), `reason`, and an optional physical
`page` for extraction failures. Partial reports also include `indexed_pages` and
`failures`, an ordered array of `{page, reason, feature?}` entries. Fully skipped
documents can include `failures` when individual pages were attempted. Failed
pages are counted separately from empty/tokenless pages. Reports are persisted in
the index and restored when unchanged documents are reused. `partial` counts
documents with retained searchable pages and extraction failures. `recovered`
counts indexed documents using cross-reference recovery, including reused documents.
Both counts are subsets of `indexed + reused`, not additional document counts.
Timings contain `hashing_ns`, `extraction_ns`, `embedding_ns`,
`total_ns`, and `saving_ns`. Stage times are cumulative across workers and can
exceed the build wall time (`total_ns`); embedding includes tokenization and
inference. Saving is measured separately from the build. Page
results contain `filename`, `page`, `text`, and a validated absolute `path` for
opening the file. Preview text is bounded to 200,000 bytes; indexing still uses
the full page. Page requests must refer to an indexed document inside the active
root, including after resolving symlinks.

Indexing sends additional progress events without completing the request:

```json
{"id":"2","event":"progress","data":{"phase":"embedding","filename":"energy.pdf","page":3,"pages":12}}
```

Phases are `discovering`, `reused`, `embedding`, `completed`, and `skipped`.
`pages` is the physical page
count of the current PDF, not a total across the library.
Events are serialized, but events from different PDFs may interleave. An
`embedding` event marks a page entering the extraction/embedding pipeline,
including a blank page which will subsequently be skipped.
A `completed` event identifies a PDF whose page embeddings and final source-hash
check have all finished. It includes `filename`, `page`, and `pages` (both page
fields equal the physical page count). Clients should keep independent active
progress entries keyed by the full relative filename and remove each entry on
`completed` or `reused`, rather than treating the last page event as completion.
A `skipped` event also removes an active PDF, with `reason` and an optional
physical `page`. `completed` and `reused` can include `recovered: true` and/or `partial: true`.
`discovering` starts a fresh progress snapshot; terminal success, cancellation,
failure, and worker restart clear the active entries.

Cancellation is handled while an operation is running:

```json
{"id":"cancel-1","method":"cancel","params":{"target":"2"}}
```

It responds with `{"cancelled":true}` when the active ID matched. The operation
subsequently returns an error with code `cancelled`; response ordering is not
fixed. Cancellation is cooperative between bounded inference calls. Failed or
cancelled builds preserve the previous saved index and in-memory library. An
individual page extraction failure excludes that page and continues with later
physical pages. A document with healthy searchable pages is retained with a
partial report; a document with failed pages and no searchable pages is skipped.
Unreadable document structure or recovery that changes page identity discards
the entire document. These file reports do not fail the request. Model failures, traversal failures,
process/disk resource errors and index-write failures remain fatal.
All indexing workers finish and owned PDF readers close before a terminal
response is sent. The index is sorted into filename and physical-page order
before saving, independent of worker completion order.

`shutdown` cancels active work, waits for cleanup, acknowledges the request with
`{"stopped":true}`, and exits. Closing stdin has the same cleanup behavior
without an acknowledgement. OS interruption also cancels the worker. The client
should enforce a shutdown timeout in case a native call becomes stuck.

Other error codes are `invalid_request`, `busy`, and `operation_failed`.
Malformed JSON is reported without terminating the worker; input lines exceeding
1 MiB terminate the session. The protocol is local stdio IPC, not a network API.

The desktop IPC bridge wraps handler replies in `{ok: true, value}` or
`{ok: false, error: {code, message}}`. This envelope crosses both IPC and
contextBridge as data. The renderer unwraps it locally, so cancellation uses the
`cancelled` code rather than diagnostic text or filenames. This desktop envelope
does not change the worker's JSON-lines protocol above.

## Resolving a file for the system viewer

The additive `resolve-pdf` method accepts the same `filename` and physical `page`
parameters as `page`, and returns `{ "path": "/absolute/library/file.pdf" }`.
It checks active-index membership, page range, resolved path containment, and
that the target is a regular file, without extracting page text. The desktop
main process uses this path to open the system viewer; paths are not forwarded
to the renderer. A failed preview therefore does not prevent opening the PDF.
Use the worker bundled with the desktop release; older version-1 workers may
not implement this additive method. Rebuilding the desktop after Go changes
requires rebuilding the worker as well.

The Go JSON dispatcher delegates to typed engine operations. The desktop client
validates method results and progress before delivering them to callers.
