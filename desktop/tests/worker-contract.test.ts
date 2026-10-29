import assert from 'node:assert/strict';
import { test } from 'node:test';
import { parseProgress, parseWorkerResult } from '../electron/worker-protocol';
import type { WorkerClient } from '../electron/worker-client';

// These checks run under tsc; request callers cannot choose an unrelated result
// type or omit fields required by the worker method.
function requestTypes(client: WorkerClient) {
  // @ts-expect-error unknown worker method
  void client.request('seach', { query: 'energy' });
  // @ts-expect-error page requires a physical page number
  void client.request('page', { filename: 'book.pdf' });
  // @ts-expect-error wrong query type
  void client.request('search', { query: 42 });
}
void requestTypes;

test('malformed progress cannot reach the renderer', () => {
  for (const data of [null, { phase: 'unknown' }, { phase: 'embedding', filename: 'a.pdf', page: 1 },
    { phase: 'embedding', filename: 'a.pdf', page: 3, pages: 2 },
    { phase: 'skipped', filename: 'a.pdf' }, { phase: 'reused', filename: 'a.pdf', pages: 3, partial: 'yes' }]) {
    assert.throws(() => parseProgress(data), /Invalid search engine progress/);
  }
});

 test('method results reject malformed data before reaching application state', () => {
   assert.throws(() => parseWorkerResult('search', { query: 'q', results: [{ filename: 'book.pdf', page: 1, score: 'high' }] }));
   assert.throws(() => parseWorkerResult('page', { text: 'incomplete page' }));
   assert.throws(() => parseWorkerResult('resolve-pdf', { path: null }));
   assert.throws(() => parseWorkerResult('open', { directory: '/library', indexed: true }));
   assert.deepEqual(parseWorkerResult('resolve-pdf', { path: '/library/book.pdf' }), { path: '/library/book.pdf' });
 });

test('issue kinds must be strings rather than coercible values', () => {
  assert.throws(() => parseWorkerResult('open', {
    directory: '/library', indexed: true, documents: 1, pages: 1, empty_pages: 0, updated_at: null,
    issues: [{ filename: 'book.pdf', kind: ['partial'], reason: 'a page failed' }],
  }));
});
