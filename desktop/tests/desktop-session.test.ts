import assert from 'node:assert/strict';
import { test } from 'node:test';
import { EventEmitter } from 'node:events';
import { mkdir, mkdtemp, rm, realpath } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { DesktopSession, type WorkerConnection } from '../electron/desktop-session';
import { LibraryStore } from '../electron/library-store';
import type { LibraryChecker } from '../electron/library-check';
import type { Library, WorkerEvent } from '../electron/types';

class FakeWorker extends EventEmitter implements WorkerConnection {
  indexed = false;
  opened: string[] = [];
  gate: Promise<void> | null = null;
  stopped = false;
  cancellations = 0;
  async start() { return { protocol: 1, pid: 42, model_loads: 1 }; }
  async stop() { this.stopped = true; }
  async cancel() { this.cancellations++; }
  request: WorkerConnection['request'] = async (method, ...args) => {
    if (method === 'search') return { query: 'q', results: [] } as never;
    assert.equal(method, 'open');
    const { directory } = args[0] as { directory: string };
    this.opened.push(directory);
    await this.gate;
    return { directory, indexed: this.indexed, documents: 0, pages: 0, empty_pages: 0, updated_at: null } as never;
  };
}

async function setup(t: { after(fn: () => Promise<void>): void }, checker?: LibraryChecker) {
  const root = await mkdtemp(path.join(os.tmpdir(), 'passagepin-session-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const profile = path.join(root, 'profile');
  const first = path.join(root, 'first'); const second = path.join(root, 'second');
  await mkdir(first); await mkdir(second);
  const store = new LibraryStore(profile);
  const workers: FakeWorker[] = [];
  const events: WorkerEvent[] = [];
  const session = new DesktopSession(store, path.join(profile, 'indexes'), () => {
    const worker = new FakeWorker(); workers.push(worker); return worker;
  }, event => events.push(event), checker);
  await session.start();
  return { session, profile, first: await realpath(first), second: await realpath(second), store, workers, events };
}

test('failed registry selection restores the worker and displayed library together', async t => {
  const { session, profile, first, second, workers, store } = await setup(t);
  await session.chooseFolder(async () => first);
  const before = store.snapshot();
  const registry = path.join(profile, 'libraries.json');
  await rm(registry); await mkdir(registry);
  await assert.rejects(session.chooseFolder(async () => second));
  assert.deepEqual(workers[0].opened, [first, second, first]);
  assert.equal(session.state.library?.directory, first);
  assert.deepEqual(store.snapshot(), before);
  assert.equal(session.state.operation, null);
});

test('operation guards allow cancellation, reject overlapping actions and restore the library on restart', async t => {
  const { session, first, workers, events } = await setup(t);
  let release!: () => void;
  workers[0].gate = new Promise<void>(resolve => { release = resolve; });
  const opening = session.chooseFolder(async () => first);
  // Let operation() pass its startup barrier.
  await Promise.resolve();
  assert.equal(session.state.operation, 'opening');
  await assert.rejects(session.renameLibrary('unknown', 'name'), /current operation/);
  await assert.rejects(session.removeLibrary('unknown'), /current operation/);
  await assert.rejects(session.restart(), /Cancel the current operation/);
  await session.cancel(); assert.equal(workers[0].cancellations, 1);
  release(); await opening;
  workers[0].emit('crashed', new Error('worker failed'));
  assert.equal(session.state.ready, null);
  assert.equal(session.state.error, 'worker failed');
  await session.restart();
  assert.equal(workers[0].stopped, true);
  assert.equal(session.state.library?.directory, first);
  assert.equal(session.state.error, null);
  assert.equal(session.state.operation, null);
  assert.ok(events.some(event => event.kind === 'failed'));
  await session.stop(); assert.equal(workers[1].stopped, true);
});


test('background checks permit searches and discard results after a library switch', async t => {
  const pending: { signal: AbortSignal; resolve: (result: Awaited<ReturnType<LibraryChecker>>) => void }[] = [];
  const checker: LibraryChecker = (_index, signal) => new Promise(resolve => pending.push({ signal, resolve }));
  const { session, first, second, workers } = await setup(t, checker);
  workers[0].indexed = true;
  await session.chooseFolder(async () => first);
  assert.equal(session.state.check?.status, 'checking');
  await session.search('q', 10, 0);
  assert.equal(session.state.operation, null);
  await session.chooseFolder(async () => second);
  assert.equal(pending[0].signal.aborted, true);
  pending[0].resolve({ changed_pages: 9, added_files: 0, removed_files: 0, errors: [], refresh: [] });
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(session.state.check?.status, 'checking');
  pending[1].resolve({ changed_pages: 1, added_files: 0, removed_files: 0, errors: [], refresh: [] });
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(session.state.check?.changed_pages, 1);
  await session.stop();
});

test('text-verified metadata refresh waits for an active search', async t => {
  let finishCheck!: (result: Awaited<ReturnType<LibraryChecker>>) => void;
  const checker: LibraryChecker = () => new Promise(resolve => { finishCheck = resolve; });
  const { session, first, workers } = await setup(t, checker);
  workers[0].indexed = true;
  await session.chooseFolder(async () => first);
  let finishSearch!: () => void;
  const gate = new Promise<void>(resolve => { finishSearch = resolve; });
  let refreshes = 0;
  workers[0].request = async (method, ..._args) => {
    if (method === 'search') { await gate; return { query: 'q', results: [] } as never; }
    assert.equal(method, 'refresh'); refreshes++;
    return session.state.library as never;
  };
  const search = session.search('q', 10, 0);
  await Promise.resolve(); await Promise.resolve();
  finishCheck({ changed_pages: 0, added_files: 0, removed_files: 0, errors: [], refresh: [{ filename: 'book.pdf', before: 'a'.repeat(64), after: 'b'.repeat(64) }] });
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(refreshes, 0);
  finishSearch(); await search;
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(refreshes, 1);
  assert.equal(session.state.operation, null);
  await session.stop();
});
