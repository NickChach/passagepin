import { test, expect, _electron as electron } from '@playwright/test';
import { mkdtemp, readFile, writeFile, rm, readdir } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { createLibrary, readState } from './fixture';

test('startup check refreshes annotation-only changes and prompts for changed page text', async () => {
  const work = await mkdtemp(path.join(os.tmpdir(), 'passagepin-check-'));
  const root = path.join(work, 'PDFs');
  const source = await createLibrary(root);
  const profile = path.join(work, 'profile');
  const launch = () => electron.launch({ args: [path.resolve('.')], env: { ...process.env, PDFSEARCH_E2E: '1', PDFSEARCH_E2E_DATA: profile, XDG_CACHE_HOME: path.join(work, 'cache') } });
  let app = await launch();
  try {
    await app.evaluate(({ dialog }, folder) => { dialog.showOpenDialog = async () => ({ canceled: false, filePaths: [folder] }); }, root);
    let page = await app.firstWindow();
    await expect(page.getByText('Search engine ready')).toBeVisible({ timeout: 60_000 });
    await page.getByRole('button', { name: 'Choose folder', exact: true }).click();
    await page.getByRole('button', { name: 'Index folder', exact: true }).click();
    await expect(page.getByText('2 searchable pages are ready.')).toBeVisible({ timeout: 60_000 });
    const indexPath = path.join(profile, 'indexes', (await readdir(path.join(profile, 'indexes')))[0]);
    const original = JSON.parse(await readFile(indexPath, 'utf8'));
    await app.close();
    const bytes = await readFile(source);
    await writeFile(source, Buffer.concat([bytes, Buffer.from('\n% saved highlight\n')]));
    app = await launch(); page = await app.firstWindow();
    await expect.poll(async () => (await readState(page)).check?.status).toBe('current');
    await expect.poll(async () => JSON.parse(await readFile(indexPath, 'utf8')).documents[0].sha256).not.toBe(original.documents[0].sha256);
    const annotated = JSON.parse(await readFile(indexPath, 'utf8'));
    expect(annotated.documents[0].pages).toEqual(original.documents[0].pages);
    await app.close();
    // Same byte length preserves xref offsets while changing one page's text.
    await writeFile(source, Buffer.from((await readFile(source, 'utf8')).replace('Sourdough', 'Chocolate')));
    app = await launch(); page = await app.firstWindow();
    await expect(page.getByText('Library changes found.')).toBeVisible({ timeout: 60_000 });
    expect((await readState(page)).check?.changed_pages).toBe(1);
    await page.getByRole('button', { name: 'Later', exact: true }).click();
    await expect(page.getByText('Index may be outdated.')).toBeVisible();
    await page.getByRole('textbox', { name: 'Search subject' }).fill('solar energy');
    await page.getByRole('button', { name: 'Search', exact: true }).click();
    await expect(page.getByText('For “solar energy”')).toBeVisible();
    await page.getByRole('button', { name: 'Update index', exact: true }).first().click();
    await expect.poll(async () => (await readState(page)).check?.status).toBe('current');
    const updated = JSON.parse(await readFile(indexPath, 'utf8'));
    expect(updated.documents[0].pages[0].vector).not.toEqual(original.documents[0].pages[0].vector);
    expect(updated.documents[0].pages[1].vector).toEqual(original.documents[0].pages[1].vector);
  } finally { await app.close(); await rm(work, { recursive: true, force: true }); }
});
