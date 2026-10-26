package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkearl/semantic-search/internal/embedding"
	"github.com/darkearl/semantic-search/internal/index"
	"github.com/darkearl/semantic-search/internal/testpdf"
)

type fakeModel struct {
	calls   atomic.Int32
	started chan struct{}
}

func TestConcurrentCancellationPreservesPreviousLibrary(t *testing.T) {
	root, path := library(t)
	model := &fakeModel{}
	engine := NewWithOptions(model, "test-model", index.Options{ExtractionWorkers: 2, EmbeddingWorkers: 2})
	if _, err := engine.Handle(context.Background(), request("open", map[string]string{"directory": root, "index_path": path}), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Handle(context.Background(), request("index", nil), nil); err != nil {
		t.Fatal(err)
	}
	previous := engine.idx
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	model.started = make(chan struct{}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		select {
		case <-model.started:
			cancel()
		case <-ctx.Done():
		}
	}()
	if _, err := engine.Handle(ctx, request("index", map[string]bool{"rebuild": true}), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) || engine.idx != previous {
		t.Fatalf("cancel replaced the saved or in-memory library: %v", err)
	}
}

func (m *fakeModel) Embed(ctx context.Context, text string) ([]float32, error) {
	m.calls.Add(1)
	if m.started != nil {
		select {
		case m.started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	v := make([]float32, embedding.Dimensions)
	if strings.Contains(text, "solar") || strings.Contains(text, "renewable") {
		v[1] = 1
	} else {
		v[0] = 1
	}
	return v, nil
}
func request(method string, params any) Request {
	raw, _ := json.Marshal(params)
	return Request{ID: method, Method: method, Params: raw}
}
func library(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	if err := testpdf.Write(filepath.Join(root, "topics.pdf"), []string{"bread and flour", "", "solar panels and clean energy"}); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(t.TempDir(), "index.json")
}
func TestPersistentEngineSearchAndPageValidation(t *testing.T) {
	root, path := library(t)
	model := &fakeModel{}
	engine := New(model, "test-model")
	ctx := context.Background()
	if _, err := engine.Handle(ctx, request("open", map[string]string{"directory": root, "index_path": path}), nil); err != nil {
		t.Fatal(err)
	}
	var progress []index.Progress
	if _, err := engine.Handle(ctx, request("index", nil), func(p index.Progress) { progress = append(progress, p) }); err != nil {
		t.Fatal(err)
	}
	if len(progress) != 5 || progress[3].Page != 3 || progress[4].Phase != "completed" || progress[4].Filename != "topics.pdf" {
		t.Fatalf("page events: %+v", progress)
	}
	for i := 0; i < 2; i++ {
		result, err := engine.Handle(ctx, request("search", map[string]any{"query": "renewable energy", "top": 1}), nil)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(result)
		var found struct {
			Results []index.Result `json:"results"`
		}
		if err := json.Unmarshal(raw, &found); err != nil {
			t.Fatal(err)
		}
		if len(found.Results) != 1 || found.Results[0].Page != 3 {
			t.Fatalf("result: %s", raw)
		}
	}
	if model.calls.Load() != 4 {
		t.Fatalf("expected two pages and two queries on the same model; got %d", model.calls.Load())
	}
	if _, err := engine.Handle(ctx, request("page", map[string]any{"filename": "topics.pdf", "page": 3}), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Handle(ctx, request("page", map[string]any{"filename": "../outside.pdf", "page": 1}), nil); err == nil {
		t.Fatal("allowed path traversal")
	}
}
func TestProtocolCancellationBusyAndShutdown(t *testing.T) {
	root, path := library(t)
	model := &fakeModel{started: make(chan struct{}, 1)}
	engine := New(model, "test-model")
	input, send := io.Pipe()
	receive, output := io.Pipe()
	messages := make(chan Message, 32)
	done := make(chan error, 1)
	go func() {
		dec := json.NewDecoder(receive)
		for {
			var m Message
			if err := dec.Decode(&m); err != nil {
				return
			}
			messages <- m
		}
	}()
	go func() { done <- Serve(context.Background(), input, output, engine); output.Close() }()
	defer send.Close()
	defer receive.Close()
	next := func(id string) Message {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case m := <-messages:
				if m.ID == id {
					return m
				}
			case <-timer.C:
				t.Fatalf("timed out waiting for %s", id)
				return Message{}
			}
		}
	}
	write := func(r Request) {
		t.Helper()
		if err := json.NewEncoder(send).Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case m := <-messages:
		if m.Event != "ready" {
			t.Fatalf("missing ready: %+v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no handshake")
	}
	write(request("open", map[string]string{"directory": root, "index_path": path}))
	if m := next("open"); m.Error != nil {
		t.Fatal(m.Error)
	}
	write(request("index", nil))
	select {
	case <-model.started:
	case <-time.After(5 * time.Second):
		t.Fatal("embedding did not start")
	}
	write(Request{ID: "busy", Method: "status"})
	if m := next("busy"); m.Error == nil || m.Error.Code != "busy" {
		t.Fatalf("expected busy: %+v", m)
	}
	write(Request{ID: "cancel", Method: "cancel", Params: json.RawMessage(`{"target":"index"}`)})
	// Cancellation acknowledgement and the operation response can arrive in either order.
	cancelled, ack := false, false
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for !cancelled || !ack {
		select {
		case m := <-messages:
			if m.ID == "cancel" {
				ack = true
			}
			if m.ID == "index" {
				cancelled = m.Error != nil && m.Error.Code == "cancelled"
			}
		case <-timer.C:
			t.Fatal("cancel did not finish")
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("cancelled build wrote an index")
	}
	write(Request{ID: "shutdown", Method: "shutdown"})
	next("shutdown")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop")
	}
}
