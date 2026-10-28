import { test, expect, _electron as electron } from '@playwright/test';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { createLibrary } from './fixture';

test('PassagePin restores the previous PDF Search library, index and theme', async () => {
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
    workerPIDs.push(await oldPage.evaluate(async () => (await window.pdfSearch.getState()).ready!.pid));
    await oldPage.getByRole('button', { name: 'Choose folder', exact: true }).click();
    await oldPage.getByRole('button', { name: 'Index folder', exact: true }).click();
    await expect(oldPage.getByText('2 searchable pages are ready.')).toBeVisible({ timeout: 60_000 });
    const oldSwitch = oldPage.getByRole('switch', { name: 'Dark mode' });
    if (!(await oldSwitch.isChecked())) await oldSwitch.click();
    const originalState = await oldPage.evaluate(() => window.pdfSearch.getState());
    const libraryPreference = await readFile(path.join(legacyProfile, 'library.json'), 'utf8');
    await application.close(); application = undefined;

    application = await electron.launch({ executablePath: current!, env });
    expect(await application.evaluate(({ app }) => app.getName())).toBe('PassagePin');
    expect(await application.evaluate(({ app }) => app.getPath('userData'))).toBe(legacyProfile);
    expect(await application.evaluate(({ app }) => app.getPath('sessionData'))).toBe(legacyProfile);
    const page = await application.firstWindow();
    await expect(page).toHaveTitle('PassagePin');
    await expect(page.getByText('Search engine ready')).toBeVisible({ timeout: 60_000 });
    await expect(page.getByRole('button', { name: 'Update index', exact: true })).toBeEnabled();
    await expect(page.getByRole('switch', { name: 'Dark mode' })).toBeChecked();
    const restored = await page.evaluate(() => window.pdfSearch.getState());
    workerPIDs.push(restored.ready!.pid);
    expect(restored.library).toEqual(originalState.library);
    expect(await readFile(path.join(legacyProfile, 'library.json'), 'utf8')).toBe(libraryPreference);
    // Search the saved index directly, without invoking indexing in the new app.
    await page.getByRole('textbox', { name: 'Search subject' }).fill('renewable energy sources');
    await page.getByRole('button', { name: 'Search', exact: true }).click();
    await expect(page.getByRole('article', { name: 'Selected PDF page' }).getByText('PDF page 3', { exact: true })).toBeVisible();
    await expect(page.getByText(/Photovoltaic panels and wind turbines/)).toBeVisible();
  } finally {
    if (application) await application.close();
    await rm(work, { recursive: true, force: true });
  }
  for (const pid of workerPIDs) await expect.poll(() => { try { process.kill(pid, 0); return true; } catch { return false; } }).toBe(false);
});
