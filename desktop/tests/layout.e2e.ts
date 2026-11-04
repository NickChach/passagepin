import { readState } from './fixture';
import { test, expect, _electron as electron } from '@playwright/test';
import { mkdtemp, rm } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import type { FileIssue, State } from '../electron/types';

interface LayoutTestMain { layoutTest: { setIssues(issues: FileIssue[]): void } }

test('main content and reports cannot move or scroll the library sidebar', async () => {
  const work = await mkdtemp(path.join(os.tmpdir(), 'passagepin-layout-'));
  const packaged = process.env.PDFSEARCH_TEST_PACKAGED_BINARY;
  const application = await electron.launch({
    ...(packaged ? { executablePath: packaged } : {}),
    args: packaged ? [] : [path.resolve('.')],
    env: { ...process.env, PDFSEARCH_E2E: '1', PDFSEARCH_E2E_DATA: path.join(work, 'profile'), XDG_CONFIG_HOME: path.join(work, 'config'), XDG_CACHE_HOME: path.join(work, 'cache') },
  });
  try {
    const page = await application.firstWindow();
    await expect(page.getByText('Search engine ready')).toBeVisible({ timeout: 60_000 });
    const snapshot: State = {
      ...await readState(page),
      activeLibraryId: 'first',
      libraries: [{ id: 'first', name: 'Research', directory: path.join(work, 'Research') }, { id: 'second', name: 'Books', directory: path.join(work, 'Books') }],
      library: { directory: path.join(work, 'Research'), indexed: true, documents: 3, pages: 884, empty_pages: 27, updated_at: '2026-10-27T12:00:00Z' },
    };
    // Hold the library data at the IPC boundary to exercise report size and
    // window geometry without depending on PDF extraction or inference timing.
    await application.evaluate(({ ipcMain, BrowserWindow }, state) => {
      ipcMain.removeHandler('state:get');
      const handle: typeof ipcMain.handle = (channel, listener) => ipcMain.handle(channel, async (event, ...args) => {
        try { return { ok: true, value: await listener(event, ...args) }; }
        catch (error) { return { ok: false, error: {
          code: error instanceof Error && 'code' in error ? error.code : 'operation_failed',
          message: error instanceof Error ? error.message : String(error),
        } }; }
      });
      handle('state:get', () => state);
      (globalThis as unknown as LayoutTestMain).layoutTest = {
        setIssues(issues) {
          state.library = { ...state.library!, issues };
          BrowserWindow.getAllWindows()[0].webContents.send('worker:event', { kind: 'library', data: state.library });
        },
      };
    }, snapshot);
    await page.reload();
    const sidebar = page.getByRole('complementary', { name: 'PDF library' });
    const footer = sidebar.locator('.sidebar-footer');
    const workspace = page.getByRole('main');
    const report = page.getByRole('region', { name: 'Indexing report', exact: true });
    const issues: FileIssue[] = Array.from({ length: 6 }, (_, index) => ({ filename: `Unreadable document ${index + 1}.pdf`, kind: 'skipped', page: 2, feature: 'stream-filter', reason: 'This PDF could not be read. '.repeat(20) }));
    const setIssues = (data: FileIssue[]) => application.evaluate((_electron, next) => (globalThis as unknown as LayoutTestMain).layoutTest.setIssues(next), data);
    await expect(sidebar.getByText('Research', { exact: true })).toBeVisible();

    for (const size of [{ width: 1440, height: 1000 }, { width: 940, height: 650 }]) {
      await application.evaluate(({ BrowserWindow }, next) => BrowserWindow.getAllWindows()[0].setContentSize(next.width, next.height), size);
      await setIssues([]);
      await expect(report).toHaveCount(0);
      await workspace.evaluate((element) => { element.scrollTop = 0; });
      await sidebar.getByRole('switch', { name: 'Dark mode' }).scrollIntoViewIfNeeded();
      const baseline = await footer.boundingBox();
      expect(baseline).not.toBeNull();
      const sidebarScroll = await sidebar.evaluate((element) => element.scrollTop);
      const expectStableSidebar = async () => {
        const bounds = await footer.boundingBox();
        expect(bounds).not.toBeNull();
        expect(Math.abs(bounds!.y - baseline!.y)).toBeLessThan(1);
        expect(await sidebar.evaluate((element) => element.scrollTop)).toBe(sidebarScroll);
        expect(await page.evaluate(() => document.documentElement.scrollHeight <= window.innerHeight)).toBe(true);
        await expect(sidebar.getByRole('switch', { name: 'Dark mode' })).toBeInViewport();
        await expect(sidebar.getByText('Search engine ready')).toBeInViewport();
      };
      await setIssues(issues);
      await expect(report.locator('summary')).toBeVisible();
      await expectStableSidebar();
      await report.locator('summary').click();
      await expect(report.getByText('Unreadable document 1.pdf', { exact: true })).toBeVisible();
      await expect(report.getByText('Skipped — no pages indexed (failed on page 2)', { exact: true })).toHaveCount(6);
      await expectStableSidebar();
      await workspace.evaluate((element) => { element.scrollTop = element.scrollHeight; });
      await expect.poll(() => workspace.evaluate((element) => element.scrollTop)).toBeGreaterThan(0);
      await expectStableSidebar();
      // Overflowing sidebar content must remain independently reachable too.
      await sidebar.getByRole('button', { name: 'Rename library Research', exact: true }).click();
      await expect(sidebar.getByRole('textbox', { name: 'Library name' })).toBeInViewport();
      await sidebar.getByRole('textbox', { name: 'Library name' }).press('Escape');
      await sidebar.getByRole('switch', { name: 'Dark mode' }).scrollIntoViewIfNeeded();
      await sidebar.getByRole('switch', { name: 'Dark mode' }).click();
      await page.screenshot({ path: path.resolve(`test-results/layout-report-${size.width}.png`), fullPage: true });
    }
  } finally { await application.close(); await rm(work, { recursive: true, force: true }); }
});

