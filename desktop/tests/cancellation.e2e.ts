import { test, expect, _electron as electron } from '@playwright/test';
import { mkdtemp, rm } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';

test('first search and automatic and manual previews expose cancellation', async () => {
  const work = await mkdtemp(path.join(os.tmpdir(), 'passagepin-cancellation-'));
  const application = await electron.launch({
    args: [path.resolve('.')],
    env: { ...process.env, PDFSEARCH_E2E: '1', PDFSEARCH_E2E_DATA: work },
  });
  try {
    // Hold requests at the IPC boundary so cancellation can be tested without
    // relying on inference or PDF extraction taking a particular duration.
    await application.evaluate(({ ipcMain }, directory) => {
      let searches = 0;
      let stop: (() => void) | undefined;
      const library = { directory, indexed: true, documents: 1, pages: 1, empty_pages: 0, updated_at: null };
      for (const channel of ['state:get', 'library:search', 'library:page', 'worker:cancel']) ipcMain.removeHandler(channel);
      ipcMain.handle('state:get', () => ({ ready: { protocol: 1, pid: process.pid, model_loads: 1 }, error: null, operation: null, library }));
      ipcMain.handle('library:search', async (event, query: string) => {
        searches++;
        if (searches === 1) {
          event.sender.send('worker:event', { kind: 'operation', data: 'searching' });
          return new Promise((_resolve, reject) => {
            stop = () => { event.sender.send('worker:event', { kind: 'operation', data: null }); reject(new Error('context canceled')); };
          });
        }
        return { results: [{ filename: 'example.pdf', page: 1, score: .8 }], query };
      });
      ipcMain.handle('library:page', async (event) => {
        event.sender.send('worker:event', { kind: 'operation', data: 'reading' });
        return new Promise((_resolve, reject) => {
          stop = () => { event.sender.send('worker:event', { kind: 'operation', data: null }); reject(new Error('context canceled')); };
        });
      });
      ipcMain.handle('worker:cancel', () => { const cancel = stop; stop = undefined; cancel?.(); });
    }, work);
    const page = await application.firstWindow();
    await page.reload();
    await expect(page.getByRole('button', { name: 'Update index', exact: true })).toBeEnabled();
    await page.getByRole('textbox', { name: 'Search subject' }).fill('renewable energy');
    await page.getByRole('button', { name: 'Search', exact: true }).click();
    await expect(page.getByRole('region', { name: 'Search results' })).toHaveCount(0);
    await page.getByRole('button', { name: 'Cancel search', exact: true }).click();
    await expect(page.getByText('Stopped. The last saved index is still available.')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Search', exact: true })).toBeEnabled();

    await page.getByRole('button', { name: 'Search', exact: true }).click();
    await expect(page.getByRole('region', { name: 'Search results' })).toBeVisible();
    await page.getByRole('button', { name: 'Cancel reading', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Search', exact: true })).toBeEnabled();

    await page.getByRole('listitem').click();
    await page.getByRole('button', { name: 'Cancel reading', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Search', exact: true })).toBeEnabled();
    await expect(page.getByRole('button', { name: /^Cancel / })).toHaveCount(0);
  } finally {
    await application.close();
    await rm(work, { recursive: true, force: true });
  }
});
