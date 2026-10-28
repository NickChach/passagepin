package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/darkearl/semantic-search/internal/bundle"
	"github.com/darkearl/semantic-search/internal/embedding"
	"github.com/darkearl/semantic-search/internal/index"
	"github.com/darkearl/semantic-search/internal/pdf"
	"github.com/darkearl/semantic-search/internal/worker"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

const usage = `pdfsearch — local semantic search across PDF pages

Usage:
  pdfsearch download [--model-dir DIR]
  pdfsearch index --dir DIR [options]
  pdfsearch search [options] "subject to search for"
  pdfsearch bundle-info
  pdfsearch licenses
  pdfsearch worker [options]

Run a command with --help for its options. Put flags before the query.
`

func defaultModelDir() string {
	if dir := os.Getenv("PDFSEARCH_MODEL_DIR"); dir != "" {
		return dir
	}
	if bundle.Available {
		return ""
	}
	return defaultDownloadDir()
}

func defaultDownloadDir() string {
	if dir := os.Getenv("PDFSEARCH_MODEL_DIR"); dir != "" {
		return dir
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = "."
	}
	return filepath.Join(cache, "pdfsearch", "all-MiniLM-L6-v2")
}

type modelFlags struct {
	dir, runtime string
	threads      int
}

func addModelFlags(f *flag.FlagSet) *modelFlags {
	m := &modelFlags{}
	threads := 0
	if f.Name() == "index" || f.Name() == "worker" {
		// Indexing parallelizes across pages. Avoid a second, large native pool
		// competing with the embedding workers; --threads remains configurable.
		threads = 1
	}
	f.StringVar(&m.dir, "model-dir", defaultModelDir(), "external model directory (bundled releases use their embedded model by default)")
	f.StringVar(&m.runtime, "runtime", os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_PATH"), "path to ONNX Runtime 1.22.0 shared library")
	f.IntVar(&m.threads, "threads", threads, "inference threads per model operation (0 lets ONNX Runtime choose)")
	return m
}

func addIndexFlags(f *flag.FlagSet) *index.Options {
	o := index.DefaultOptions()
	f.IntVar(&o.ExtractionWorkers, "index-workers", o.ExtractionWorkers, "parallel PDF readers (0 selects default; maximum 32)")
	f.IntVar(&o.EmbeddingWorkers, "embedding-workers", o.EmbeddingWorkers, "parallel page embeddings (0 selects default; maximum 32); tune with --threads")
	return &o
}

func (m *modelFlags) assets() embedding.Assets {
	if m.dir != "" {
		return embedding.Assets{Directory: m.dir}
	}
	return embedding.Assets{Weights: bundle.Weights, Vocabulary: bundle.Vocabulary}
}

func (m *modelFlags) open(ctx context.Context, assets embedding.Assets) (*embedding.Model, error) {
	library := m.runtime
	if library == "" && bundle.Available {
		var err error
		library, err = bundle.Prepare(ctx)
		if err != nil {
			return nil, err
		}
	}
	return assets.Open(library, m.threads)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return nil
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	f.SetOutput(stderr)
	switch args[0] {
	case "worker":
		m := addModelFlags(f)
		o := addIndexFlags(f)
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 || m.threads < 0 {
			return fmt.Errorf("usage: pdfsearch worker [model options]")
		}
		if err := o.Validate(); err != nil {
			return err
		}
		assets := m.assets()
		fingerprint, err := assets.Fingerprint()
		if err != nil {
			return err
		}
		model, err := m.open(ctx, assets)
		if err != nil {
			return err
		}
		defer model.Close()
		return worker.Serve(ctx, os.Stdin, stdout, worker.NewWithOptions(model, fingerprint, *o))
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return nil
	case "bundle-info":
		if len(args) != 1 {
			return fmt.Errorf("bundle-info does not accept arguments")
		}
		if !bundle.Available {
			fmt.Fprintln(stdout, "This is a source build without embedded model/runtime assets.")
			return nil
		}
		info, err := bundle.Info()
		if err != nil {
			return err
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(info)
	case "licenses":
		if len(args) != 1 {
			return fmt.Errorf("licenses does not accept arguments")
		}
		licenses, err := bundle.Licenses()
		if err != nil {
			return err
		}
		_, err = stdout.Write(licenses)
		return err
	case "download":
		dir := f.String("model-dir", defaultDownloadDir(), "destination for optional external model assets; bundled releases need no download")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return fmt.Errorf("download does not accept positional arguments")
		}
		if err := embedding.Download(ctx, *dir, stderr); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Model saved to", *dir)
		return nil
	case "index":
		m := addModelFlags(f)
		o := addIndexFlags(f)
		timings := f.Bool("timings", false, "report build, hashing, extraction, embedding, and save durations")
		dir := f.String("dir", "", "directory of PDFs (required)")
		path := f.String("index", "pdfsearch.index.json", "persistent index path")
		recursive := f.Bool("recursive", true, "include PDFs in subdirectories")
		rebuild := f.Bool("rebuild", false, "replace any existing index without reusing embeddings")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if *dir == "" || f.NArg() != 0 {
			return fmt.Errorf("usage: pdfsearch index --dir DIR [options]")
		}
		if m.threads < 0 {
			return fmt.Errorf("threads must be nonnegative")
		}
		if err := o.Validate(); err != nil {
			return err
		}
		var old *index.Index
		if !*rebuild {
			var err error
			old, err = index.Load(*path)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		assets := m.assets()
		fingerprint, err := assets.Fingerprint()
		if err != nil {
			return err
		}
		model, err := m.open(ctx, assets)
		if err != nil {
			return err
		}
		defer model.Close()
		idx, stats, err := index.BuildConcurrent(ctx, *dir, fingerprint, *recursive, old, func() (index.Extractor, error) { return pdf.New() }, model, stderr, nil, *o)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		saving := time.Now()
		if err := idx.Save(*path); err != nil {
			return err
		}
		stats.Timings.Saving = time.Since(saving)
		if *timings {
			t := stats.Timings
			fmt.Fprintf(stderr, "Build wall time: %s; cumulative worker time: hashing %s, extraction %s, embedding (tokenization + inference) %s; saving %s.\n", t.Total, t.Hashing, t.Extraction, t.Embedding, t.Saving)
		}
		fmt.Fprintf(stdout, "Saved %s: %d PDFs indexed, %d reused, %d searchable pages, %d empty pages skipped.\n", *path, stats.Indexed, stats.Reused, stats.Pages, stats.EmptyPages)
		if stats.Recovered > 0 || stats.Skipped > 0 {
			fmt.Fprintf(stdout, "File report: %d indexed using recovery, %d PDFs skipped.\n", stats.Recovered, stats.Skipped)
			for _, issue := range stats.Issues {
				fmt.Fprintf(stderr, "%s: %s", issue.Kind, issue.Filename)
				if issue.Page > 0 {
					fmt.Fprintf(stderr, " (page %d)", issue.Page)
				}
				if issue.Feature != "" {
					fmt.Fprintf(stderr, " [%s]", issue.Feature)
				}
				fmt.Fprintf(stderr, ": %s", issue.Reason)
				if issue.Kind == "skipped" {
					fmt.Fprint(stderr, "; no pages from this PDF were indexed")
				}
				fmt.Fprintln(stderr)
			}
		}
		if stats.EmptyPages > 0 {
			fmt.Fprintln(stderr, "Blank pages and pages with no searchable text were skipped. Scanned image-only pages need OCR.")
		}
		return nil
	case "search":
		m := addModelFlags(f)
		path := f.String("index", "pdfsearch.index.json", "persistent index path")
		top := f.Int("top", 10, "maximum number of matching pages")
		minScore := f.Float64("min-score", 0, "minimum cosine similarity (-1 to 1)")
		asJSON := f.Bool("json", false, "output a JSON array of filename, page and score")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		query := strings.TrimSpace(strings.Join(f.Args(), " "))
		if query == "" {
			return fmt.Errorf("usage: pdfsearch search [options] \"subject\"")
		}
		if *top < 1 || math.IsNaN(*minScore) || *minScore < -1 || *minScore > 1 || m.threads < 0 {
			return fmt.Errorf("top must be positive, min-score between -1 and 1, and threads nonnegative")
		}
		idx, err := index.Load(*path)
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("index not found; run pdfsearch index --dir DIR first")
		}
		if err != nil {
			return err
		}
		assets := m.assets()
		fingerprint, err := assets.Fingerprint()
		if err != nil {
			return err
		}
		if fingerprint != idx.Fingerprint {
			return fmt.Errorf("model differs from the indexed model; run index --rebuild with these model assets")
		}
		model, err := m.open(ctx, assets)
		if err != nil {
			return err
		}
		defer model.Close()
		vector, err := model.Embed(ctx, query)
		if err != nil {
			return err
		}
		results, err := idx.Search(vector, *top, *minScore)
		if err != nil {
			return err
		}
		if *asJSON {
			return json.NewEncoder(stdout).Encode(results)
		}
		if len(results) == 0 {
			fmt.Fprintln(stdout, "No pages matched the requested similarity threshold.")
			return nil
		}
		w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "SCORE\tPAGE\tFILENAME")
		for _, result := range results {
			fmt.Fprintf(w, "%.4f\t%d\t%s\n", result.Score, result.Page, result.Filename)
		}
		return w.Flush()
	default:
		return fmt.Errorf("unknown command %q; run pdfsearch --help", args[0])
	}
}
