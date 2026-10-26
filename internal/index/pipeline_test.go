package index

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkearl/semantic-search/internal/pdf"
	"github.com/darkearl/semantic-search/internal/testpdf"
)

type embedFunc func(context.Context, string) ([]float32, error)

func (f embedFunc) Embed(ctx context.Context, text string) ([]float32, error) { return f(ctx, text) }

type pipelinePDF struct {
	count  int
	text   func(context.Context, string, int) (string, error)
	closed *atomic.Int32
}

func (p *pipelinePDF) PageCount(context.Context, string) (int, error) { return p.count, nil }
func (p *pipelinePDF) PageText(ctx context.Context, path string, page int) (string, error) {
	return p.text(ctx, path, page)
}
func (p *pipelinePDF) Close() error { p.closed.Add(1); return nil }

func makeFiles(t *testing.T, count int) string {
	t.Helper()
	root := t.TempDir()
	for i := 0; i < count; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%02d.pdf", i)), []byte("pdf"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestConcurrentReadersAndEmbeddings(t *testing.T) {
	root := makeFiles(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var reading, embedding, closed atomic.Int32
	readBarrier, embedBarrier := make(chan struct{}), make(chan struct{})
	factory := func() (Extractor, error) {
		return &pipelinePDF{count: 1, closed: &closed, text: func(ctx context.Context, path string, page int) (string, error) {
			if reading.Add(1) == 2 {
				close(readBarrier)
			}
			select {
			case <-readBarrier:
				return filepath.Base(path), nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}}, nil
	}
	model := embedFunc(func(ctx context.Context, text string) ([]float32, error) {
		if embedding.Add(1) == 2 {
			close(embedBarrier)
		}
		select {
		case <-embedBarrier:
			return unit(0), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	// An ordinary bytes.Buffer and slice callback also exercise serialized output.
	var log bytes.Buffer
	var events []Progress
	idx, stats, err := BuildConcurrent(ctx, root, "weights", true, nil, factory, model, &log, func(p Progress) { events = append(events, p) }, Options{2, 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Documents) != 2 || stats.Pages != 2 || closed.Load() != 2 || len(events) != 5 {
		t.Fatalf("stats=%+v closed=%d events=%v", stats, closed.Load(), events)
	}
	if idx.Documents[0].Filename != "00.pdf" || idx.Documents[1].Filename != "01.pdf" {
		t.Fatal("nondeterministic document order")
	}
}

func TestDocumentCompletionWaitsForAllEmbeddings(t *testing.T) {
	root := makeFiles(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var closed, finished, completed atomic.Int32
	factory := func() (Extractor, error) {
		return &pipelinePDF{count: 3, closed: &closed, text: func(context.Context, string, int) (string, error) { return "text", nil }}, nil
	}
	model := embedFunc(func(context.Context, string) ([]float32, error) {
		finished.Add(1)
		return unit(0), nil
	})
	_, _, err := BuildConcurrent(ctx, root, "weights", true, nil, factory, model, io.Discard, func(p Progress) {
		if p.Phase == "completed" {
			completed.Add(1)
			if finished.Load() != 3 || p.Page != 3 || p.Pages != 3 || p.Filename != "00.pdf" {
				t.Errorf("premature or incorrect completion: %+v, embeddings finished=%d", p, finished.Load())
			}
		}
	}, Options{1, 2})
	if err != nil || completed.Load() != 1 {
		t.Fatalf("err=%v completion events=%d", err, completed.Load())
	}
}

func TestPipelineBackpressureCancellationAndCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	root := makeFiles(t, 1)
	var extracted, active, closed atomic.Int32
	// With two embedding workers the queue holds four pages. The reader can
	// hold one further page before sending, but it must not extract page eight.
	factory := func() (Extractor, error) {
		return &pipelinePDF{count: 1000, closed: &closed, text: func(context.Context, string, int) (string, error) {
			if extracted.Add(1) == 7 {
				cancel()
			}
			return "text", nil
		}}, nil
	}
	model := embedFunc(func(ctx context.Context, _ string) ([]float32, error) {
		active.Add(1)
		defer active.Add(-1)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	idx, _, err := BuildConcurrent(ctx, root, "weights", true, nil, factory, model, io.Discard, nil, Options{1, 2})
	if !errors.Is(err, context.Canceled) || idx != nil || extracted.Load() != 7 || active.Load() != 0 || closed.Load() != 1 {
		t.Fatalf("err=%v extracted=%d active=%d closed=%d", err, extracted.Load(), active.Load(), closed.Load())
	}
}

func TestPipelineCancellationPreservesContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := makeFiles(t, 1)
	var closed atomic.Int32
	factory := func() (Extractor, error) {
		return &pipelinePDF{count: 1, closed: &closed, text: func(context.Context, string, int) (string, error) {
			cancel()
			return "", errors.New("parser read interrupted")
		}}, nil
	}
	idx, _, err := BuildConcurrent(ctx, root, "weights", true, nil, factory, &fakeModel{}, io.Discard, nil, Options{1, 1})
	if idx != nil || !errors.Is(err, context.Canceled) || closed.Load() != 1 {
		t.Fatalf("cancellation became a parser error: idx=%v err=%v closed=%d", idx, err, closed.Load())
	}
}

func TestConcurrentIndexMatchesSingleWorkerAndReusesFiles(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 6; i++ {
		if err := testpdf.Write(filepath.Join(root, fmt.Sprintf("%d.pdf", i)), []string{"bread", "", "solar", "water"}); err != nil {
			t.Fatal(err)
		}
	}
	model := embedFunc(func(_ context.Context, text string) ([]float32, error) {
		axis := 0
		if strings.Contains(text, "solar") {
			axis = 1
		}
		return unit(axis), nil
	})
	factory := func() (Extractor, error) { return pdf.New() }
	ctx := context.Background()
	serial, _, err := BuildConcurrent(ctx, root, "weights", true, nil, factory, model, io.Discard, nil, Options{1, 1})
	if err != nil {
		t.Fatal(err)
	}
	parallel, stats, err := BuildConcurrent(ctx, root, "weights", true, nil, factory, model, io.Discard, nil, Options{3, 2})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(serial.Documents, parallel.Documents) || stats.Indexed != 6 || stats.Pages != 18 || stats.EmptyPages != 6 {
		t.Fatalf("serial/parallel mismatch: %+v", stats)
	}
	if err := parallel.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "0.pdf")); err != nil {
		t.Fatal(err)
	}
	if err := testpdf.Write(filepath.Join(root, "1.pdf"), []string{"new"}); err != nil {
		t.Fatal(err)
	}
	parallel, stats, err = BuildConcurrent(ctx, root, "weights", true, parallel, factory, model, io.Discard, nil, Options{3, 2})
	if err != nil || stats.Reused != 4 || stats.Indexed != 1 || stats.Pages != 13 || stats.EmptyPages != 4 || len(parallel.Documents) != 5 {
		t.Fatalf("update: stats=%+v err=%v", stats, err)
	}
}

func TestPipelineFailuresAndSourceChanges(t *testing.T) {
	for _, failure := range []string{"extraction", "embedding", "invalid-vector", "changed-source"} {
		t.Run(failure, func(t *testing.T) {
			root := makeFiles(t, 4)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var closed atomic.Int32
			sentinel := errors.New("injected failure")
			factory := func() (Extractor, error) {
				return &pipelinePDF{count: 5, closed: &closed, text: func(_ context.Context, path string, _ int) (string, error) {
					if failure == "extraction" {
						return "", sentinel
					}
					return path, nil
				}}, nil
			}
			var changed atomic.Bool
			model := embedFunc(func(_ context.Context, path string) ([]float32, error) {
				switch failure {
				case "embedding":
					return nil, sentinel
				case "invalid-vector":
					return []float32{1}, nil
				case "changed-source":
					if changed.CompareAndSwap(false, true) {
						if err := os.WriteFile(path, []byte("changed during inference"), 0600); err != nil {
							return nil, err
						}
					}
				}
				return unit(0), nil
			})
			idx, stats, err := BuildConcurrent(ctx, root, "weights", true, nil, factory, model, io.Discard, nil, Options{2, 2})
			if failure == "extraction" || failure == "changed-source" {
				want := 4
				if failure == "changed-source" {
					want = 1
				}
				if err != nil || idx == nil || stats.Skipped != want || len(idx.Documents) != 4-want || closed.Load() != 2 {
					t.Fatalf("skip failed: stats=%+v err=%v closed=%d", stats, err, closed.Load())
				}
				return
			}
			if err == nil || idx != nil || closed.Load() != 2 || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("idx=%v err=%v closed=%d", idx, err, closed.Load())
			}
			if (failure == "extraction" || failure == "embedding") && !errors.Is(err, sentinel) {
				t.Fatalf("lost original error: %v", err)
			}
			if failure == "changed-source" && !strings.Contains(err.Error(), "changed during indexing") {
				t.Fatal(err)
			}
		})
	}
}

func TestPipelineFactoryFailureAndInvalidWorkerCounts(t *testing.T) {
	var created, closed atomic.Int32
	sentinel := errors.New("cannot create reader")
	factory := func() (Extractor, error) {
		if created.Add(1) == 2 {
			return nil, sentinel
		}
		return &pipelinePDF{closed: &closed}, nil
	}
	_, _, err := BuildConcurrent(context.Background(), t.TempDir(), "weights", true, nil, factory, &fakeModel{}, io.Discard, nil, Options{3, 1})
	if !errors.Is(err, sentinel) || closed.Load() != 1 {
		t.Fatalf("err=%v closed=%d", err, closed.Load())
	}
	for _, options := range []Options{{-1, 1}, {1, -1}, {33, 1}, {1, 33}} {
		before := created.Load()
		if _, _, err := BuildConcurrent(context.Background(), t.TempDir(), "weights", true, nil, factory, &fakeModel{}, io.Discard, nil, options); err == nil || created.Load() != before {
			t.Fatalf("invalid options started work: %+v", options)
		}
	}
}
