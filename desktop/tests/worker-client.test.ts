import assert from 'node:assert/strict';
import { test } from 'node:test';
import { existsSync } from 'node:fs';
import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { WorkerClient } from '../electron/worker-client';
import { createLibrary } from './fixture';
import type { Library, SearchResult } from '../electron/types';

test('real Go worker keeps one process and model for indexing and repeated searches', async (t) => {
  const binary = process.env.PDFSEARCH_WORKER || path.resolve('../dist/pdfsearch-linux-amd64');
  if (!existsSync(binary)) { t.skip('run make bundle first'); return; }
  const work = await mkdtemp(path.join(os.tmpdir(), 'pdfsearch-client-'));
  const client = new WorkerClient(binary);
  t.after(async () => { await client.stop(); await rm(work, { recursive: true, force: true }); });
  const root = path.join(work, 'pdfs'); await createLibrary(root, true);
  const ready = await client.start(); assert.equal(ready.model_loads, 1);
  await client.request('open', { directory: root, index_path: path.join(work, 'index.json') });
  let progress = 0; client.on('progress', () => progress++);
  const indexed = await client.request<{ library: Library }>('index');
  assert.equal(indexed.library.pages, 2); assert.equal(indexed.library.empty_pages, 1); assert.ok(progress >= 4);
  for (const query of ['renewable energy sources', 'generating electricity from sunlight']) {
    const result = await client.request<{ results: SearchResult[] }>('search', { query, top: 1 });
    assert.equal(result.results[0]?.page, 3); assert.equal(result.results[0]?.filename, 'Energy and bread.pdf');
    assert.equal((await client.start()).pid, ready.pid);
  }
  const page = await client.request<{ text: string }>('page', { filename: 'Energy and bread.pdf', page: 3 });
  assert.match(page.text, /Photovoltaic/);
  await assert.rejects(client.request('page', { filename: '../secret.pdf', page: 1 }), /not in the active library/);
});
