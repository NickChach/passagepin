import assert from 'node:assert/strict';
import { test } from 'node:test';
import { existsSync } from 'node:fs';
import { copyFile, mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { WorkerClient, WorkerError } from '../electron/worker-client';
import { createLibrary } from './fixture';
import type { Progress } from '../electron/types';

test('real Go worker keeps one process and model for indexing and repeated searches', async (t) => {
  const binary = process.env.PDFSEARCH_WORKER || path.resolve('../dist/pdfsearch-linux-amd64');
  if (!existsSync(binary)) { t.skip('run make bundle first'); return; }
  const work = await mkdtemp(path.join(os.tmpdir(), 'pdfsearch-client-'));
  const client = new WorkerClient(binary);
  t.after(async () => { await client.stop(); await rm(work, { recursive: true, force: true }); });
  const root = path.join(work, 'pdfs');
  const original = await createLibrary(root, true);
  await copyFile(original, path.join(root, 'More research.pdf'));
  const ready = await client.start(); assert.equal(ready.model_loads, 1);
  await client.request('open', { directory: root, index_path: path.join(work, 'index.json') });
  const progress: Progress[] = []; client.on('progress', (event: Progress) => progress.push(event));
  const indexed = await client.request('index');
  assert.equal(indexed.library.documents, 2); assert.equal(indexed.library.pages, 4); assert.equal(indexed.library.empty_pages, 2);
  for (const filename of ['Energy and bread.pdf', 'More research.pdf']) {
    const events = progress.filter((event) => event.filename === filename);
    assert.deepEqual(events.filter((event) => event.phase === 'embedding').map((event) => event.page), [1, 2, 3]);
    assert.deepEqual(events.at(-1), { phase: 'completed', filename, page: 3, pages: 3 });
  }
  for (const query of ['renewable energy sources', 'generating electricity from sunlight']) {
    const result = await client.request('search', { query, top: 1 });
    assert.equal(result.results[0]?.page, 3); assert.equal(result.results[0]?.filename, 'Energy and bread.pdf');
    assert.equal((await client.start()).pid, ready.pid);
  }
  const page = await client.request('page', { filename: 'Energy and bread.pdf', page: 3 });
  assert.match(page.text, /Photovoltaic/);
  assert.deepEqual(await client.request('resolve-pdf', { filename: 'Energy and bread.pdf', page: 3 }), { path: page.path });
  assert.equal(page.path, path.join(root, 'Energy and bread.pdf'));
  const status = await client.request('status');
  assert.deepEqual(status, indexed.library);
  assert.equal(indexed.stats.pages, status.pages);
  assert.equal(indexed.stats.empty_pages, status.empty_pages);
  assert.equal(indexed.stats.failed_pages, status.failed_pages);
  assert.deepEqual(Object.keys(indexed.stats.timings).sort(), ['embedding_ns', 'extraction_ns', 'hashing_ns', 'saving_ns', 'total_ns']);
  for (const value of Object.values(indexed.stats.timings)) assert.equal(typeof value, 'number');
  const cancelledFile = path.join(root, 'cancelled.pdf');
  await copyFile(original, cancelledFile);
  await client.request('index');
  await rm(cancelledFile);
  await assert.rejects(client.request('page', { filename: 'cancelled.pdf', page: 1 }), (error: unknown) =>
    error instanceof WorkerError && error.code === 'operation_failed' && error.message.includes('cancelled.pdf'));

  await assert.rejects(client.request('page', { filename: '../secret.pdf', page: 1 }), /not in the active library/);
});
