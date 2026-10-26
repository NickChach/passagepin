package pdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
)

type xrefEntry struct {
	kind                                    int
	offset, generation, objectStream, index int64
}
type pageInfo struct {
	object    dict
	resources value
	identity  reference
}
type document struct {
	file       *os.File
	size       int64
	ctx        context.Context
	xref       map[int64]xrefEntry
	cache      map[reference]value
	active     map[reference]bool
	pages      []pageInfo
	recovery   string
	recovering bool
}

func openDocument(ctx context.Context, f *os.File) (*document, error) {
	d, err := readDocument(ctx, f)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var structural *xrefError
	if err != nil && ctx.Err() == nil && errors.As(err, &structural) {
		return recoverDocument(ctx, f, err)
	}
	return d, err
}

func readDocument(ctx context.Context, f *os.File) (*document, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size < 8 {
		return nil, fmt.Errorf("not a PDF")
	}
	header := make([]byte, 8)
	if _, err := f.ReadAt(header, 0); err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(header, []byte("%PDF-")) {
		return nil, fmt.Errorf("missing PDF header")
	}
	d := &document{file: f, size: size, ctx: ctx, xref: map[int64]xrefEntry{}, cache: map[reference]value{}, active: map[reference]bool{}}
	tail := make([]byte, min(size, 64<<10))
	if _, err := f.ReadAt(tail, size-int64(len(tail))); err != nil {
		return nil, err
	}
	at := bytes.LastIndex(tail, []byte("startxref"))
	if at < 0 {
		return nil, &xrefError{fmt.Errorf("missing startxref")}
	}
	p := newSyntax(bytes.NewReader(tail[at+9:]), 0)
	raw, err := p.next()
	if err != nil {
		return nil, err
	}
	offset, ok := integer(raw)
	if !ok {
		return nil, &xrefError{fmt.Errorf("invalid startxref offset")}
	}
	var root value
	visited := map[int64]bool{}
	for sections := 0; ; sections++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if sections >= 128 || visited[offset] {
			return nil, fmt.Errorf("cross-reference chain is cyclic or too long")
		}
		visited[offset] = true
		entries, trailer, err := d.xrefSection(offset)
		if err != nil {
			return nil, &xrefError{err}
		}
		if hybrid, ok := integer(trailer["XRefStm"]); ok {
			extra, _, err := d.xrefStream(hybrid)
			if err != nil {
				return nil, &xrefError{err}
			}
			for id, e := range extra {
				entries[id] = e
			}
		}
		for id, e := range entries {
			if _, exists := d.xref[id]; !exists {
				d.xref[id] = e
			}
		}
		if len(d.xref) > maxObjects {
			return nil, fmt.Errorf("too many PDF objects")
		}
		if trailer["Encrypt"] != nil {
			return nil, fmt.Errorf("encrypted PDFs are not supported; decrypt the PDF first")
		}
		if root == nil {
			root = trailer["Root"]
		}
		prev, exists := trailer["Prev"]
		if !exists {
			break
		}
		offset, ok = integer(prev)
		if !ok {
			return nil, fmt.Errorf("invalid Prev reference")
		}
	}
	catalog, err := d.dictionary(root)
	if err != nil {
		return nil, err
	}
	if catalog["Type"] != name("Catalog") {
		return nil, fmt.Errorf("missing PDF catalog")
	}
	if err := d.walkPages(catalog["Pages"], nil, map[reference]bool{}, 0); err != nil {
		return nil, err
	}
	if len(d.pages) == 0 {
		return nil, fmt.Errorf("PDF contains no pages")
	}
	return d, nil
}
func (d *document) parser(offset int64) (*syntax, error) {
	if err := d.ctx.Err(); err != nil {
		return nil, err
	}
	if offset < 0 || offset >= d.size {
		return nil, fmt.Errorf("object offset outside PDF: %d", offset)
	}
	return newSyntax(contextReader{d, io.NewSectionReader(d.file, offset, d.size-offset)}, offset), nil
}
func (d *document) indirect(offset int64) (reference, value, error) {
	p, err := d.parser(offset)
	if err != nil {
		return reference{}, nil, &xrefError{err}
	}
	return d.readIndirect(p, offset)
}

