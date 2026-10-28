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
      ...await page.evaluate(() => window.pdfSearch.getState()),
      activeLibraryId: 'first',
      libraries: [{ id: 'first', name: 'Research', directory: path.join(work, 'Research') }, { id: 'second', name: 'Books', directory: path.join(work, 'Books') }],
      library: { directory: path.join(work, 'Research'), indexed: true, documents: 3, pages: 884, empty_pages: 27, updated_at: '2026-10-27T12:00:00Z' },
    };
    // Hold the library data at the IPC boundary to exercise report size and
    // window geometry without depending on PDF extraction or inference timing.
    await application.evaluate(({ ipcMain, BrowserWindow }, state) => {
      ipcMain.removeHandler('state:get');
      ipcMain.handle('state:get', () => state);
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
