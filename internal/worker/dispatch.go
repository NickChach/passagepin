package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/darkearl/semantic-search/internal/index"
)

func decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	return json.Unmarshal(raw, v)
}

// Handle adapts the JSON protocol to typed engine operations.
func (e *Engine) Handle(ctx context.Context, req Request, emit func(index.Progress)) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch req.Method {
	case "refresh":
		var p struct {
			Files []index.FileRefresh `json:"files"`
		}
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		if e.idx == nil {
			return nil, fmt.Errorf("index is unavailable")
		}
		next, err := e.idx.RefreshFiles(p.Files)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := next.Save(e.indexPath); err != nil {
			return nil, err
		}
		e.idx = next
		return e.summary(), nil
	case "status":
		return e.summary(), nil
	case "open":
		var p OpenParams
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		return e.Open(ctx, p)
	case "index":
		var p IndexParams
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		return e.BuildIndex(ctx, p, emit)
	case "search":
		var p SearchParams
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		return e.Search(ctx, p)
	case "page":
		var p PageParams
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		return e.Page(ctx, p)
	case "resolve-pdf":
		var p PageParams
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		return e.ResolvePDF(ctx, p)
	default:
		return nil, fmt.Errorf("unknown worker method %q", req.Method)
	}
}