func (d *document) readIndirect(p *syntax, offset int64) (reference, value, error) {
	n, err := p.next()
	if err != nil {
		return reference{}, nil, err
	}
	g, err := p.next()
	if err != nil {
		return reference{}, nil, err
	}
	op, err := p.next()
	if err != nil {
		return reference{}, nil, err
	}
	id, ok := integer(n)
	gen, okGen := integer(g)
	if !ok || !okGen || op != keyword("obj") {
		return reference{}, nil, &xrefError{fmt.Errorf("invalid indirect object at %d", offset)}
	}
	v, err := p.value(0)
	if err != nil {
		return reference{}, nil, err
	}
	end, err := p.next()
	if err != nil {
		return reference{}, nil, err
	}
	if end == keyword("stream") {
		dictionary, ok := v.(dict)
		if !ok {
			return reference{}, nil, fmt.Errorf("stream without dictionary")
		}
		c, err := p.read()
		if err != nil {
			return reference{}, nil, err
		}
		if c == '\r' {
			if c, _ := p.peek(); c == '\n' {
				p.read()
			}
		} else if c != '\n' {
			return reference{}, nil, fmt.Errorf("stream missing line ending")
		}
		return reference{id, gen}, &stream{dict: dictionary, offset: p.pos}, nil
	}
	if end != keyword("endobj") {
		return reference{}, nil, fmt.Errorf("object %d missing endobj", id)
	}
	return reference{id, gen}, v, nil
}
func (d *document) resolve(v value) (value, error) {
	for depth := 0; depth < maxDepth; depth++ {
		if err := d.ctx.Err(); err != nil {
			return nil, err
		}
		ref, ok := v.(reference)
		if !ok {
			return v, nil
		}
		if cached, ok := d.cache[ref]; ok {
			v = cached
			continue
		}
		if d.active[ref] {
			return nil, fmt.Errorf("cyclic object reference %d", ref.number)
		}
		d.active[ref] = true
		resolved, err := d.resolveObject(ref)
		delete(d.active, ref)
		if err != nil {
			return nil, err
		}
		d.cache[ref] = resolved
		v = resolved
	}
	return nil, fmt.Errorf("object reference nesting too deep")
}
func (d *document) resolveObject(ref reference) (value, error) {
	entry, ok := d.xref[ref.number]
	if !ok || entry.kind == 0 {
		return nil, &xrefError{fmt.Errorf("missing object %d", ref.number)}
	}
	if entry.kind == 1 {
		if entry.generation != ref.generation {
			return nil, &xrefError{fmt.Errorf("object generation mismatch for %d", ref.number)}
		}
		got, v, err := d.indirect(entry.offset)
		if err != nil {
			return nil, err
		}
		if got != ref {
			return nil, &xrefError{fmt.Errorf("cross-reference points to wrong object")}
		}
		return v, nil
	}
	if entry.kind != 2 || ref.generation != 0 {
		return nil, fmt.Errorf("unsupported cross-reference entry")
	}
	parent, exists := d.xref[entry.objectStream]
	if !exists || parent.kind != 1 {
		return nil, fmt.Errorf("invalid object stream reference")
	}
	v, err := d.resolve(reference{entry.objectStream, parent.generation})
	if err != nil {
		return nil, err
	}
	s, ok := v.(*stream)
	if !ok || s.dict["Type"] != name("ObjStm") {
		return nil, fmt.Errorf("object stream has wrong type")
	}
	n, ok := integer(s.dict["N"])
	first, okFirst := integer(s.dict["First"])
	if !ok || !okFirst || n < 1 || n > maxObjects || entry.index < 0 || entry.index >= n {
		return nil, fmt.Errorf("invalid object stream header")
	}
	data, err := d.decode(s)
	if err != nil {
		return nil, err
	}
	if first < 0 || first > int64(len(data)) {
		return nil, fmt.Errorf("invalid object stream offset")
	}
	p := newSyntax(bytes.NewReader(data[:first]), 0)
	var position int64
	for i := int64(0); i <= entry.index; i++ {
		id, err := p.next()
		if err != nil {
			return nil, err
		}
		off, err := p.next()
		if err != nil {
			return nil, err
		}
		object, ok := integer(id)
		o, okOff := integer(off)
		if !ok || !okOff || o < 0 {
			return nil, fmt.Errorf("invalid object stream index")
		}
		if i == entry.index {
			if object != ref.number {
				return nil, fmt.Errorf("wrong object stream member")
			}
			position = o
		}
	}
	if position >= int64(len(data))-first {
		return nil, fmt.Errorf("object stream member out of range")
	}
	return newSyntax(bytes.NewReader(data[first+position:]), 0).value(0)
}
func (d *document) dictionary(v value) (dict, error) {
	v, err := d.resolve(v)
	if err != nil {
		return nil, err
	}
	result, ok := v.(dict)
	if !ok {
		return nil, fmt.Errorf("expected PDF dictionary, got %T", v)
	}
	return result, nil
}
func (d *document) xrefSection(offset int64) (map[int64]xrefEntry, dict, error) {
	p, err := d.parser(offset)
	if err != nil {
		return nil, nil, err
	}
	token, err := p.next()
	if err != nil {
		return nil, nil, err
	}
	if token != keyword("xref") {
		return d.xrefStream(offset)
	}
	return d.classicXref(p)
}

