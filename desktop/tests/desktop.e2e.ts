import { test, expect, _electron as electron } from '@playwright/test';
import { mkdtemp, rm, mkdir } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { createLibrary } from './fixture';

test('Electron searches with one resident worker and remembers the selected theme across restarts', async () => {
  const work = await mkdtemp(path.join(os.tmpdir(), 'pdfsearch-electron-'));
  const root = path.join(work, 'Research library'); await createLibrary(root, true);
  const packaged = process.env.PDFSEARCH_TEST_PACKAGED_BINARY;
  const launch = () => electron.launch({
    ...(packaged ? { executablePath: packaged } : {}),
    args: packaged ? [] : [path.resolve('.')],
    env: { ...process.env, XDG_CONFIG_HOME: path.join(work, 'config'), XDG_CACHE_HOME: path.join(work, 'cache'), PDFSEARCH_E2E: '1', PDFSEARCH_E2E_DATA: path.join(work, 'app-data'), PDFSEARCH_WORKER: path.resolve('../dist/pdfsearch-linux-amd64'), PDFSEARCH_MODEL_DIR: '', ONNXRUNTIME_SHARED_LIBRARY_PATH: '' },
  });
  let application = await launch();
  let workerPID = 0;
  try {
    await application.evaluate(({ dialog }, directory) => {
      dialog.showOpenDialog = async () => ({ canceled: false, filePaths: [directory] });
    }, root);
    const page = await application.firstWindow();
    await expect(page).toHaveTitle('PassagePin');
    await expect(page.getByText('PassagePin', { exact: true })).toBeVisible();
    expect(await application.evaluate(({ app }) => app.getName())).toBe('PassagePin');
    const errors: string[] = []; page.on('pageerror', (error) => errors.push(error.message));
    await expect(page.getByText('Search engine ready')).toBeVisible({ timeout: 60_000 });
    await expect(page.getByRole('heading', { name: 'Find where an idea is discussed.' })).toBeVisible();
    const systemTheme = await page.evaluate(() => window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
    await expect(page.locator('html')).toHaveAttribute('data-theme', systemTheme);
    const themeSwitch = page.getByRole('switch', { name: 'Dark mode' });
    if (systemTheme === 'dark') await themeSwitch.click();
    await expect(themeSwitch).not.toBeChecked();
    await page.getByRole('button', { name: 'Choose folder', exact: true }).click();
    await expect(page.getByText('Research library', { exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Index folder', exact: true }).click();
    await expect(page.getByText('2 searchable pages are ready.')).toBeVisible({ timeout: 60_000 });
    const pid = await page.evaluate(async () => (await window.pdfSearch.getState()).ready!.pid);
    workerPID = pid;
    await page.getByRole('textbox', { name: 'Search subject' }).fill('renewable energy sources');
    await page.getByRole('button', { name: 'Search', exact: true }).click();
    await expect(page.getByRole('article', { name: 'Selected PDF page' }).getByText('Page 3', { exact: true })).toBeVisible();
    await expect(page.getByText(/Photovoltaic panels and wind turbines/)).toBeVisible();
    await page.getByRole('textbox', { name: 'Search subject' }).fill('generating electricity from sunlight');
    await page.getByRole('button', { name: 'Search', exact: true }).click();
    await expect(page.getByText('For “generating electricity from sunlight”')).toBeVisible();
    await expect(page.getByText(/Photovoltaic panels and wind turbines/)).toBeVisible();
    expect(await page.evaluate(async () => (await window.pdfSearch.getState()).ready!.pid)).toBe(pid);
    expect(await page.evaluate(() => typeof (window as unknown as Record<string, unknown>).require)).toBe('undefined');
    await mkdir(path.resolve('test-results'), { recursive: true });
    await page.screenshot({ path: path.resolve('test-results/desktop-search.png'), fullPage: true });
    const lightBackground = await page.locator('.workspace').evaluate((element) => getComputedStyle(element).backgroundColor);
    // Switching appearance must retain the active search and resident model.
    await themeSwitch.click();
    await expect(themeSwitch).toBeChecked();
    await expect(page.locator('html')).toHaveCSS('color-scheme', 'dark');
    expect(await page.locator('.workspace').evaluate((element) => getComputedStyle(element).backgroundColor)).not.toBe(lightBackground);
    await expect(page.getByText(/Photovoltaic panels and wind turbines/)).toBeVisible();
    expect(await page.evaluate(async () => (await window.pdfSearch.getState()).ready!.pid)).toBe(pid);
    await page.getByText('Search options', { exact: true }).click();
    await expect(page.getByRole('combobox', { name: 'Maximum results' })).toBeVisible();
    await page.getByText('Search options', { exact: true }).click();
    await page.screenshot({ path: path.resolve('test-results/desktop-search-dark.png'), fullPage: true });
    await themeSwitch.focus(); await themeSwitch.press('Space');
    await expect(themeSwitch).not.toBeChecked();
    await themeSwitch.press('Enter'); await expect(themeSwitch).toBeChecked();
    await page.reload();
    await expect(themeSwitch).toBeChecked();
    await expect(page.locator('html')).toHaveCSS('color-scheme', 'dark');
    expect(errors).toEqual([]);
    await application.close();
    await expect.poll(() => { try { process.kill(workerPID, 0); return true; } catch { return false; } }).toBe(false);
    application = await launch();
    const reopened = await application.firstWindow();
    await expect(reopened.getByText('Search engine ready')).toBeVisible({ timeout: 60_000 });
    workerPID = await reopened.evaluate(async () => (await window.pdfSearch.getState()).ready!.pid);
    await expect(reopened.getByRole('switch', { name: 'Dark mode' })).toBeChecked();
    await expect(reopened.getByRole('button', { name: 'Update index', exact: true })).toBeEnabled();
    await reopened.getByRole('switch', { name: 'Dark mode' }).click();
    await expect(reopened.locator('html')).toHaveCSS('color-scheme', 'light');
    await reopened.reload();
    await expect(reopened.getByRole('switch', { name: 'Dark mode' })).not.toBeChecked();
  } finally { await application.close(); await rm(work, { recursive: true, force: true }); }
  if (workerPID) await expect.poll(() => { try { process.kill(workerPID, 0); return true; } catch { return false; } }).toBe(false);
});
