package embedding

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEmbedTokenlessInput(t *testing.T) {
	// No inference session: empty input must return before allocating ONNX tensors.
	m := &Model{tokenizer: testTokenizer(t)}
	for _, text := range []string{"", " \n\t", "\u200b", "\x00\x01\ufeff\ufffd", "\u0301\u0308", " \u200b\u0301\n"} {
		t.Run(text, func(t *testing.T) {
			vector, err := m.Embed(context.Background(), text)
			if vector != nil || !errors.Is(err, ErrNoTokens) {
				t.Fatalf("vector=%v err=%v; want nil vector and ErrNoTokens", vector, err)
			}
		})
	}
}

func TestEmbedTokenlessInputPreservesCancellation(t *testing.T) {
	m := &Model{tokenizer: testTokenizer(t)}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Time{})
	defer stop()
	for _, ctx := range []context.Context{canceled, expired} {
		vector, err := m.Embed(ctx, "\u200b")
		if vector != nil || !errors.Is(err, ctx.Err()) || errors.Is(err, ErrNoTokens) {
			t.Fatalf("vector=%v err=%v; want %v", vector, err, ctx.Err())
		}
	}
}
