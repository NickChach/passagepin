import { test, expect, _electron as electron } from '@playwright/test';
import { spawn, type ChildProcess } from 'node:child_process';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { createLibrary } from './fixture';

interface InstanceTestMain { secondLaunches: number; focusRequests: number }

test('repeat launches focus one window and preserve libraries, names and selection through restart', async () => {
  const work = await mkdtemp(path.join(os.tmpdir(), 'passagepin-single-instance-'));
  const first = path.join(work, 'Research'); const second = path.join(work, 'Books');
  await createLibrary(first, true); await createLibrary(second, true);
  const packaged = process.env.PDFSEARCH_TEST_PACKAGED_BINARY;
  const args = packaged ? [] : [path.resolve('.')];
  const env = { ...process.env, PDFSEARCH_E2E: '1', PDFSEARCH_E2E_DATA: path.join(work, 'profile'), XDG_CONFIG_HOME: path.join(work, 'config'), XDG_CACHE_HOME: path.join(work, 'cache') };
  const launch = () => electron.launch({ ...(packaged ? { executablePath: packaged } : {}), args, env });
  const duplicates: ChildProcess[] = [];
  let application = await launch();
  try {
    let page = await application.firstWindow();
    await expect(page.getByText('Search engine ready')).toBeVisible({ timeout: 60_000 });
    const ready = (await page.evaluate(() => window.pdfSearch.getState())).ready;
    await application.evaluate(({ app, BrowserWindow }) => {
      const counters = globalThis as unknown as InstanceTestMain;
      counters.secondLaunches = 0; counters.focusRequests = 0;
      app.on('second-instance', () => { counters.secondLaunches++; });
      // Window managers may deny focus stealing, especially on Wayland. Check
      // that focus is requested while still calling the real native method.
      const current = BrowserWindow.getAllWindows()[0];
      const focus = current.focus.bind(current);
      current.focus = () => { counters.focusRequests++; focus(); };
    });
    const repeatLaunch = async (expected: number) => {
      // A duplicate exits before Playwright could attach to its main process,
      // so launch it directly and verify the exit and the primary's response.
      const child = spawn(application.process().spawnfile, args, { env, stdio: 'ignore' });
      duplicates.push(child);
      let launchError: Error | undefined;
      child.on('error', (error) => { launchError = error; });
      await expect.poll(() => launchError?.message ?? child.exitCode, { timeout: 10_000, message: 'A second app process must exit without opening another window or worker.' }).toBe(0);
      await expect.poll(() => application.evaluate(() => (globalThis as unknown as InstanceTestMain).secondLaunches)).toBe(expected);
      await expect.poll(() => application.evaluate(({ BrowserWindow }) => {
        const windows = BrowserWindow.getAllWindows();
        return { count: windows.length, visible: windows[0]?.isVisible(), minimized: windows[0]?.isMinimized() };
      })).toEqual({ count: 1, visible: true, minimized: false });
      await expect.poll(() => application.evaluate(() => (globalThis as unknown as InstanceTestMain).focusRequests)).toBeGreaterThanOrEqual(expected);
      expect((await page.evaluate(() => window.pdfSearch.getState())).ready).toEqual(ready);
    };
    // Exercise another launch before any library has been added, which used to
    // leave a second process holding an empty, stale registry.
    await application.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].hide());
    await repeatLaunch(1);
    for (const directory of [first, second]) {
      await application.evaluate(({ dialog }, folder) => { dialog.showOpenDialog = async () => ({ canceled: false, filePaths: [folder] }); }, directory);
      await page.getByRole('button', { name: /^(Choose folder|Add library)$/ }).click();
    }
    await page.getByRole('button', { name: 'Rename library Research', exact: true }).click();
    await page.getByRole('textbox', { name: 'Library name' }).fill('Study shelf');
    await page.getByRole('button', { name: 'Save name', exact: true }).click();
    await expect(page.getByText('Study shelf', { exact: true })).toBeVisible();
    await page.getByRole('list', { name: 'Saved libraries' }).getByText('Study shelf', { exact: true }).click();
    await page.getByRole('button', { name: 'Index folder', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Update index', exact: true })).toBeEnabled();
    const original = await page.evaluate(() => window.pdfSearch.getState());
    const profile = await application.evaluate(({ app }) => app.getPath('userData'));
    const registry = path.join(profile, 'libraries.json');
    const saved = await readFile(registry, 'utf8');
    const index = path.join(profile, 'indexes', original.activeLibraryId! + '.json');
    const savedIndex = await readFile(index, 'utf8');
    await application.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].minimize());
    await expect.poll(() => application.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].isMinimized())).toBe(true);
    await repeatLaunch(2);
    expect(await page.evaluate(() => window.pdfSearch.getState())).toEqual(original);
    expect(await readFile(registry, 'utf8')).toBe(saved);
    expect(await readFile(index, 'utf8')).toBe(savedIndex);
    await application.close(); application = await launch(); page = await application.firstWindow();
    await expect(page.getByRole('button', { name: 'Update index', exact: true })).toBeEnabled({ timeout: 60_000 });
    const restored = await page.evaluate(() => window.pdfSearch.getState());
    expect(restored.libraries).toEqual(original.libraries);
    expect(restored.activeLibraryId).toBe(original.activeLibraryId);
    expect(restored.library).toEqual(original.library);
    expect(await readFile(registry, 'utf8')).toBe(saved);
    expect(await readFile(index, 'utf8')).toBe(savedIndex);
    await page.getByRole('textbox', { name: 'Search subject' }).fill('renewable energy');
    await page.getByRole('button', { name: 'Search', exact: true }).click();
    await expect(page.getByText(/Photovoltaic panels and wind turbines/)).toBeVisible();
  } finally {
    for (const child of duplicates) {
      if (child.pid && child.exitCode === null && child.signalCode === null) {
        const exited = new Promise<void>((resolve) => child.once('exit', () => resolve()));
        child.kill('SIGTERM'); await exited;
      }
    }
    await application.close(); await rm(work, { recursive: true, force: true });
  }
});
