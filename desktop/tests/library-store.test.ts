import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, mkdir, readFile, realpath, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { LibraryStore, libraryID } from '../electron/library-store';

test('saved names, selection and removals persist without resurrecting the legacy folder', async (t) => {
  const profile = await mkdtemp(path.join(os.tmpdir(), 'passagepin-libraries-'));
  t.after(() => rm(profile, { recursive: true, force: true }));
  const first = path.join(profile, 'Existing library'); await mkdir(first);
  const legacy = JSON.stringify({ directory: first });
  await writeFile(path.join(profile, 'library.json'), legacy);
  const store = new LibraryStore(profile); await store.load();
  const old = store.snapshot().libraries[0];
  assert.deepEqual(old, { id: libraryID(await realpath(first)), name: 'Existing library', directory: await realpath(first) });
  assert.equal(store.snapshot().activeLibraryId, old.id);
  assert.equal(await readFile(path.join(profile, 'library.json'), 'utf8'), legacy);
  await store.rename(old.id, '  Research notes  ');
  const alias = path.join(profile, 'alias'); await symlink(first, alias);
  await store.select(store.entry(await realpath(alias)));
  assert.equal(store.snapshot().libraries.length, 1, 'canonical aliases must reuse the saved entry');
  assert.equal(store.get(old.id).name, 'Research notes');
  const second = store.entry(path.join(profile, 'Other library'));
  await store.select(second);
  const reopened = new LibraryStore(profile); await reopened.load();
  assert.equal(reopened.snapshot().activeLibraryId, second.id);
  assert.equal(reopened.get(old.id).name, 'Research notes');
  await reopened.remove(old.id);
  assert.equal(reopened.snapshot().activeLibraryId, second.id, 'removing an inactive entry must preserve selection');
  await reopened.remove(second.id);
  const empty = new LibraryStore(profile); await empty.load();
  assert.deepEqual(empty.snapshot(), { libraries: [], activeLibraryId: null });
  assert.equal(await readFile(path.join(profile, 'library.json'), 'utf8'), legacy);
});

test('unavailable legacy folders remain saved so they can be retried or removed', async (t) => {
  const profile = await mkdtemp(path.join(os.tmpdir(), 'passagepin-unavailable-'));
  t.after(() => rm(profile, { recursive: true, force: true }));
  const directory = path.join(profile, 'Unplugged drive');
  await writeFile(path.join(profile, 'library.json'), JSON.stringify({ directory }));
  const store = new LibraryStore(profile); await store.load();
  assert.equal(store.snapshot().libraries[0].directory, directory);
  assert.equal(store.snapshot().activeLibraryId, libraryID(directory));
});

test('invalid names and unknown IDs cannot change the registry', async (t) => {
  const profile = await mkdtemp(path.join(os.tmpdir(), 'passagepin-validation-'));
  t.after(() => rm(profile, { recursive: true, force: true }));
  const store = new LibraryStore(profile); await store.load();
  const entry = store.entry(path.join(profile, 'PDFs')); await store.select(entry);
  const original = await readFile(path.join(profile, 'libraries.json'), 'utf8');
  for (const name of ['', '   ', 'a'.repeat(121), 'name\nwith control', 7, null]) {
    await assert.rejects(store.rename(entry.id, name), /library name/);
  }
  await assert.rejects(store.remove('../outside'), /no longer saved/);
  await assert.rejects(store.rename('unknown', 'Other name'), /no longer saved/);
  const snapshot = store.snapshot(); snapshot.libraries[0].name = 'Mutated';
  assert.equal(store.get(entry.id).name, 'PDFs');
  assert.equal(await readFile(path.join(profile, 'libraries.json'), 'utf8'), original);
});

test('corrupt registries are never overwritten by legacy migration', async (t) => {
  const profile = await mkdtemp(path.join(os.tmpdir(), 'passagepin-corrupt-'));
  t.after(() => rm(profile, { recursive: true, force: true }));
  const filename = path.join(profile, 'libraries.json');
  await writeFile(path.join(profile, 'library.json'), JSON.stringify({ directory: '/legacy' }));
  for (const source of ['{', 'null', JSON.stringify({ version: 1, libraries: [{ id: '../../other', directory: '/PDFs', name: 'PDFs' }], activeLibraryId: null })]) {
    await writeFile(filename, source);
    await assert.rejects(new LibraryStore(profile).load(), /saved librar/);
    assert.equal(await readFile(filename, 'utf8'), source);
  }
});

test('failed atomic writes preserve the last committed in-memory registry', async (t) => {
  const profile = await mkdtemp(path.join(os.tmpdir(), 'passagepin-save-failure-'));
  t.after(() => rm(profile, { recursive: true, force: true }));
  const store = new LibraryStore(profile);
  const entry = store.entry(path.join(profile, 'PDFs')); await store.select(entry);
  const original = store.snapshot();
  await rm(path.join(profile, 'libraries.json')); await mkdir(path.join(profile, 'libraries.json'));
  await assert.rejects(store.rename(entry.id, 'New name'));
  assert.deepEqual(store.snapshot(), original);
  await assert.rejects(store.remove(entry.id));
  assert.deepEqual(store.snapshot(), original);
});
