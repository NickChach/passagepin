import assert from 'node:assert/strict';
import { test } from 'node:test';
import { parseProgress } from '../electron/worker-protocol';
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
