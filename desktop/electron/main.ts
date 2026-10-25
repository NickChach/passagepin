import { app, BrowserWindow, dialog, ipcMain, shell, type IpcMainInvokeEvent } from 'electron';
import { createHash } from 'node:crypto';
import { mkdir, readFile, realpath, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { WorkerClient } from './worker-client';
import type { Library, PageText, State, WorkerEvent } from './types';

let window: BrowserWindow | null = null;
let client: WorkerClient | null = null;
let startup: Promise<void> | null = null;
let shuttingDown = false;
let stopped = false;
const state: State = { ready: null, error: null, operation: null, library: null };
const e2e = !app.isPackaged && process.env.PDFSEARCH_E2E === '1';
app.setName('PassagePin');
// Preserve the original profile, including indexes, library and Chromium theme
// storage. Display/package names can change without moving the user's data.
const profile = e2e && process.env.PDFSEARCH_E2E_DATA
  ? process.env.PDFSEARCH_E2E_DATA : path.join(app.getPath('appData'), 'pdfsearch-desktop');
app.setPath('userData', profile);
app.setPath('sessionData', profile);

function publish(event: WorkerEvent): void { if (window && !window.isDestroyed()) window.webContents.send('worker:event', event); }
function workerPath(): string {
  if (!app.isPackaged && process.env.PDFSEARCH_WORKER) return path.resolve(process.env.PDFSEARCH_WORKER);
  if (app.isPackaged) return path.join(process.resourcesPath, 'worker', process.platform === 'win32' ? 'pdfsearch.exe' : 'pdfsearch');
  return path.resolve(__dirname, '../../dist', process.platform === 'win32' ? 'pdfsearch-windows-amd64.exe' : `pdfsearch-${process.platform === 'darwin' ? 'darwin' : 'linux'}-${process.arch === 'x64' ? 'amd64' : 'arm64'}`);
}
function trusted(event: IpcMainInvokeEvent): void {
  if (!window || event.sender !== window.webContents || event.senderFrame !== window.webContents.mainFrame) throw new Error('Untrusted application frame.');
}
function text(value: unknown, name: string, maximum = 16000): string {
  if (typeof value !== 'string' || !value.trim() || Buffer.byteLength(value) > maximum) throw new Error(`Invalid ${name}.`);
  return value;
}
function integer(value: unknown, name: string, maximum: number): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 1 || value > maximum) throw new Error(`Invalid ${name}.`);
  return value;
}
async function operation<T>(name: string, action: () => Promise<T>): Promise<T> {
  await startup;
  if (!client || !state.ready || state.error) throw new Error(state.error || 'The search engine is still starting.');
  if (state.operation) throw new Error('Wait for the current operation or cancel it first.');
  state.operation = name; publish({ kind: 'operation', data: name });
  try { return await action(); }
  finally { state.operation = null; publish({ kind: 'operation', data: null }); }
}
async function openLibrary(directory: string): Promise<Library> {
  if (!client) throw new Error('Search engine is unavailable.');
  const canonical = await realpath(directory);
  const indexDir = path.join(app.getPath('userData'), 'indexes');
  await mkdir(indexDir, { recursive: true });
  const id = createHash('sha256').update(canonical).digest('hex');
  const library = await client.request<Library>('open', { directory: canonical, index_path: path.join(indexDir, id + '.json') });
  state.library = library; publish({ kind: 'library', data: library });
  await writeFile(path.join(app.getPath('userData'), 'library.json'), JSON.stringify({ directory: canonical }), { mode: 0o600 });
  return library;
}
async function startWorker(): Promise<void> {
  state.ready = null; state.error = null; publish({ kind: 'starting' });
  const next = new WorkerClient(workerPath()); client = next;
  next.on('progress', (data) => publish({ kind: 'progress', data }));
  next.on('crashed', (error: Error) => {
    if (client !== next) return;
    state.ready = null; state.error = error.message;
    publish({ kind: 'failed', data: error.message });
  });
  try {
    state.ready = await next.start(); publish({ kind: 'ready', data: state.ready });
    let previous: string | undefined;
    try { const saved = JSON.parse(await readFile(path.join(app.getPath('userData'), 'library.json'), 'utf8')) as { directory?: unknown }; if (typeof saved.directory === 'string') previous = saved.directory; } catch { /* First launch or removed preference. */ }
    if (previous) { try { await openLibrary(previous); } catch { state.library = null; } }
  } catch (error) {
    state.error = error instanceof Error ? error.message : String(error);
    publish({ kind: 'failed', data: state.error });
  }
}
function handlers(): void {
  ipcMain.handle('state:get', (event) => { trusted(event); return state; });
  ipcMain.handle('library:choose', async (event) => {
    trusted(event);
    await startup;
    if (!window || state.operation) throw new Error('Finish the current operation before changing folders.');
    let directory: string | undefined;
    if (e2e && process.env.PDFSEARCH_E2E_FOLDER) directory = process.env.PDFSEARCH_E2E_FOLDER;
    else { const result = await dialog.showOpenDialog(window, { title: 'Choose a folder of PDFs', properties: ['openDirectory'] }); directory = result.canceled ? undefined : result.filePaths[0]; }
    if (!directory) return null;
    return operation('opening', () => openLibrary(directory));
  });
  ipcMain.handle('library:index', async (event, rebuild: unknown) => {
    trusted(event); if (typeof rebuild !== 'boolean') throw new Error('Invalid rebuild option.');
    return operation('indexing', async () => {
      const result = await client!.request<{ library: Library; stats: unknown }>('index', { rebuild });
      state.library = result.library; publish({ kind: 'library', data: result.library });
      return result;
    });
  });
  ipcMain.handle('library:search', async (event, query: unknown, top: unknown, minScore: unknown) => {
    trusted(event); const subject = text(query, 'subject'); const limit = integer(top, 'result limit', 500);
    if (typeof minScore !== 'number' || !Number.isFinite(minScore) || minScore < -1 || minScore > 1) throw new Error('Invalid similarity threshold.');
    return operation('searching', () => client!.request('search', { query: subject, top: limit, min_score: minScore }));
  });
  const getPage = (filename: unknown, page: unknown) => {
    const file = text(filename, 'filename', 4096); const number = integer(page, 'page number', 1_000_000);
    return operation('reading', () => client!.request<PageText & { path: string }>('page', { filename: file, page: number }));
  };
  ipcMain.handle('library:page', async (event, filename: unknown, page: unknown) => {
    trusted(event); const result = await getPage(filename, page);
    return { filename: result.filename, page: result.page, text: result.text };
  });
  ipcMain.handle('library:open-pdf', async (event, filename: unknown, page: unknown) => {
    trusted(event); const result = await getPage(filename, page);
    const error = await shell.openPath(result.path); if (error) throw new Error(error);
  });
  ipcMain.handle('worker:cancel', async (event) => { trusted(event); await client?.cancel(); });
  ipcMain.handle('worker:restart', async (event) => {
    trusted(event); if (state.operation) throw new Error('Cancel the current operation before restarting.');
    await client?.stop(); startup = startWorker(); await startup; return state;
  });
}
function createWindow(): void {
  window = new BrowserWindow({
    width: 1280, height: 820, minWidth: 940, minHeight: 650, title: 'PassagePin',
    backgroundColor: '#edf2f6', autoHideMenuBar: true, show: false,
    icon: path.join(app.getAppPath(), 'assets/icon.png'),
    webPreferences: { preload: path.join(__dirname, 'preload.js'), contextIsolation: true, nodeIntegration: false, sandbox: true, webSecurity: true },
  });
  // Wait for the renderer to apply its saved theme before showing the window.
  window.once('ready-to-show', () => window?.show());
  window.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
  window.webContents.on('will-navigate', (event) => event.preventDefault());
  window.webContents.session.setPermissionRequestHandler((_contents, _permission, callback) => callback(false));
  void window.loadFile(path.join(__dirname, '../build/index.html'));
  window.on('closed', () => { window = null; });
}
app.whenReady().then(() => {
  handlers(); createWindow(); startup = startWorker();
  app.on('activate', () => { if (!window) createWindow(); });
}).catch((error: unknown) => { console.error(error); app.quit(); });
app.on('window-all-closed', () => { if (process.platform !== 'darwin') app.quit(); });
app.on('before-quit', (event) => {
  if (stopped) return;
  event.preventDefault(); if (shuttingDown) return; shuttingDown = true;
  void (async () => { await client?.stop(); stopped = true; app.quit(); })();
});
