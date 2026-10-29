import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { ChildProcessWithoutNullStreams } from 'node:child_process';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import { WorkerClient, WorkerError } from '../electron/worker-client';

interface Request { id: string; method: string; params: Record<string, unknown> }

test('page cancellation keeps targeting the page until its final reply', async (t) => {
  const requests: Request[] = [];
  const child = Object.assign(new EventEmitter(), {
    stdin: new PassThrough(), stdout: new PassThrough(), stderr: new PassThrough(),
    kill: () => { child.emit('exit', null, 'SIGTERM'); return true; },
  });
  const reply = (data: unknown) => child.stdout.write(JSON.stringify(data) + '\n');
  child.stdin.on('data', (line: Buffer) => {
    const request = JSON.parse(line.toString()) as Request;
    requests.push(request);
    if (request.method === 'cancel') reply({ id: request.id, result: { cancelled: true } });
  });
  child.stdin.on('finish', () => child.emit('exit', 0, null));
  const launch = () => {
    queueMicrotask(() => reply({ event: 'ready', data: { protocol: 1, pid: 42, model_loads: 1 } }));
    return child as unknown as ChildProcessWithoutNullStreams;
  };
  const client = new WorkerClient('fake-worker', [], launch);
  t.after(() => client.stop());
  await client.start();

  const page = client.request('page', { filename: 'example.pdf', page: 1 });
  const cancelled = assert.rejects(page, (error: unknown) => error instanceof WorkerError && error.code === 'cancelled');
  // Allow request() to finish its readiness await and write the request.
  await Promise.resolve();
  const target = requests[0].id;
  await client.cancel();
  await client.cancel();
  assert.deepEqual(requests.slice(1).map((request) => request.params.target), [target, target]);
  reply({ id: target, error: { code: 'cancelled', message: 'context canceled' } });
  await cancelled;

  await client.cancel();
  assert.equal(requests.length, 3, 'completed requests must not remain cancellable');

  const nextPage = client.request('page', { filename: 'example.pdf', page: 2 });
  await Promise.resolve();
  const nextTarget = requests[3].id;
  assert.notEqual(nextTarget, target);
  reply({ id: nextTarget, result: { filename: 'example.pdf', page: 2, text: 'second page', path: '/library/example.pdf' } });
  assert.deepEqual(await nextPage, { filename: 'example.pdf', page: 2, text: 'second page', path: '/library/example.pdf' });
  await client.cancel();
  assert.equal(requests.length, 4, 'successful pages must clear the cancellation target');
  const malformed = client.request('page', { filename: 'example.pdf', page: 3 });
  const rejected = assert.rejects(malformed, /Invalid search engine response/);
  await Promise.resolve();
  reply({ id: requests[4].id, result: { text: 'incomplete response' } });
  await rejected;
});
