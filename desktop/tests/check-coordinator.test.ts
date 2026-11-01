import assert from 'node:assert/strict';
import { test } from 'node:test';
import { CheckCoordinator, refreshBatches } from '../electron/check-coordinator';
import type { CheckResult, FileRefresh } from '../electron/library-check';
import type { LibraryCheck } from '../electron/types';

test('refresh batches preserve all files and stay below the worker request limit', () => {
  const files: FileRefresh[] = Array.from({ length: 6000 }, (_, i) => ({ filename: `研究/${i}.pdf`, before: 'a'.repeat(64), after: 'b'.repeat(64) }));
  const batches = [...refreshBatches(files)];
  assert.ok(batches.length > 1);
  assert.deepEqual(batches.flat(), files);
  for (const batch of batches) {
    assert.ok(Buffer.byteLength(JSON.stringify({ id: '1', method: 'refresh', params: { files: batch } })) < 1024 * 1024);
  }
  assert.deepEqual([...refreshBatches([])], []);
});

test('invalidation discards queued refreshes and errors from an older generation', async () => {
  let complete!: (result: CheckResult) => void;
  let status: LibraryCheck | null = null;
  let ready = 0;
  const checks = new CheckCoordinator(() => new Promise(resolve => { complete = resolve; }), data => { status = data; }, () => { ready++; });
  const result: CheckResult = { changed_pages: 0, added_files: 0, removed_files: 0, errors: [], refresh: [{ filename: 'book.pdf', before: 'a'.repeat(64), after: 'b'.repeat(64) }] };
  checks.start('/index.json'); complete(result);
  await new Promise(resolve => setImmediate(resolve));
  const pending = checks.takeRefresh(); assert.ok(pending);
  assert.equal(ready, 1);
  checks.invalidate();
  checks.reportRefreshError(pending.generation, new Error('stale save failure'));
  assert.equal(status, null);
  checks.start('/other.json'); complete(result);
  await new Promise(resolve => setImmediate(resolve));
  checks.invalidate();
  assert.equal(checks.takeRefresh(), null);
});