func (d *document) classicXref(p *syntax) (map[int64]xrefEntry, dict, error) {
	limit := int64(maxObjects)
	if d.recovering {
		limit = maxRecoveryObjects
	}
	entries := map[int64]xrefEntry{}
	for {
		token, err := p.next()
		if err != nil {
			return nil, nil, err
		}
		if token == keyword("trailer") {
			v, err := p.value(0)
			if err != nil {
				return nil, nil, err
			}
			trailer, ok := v.(dict)
			if !ok {
				return nil, nil, fmt.Errorf("invalid trailer")
			}
			return entries, trailer, nil
		}
		start, ok := integer(token)
		countRaw, err := p.next()
		if err != nil {
			return nil, nil, err
		}
		count, okCount := integer(countRaw)
		if !ok || !okCount || start < 0 || count < 0 || start > limit || count > limit-start || int64(len(entries))+count > limit {
			return nil, nil, fmt.Errorf("invalid cross-reference subsection")
		}
		for i := int64(0); i < count; i++ {
			if err := d.ctx.Err(); err != nil {
				return nil, nil, err
			}
			offsetRaw, err := p.next()
			if err != nil {
				return nil, nil, err
			}
			genRaw, err := p.next()
			if err != nil {
				return nil, nil, err
			}
			flag, err := p.next()
			if err != nil {
				return nil, nil, err
			}
			off, ok := integer(offsetRaw)
			gen, okGen := integer(genRaw)
			if !ok || !okGen || (flag != keyword("n") && flag != keyword("f")) {
				return nil, nil, fmt.Errorf("invalid cross-reference entry")
			}
			kind := 0
			if flag == keyword("n") {
				kind = 1
			}
			entries[start+i] = xrefEntry{kind: kind, offset: off, generation: gen}
		}
	}
}
func (d *document) xrefStream(offset int64) (map[int64]xrefEntry, dict, error) {
	_, v, err := d.indirect(offset)
	if err != nil {
		return nil, nil, err
	}
	s, ok := v.(*stream)
	if !ok || s.dict["Type"] != name("XRef") {
		return nil, nil, fmt.Errorf("expected cross-reference stream")
	}
	widths, ok := s.dict["W"].(array)
	if !ok || len(widths) != 3 {
		return nil, nil, fmt.Errorf("invalid xref stream W")
	}
	var w [3]int64
	total := int64(0)
	for i, v := range widths {
		n, ok := integer(v)
		if !ok || n < 0 || n > 8 {
			return nil, nil, fmt.Errorf("invalid xref field width")
		}
		w[i] = n
		total += n
	}
	if total == 0 {
		return nil, nil, fmt.Errorf("empty xref entries")
	}
	size, ok := integer(s.dict["Size"])
	if !ok || size < 1 || size > maxObjects {
		return nil, nil, fmt.Errorf("invalid xref Size")
	}
	indices := array{int64(0), size}
	if v, exists := s.dict["Index"]; exists {
		var ok bool
		indices, ok = v.(array)
		if !ok || len(indices)%2 != 0 {
			return nil, nil, fmt.Errorf("invalid xref Index")
		}
	}
	data, err := d.decode(s)
	if err != nil {
		return nil, nil, err
	}
	pos := 0
	entries := map[int64]xrefEntry{}
	for i := 0; i < len(indices); i += 2 {
		start, ok := integer(indices[i])
		count, okCount := integer(indices[i+1])
		if !ok || !okCount || start < 0 || count < 0 || start > maxObjects || count > maxObjects-start {
			return nil, nil, fmt.Errorf("invalid xref range")
		}
		if count > int64(len(data)-pos)/total {
			return nil, nil, fmt.Errorf("truncated xref stream")
		}
		for j := int64(0); j < count; j++ {
			fields := [3]int64{}
			for k, width := range w {
				var n uint64
				for b := int64(0); b < width; b++ {
					n = n<<8 | uint64(data[pos])
					pos++
				}
				if n > 1<<63-1 {
					return nil, nil, fmt.Errorf("xref field overflow")
				}
				fields[k] = int64(n)
			}
			if w[0] == 0 {
				fields[0] = 1
			}
			if fields[0] < 0 || fields[0] > 2 {
				return nil, nil, fmt.Errorf("unknown xref entry type")
			}
			e := xrefEntry{kind: int(fields[0]), offset: fields[1], generation: fields[2]}
			if e.kind == 2 {
				e.objectStream, e.index = fields[1], fields[2]
			}
			entries[start+j] = e
		}
	}
	return entries, s.dict, nil
}
func (d *document) walkPages(v value, resources value, visited map[reference]bool, depth int) error {
	if depth > maxDepth {
		return fmt.Errorf("page tree too deep")
	}
	if ref, ok := v.(reference); ok {
		if visited[ref] {
			return fmt.Errorf("page tree contains duplicate or cyclic nodes")
		}
		visited[ref] = true
	}
	node, err := d.dictionary(v)
	if err != nil {
		return err
	}
	if own, ok := node["Resources"]; ok {
		resources = own
	}
	switch node["Type"] {
	case name("Page"):
		if len(d.pages) >= maxObjects {
			return fmt.Errorf("too many pages")
		}
		identity, _ := v.(reference)
		d.pages = append(d.pages, pageInfo{object: node, resources: resources, identity: identity})
		return nil
	case name("Pages"):
		children, err := d.resolve(node["Kids"])
		if err != nil {
			return err
		}
		kids, ok := children.(array)
		if !ok {
			return fmt.Errorf("invalid page tree Kids")
		}
		before := len(d.pages)
		for _, child := range kids {
			if err := d.walkPages(child, resources, visited, depth+1); err != nil {
				return err
			}
		}
		count, ok := integer(node["Count"])
		if !ok || count != int64(len(d.pages)-before) {
			return fmt.Errorf("page tree Count disagrees with its children")
		}
		return nil
	default:
		return fmt.Errorf("unexpected page tree node %s", strconv.Quote(fmt.Sprint(node["Type"])))
	}
}
