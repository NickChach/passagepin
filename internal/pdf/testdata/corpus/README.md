# PDF extraction corpus

These 12 small PDFs and their generator are original fixtures distributed under
the repository's MIT license. No external documents, embedded font programs,
personal data or additional redistribution permissions are required. They are
deterministic structural examples, not a representative sample of production
PDFs; add legally redistributable real-world files as their failures are triaged.

`manifest.json` records provenance, feature tags and expectations for every
physical page. `text` is whitespace-normalized Unicode, authored from the
fixture source rather than copied from another extractor. Error cases require
an explicit failure and an empty result, with a stable unsupported-feature ID.
`columns.pdf` records the desired column-major reading order as a known
limitation: the test checks retention of every word and logs order differences.
It does not claim that layout reconstruction passes.

The fixtures cover inherited resources, Flate streams, Type 1/WinAnsi fonts,
Identity-H/CID fonts, ToUnicode ligatures and surrogate pairs, direct and
indirect marked-content properties, nested replacements and forms, image-only
content, inline images, inherited ToUnicode maps and late-page filter failures.
Existing generated unit fixtures cover object streams, other filters, font
encodings, recovery, malformed syntax and cancellation.

From the repository root:

```sh
go run ./internal/pdf/testdata/corpus/generate.go
go test -v ./internal/pdf -run '^TestExtractionCorpus$'
go test ./internal/index -run '^TestCorpusIndexReportsAndReplacementText$'
```

Regeneration overwrites only the named corpus PDFs and manifest. Review changes
to both generator and expectations together; do not update expected text merely
to match current parser output. For new external files record their source URL,
author/license and redistribution basis, feature tags, physical page numbers,
manually verified text or desired error, and any remaining fidelity limitation.
Do not add private/user documents without permission.

Optional independent checks use Poppler, solely as a development aid:

```sh
pdftotext internal/pdf/testdata/corpus/actualtext-inline.pdf -
pdftotext -layout internal/pdf/testdata/corpus/columns.pdf -
pdftoppm -f 1 -singlefile -scale-to 900 -png \
  internal/pdf/testdata/corpus/columns.pdf /tmp/passagepin-corpus-columns
```

PDF renderers show the painted glyphs; `ActualText` deliberately replaces their
extracted text. The CID fixture tests Unicode mappings without embedding a font
program, so its visual glyph appearance is not a reference. Poppler is not the
oracle for nested replacements: its extraction can select an inner replacement
instead of the whole outer sequence. PassagePin's fixture expects the outer
replacement once, consistently with replacing all enclosed content. See
[ISO 32000-1, clauses 7.9.2.2, 14.6 and 14.9.4](https://opensource.adobe.com/dc-acrobat-sdk-docs/standards/pdfstandards/pdf/PDF32000_2008.pdf)
and the [PDF Association guidance on replacement text](https://pdfa.org/understanding-pdf-accessibility-techniques/).

Still out of scope: structure-tree `ActualText`, general tagged-PDF reading
order, column/table reconstruction, annotation extraction, OCR, encryption,
custom/vertical composite encodings, inherited CMaps and embedded fonts that
cannot be loaded using the existing supported font structures. Supported font
advances are preserved even where an `ActualText` sequence covers unmapped codes.
