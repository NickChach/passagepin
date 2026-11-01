import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { WorkerClient } from '../electron/worker-client';

test('failed spawns allow stop both during startup and after rejection', { timeout: 3000 }, async () => {
  const work = await mkdtemp(path.join(os.tmpdir(), 'passagepin-missing-worker-'));
  try {
    for (const stopDuringStartup of [false, true]) {
      const client = new WorkerClient(path.join(work, 'missing-worker'));
      const starting = assert.rejects(client.start(), (error: NodeJS.ErrnoException) => error.code === 'ENOENT');
      if (stopDuringStartup) await client.stop();
      await starting;
      await client.stop();
    }
  } finally { await rm(work, { recursive: true, force: true }); }
});
