import { test, expect, _electron as electron } from '@playwright/test';
import { mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { createLibrary, readState, readLegacyState } from './fixture';

test('PassagePin restores the previous PDF Search library and theme and rebuilds its index', async () => {
  const previous = process.env.PDFSEARCH_TEST_PREVIOUS_BINARY;
  const current = process.env.PDFSEARCH_TEST_PACKAGED_BINARY;
  test.skip(!previous || !current, 'Provide both previous and current packaged binaries to check the rename.');
  const work = await mkdtemp(path.join(os.tmpdir(), 'passagepin-upgrade-'));
  const root = path.join(work, 'Existing library'); await createLibrary(root, true);
  const env = { ...process.env, XDG_CONFIG_HOME: path.join(work, 'config'), XDG_CACHE_HOME: path.join(work, 'cache'), PDFSEARCH_MODEL_DIR: '', ONNXRUNTIME_SHARED_LIBRARY_PATH: '' };
  let application: Awaited<ReturnType<typeof electron.launch>> | undefined;
  const workerPIDs: number[] = [];
  try {
    application = await electron.launch({ executablePath: previous!, env });
    const legacyProfile = await application.evaluate(({ app }) => app.getPath('userData'));
    await application.evaluate(({ dialog }, directory) => {
      dialog.showOpenDialog = async () => ({ canceled: false, filePaths: [directory] });
    }, root);
    const oldPage = await application.firstWindow();
    await expect(oldPage.getByText('Search engine ready')).toBeVisible({ timeout: 60_000 });
    workerPIDs.push((await readLegacyState(oldPage)).ready!.pid);
    await oldPage.getByRole('button', { name: 'Choose folder', exact: true }).click();
    await oldPage.getByRole('button', { name: 'Index folder', exact: true }).click();
    await expect(oldPage.getByText('2 searchable pages are ready.')).toBeVisible({ timeout: 60_000 });
    const oldSwitch = oldPage.getByRole('switch', { name: 'Dark mode' });
    if (!(await oldSwitch.isChecked())) await oldSwitch.click();
    const originalState = await readLegacyState(oldPage);
    const libraryPreference = await readFile(path.join(legacyProfile, 'library.json'), 'utf8');
    const indexDirectory = path.join(legacyProfile, 'indexes');
    const indexFiles = await readdir(indexDirectory);
    expect(indexFiles).toHaveLength(1);
    const indexPath = path.join(indexDirectory, indexFiles[0]);
    const originalIndex = await readFile(indexPath, 'utf8');
    await application.close(); application = undefined;

    application = await electron.launch({ executablePath: current!, env });
    expect(await application.evaluate(({ app }) => app.getName())).toBe('PassagePin');
    expect(await application.evaluate(({ app }) => app.getPath('userData'))).toBe(legacyProfile);
    expect(await application.evaluate(({ app }) => app.getPath('sessionData'))).toBe(legacyProfile);
    const page = await application.firstWindow();
    await expect(page).toHaveTitle('PassagePin');
    await expect(page.getByText('Search engine ready')).toBeVisible({ timeout: 60_000 });
    await expect(page.getByRole('button', { name: 'Rebuild index', exact: true })).toBeEnabled();
    await expect(page.getByText('The saved index needs rebuilding before it can be searched.')).toBeVisible();
    await expect(page.getByRole('switch', { name: 'Dark mode' })).toBeChecked();
    const restored = await readState(page);
    workerPIDs.push(restored.ready!.pid);
    expect(restored.library?.directory).toBe(originalState.library?.directory);
    expect(restored.library?.indexed).toBe(false);
    expect(restored.libraries.some((entry) => entry.id === restored.activeLibraryId && entry.directory === originalState.library?.directory)).toBe(true);
    expect(await readFile(path.join(legacyProfile, 'library.json'), 'utf8')).toBe(libraryPreference);
    expect(await readFile(indexPath, 'utf8')).toBe(originalIndex);
    await page.getByRole('textbox', { name: 'Search subject' }).fill('renewable energy sources');
    await expect(page.getByRole('button', { name: 'Search', exact: true })).toBeDisabled();
    await page.getByRole('button', { name: 'Rebuild index', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Update index', exact: true })).toBeEnabled({ timeout: 60_000 });
    const rebuiltIndex = JSON.parse(await readFile(indexPath, 'utf8'));
    expect(rebuiltIndex.version).not.toBe(JSON.parse(originalIndex).version);
    await expect(page.getByRole('button', { name: 'Search', exact: true })).toBeEnabled();
    await page.getByRole('button', { name: 'Search', exact: true }).click();
    await expect(page.getByRole('article', { name: 'Selected PDF page' }).getByText('PDF page 3', { exact: true })).toBeVisible();
    await expect(page.getByText(/Photovoltaic panels and wind turbines/)).toBeVisible();
  } finally {
    if (application) await application.close();
    await rm(work, { recursive: true, force: true });
  }
  for (const pid of workerPIDs) await expect.poll(() => { try { process.kill(pid, 0); return true; } catch { return false; } }).toBe(false);
});

test('an incompatible saved index is rebuilt before search', async () => {
  const work = await mkdtemp(path.join(os.tmpdir(), 'passagepin-index-upgrade-'));
  const root = path.join(work, 'Existing library');
  const pdf = await createLibrary(root, true);
  const originalPDF = await readFile(pdf);
  const profile = path.join(work, 'config', 'pdfsearch-desktop');
  const packaged = process.env.PDFSEARCH_TEST_PACKAGED_BINARY;
  const launch = () => electron.launch({
    ...(packaged ? { executablePath: packaged } : {}),
    args: packaged ? [] : [path.resolve('.')],
    env: { ...process.env, XDG_CONFIG_HOME: path.join(work, 'config'), XDG_CACHE_HOME: path.join(work, 'cache'), PDFSEARCH_E2E: '1', PDFSEARCH_E2E_DATA: profile },
  });
  let application: Awaited<ReturnType<typeof electron.launch>> | undefined;
  try {
    application = await launch();
    await application.evaluate(({ dialog }, directory) => {
      dialog.showOpenDialog = async () => ({ canceled: false, filePaths: [directory] });
    }, root);
    let page = await application.firstWindow();
    await expect(page.getByText('Search engine ready')).toBeVisible({ timeout: 60_000 });
    await page.getByRole('button', { name: 'Choose folder', exact: true }).click();
    await page.getByRole('button', { name: 'Index folder', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Update index', exact: true })).toBeEnabled({ timeout: 60_000 });
    const theme = page.getByRole('switch', { name: 'Dark mode' });
    if (!(await theme.isChecked())) await theme.click();
    const originalState = await readState(page);
    const indexPath = path.join(profile, 'indexes', originalState.activeLibraryId + '.json');
    await application.close(); application = undefined;

    const currentIndex = JSON.parse(await readFile(indexPath, 'utf8'));
    expect(currentIndex.version).toBeGreaterThan(1);
    // Preserve a real index and simulate a version produced by an older app.
    const oldIndex = JSON.stringify({ ...currentIndex, version: currentIndex.version - 1 });
    await writeFile(indexPath, oldIndex);
    await writeFile(path.join(profile, 'library.json'), JSON.stringify({ directory: originalState.library!.directory }));
    await rm(path.join(profile, 'libraries.json'));

    application = await launch();
    page = await application.firstWindow();
    await expect(page.getByText('Search engine ready')).toBeVisible({ timeout: 60_000 });
    await expect(page.getByRole('switch', { name: 'Dark mode' })).toBeChecked();
    await expect(page.getByText('The saved index needs rebuilding before it can be searched.')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Rebuild index', exact: true })).toBeEnabled();
    const restored = await readState(page);
    expect(restored.activeLibraryId).toBe(originalState.activeLibraryId);
    expect(restored.library?.directory).toBe(originalState.library?.directory);
    expect(restored.library?.indexed).toBe(false);
    expect(await readFile(indexPath, 'utf8')).toBe(oldIndex);
    await page.getByRole('textbox', { name: 'Search subject' }).fill('renewable energy sources');
    await expect(page.getByRole('button', { name: 'Search', exact: true })).toBeDisabled();
    await page.getByRole('button', { name: 'Rebuild index', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Update index', exact: true })).toBeEnabled({ timeout: 60_000 });
    expect(JSON.parse(await readFile(indexPath, 'utf8')).version).toBe(currentIndex.version);
    expect(await readFile(pdf)).toEqual(originalPDF);
    await page.getByRole('button', { name: 'Search', exact: true }).click();
    await expect(page.getByRole('article', { name: 'Selected PDF page' }).getByText('PDF page 3', { exact: true })).toBeVisible();
    await expect(page.getByText(/Photovoltaic panels and wind turbines/)).toBeVisible();
  } finally {
    if (application) await application.close();
    await rm(work, { recursive: true, force: true });
  }
});
