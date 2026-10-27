package embedding

import (
	"context"
	"errors"
	"math"
	"os"
	"strings"
	"testing"

	ort "github.com/yalue/onnxruntime_go"
)

// copiedEmbedding preserves the old copy-before-pooling path as an independent
// reference. In particular, release the output before accumulating its copy.
func copiedEmbedding(t *testing.T, m *Model, text string) []float32 {
	t.Helper()
	sum := make([]float64, Dimensions)
	for _, ids := range m.tokenizer.Windows(text) {
		data := func() []float32 {
			shape := ort.NewShape(1, int64(len(ids)))
			mask := make([]int64, len(ids))
			for i := range mask {
				mask[i] = 1
			}
			forInput := [][]int64{ids, mask, make([]int64, len(ids))}
			var inputs []ort.Value
			for _, values := range forInput {
				input, err := ort.NewTensor(shape, values)
				if err != nil {
					t.Fatal(err)
				}
				defer input.Destroy()
				inputs = append(inputs, input)
			}
			outputs := []ort.Value{nil}
			defer func() {
				if outputs[0] != nil {
					outputs[0].Destroy()
				}
			}()
			if err := m.session.Run(inputs, outputs); err != nil {
				t.Fatal(err)
			}
			return append([]float32(nil), outputs[0].(*ort.Tensor[float32]).GetData()...)
		}()
		for i, value := range data {
			sum[i%Dimensions] += float64(value)
		}
	}
	vector := make([]float32, Dimensions)
	for i := range vector {
		vector[i] = float32(sum[i])
	}
	if err := Normalize(vector); err != nil {
		t.Fatal(err)
	}
	return vector
}

func TestDirectPoolingIntegration(t *testing.T) {
	dir, library := os.Getenv("PDFSEARCH_TEST_MODEL_DIR"), os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_PATH")
	if dir == "" || library == "" {
		t.Skip("set local model and runtime paths")
	}
	m, err := Open(dir, library, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	fixtures := append(embeddingFixtures(),
		struct{ name, text string }{"full_window", strings.Repeat("water ", 254)},
		struct{ name, text string }{"window_plus_one", strings.Repeat("water ", 255)},
		struct{ name, text string }{"two_full_windows", strings.Repeat("water ", 508)},
		struct{ name, text string }{"special_tokens", "[CLS] water [SEP] [MASK] [UNK] [PAD]"},
	)
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			want := copiedEmbedding(t, m, fixture.text)
			got, err := m.Embed(context.Background(), fixture.text)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(want) {
				t.Fatalf("dimensions: got %d want %d", len(got), len(want))
			}
			for i := range want {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("component %d changed: got %08x want %08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
				}
			}
		})
	}
	t.Run("cancel_between_windows", func(t *testing.T) {
		ctx := &cancelAfterFirstWindow{Context: context.Background()}
		got, err := m.Embed(ctx, strings.Repeat("water ", 255))
		if got != nil || !errors.Is(err, context.Canceled) || ctx.checks != 3 {
			t.Fatalf("got vector=%v err=%v context checks=%d", got, err, ctx.checks)
		}
	})
	t.Run("empty_text", func(t *testing.T) {
		if got, err := m.Embed(context.Background(), "  "); got != nil || err == nil {
			t.Fatalf("got vector=%v err=%v", got, err)
		}
	})
	t.Run("invalid_input", func(t *testing.T) {
		sum := make([]float64, Dimensions)
		if err := m.run([]int64{999999}, sum); err == nil {
			t.Fatal("expected inference error for out-of-vocabulary token ID")
		}
		for _, value := range sum {
			if value != 0 {
				t.Fatal("failed inference changed accumulator")
			}
		}
		// The same session must remain usable after failure.
		if _, err := m.Embed(context.Background(), "water"); err != nil {
			t.Fatal(err)
		}
	})
}

// Embed checks once before tokenization and once before each window. Cancel at
// the third check so one full inference and pooling completes before stopping.
type cancelAfterFirstWindow struct {
	context.Context
	checks int
}

func (c *cancelAfterFirstWindow) Err() error {
	c.checks++
	if c.checks >= 3 {
		return context.Canceled
	}
	return nil
}
