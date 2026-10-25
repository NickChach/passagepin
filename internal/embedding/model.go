package embedding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	Dimensions = 384
	ModelID    = "sentence-transformers/all-MiniLM-L6-v2"
	Revision   = "1110a243fdf4706b3f48f1d95db1a4f5529b4d41"
	Pipeline   = "bert-uncased-windows254-token-weighted-mean-l2-v1"
)

type Model struct {
	tokenizer *Tokenizer
	session   *ort.DynamicAdvancedSession
}

// Fingerprint binds an index to the exact vocabulary, weights and pooling policy.
func Fingerprint(dir string) (string, error) {
	h := sha256.New()
	io.WriteString(h, ModelID+"\x00"+Pipeline+"\x00")
	for _, name := range []string{"vocab.txt", "model.onnx"} {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return "", fmt.Errorf("open model asset: %w (run pdfsearch download)", err)
		}
		_, err = io.Copy(h, f)
		closeErr := f.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func Open(dir, library string, threads int) (*Model, error) {
	t, err := LoadTokenizer(filepath.Join(dir, "vocab.txt"))
	if err != nil {
		return nil, err
	}
	return openModel(t, filepath.Join(dir, "model.onnx"), nil, library, threads)
}

// Assets can describe an external model directory or a model embedded in a
// bundled executable. Both forms produce identical index fingerprints.
type Assets struct {
	Directory           string
	Weights, Vocabulary []byte
}

func (a Assets) Fingerprint() (string, error) {
	if a.Directory != "" {
		return Fingerprint(a.Directory)
	}
	if len(a.Weights) == 0 || len(a.Vocabulary) == 0 {
		return "", fmt.Errorf("model assets are missing")
	}
	h := sha256.New()
	io.WriteString(h, ModelID+"\x00"+Pipeline+"\x00")
	h.Write(a.Vocabulary)
	h.Write(a.Weights)
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func (a Assets) Open(library string, threads int) (*Model, error) {
	if a.Directory != "" {
		return Open(a.Directory, library, threads)
	}
	if len(a.Weights) == 0 || len(a.Vocabulary) == 0 {
		return nil, fmt.Errorf("model assets are missing")
	}
	t, err := ReadTokenizer(bytes.NewReader(a.Vocabulary))
	if err != nil {
		return nil, err
	}
	return openModel(t, "", a.Weights, library, threads)
}

func openModel(t *Tokenizer, path string, weights []byte, library string, threads int) (*Model, error) {
	if library != "" {
		ort.SetSharedLibraryPath(library)
	}
	if err := ort.InitializeEnvironment(); err != nil {
		return nil, fmt.Errorf("load ONNX Runtime 1.22.0 (set --runtime or ONNXRUNTIME_SHARED_LIBRARY_PATH): %w", err)
	}
	fail := func(err error) (*Model, error) { ort.DestroyEnvironment(); return nil, err }
	options, err := ort.NewSessionOptions()
	if err != nil {
		return fail(err)
	}
	defer options.Destroy()
	if err := options.SetIntraOpNumThreads(threads); err != nil {
		return fail(err)
	}
	if err := options.SetInterOpNumThreads(1); err != nil {
		return fail(err)
	}
	inputs := []string{"input_ids", "attention_mask", "token_type_ids"}
	outputs := []string{"last_hidden_state"}
	var session *ort.DynamicAdvancedSession
	if len(weights) > 0 {
		session, err = ort.NewDynamicAdvancedSessionWithONNXData(weights, inputs, outputs, options)
	} else {
		session, err = ort.NewDynamicAdvancedSession(path, inputs, outputs, options)
	}
	if err != nil {
		return fail(fmt.Errorf("open MiniLM ONNX model: %w", err))
	}
	return &Model{tokenizer: t, session: session}, nil
}

func (m *Model) Close() { m.session.Destroy(); ort.DestroyEnvironment() }

func Normalize(v []float32) error {
	var squared float64
	for _, x := range v {
		squared += float64(x) * float64(x)
	}
	if squared == 0 || math.IsNaN(squared) || math.IsInf(squared, 0) {
		return fmt.Errorf("invalid embedding norm")
	}
	norm := math.Sqrt(squared)
	for i := range v {
		v[i] = float32(float64(v[i]) / norm)
	}
	return nil
}

func (m *Model) Embed(ctx context.Context, text string) ([]float32, error) {
	windows := m.tokenizer.Windows(text)
	if len(windows) == 0 {
		return nil, fmt.Errorf("text has no tokens")
	}
	sum := make([]float64, Dimensions)
	for _, ids := range windows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := m.run(ids)
		if err != nil {
			return nil, err
		}
		// Sum all unpadded token embeddings, including CLS/SEP as in the model
		// card. Normalizing once is equivalent to a token-weighted mean.
		for i, value := range data {
			sum[i%Dimensions] += float64(value)
		}
	}
	vector := make([]float32, Dimensions)
	for i := range vector {
		vector[i] = float32(sum[i])
	}
	if err := Normalize(vector); err != nil {
		return nil, err
	}
	return vector, nil
}

func (m *Model) run(ids []int64) ([]float32, error) {
	shape := ort.NewShape(1, int64(len(ids)))
	mask := make([]int64, len(ids))
	for i := range mask {
		mask[i] = 1
	}
	var inputs []ort.Value
	defer func() {
		for _, input := range inputs {
			input.Destroy()
		}
	}()
	for _, data := range [][]int64{ids, mask, make([]int64, len(ids))} {
		tensor, err := ort.NewTensor(shape, data)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, tensor)
	}
	outputs := []ort.Value{nil}
	defer func() {
		if outputs[0] != nil {
			outputs[0].Destroy()
		}
	}()
	if err := m.session.Run(inputs, outputs); err != nil {
		return nil, fmt.Errorf("MiniLM inference: %w", err)
	}
	tensor, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("unexpected model output type")
	}
	got := tensor.GetShape()
	if len(got) != 3 || got[0] != 1 || got[1] != int64(len(ids)) || got[2] != Dimensions {
		return nil, fmt.Errorf("unexpected model output shape %v", got)
	}
	return append([]float32(nil), tensor.GetData()...), nil
}
