import { test, expect, _electron as electron } from '@playwright/test';
import { mkdtemp, mkdir, readFile, rename, rm, stat, symlink, writeFile } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { createLibrary } from './fixture';

test('libraries retain names and independent indexes across switches, restarts and removal', async () => {
  const work = await mkdtemp(path.join(os.tmpdir(), 'passagepin-multiple-'));
  const profile = path.join(work, 'profile');
  const first = path.join(work, 'Research'); const second = path.join(work, 'Cooking');
  await rename(await createLibrary(first, true), path.join(first, 'Research.pdf'));
  await rename(await createLibrary(second, true), path.join(second, 'Recipes.pdf'));
  const launch = () => electron.launch({ args: [path.resolve('.')], env: { ...process.env, PDFSEARCH_E2E: '1', PDFSEARCH_E2E_DATA: profile } });
  let application = await launch();
  try {
    let page = await application.firstWindow();
    await expect(page.getByText('Search engine ready')).toBeVisible({ timeout: 60_000 });
    const errors: string[] = []; page.on('pageerror', (error) => errors.push(error.message));
    const choose = async (directory: string) => {
      await application.evaluate(({ dialog }, folder) => { dialog.showOpenDialog = async () => ({ canceled: false, filePaths: [folder] }); }, directory);
      await page.getByRole('button', { name: /^(Choose folder|Add library)$/ }).click();
    };
    await choose(first);
    await page.getByRole('button', { name: 'Index folder', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Update index', exact: true })).toBeEnabled();
    await page.getByRole('button', { name: 'Rename library Research', exact: true }).click();
    await page.getByRole('textbox', { name: 'Library name', exact: true }).fill('Study shelf');
    await page.getByRole('button', { name: 'Save name', exact: true }).click();
    await expect(page.getByRole('list', { name: 'Saved libraries' }).getByText('Study shelf', { exact: true })).toBeVisible();
    const original = await page.evaluate(() => window.pdfSearch.getState());
    const firstIndex = path.join(profile, 'indexes', original.activeLibraryId! + '.json');
    const firstModified = (await stat(firstIndex)).mtimeMs;
    const pid = original.ready!.pid;
    // Adding a symlink to the same folder must keep its name and saved index.
    const alias = path.join(work, 'Research alias'); await symlink(first, alias);
    await choose(alias);
    await expect(page.getByRole('list', { name: 'Saved libraries' }).locator('li')).toHaveCount(1);
    expect((await page.evaluate(() => window.pdfSearch.getState())).libraries[0].name).toBe('Study shelf');
    await choose(second);
    await expect(page.getByRole('button', { name: 'Index folder', exact: true })).toBeEnabled();
    await page.getByRole('button', { name: 'Index folder', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Update index', exact: true })).toBeEnabled();
    const secondState = await page.evaluate(() => window.pdfSearch.getState());
    const secondId = secondState.activeLibraryId!;
    const secondIndex = path.join(profile, 'indexes', secondId + '.json');
    const secondModified = (await stat(secondIndex)).mtimeMs;
    await mkdir(path.resolve('test-results'), { recursive: true });
    await page.screenshot({ path: path.resolve('test-results/libraries-light.png'), fullPage: true });
    await page.getByRole('textbox', { name: 'Search subject' }).fill('renewable energy');
    await page.getByRole('button', { name: 'Search', exact: true }).click();
    await expect(page.locator('.result-info strong').first()).toHaveText('Recipes.pdf');
    await expect(page.getByRole('article', { name: 'Selected PDF page' })).toBeVisible();
    await page.getByRole('list', { name: 'Saved libraries' }).getByText('Study shelf', { exact: true }).click();
    await expect(page.getByRole('region', { name: 'Search results' })).toHaveCount(0);
    await expect(page.getByRole('article', { name: 'Selected PDF page' })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Update index', exact: true })).toBeEnabled();
    await page.getByRole('button', { name: 'Search', exact: true }).click();
    await expect(page.locator('.result-info strong').first()).toHaveText('Research.pdf');
    expect((await page.evaluate(() => window.pdfSearch.getState())).ready).toEqual(original.ready);
    expect((await stat(firstIndex)).mtimeMs).toBe(firstModified);
    expect((await stat(secondIndex)).mtimeMs).toBe(secondModified);
    // Invalid and concurrent renderer requests must not bypass main-process guards.
    const guarded = await page.evaluate(async (id) => {
      const opening = window.pdfSearch.selectLibrary(id);
      const attempts = await Promise.allSettled([
        window.pdfSearch.renameLibrary(id, 'Racing rename'), window.pdfSearch.removeLibrary(id), window.pdfSearch.selectLibrary(id),
      ]);
      await opening;
      return attempts.map((result) => result.status === 'rejected' ? String(result.reason) : 'unexpected success');
    }, secondId);
    expect(guarded.every((result) => result.includes('current operation'))).toBe(true);
    const invalid = await page.evaluate(async () => {
      try { await window.pdfSearch.removeLibrary('../outside'); return 'unexpected success'; } catch (error) { return String(error); }
    });
    expect(invalid).toContain('no longer saved');
    await application.close(); application = await launch(); page = await application.firstWindow();
    await expect(page.getByRole('button', { name: 'Update index', exact: true })).toBeEnabled({ timeout: 60_000 });
    const restored = await page.evaluate(() => window.pdfSearch.getState());
    expect(restored.activeLibraryId).toBe(secondId); expect(restored.libraries.map((entry) => entry.name)).toEqual(['Study shelf', 'Cooking']);
    expect(restored.ready!.model_loads).toBe(1);
    expect(pid).toBeGreaterThan(0);
    await page.getByRole('button', { name: 'Remove library Study shelf', exact: true }).click();
    await page.getByRole('button', { name: 'Keep library', exact: true }).click();
    await expect(page.getByRole('list', { name: 'Saved libraries' }).locator('li')).toHaveCount(2);
    await page.getByRole('button', { name: 'Remove library Study shelf', exact: true }).click();
    await page.getByRole('button', { name: 'Remove library', exact: true }).click();
    await expect(page.getByRole('list', { name: 'Saved libraries' }).locator('li')).toHaveCount(1);
    expect((await page.evaluate(() => window.pdfSearch.getState())).activeLibraryId).toBe(secondId);
    await expect(stat(firstIndex)).rejects.toMatchObject({ code: 'ENOENT' });
    await stat(path.join(first, 'Research.pdf'));
    await page.getByRole('button', { name: 'Remove library Cooking', exact: true }).click();
    await page.getByRole('button', { name: 'Remove library', exact: true }).click();
    await expect(page.getByRole('list', { name: 'Saved libraries' })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Search', exact: true })).toBeDisabled();
    await expect(stat(secondIndex)).rejects.toMatchObject({ code: 'ENOENT' });
    await stat(path.join(second, 'Recipes.pdf'));
    const removed = await page.evaluate(async () => {
      const results = await Promise.allSettled([window.pdfSearch.index(false), window.pdfSearch.search('energy', 10, 0), window.pdfSearch.page('Recipes.pdf', 3)]);
      return results.map((result) => result.status === 'rejected' ? String(result.reason) : 'unexpected success');
    });
    expect(removed.every((result) => result.includes('available library') || result.includes('current operation'))).toBe(true);
    await application.close(); application = await launch(); page = await application.firstWindow();
    await expect(page.getByText('Search engine ready')).toBeVisible({ timeout: 60_000 });
    expect((await page.evaluate(() => window.pdfSearch.getState())).libraries).toEqual([]);
    expect(errors).toEqual([]);
  } finally { await application.close(); await rm(work, { recursive: true, force: true }); }
});

test('legacy selection migrates and unavailable libraries survive failed opens and restarts', async () => {
  const work = await mkdtemp(path.join(os.tmpdir(), 'passagepin-library-upgrade-'));
  const profile = path.join(work, 'profile'); await mkdir(profile);
  const directory = path.join(work, 'Archive'); await createLibrary(directory, true);
  const legacy = JSON.stringify({ directory }); await writeFile(path.join(profile, 'library.json'), legacy);
  const launch = () => electron.launch({ args: [path.resolve('.')], env: { ...process.env, PDFSEARCH_E2E: '1', PDFSEARCH_E2E_DATA: profile } });
  let application = await launch();
  try {
    let page = await application.firstWindow();
    await expect(page.getByRole('button', { name: 'Index folder', exact: true })).toBeEnabled({ timeout: 60_000 });
    let state = await page.evaluate(() => window.pdfSearch.getState());
    const id = state.activeLibraryId!;
    expect(state.libraries[0].name).toBe('Archive');
    expect(await readFile(path.join(profile, 'library.json'), 'utf8')).toBe(legacy);
    await page.getByRole('button', { name: 'Index folder', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Update index', exact: true })).toBeEnabled();
    // A failed switch must retain the working library and results.
    const other = path.join(work, 'Other'); await createLibrary(other, true);
    await application.evaluate(({ dialog }, folder) => { dialog.showOpenDialog = async () => ({ canceled: false, filePaths: [folder] }); }, other);
    await page.getByRole('button', { name: 'Add library', exact: true }).click();
    await page.getByRole('list', { name: 'Saved libraries' }).getByText('Archive', { exact: true }).click();
    await page.getByRole('textbox', { name: 'Search subject' }).fill('renewable energy');
    await page.getByRole('button', { name: 'Search', exact: true }).click();
    await expect(page.getByText(/Photovoltaic panels and wind turbines/)).toBeVisible();
    // If saving selection fails after the worker opens another index, both the
    // visible library and the worker must return to the previous folder.
    const filename = path.join(profile, 'libraries.json');
    const saved = await readFile(filename, 'utf8');
    await rm(filename); await mkdir(filename);
    await page.getByRole('list', { name: 'Saved libraries' }).getByText('Other', { exact: true }).click();
    await expect(page.getByRole('alert')).toBeVisible();
    state = await page.evaluate(() => window.pdfSearch.getState());
    expect(state.activeLibraryId).toBe(id); expect(state.library?.directory).toBe(directory);
    await rm(filename, { recursive: true }); await writeFile(filename, saved);
    await page.getByRole('button', { name: 'Search', exact: true }).click();
    await expect(page.getByText(/Photovoltaic panels and wind turbines/)).toBeVisible();
    await rename(other, other + '-offline');
    await page.getByRole('list', { name: 'Saved libraries' }).getByText('Other', { exact: true }).click();
    await expect(page.getByRole('alert')).toBeVisible();
    state = await page.evaluate(() => window.pdfSearch.getState());
    expect(state.activeLibraryId).toBe(id); expect(state.library?.directory).toBe(directory);
    await expect(page.getByText(/Photovoltaic panels and wind turbines/)).toBeVisible();
    await application.close(); await rename(directory, directory + '-offline');
    application = await launch(); page = await application.firstWindow();
    await expect(page.getByText('Search engine ready')).toBeVisible({ timeout: 60_000 });
    await expect(page.getByRole('alert')).toContainText('Reconnect its folder');
    state = await page.evaluate(() => window.pdfSearch.getState());
    expect(state.library).toBeNull(); expect(state.activeLibraryId).toBe(id); expect(state.libraries).toHaveLength(2);
    await rename(directory + '-offline', directory);
    await page.getByRole('list', { name: 'Saved libraries' }).getByText('Archive', { exact: true }).click();
    await expect(page.getByRole('button', { name: 'Update index', exact: true })).toBeEnabled();
    await expect(page.getByRole('alert')).toHaveCount(0);
    await page.getByRole('textbox', { name: 'Search subject' }).fill('renewable energy');
    await page.getByRole('button', { name: 'Search', exact: true }).click();
    await expect(page.getByText(/Photovoltaic panels and wind turbines/)).toBeVisible();
    await page.getByRole('switch', { name: 'Dark mode' }).click();
    await page.getByRole('button', { name: 'Rename library Archive', exact: true }).click();
    await page.screenshot({ path: path.resolve('test-results/libraries-dark.png'), fullPage: true });
    await page.getByRole('button', { name: 'Cancel', exact: true }).click();
    await page.getByRole('button', { name: 'Remove library Other', exact: true }).click();
    await page.getByRole('button', { name: 'Remove library', exact: true }).click();
    await expect(page.getByRole('list', { name: 'Saved libraries' }).locator('li')).toHaveCount(1);
    await expect(page.getByText(/Photovoltaic panels and wind turbines/)).toBeVisible();
  } finally { await application.close(); await rm(work, { recursive: true, force: true }); }
});
