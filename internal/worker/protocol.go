package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/darkearl/semantic-search/internal/index"
)

const ProtocolVersion = 1

type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Message struct {
	ID     string `json:"id,omitempty"`
	Event  string `json:"event,omitempty"`
	Data   any    `json:"data,omitempty"`
	Result any    `json:"result,omitempty"`
	Error  *Error `json:"error,omitempty"`
}

// Serve serializes model operations while continuing to read cancellation and
// shutdown requests. EOF cancels active work and releases the worker's caller.
func Serve(ctx context.Context, input io.Reader, output io.Writer, engine *Engine) error {
	done := make(chan struct{})
	defer close(done)
	if closer, ok := input.(io.ReadCloser); ok {
		go func() {
			select {
			case <-ctx.Done():
				_ = closer.Close()
			case <-done:
			}
		}()
	}
	enc := json.NewEncoder(output)
	var writeMu sync.Mutex
	write := func(m Message) { writeMu.Lock(); defer writeMu.Unlock(); _ = enc.Encode(m) }
	write(Message{Event: "ready", Data: map[string]any{"protocol": ProtocolVersion, "pid": os.Getpid(), "model_loads": 1}})
	var activeMu sync.Mutex
	var activeID string
	var cancel context.CancelFunc
	var work sync.WaitGroup
	stop := func() {
		activeMu.Lock()
		if cancel != nil {
			cancel()
		}
		activeMu.Unlock()
		work.Wait()
	}
	defer stop()
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var req Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil || req.ID == "" || len(req.ID) > 128 {
			write(Message{ID: req.ID, Error: &Error{Code: "invalid_request", Message: "Expected a JSON request with a nonempty id and method."}})
			continue
		}
		if req.Method == "shutdown" {
			stop()
			write(Message{ID: req.ID, Result: map[string]bool{"stopped": true}})
			return nil
		}
		if req.Method == "cancel" {
			var p struct {
				Target string `json:"target"`
			}
			_ = decode(req.Params, &p)
			activeMu.Lock()
			matched := cancel != nil && activeID == p.Target
			if matched {
				cancel()
			}
			activeMu.Unlock()
			write(Message{ID: req.ID, Result: map[string]bool{"cancelled": matched}})
			continue
		}
		activeMu.Lock()
		if cancel != nil {
			activeMu.Unlock()
			write(Message{ID: req.ID, Error: &Error{Code: "busy", Message: "Another operation is running."}})
			continue
		}
		opCtx, opCancel := context.WithCancel(ctx)
		activeID, cancel = req.ID, opCancel
		work.Add(1)
		activeMu.Unlock()
		go func(req Request) {
			defer work.Done()
			result, err := engine.Handle(opCtx, req, func(p index.Progress) { write(Message{ID: req.ID, Event: "progress", Data: p}) })
			opCancel()
			activeMu.Lock()
			activeID, cancel = "", nil
			activeMu.Unlock()
			if err != nil {
				code := "operation_failed"
				if errors.Is(err, context.Canceled) {
					code = "cancelled"
				}
				write(Message{ID: req.ID, Error: &Error{Code: code, Message: err.Error()}})
			} else {
				write(Message{ID: req.ID, Result: result})
			}
		}(req)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("worker input: %w", err)
	}
	return nil
}
