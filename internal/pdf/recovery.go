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

type recoveryObject struct {
	entry     xrefEntry
	duplicate bool
}

type recoveryRevision struct {
	offset, declared int64
	trailer          dict
	objects          map[int64]recoveryObject
}

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
	headers := map[int64]reference{}
	revisionObjects := map[int64]recoveryObject{}
	var revisions []recoveryRevision
	objects := 0
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
			headers[offset] = ref
			_, duplicate := revisionObjects[ref.number]
			revisionObjects[ref.number] = recoveryObject{
				entry:     xrefEntry{kind: 1, offset: offset, generation: ref.generation},
				duplicate: duplicate,
			}
			awaitingEOF = false
			continue
		}
		switch token {
		case keyword("xref"):
			complete = false
			if len(revisions) >= 128 {
				return fmt.Errorf("recovery revision limit exceeded")
			}
			_, trailer, err := d.classicXref(p)
			if err != nil {
				return err
			}
			if trailer["Encrypt"] != nil {
				return fmt.Errorf("encrypted PDFs cannot be recovered")
			}
			if trailer["XRefStm"] != nil {
				return fmt.Errorf("hybrid cross-reference recovery is not supported")
			}
			// Keep revisions separate until their /Prev links have been resolved.
			// Reparse selected tables later instead of retaining every table's entries.
			revisions = append(revisions, recoveryRevision{offset: offset, trailer: trailer, objects: revisionObjects})
			awaitingEOF = true
		case keyword("startxref"):
			if !awaitingEOF {
				return fmt.Errorf("startxref without a preceding table")
			}
			n, err := p.next()
			declared, ok := integer(n)
			if err != nil || !ok {
				return fmt.Errorf("invalid startxref during recovery")
			}
			revisions[len(revisions)-1].declared = declared
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
			revisionObjects = map[int64]recoveryObject{}
			awaitingEOF = false
			complete = true
		default:
			return fmt.Errorf("unexpected token outside PDF objects")
		}
	}
	if len(revisions) == 0 || awaitingEOF || !complete {
		return fmt.Errorf("no complete classic cross-reference revision")
	}
	root, err := d.recoverRevisionChain(revisions, headers)
	if err != nil {
		return err
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

// A valid byte offset always wins. A damaged /Prev can be repaired only when
// exactly one earlier table declared that same offset in its own startxref.
// Physical adjacency alone is not evidence that two revisions belong together.
func (d *document) recoverRevisionChain(revisions []recoveryRevision, headers map[int64]reference) (value, error) {
	byOffset := make(map[int64]int, len(revisions))
	for i, revision := range revisions {
		byOffset[revision.offset] = i
	}
	selected := len(revisions) - 1
	if actual, ok := byOffset[revisions[selected].declared]; ok {
		selected = actual
	}
	var chain []int // Newest to oldest, like normal parsing.
	for {
		if err := d.ctx.Err(); err != nil {
			return nil, err
		}
		chain = append(chain, selected)
		prev, exists := revisions[selected].trailer["Prev"]
		if !exists {
			break
		}
		offset, ok := integer(prev)
		if !ok || offset <= 0 {
			return nil, fmt.Errorf("invalid Prev reference during recovery")
		}
		previous, exact := byOffset[offset]
		if !exact {
			previous = -1
			for i := 0; i < selected; i++ {
				if revisions[i].declared != offset {
					continue
				}
				if previous >= 0 {
					return nil, fmt.Errorf("ambiguous Prev reference during recovery")
				}
				previous = i
			}
		}
		if previous < 0 || previous >= selected {
			return nil, fmt.Errorf("cannot resolve Prev reference during recovery")
		}
		selected = previous
	}

	var root value
	for position, index := range chain {
		revision := revisions[index]
		entries, _, err := d.xrefSection(revision.offset)
		if err != nil {
			return nil, err
		}
		for id, entry := range entries {
			if err := d.ctx.Err(); err != nil {
				return nil, err
			}
			if _, exists := d.xref[id]; exists {
				continue
			}
			if entry.kind == 1 {
				declared, exists := headers[entry.offset]
				if !exists || entry.offset >= revision.offset || declared != (reference{id, entry.generation}) {
					// Search only this revision and its selected ancestors. Objects
					// from a physically intervening, discarded revision cannot fill gaps.
					found := false
					for _, ancestor := range chain[position:] {
						actual, exists := revisions[ancestor].objects[id]
						if !exists {
							continue
						}
						if actual.duplicate {
							return nil, fmt.Errorf("ambiguous duplicate object %d", id)
						}
						if actual.entry.generation != entry.generation {
							break
						}
						entry.offset, found = actual.entry.offset, true
						break
					}
					if !found {
						return nil, fmt.Errorf("cannot locate object %d generation %d in selected revision chain", id, entry.generation)
					}
				}
			}
			// Newer entries win, including free entries for deleted objects.
			d.xref[id] = entry
		}
		if len(d.xref) > maxRecoveryObjects {
			return nil, fmt.Errorf("recovery object limit exceeded")
		}
		if root == nil {
			root = revision.trailer["Root"]
		}
	}
	return root, nil
}
