import { test, expect, _electron as electron } from '@playwright/test';
import { mkdtemp, mkdir, rm } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import type { Progress } from '../electron/types';

interface IndexTestMain {
  indexProgressTest: { emit(events: Progress[]): void; finish(error?: string): void };
}

test('concurrent PDFs keep separate progress rows through completion, cancellation and failure', async () => {
  const work = await mkdtemp(path.join(os.tmpdir(), 'passagepin-index-progress-'));
  const application = await electron.launch({
    args: [path.resolve('.')],
    env: { ...process.env, PDFSEARCH_E2E: '1', PDFSEARCH_E2E_DATA: work },
  });
  try {
    const page = await application.firstWindow();
    await expect(page.getByText('Search engine ready')).toBeVisible({ timeout: 60_000 });
    // Control event ordering at the IPC boundary so overlap and terminal events
    // are deterministic, without depending on how quickly the CPU indexes PDFs.
    await application.evaluate(({ ipcMain, BrowserWindow }, directory) => {
      let library = { directory, indexed: false, documents: 0, pages: 0, empty_pages: 0, updated_at: null };
      let pending: { resolve(value: unknown): void; reject(error: Error): void } | undefined;
      const publish = (event: unknown) => BrowserWindow.getAllWindows()[0].webContents.send('worker:event', event);
      const controls = {
        emit(events: Progress[]) { for (const data of events) publish({ kind: 'progress', data }); },
        finish(error?: string) {
          if (!pending) throw new Error('No index operation is pending');
          const job = pending; pending = undefined;
          publish({ kind: 'operation', data: null });
          if (error) { job.reject(new Error(error)); return; }
          library = { ...library, indexed: true, documents: 3, pages: 30 };
          publish({ kind: 'library', data: library });
          job.resolve({ library, stats: { indexed: 3, reused: 2, pages: 30, empty_pages: 0 } });
        },
      };
      (globalThis as unknown as IndexTestMain).indexProgressTest = controls;
      for (const name of ['state:get', 'library:index', 'worker:cancel']) ipcMain.removeHandler(name);
      ipcMain.handle('state:get', () => ({ ready: { protocol: 1, pid: process.pid, model_loads: 1 }, error: null, operation: null, library }));
      ipcMain.handle('library:index', () => {
        publish({ kind: 'operation', data: 'indexing' });
        return new Promise((resolve, reject) => { pending = { resolve, reject }; });
      });
      ipcMain.handle('worker:cancel', () => controls.finish('context canceled'));
    }, work);
    await page.reload();
    const emit = (events: Progress[]) => application.evaluate((_electron, data) => (globalThis as unknown as IndexTestMain).indexProgressTest.emit(data), events);
    const finish = (error?: string) => application.evaluate((_electron, detail) => (globalThis as unknown as IndexTestMain).indexProgressTest.finish(detail), error);
    const panel = page.getByRole('region', { name: 'Indexing progress', exact: true });
    const files = panel.getByRole('list', { name: 'PDFs being indexed' });
    const first = 'Books/Long filenames about renewable energy and climate research.pdf';
    const second = 'Papers/Long filenames about renewable energy and climate research.pdf';
    const third = 'Reports/wind.pdf';
    const bar = (filename: string) => panel.getByRole('progressbar', { name: `Indexing ${filename}`, exact: true });

    await page.getByRole('button', { name: 'Index folder', exact: true }).click();
    await expect(panel.getByText('Reading your PDFs…')).toBeVisible();
    await emit([
      { phase: 'discovering' },
      { phase: 'embedding', filename: first, page: 1, pages: 10 },
      { phase: 'embedding', filename: second, page: 1, pages: 12 },
    ]);
    await expect(files.locator('.index-file-name')).toHaveText([first, second]);
    await emit([{ phase: 'embedding', filename: first, page: 2, pages: 10 }]);
    await expect(bar(first)).toHaveAttribute('value', '2');
    await expect(bar(second)).toHaveAttribute('value', '1');
    await emit([{ phase: 'embedding', filename: second, page: 3, pages: 12 }]);
    await expect(bar(first)).toHaveAttribute('value', '2');
    await expect(bar(second)).toHaveAttribute('value', '3');
    await expect(files.locator('.index-file-name')).toHaveText([first, second]);

    const theme = page.getByRole('switch', { name: 'Dark mode' });
    if (await theme.isChecked()) await theme.click();
    await mkdir(path.resolve('test-results'), { recursive: true });
    await page.screenshot({ path: path.resolve('test-results/indexing-progress-light.png'), fullPage: true });
    await theme.click();
    await page.screenshot({ path: path.resolve('test-results/indexing-progress-dark.png'), fullPage: true });

    // A last-page event is not completion: inference may still be running.
    await emit([{ phase: 'embedding', filename: first, page: 10, pages: 10 }]);
    await expect(bar(first)).toHaveAttribute('value', '10');
    await expect(files.getByRole('listitem')).toHaveCount(2);
    await emit([{ phase: 'completed', filename: first, page: 10, pages: 10 }]);
    await expect(bar(first)).toHaveCount(0);
    await expect(bar(second)).toHaveAttribute('value', '3');
    await emit([
      { phase: 'embedding', filename: third, page: 1, pages: 8 },
      { phase: 'reused', filename: 'unchanged-1.pdf', pages: 3 },
      { phase: 'reused', filename: 'unchanged-2.pdf', pages: 4 },
    ]);
    await expect(files.locator('.index-file-name')).toHaveText([second, third]);
    await expect(panel.getByText('1 processed, 2 unchanged PDFs reused')).toBeVisible();
    await emit([{ phase: 'skipped', filename: third, page: 2, reason: 'Unreadable page' }]);
    await expect(bar(third)).toHaveCount(0);
    await expect(bar(second)).toHaveAttribute('value', '3');
    await expect(panel.getByText('1 processed, 2 unchanged PDFs reused, 1 skipped')).toBeVisible();
    await emit([
      { phase: 'completed', filename: second, page: 12, pages: 12 },
    ]);
    await expect(files).toHaveCount(0);
    await expect(panel.getByText('Updating your library…')).toBeVisible();
    await finish();
    await expect(panel).toHaveCount(0);

    await page.getByRole('button', { name: 'Update index', exact: true }).click();
    await emit([{ phase: 'embedding', filename: first, page: 1, pages: 10 }]);
    await expect(files.locator('.index-file-name')).toHaveText([first]);
    await expect(panel.locator('.index-progress-summary')).toHaveCount(0);
    await page.getByRole('button', { name: 'Cancel indexing', exact: true }).click();
    await expect(panel).toHaveCount(0);
    await expect(page.getByText('Stopped. The last saved index is still available.')).toBeVisible();

    await page.getByRole('button', { name: 'Update index', exact: true }).click();
    await emit([{ phase: 'embedding', filename: second, page: 1, pages: 12 }]);
    await expect(files.locator('.index-file-name')).toHaveText([second]);
    await finish('PDF extraction failed');
    await expect(panel).toHaveCount(0);
    await expect(page.getByRole('alert')).toContainText('PDF extraction failed');

    await page.getByRole('button', { name: 'Update index', exact: true }).click();
    await emit([{ phase: 'embedding', filename: third, page: 1, pages: 8 }]);
    await expect(files.locator('.index-file-name')).toHaveText([third]);
    await expect(panel.locator('.index-progress-summary')).toHaveCount(0);
    await finish();
    await expect(panel).toHaveCount(0);
  } finally {
    await application.close();
    await rm(work, { recursive: true, force: true });
  }
});
