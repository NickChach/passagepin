package pdf

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

const (
	maxRecoveryBytes   = 128 << 20
	maxRecoveryObjects = 100_000
	recoveryTimeout    = 3 * time.Second
)

// xrefError marks failures for which rebuilding byte offsets may help. Other
// syntax errors, unsupported features and normal extraction errors stay errors.
type xrefError struct{ error }

func (e *xrefError) Unwrap() error { return e.error }

// Recovery deliberately supports classic-table PDFs with intact object syntax
// and direct stream lengths. It never searches arbitrary binary data for obj
// markers, guesses missing content, or modifies the source file.
func recoverDocument(ctx context.Context, file *os.File, original error) (*document, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > maxRecoveryBytes {
		return nil, fmt.Errorf("%w; recovery limited to files up to 128 MiB", original)
	}
	budget, cancel := context.WithTimeout(ctx, recoveryTimeout)
	defer cancel()
	d := &document{file: file, size: info.Size(), ctx: budget, recovering: true, xref: map[int64]xrefEntry{}, cache: map[reference]value{}, active: map[reference]bool{}}
	if err := d.reconstruct(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if budget.Err() != nil {
			return nil, fmt.Errorf("%w; recovery exceeded its 3-second time limit", original)
		}
		return nil, fmt.Errorf("%w; cross-reference recovery unavailable: %v", original, err)
	}
	d.ctx, d.recovering, d.recovery = ctx, false, original.Error()
	return d, nil
}

func (d *document) reconstruct() error {
	p, err := d.parser(0)
	if err != nil {
		return err
	}
	seen := map[int64]xrefEntry{}
	headers := map[int64]reference{}
	duplicates := map[int64]bool{}
	revisionSeen := map[int64]bool{}
	var root value
	objects, revisions := 0, 0
	awaitingEOF := false
	complete := false
	for {
		if err := d.ctx.Err(); err != nil {
			return err
		}
		if err := p.skip(); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		offset := p.pos
		token, err := p.next()
		if err != nil {
			return err
		}
		if awaitingEOF && token != keyword("startxref") {
			return fmt.Errorf("unfinished cross-reference revision")
		}
		if _, ok := integer(token); ok {
			complete = false
			objects++
			if objects > maxRecoveryObjects {
				return fmt.Errorf("recovery object limit exceeded")
			}
			p.pushed = append([]value{token}, p.pushed...)
			ref, v, err := d.readIndirect(p, offset)
			if err != nil {
				return err
			}
			if ref.number < 1 || ref.number > maxRecoveryObjects || ref.generation < 0 || ref.generation > 65535 {
				return fmt.Errorf("invalid object identity during recovery")
			}
			if s, ok := v.(*stream); ok {
				if s.dict["Type"] == name("XRef") || s.dict["Type"] == name("ObjStm") {
					return fmt.Errorf("recovery of cross-reference/object streams is not supported")
				}
				length, direct := integer(s.dict["Length"])
				if !direct || length < 0 || length > d.size-s.offset {
					return fmt.Errorf("recovery requires intact direct stream lengths")
				}
				p, err = d.parser(s.offset + length)
				if err != nil {
					return err
				}
				end, err := p.next()
				if err != nil || end != keyword("endstream") {
					return fmt.Errorf("invalid stream boundary during recovery")
				}
				end, err = p.next()
				if err != nil || end != keyword("endobj") {
					return fmt.Errorf("missing endobj after stream")
				}
			}
			seen[ref.number] = xrefEntry{kind: 1, offset: offset, generation: ref.generation}
			headers[offset] = ref
			if revisionSeen[ref.number] {
				duplicates[ref.number] = true
			}
			revisionSeen[ref.number] = true
			awaitingEOF = false
			continue
		}
		switch token {
		case keyword("xref"):
			complete = false
			revisions++
			if revisions > 128 {
				return fmt.Errorf("recovery revision limit exceeded")
			}
			entries, trailer, err := d.classicXref(p)
			if err != nil {
				return err
			}
			if trailer["Encrypt"] != nil {
				return fmt.Errorf("encrypted PDFs cannot be recovered")
			}
			if trailer["XRefStm"] != nil {
				return fmt.Errorf("hybrid cross-reference recovery is not supported")
			}
			for id, entry := range entries {
				if entry.kind == 1 {
					if declared, exists := headers[entry.offset]; exists && declared == (reference{id, entry.generation}) {
						d.xref[id] = entry
						continue
					}
					if duplicates[id] {
						return fmt.Errorf("ambiguous duplicate object %d", id)
					}
					actual, exists := seen[id]
					if !exists || actual.generation != entry.generation {
						return fmt.Errorf("cannot locate object %d generation %d", id, entry.generation)
					}
					entry.offset = actual.offset
				}
				// Free entries are retained: deleted objects must not be resurrected.
				d.xref[id] = entry
			}
			if len(d.xref) > maxRecoveryObjects {
				return fmt.Errorf("recovery object limit exceeded")
			}
			if trailer["Root"] != nil {
				root = trailer["Root"]
			}
			awaitingEOF = true
		case keyword("startxref"):
			if !awaitingEOF {
				return fmt.Errorf("startxref without a preceding table")
			}
			n, err := p.next()
			if _, ok := integer(n); err != nil || !ok {
				return fmt.Errorf("invalid startxref during recovery")
			}
			// Require the revision's EOF marker; p.skip would discard it as a comment.
			for {
				b, err := p.peek()
				if err != nil {
					return err
				}
				if !white(b) {
					break
				}
				p.read()
			}
			for _, want := range []byte("%%EOF") {
				got, err := p.read()
				if err != nil || got != want {
					return fmt.Errorf("missing revision EOF marker")
				}
			}
			// Some incremental writers append the next object/table directly
			// after %%EOF. Its complete header/body and following revision still
			// have to validate; arbitrary trailing tokens remain unsupported.
			if next, err := p.peek(); err != io.EOF && (err != nil || (!white(next) && next != 'x' && (next < '0' || next > '9'))) {
				return fmt.Errorf("invalid revision EOF boundary")
			}
			revisionSeen, duplicates = map[int64]bool{}, map[int64]bool{}
			awaitingEOF = false
			complete = true
		default:
			return fmt.Errorf("unexpected token outside PDF objects")
		}
	}
	if revisions == 0 || awaitingEOF || !complete {
		return fmt.Errorf("no complete classic cross-reference revision")
	}
	catalog, err := d.dictionary(root)
	if err != nil {
		return err
	}
	if catalog["Type"] != name("Catalog") {
		return fmt.Errorf("missing recovered catalog")
	}
	if err := d.walkPages(catalog["Pages"], nil, map[reference]bool{}, 0); err != nil {
		return err
	}
	if len(d.pages) == 0 {
		return fmt.Errorf("no recovered pages")
	}
	return nil
}
