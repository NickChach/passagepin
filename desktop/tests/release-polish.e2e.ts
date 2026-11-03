import { test, expect, _electron as electron } from '@playwright/test';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { createLibrary, readState } from './fixture';

test('manual checks preserve results, rebuilds require confirmation, and help works offline', async () => {
  const work = await mkdtemp(path.join(os.tmpdir(), 'passagepin-polish-'));
  const root = path.join(work, 'PDFs');
  const source = await createLibrary(root);
  const original = await readFile(source);
  const packaged = process.env.PDFSEARCH_TEST_PACKAGED_BINARY;
  const application = await electron.launch({ ...(packaged ? { executablePath: packaged } : {}), args: packaged ? [] : [path.resolve('.')], env: { ...process.env,
    XDG_CONFIG_HOME: path.join(work, 'config'), XDG_CACHE_HOME: path.join(work, 'cache'),
    PDFSEARCH_E2E: '1', PDFSEARCH_E2E_DATA: path.join(work, 'profile') } });
  try {
    await application.evaluate(({ dialog }, folder) => { dialog.showOpenDialog = async () => ({ canceled: false, filePaths: [folder] }); }, root);
    const page = await application.firstWindow();
    await expect(page.getByText('Search engine ready')).toBeVisible({ timeout: 60_000 });
    await page.getByRole('button', { name: 'Choose folder', exact: true }).click();
    await page.getByRole('button', { name: 'Index folder', exact: true }).click();
    await expect(page.getByText('2 searchable pages are ready.', { exact: false })).toBeVisible({ timeout: 60_000 });
    await page.getByRole('textbox', { name: 'Search subject' }).fill('renewable energy');
    await page.getByRole('button', { name: 'Search', exact: true }).click();
    await expect(page.getByRole('article')).toContainText('Photovoltaic');
    const before = await readState(page);
    await page.getByRole('button', { name: 'Check for changes', exact: true }).click();
    await expect.poll(async () => (await readState(page)).check?.status).toBe('current');
    await expect(page.getByText('For “renewable energy”')).toBeVisible();
    await expect(page.getByRole('article')).toContainText('Photovoltaic');
    expect((await readState(page)).ready?.pid).toBe(before.ready?.pid);
    const result = page.getByRole('list', { name: 'Matching pages' }).getByRole('button').first();
    await result.focus(); await page.keyboard.press('Enter');
    await expect(page.getByRole('article')).toContainText('Photovoltaic');
    await page.getByRole('button', { name: 'Rebuild index…', exact: true }).click();
    await expect(page.getByRole('region', { name: 'Confirm index rebuild' })).toBeVisible();
    await page.getByRole('button', { name: 'Keep current index' }).click();
    expect((await readState(page)).library?.updated_at).toBe(before.library?.updated_at);
    await page.getByRole('button', { name: 'Rebuild index…', exact: true }).click();
    await page.getByRole('button', { name: 'Rebuild from scratch' }).click();
    await expect(page.getByText('1 PDFs indexed, 0 unchanged PDFs reused.', { exact: false })).toBeVisible({ timeout: 60_000 });
    expect(await readFile(source)).toEqual(original);
    await page.getByRole('button', { name: 'About and help' }).click();
    const about = page.getByRole('dialog', { name: 'About PassagePin' });
    const metadata = JSON.parse(await readFile('package.json', 'utf8'));
    await expect(about).toContainText(`Version ${metadata.version}`);
    await about.getByRole('button', { name: 'License notices', exact: true }).click();
    await expect(about.locator('pre')).toContainText('MIT License');
    await expect(about.locator('pre')).toContainText('ONNX Runtime');
    await page.screenshot({ path: 'test-results/about-light.png' });
    // The AppImage mount may be invisible to a sandboxed system browser. Read
    // the real bundled notices through IPC and keep the entire text in-app.
    await application.evaluate(({ shell }) => {
      shell.openPath = async () => { throw new Error('Notices must stay inside PassagePin.'); };
      shell.openExternal = async () => { throw new Error('Notices must stay inside PassagePin.'); };
    });
    const requests: string[] = [];
    page.on('request', request => requests.push(request.url()));
    const html = await readFile('node_modules/electron/dist/LICENSES.chromium.html', 'utf8');
    const expected = await page.evaluate(source => {
      const document = new DOMParser().parseFromString(source, 'text/html');
      return {
        titles: Array.from(document.querySelectorAll('.product .title'), node => node.textContent!.trim()),
        licenses: Array.from(document.querySelectorAll('.product pre'), node => node.textContent!.trim()),
        urls: Array.from(document.querySelectorAll('a[href]'), node => node.getAttribute('href')!).filter(url => url !== '#'),
      };
    }, html);
    await about.getByRole('button', { name: 'Chromium notices', exact: true }).click();
    const chromium = about.getByRole('region', { name: 'Chromium notices', exact: true }).locator('pre');
    await expect(chromium).toContainText('The Chromium Project');
    const content = await chromium.textContent();
    for (const text of [...expected.titles, ...expected.licenses, ...expected.urls]) expect(content).toContain(text);
    expect(await chromium.locator('*').count()).toBe(0);
    expect(requests).toEqual([]);
    await page.screenshot({ path: 'test-results/chromium-notices-light.png' });
    await chromium.focus();
    await page.keyboard.press('End');
    expect(await chromium.evaluate(node => node.scrollHeight > node.clientHeight && node.scrollTop > 0)).toBe(true);
    await about.getByRole('button', { name: 'License notices', exact: true }).click();
    await expect(about.getByRole('region', { name: 'License notices', exact: true }).locator('pre')).toContainText('ONNX Runtime');
    await page.keyboard.press('Escape');
    await expect(about).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'About and help' })).toBeFocused();
    const theme = page.getByRole('switch', { name: 'Dark mode' });
    if (!await theme.isChecked()) await theme.click();
    await page.getByRole('button', { name: 'About and help' }).click();
    await expect(about).toBeVisible();
    await page.screenshot({ path: 'test-results/about-dark.png' });
    await about.getByRole('button', { name: 'Close', exact: true }).click();
  } finally { await application.close(); await rm(work, { recursive: true, force: true }); }
});
