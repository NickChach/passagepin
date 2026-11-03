package embedding

import (
	"context"
	"errors"
	"strings"
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

// Cancel deterministically while tokenization is in progress, without depending
// on machine speed or constructing an ONNX session.
type tokenizationCancelContext struct {
	context.Context
	cancel context.CancelFunc
	polls  int
}

func (c *tokenizationCancelContext) Err() error {
	c.polls++
	if c.polls == 10 {
		c.cancel()
	}
	return c.Context.Err()
}
func TestEmbedCancellationDuringTokenization(t *testing.T) {
	m := &Model{tokenizer: testTokenizer(t)}
	for _, text := range []string{strings.Repeat("[CLS]", 100000), strings.Repeat("hello ", 100000), strings.Repeat("a", 100000)} {
		base, cancel := context.WithCancel(context.Background())
		ctx := &tokenizationCancelContext{Context: base, cancel: cancel}
		vector, err := m.Embed(ctx, text)
		cancel()
		if vector != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("vector=%v err=%v", vector, err)
		}
	}
}
