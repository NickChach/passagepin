import assert from 'node:assert/strict';
import { test } from 'node:test';
import { DesktopError, ipcResult, unwrapResult } from '../electron/ipc-result';
import { WorkerError } from '../electron/worker-client';

test('error codes survive serialization independently of message text', async () => {
  for (const [code, message] of [
    ['cancelled', 'Request stopped by the user'],
    ['operation_failed', 'Cannot open cancelled.pdf: no such file'],
  ]) {
    const reply = structuredClone(await ipcResult(() => { throw new WorkerError(message, code); }));
    assert.throws(() => unwrapResult(reply), (error: unknown) =>
      error instanceof DesktopError && error.code === code && error.message === message);
  }
  const ordinary = await ipcResult(() => { throw new Error('context canceled is part of this diagnostic'); });
  assert.equal(ordinary.ok, false);
  if (!ordinary.ok) assert.equal(ordinary.error.code, 'operation_failed');
  assert.equal(unwrapResult(await ipcResult(() => 42)), 42);
  assert.equal(unwrapResult(await ipcResult(() => undefined)), undefined);
});
