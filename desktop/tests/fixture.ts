import type { Page } from '@playwright/test';
import type { DesktopAPI, State } from '../electron/types';
import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
export async function createLibrary(root: string, compositeFont = false, failedPages: number[] = []): Promise<string> {
  await mkdir(root, { recursive: true });
  const pages = [
    'Sourdough bread needs flour, water, salt, and a starter. Let the dough rise before baking.',
    '',
    'Photovoltaic panels and wind turbines generate clean electricity without burning fossil fuels.',
  ];
  let pdf = '%PDF-1.4\n'; const offsets = [0];
  const object = (id: number, body: string) => { offsets.push(Buffer.byteLength(pdf)); pdf += `${id} 0 obj\n${body}\nendobj\n`; };
  object(1, '<< /Type /Catalog /Pages 2 0 R >>');
  object(2, '<< /Type /Pages /Count 3 /Kids [4 0 R 6 0 R 8 0 R] >>');
  object(3, compositeFont
    ? '<< /Type /Font /Subtype /Type0 /BaseFont /Example /Encoding /Identity-H /DescendantFonts [10 0 R] /ToUnicode 11 0 R >>'
    : '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>');
  pages.forEach((text, i) => {
    const id = 4 + i * 2;
    object(id, `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents ${id + 1} 0 R >>`);
    const escaped = text.replace(/([\\()])/g, '\\$1');
    const encoded = compositeFont
      ? `<${Array.from(text, (character) => character.charCodeAt(0).toString(16).padStart(4, '0')).join('')}>`
      : `(${escaped})`;
    const content = `BT /F1 10 Tf 40 700 Td ${encoded} Tj ET\n`;
    object(id + 1, `<< /Length ${Buffer.byteLength(content)}${failedPages.includes(i + 1) ? ' /Filter /UnsupportedDecode' : ''} >>\nstream\n${content}endstream`);
  });
  if (compositeFont) {
    // Regression: real PDFs can reference a separate CID-width array and
    // include unused surrogate entries in a broad identity Unicode map.
    object(10, '<< /Type /Font /Subtype /CIDFontType2 /DW 600 /W [0 12 0 R] >>');
    const cmap = '1 begincodespacerange <0000> <FFFF> endcodespacerange 2 beginbfrange <0000> <007F> <0000> <D800> <DFFF> <D800> endbfrange\n';
    object(11, `<< /Length ${Buffer.byteLength(cmap)} >>\nstream\n${cmap}endstream`);
    object(12, `[${Array.from({ length: 128 }, () => '600').join(' ')}]`);
  }
  const xref = Buffer.byteLength(pdf);
  pdf += `xref\n0 ${offsets.length}\n0000000000 65535 f \n`;
  for (const offset of offsets.slice(1)) pdf += `${String(offset).padStart(10, '0')} 00000 n \n`;
  pdf += `trailer\n<< /Size ${offsets.length} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  const file = path.join(root, 'Energy and bread.pdf'); await writeFile(file, pdf); return file;
}

export async function readState(page: Page): Promise<State> {
  return page.evaluate(async () => {
    const reply = await window.pdfSearch.getState();
    if (!reply.ok) throw new Error(reply.error.message);
    return reply.value;
  });
}

// Historical packaged builds exposed raw results before the IPC envelope.
export async function readLegacyState(page: Page): Promise<State> {
  return page.evaluate(() => (window.pdfSearch as unknown as DesktopAPI).getState());
}