test('long results and page text fit the desktop reading area at minimum window size', async () => {
  const work = await mkdtemp(path.join(os.tmpdir(), 'passagepin-reader-layout-'));
  const application = await electron.launch({ args: [path.resolve('.')], env: { ...process.env,
    PDFSEARCH_E2E: '1', PDFSEARCH_E2E_DATA: path.join(work, 'profile'), XDG_CACHE_HOME: path.join(work, 'cache') } });
  try {
    const page = await application.firstWindow();
    await expect(page.getByText('Search engine ready')).toBeVisible({ timeout: 60_000 });
    const snapshot: State = { ...await readState(page), activeLibraryId: 'research',
      libraries: [{ id: 'research', name: 'Research', directory: work }],
      library: { directory: work, indexed: true, documents: 20, pages: 1000, empty_pages: 0, updated_at: null } };
    await application.evaluate(({ ipcMain }, state) => {
      const handle = (name: string, result: (...args: unknown[]) => unknown) => {
        ipcMain.removeHandler(name);
        ipcMain.handle(name, (_event, ...args) => ({ ok: true, value: result(...args) }));
      };
      handle('state:get', () => state);
      handle('library:search', query => ({ query, results: Array.from({ length: 20 }, (_, i) => ({
        filename: 'Books/A long document filename about financial history and economic crises.pdf', page: i + 1, score: .8 - i / 100,
      })) }));
      handle('library:page', (filename, page) => ({ filename, page, text: 'Evidence from financial history explains how institutions respond to crises. '.repeat(200) }));
    }, snapshot);
    await page.reload();
    await page.getByRole('textbox', { name: 'Search subject' }).fill('financial history');
    await page.getByRole('button', { name: 'Search', exact: true }).click();
    await expect(page.getByText('20 ranked pages')).toBeVisible();
    for (const size of [{ width: 1280, height: 820 }, { width: 940, height: 650 }]) {
      await application.evaluate(({ BrowserWindow }, next) => BrowserWindow.getAllWindows()[0].setContentSize(next.width, next.height), size);
      const workspace = page.getByRole('main');
      await expect.poll(() => workspace.evaluate(e => e.scrollHeight - e.clientHeight)).toBeLessThanOrEqual(1);
      const paper = page.locator('.page-paper');
      expect((await paper.boundingBox())!.height).toBeGreaterThan(250);
      expect(await paper.evaluate(e => e.scrollHeight > e.clientHeight)).toBe(true);
      expect(await page.locator('.results-list').evaluate(e => e.scrollHeight > e.clientHeight)).toBe(true);
      await expect(page.getByRole('button', { name: 'Open PDF', exact: true })).toBeInViewport();
      await page.screenshot({ path: path.resolve(`test-results/reader-${size.width}.png`) });
    }
    // Updating an existing index must keep cancellation visible even when the
    // retained reader is scrolled to the end of the workspace.
    await application.evaluate(({ BrowserWindow }) => {
      const window = BrowserWindow.getAllWindows()[0];
      window.webContents.send('worker:event', { kind: 'operation', data: 'indexing' });
      window.webContents.send('worker:event', { kind: 'progress', data: {
        phase: 'embedding', filename: 'Research.pdf', page: 1, pages: 200,
      } });
    });
    const cancel = page.getByRole('button', { name: 'Cancel indexing', exact: true });
    await expect(cancel).toBeVisible();
    await page.getByRole('main').evaluate(e => { e.scrollTop = e.scrollHeight; });
    await expect(cancel).toBeInViewport();
    await expect(page.getByRole('list', { name: 'Matching pages' })).toBeVisible();
    await page.screenshot({ path: path.resolve('test-results/reader-indexing-small.png') });
    await application.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].webContents.send('worker:event', { kind: 'operation', data: null }));
    await expect(cancel).toHaveCount(0);
    await page.getByText('About PDF page numbers', { exact: true }).click();
    await expect(page.getByText(/They may differ/)).toBeVisible();
  } finally { await application.close(); await rm(work, { recursive: true, force: true }); }
});
