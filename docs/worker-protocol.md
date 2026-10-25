# Persistent worker protocol, version 1

Start `pdfsearch worker --threads 2`. Model/runtime options are the same as the
CLI. A bundled worker needs no model options. The model loads before readiness;
stdout is exclusively newline-delimited JSON and diagnostics go to stderr.

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
access to the shared model and library. A concurrent operation returns `busy`.
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
`empty_pages`, `updated_at`, and optional `issue`. Index results contain a
`library` summary and `stats` (`indexed`, `reused`, `pages`, `empty_pages`). Page
results contain `filename`, `page`, `text`, and a validated absolute `path` for
opening the file. Preview text is bounded to 200,000 bytes; indexing still uses
the full page. Page requests must refer to an indexed document inside the active
root, including after resolving symlinks.

Indexing sends additional progress events without completing the request:

```json
{"id":"2","event":"progress","data":{"phase":"embedding","filename":"energy.pdf","page":3,"pages":12}}
```

Phases are `discovering`, `reused`, and `embedding`. `pages` is the physical page
count of the current PDF, not a total across the library.

Cancellation is handled while an operation is running:

```json
{"id":"cancel-1","method":"cancel","params":{"target":"2"}}
```

It responds with `{"cancelled":true}` when the active ID matched. The operation
subsequently returns an error with code `cancelled`; response ordering is not
fixed. Cancellation is cooperative between bounded inference calls. Failed or
cancelled builds preserve the previous saved index and in-memory library.

`shutdown` cancels active work, waits for cleanup, acknowledges the request with
`{"stopped":true}`, and exits. Closing stdin has the same cleanup behavior
without an acknowledgement. OS interruption also cancels the worker. The client
should enforce a shutdown timeout in case a native call becomes stuck.

Other error codes are `invalid_request`, `busy`, and `operation_failed`.
Malformed JSON is reported without terminating the worker; input lines exceeding
1 MiB terminate the session. The protocol is local stdio IPC, not a network API.
