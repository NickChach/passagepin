import { test, expect, _electron as electron } from '@playwright/test';
import { copyFile, mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { createLibrary } from './fixture';

test('indexing retains readable pages and persists partial, recovered, and skipped reports', async () => {
  const work = await mkdtemp(path.join(os.tmpdir(), 'passagepin-partial-index-'));
  const root = path.join(work, 'Research library');
  const original = await createLibrary(root, true);
  const bytes = await readFile(original);
  const recovered = path.join(root, 'Recovered.pdf');
  const broken = path.join(root, 'Broken.pdf');
  const partial = path.join(root, 'Partial.pdf');
  const partialSource = await createLibrary(path.join(work, 'partial-source'), true, [1]);
  await copyFile(partialSource, partial);
  const at = bytes.lastIndexOf('startxref');
  const damaged = Buffer.concat([bytes.subarray(0, at), Buffer.from('startxref\n0\n%%EOF\n')]);
  await writeFile(recovered, damaged);
  await writeFile(broken, 'broken');
  const launch = () => electron.launch({
    args: [path.resolve('.')],
    env: { ...process.env, PDFSEARCH_E2E: '1', PDFSEARCH_E2E_DATA: path.join(work, 'app-data'), XDG_CACHE_HOME: path.join(work, 'cache') },
  });
  let app = await launch();
  try {
    await app.evaluate(({ dialog }, directory) => {
      dialog.showOpenDialog = async () => ({ canceled: false, filePaths: [directory] });
    }, root);
    let page = await app.firstWindow();
    await expect(page.getByText('Search engine ready')).toBeVisible({ timeout: 60_000 });
    await page.getByRole('button', { name: 'Choose folder', exact: true }).click();
    await page.getByRole('button', { name: 'Index folder', exact: true }).click();
    await expect(page.getByText('5 searchable pages are ready.', { exact: false })).toBeVisible({ timeout: 60_000 });
    let report = page.getByRole('region', { name: 'Indexing report', exact: true });
    await expect(report.getByText('Indexing report: 1 partially indexed, 1 skipped, 1 indexed using recovery', { exact: true })).toBeVisible();
    await report.locator(':scope > details > summary').click();
    await expect(report.getByText('Broken.pdf', { exact: true })).toBeVisible();
    await expect(report.getByText('Recovered.pdf', { exact: true })).toBeVisible();
    await expect(report.getByText('Partially indexed — 1 searchable page; 1 page failed', { exact: true })).toBeVisible();
    await report.getByText('Failed pages (1)', { exact: true }).click();
    await expect(report.getByText('Page 1: unsupported PDF stream filter /UnsupportedDecode', { exact: true })).toBeVisible();
    await expect(page.getByText('3 pages had no extractable text.', { exact: false })).toBeVisible();
    const theme = page.getByRole('switch', { name: 'Dark mode' });
    if (await theme.isChecked()) await theme.click();
    await mkdir(path.resolve('test-results'), { recursive: true });
    await page.screenshot({ path: path.resolve('test-results/index-report-light.png'), fullPage: true });
    await theme.click();
    await page.screenshot({ path: path.resolve('test-results/index-report-dark.png'), fullPage: true });
    await expect(page.getByRole('alert')).toHaveCount(0);
    await page.getByRole('textbox', { name: 'Search subject' }).fill('renewable energy sources');
    await page.getByRole('button', { name: 'Search', exact: true }).click();
    await expect(page.getByRole('article', { name: 'Selected PDF page' }).getByText('Page 3', { exact: true })).toBeVisible();
    expect(await readFile(recovered)).toEqual(damaged);
    await app.close();
    app = await launch();
    page = await app.firstWindow();
    await expect(page.getByText('Search engine ready')).toBeVisible({ timeout: 60_000 });
    report = page.getByRole('region', { name: 'Indexing report', exact: true });
    await expect(report.getByText('Indexing report: 1 partially indexed, 1 skipped, 1 indexed using recovery', { exact: true })).toBeVisible();
    // Fix the fixture ourselves: a later update must retry it and clear its issue.
    await copyFile(original, broken);
    await copyFile(original, partial);
    await page.getByRole('button', { name: 'Update index', exact: true }).click();
    await expect(page.getByText('8 searchable pages are ready.', { exact: false })).toBeVisible({ timeout: 60_000 });
    await expect(report.getByText('Indexing report: 0 skipped, 1 indexed using recovery', { exact: true })).toBeVisible();
    await report.locator(':scope > details > summary').click();
    await expect(report.getByText('Broken.pdf', { exact: true })).toHaveCount(0);
    // Changed files which become unreadable must not retain their old vectors.
    for (const file of [original, recovered, broken, partial]) await writeFile(file, 'broken');
    await page.getByRole('button', { name: 'Update index', exact: true }).click();
    await expect(page.getByText('No searchable pages were indexed.', { exact: false })).toBeVisible();
    await expect(report.getByText('Indexing report: 4 skipped, 0 indexed using recovery', { exact: true })).toBeVisible();
    await page.getByRole('textbox', { name: 'Search subject' }).fill('renewable energy');
    await expect(page.getByRole('button', { name: 'Search', exact: true })).toBeDisabled();
  } finally { await app.close(); await rm(work, { recursive: true, force: true }); }
});
