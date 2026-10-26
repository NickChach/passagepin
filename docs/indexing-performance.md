# Indexing performance

The Go core runs directory discovery, a pool of PDF readers, and a pool of page
embedding workers as a pipeline. Each PDF reader owns its parser and document
cache. A channel holds at most twice the embedding worker count in queued page
strings, with additional pages held by the active readers and model calls.
Extracted text is discarded after embedding; the index still stores exactly one
vector per nonempty physical page.

The default is up to two readers and two embedding workers, capped by
`runtime.GOMAXPROCS(0)`, with one native inference thread per call. The CPU ONNX
session and immutable vocabulary are shared; tensors, outputs, and pooling
buffers belong to individual calls. Runtime/session construction and destruction
remain serial. The indexer joins every worker before the model can close.
ONNX Runtime tests concurrent CPU session calls in
[`testRunModelMultipleThreads`](https://github.com/microsoft/onnxruntime/blob/v1.22.0/onnxruntime/test/python/onnxruntime_test_python.py).
The application also verifies concurrent long/short-page results against serial
results using the real pinned model and runtime.

Window order and pooling within a page are unchanged, so existing indexes and
model fingerprints remain compatible. Completion order does not affect saved
document or page order. File hashes are checked before reading and again after
all embeddings for that PDF finish. Individual document failures discard that
PDF's work and produce a skipped-file report while other readers continue.
Fatal errors or cancellation stop the pipeline, drain outstanding jobs, close
readers, and leave the previous saved and in-memory index available.

## Measure a library

```sh
./bin/pdfsearch index --dir /path/to/pdfs --index /tmp/benchmark.index.json \
  --rebuild --timings --index-workers 2 --embedding-workers 2 --threads 1
```

Use a separate index path for measurements. `--rebuild` measures initial
indexing; omit it on a second run to measure reuse. `--timings` reports build
wall time, cumulative hashing, extraction, and embedding worker time, plus
saving time. Embedding includes tokenization and inference. Stage times overlap
and must not be added to estimate wall time. Model loading is outside build
time; a process timer also includes startup. The worker returns the same stage
durations in `stats.timings`, encoded as nanoseconds.

Tune `--embedding-workers` together with `--threads`: more native threads and
more concurrent calls can compete for the same CPU. Each worker count accepts
1–32, with 0 selecting the default. Reader concurrency helps extraction-heavy
PDFs and can overlap page preparation with inference even for a single PDF.
Embedding concurrency can process independent pages of a single PDF. A page's
individual windows still execute in order. Batching is not used.

## Offline benchmarks and correctness checks

With the model and runtime already available locally:

```sh
export PDFSEARCH_TEST_MODEL_DIR="$PWD/.models"
export ONNXRUNTIME_SHARED_LIBRARY_PATH="$PWD/.runtime/onnxruntime-linux-x64-1.22.0/lib/libonnxruntime.so.1.22.0"

go test -race ./...
go test -p 1 ./...  # Also runs real-model integration checks with these variables.

# Run benchmarks separately to avoid competing with other tests or benchmarks.
go test ./internal/index -run '^$' -bench BenchmarkIndexReal -benchtime=1x -count=3
go test ./internal/embedding -run '^$' -bench BenchmarkEmbeddingStages -benchtime=10x
```

`BenchmarkIndexReal` generates eight PDFs of six pages each: 40 searchable pages
and eight blank pages, with long-page windows. It compares reader, embedding,
and native-thread counts and reports cumulative stage times, saving, and total
time. Set `PDFSEARCH_BENCH_DIR` to an empty scratch directory to keep this corpus
for comparing executables from different revisions. The fixture PDFs in that
directory are overwritten by each benchmark run. Model setup and one warmup
embedding are excluded from benchmark time.

`BenchmarkEmbeddingStages` measures tokenization and inference separately. The
concurrency tests exercise bounded lookahead, concurrent readers and embeddings,
stable results, blank pages, incremental reuse and deletion, extraction/model
failure, source edits during inference, cancellation with queued work, and
reader cleanup. Real-model tests compare concurrent embeddings with serial
vectors; CLI and worker tests cover semantic search and preserving saved state.

## Local before/after measurement (2026-10-26)

On the AMD Ryzen 3 3250U used for development (four logical CPUs), the original
executable built from dev commit `f0c4a8e` was compared with the new pipeline on
the generated corpus above. The original used Electron's previous setting of
two native inference threads. The candidate used two readers, two embedding
workers, and one native inference thread. Each executable received one warmup
run, followed by three measured rebuilds in alternating order. No other test or
benchmark ran alongside this comparison. Times include model loading and saving.

| Implementation | Measured times (seconds) | Median |
| --- | --- | --- |
| Original sequential indexer | 4.379, 4.674, 4.560 | 4.560 s |
| Bounded concurrent pipeline | 4.291, 5.010, 4.280 | 4.291 s |

The median fell by about 6%; the saved vectors were identical (maximum component
difference zero). This is a small, synthetic sample with noticeable run-to-run
variation, rather than a guarantee for other libraries or CPUs. In the worker
configuration benchmark, extraction and hashing took tens of milliseconds while
embedding took seconds. This corpus is limited by model inference, so reader
concurrency alone cannot provide a large improvement.
