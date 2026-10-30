import { app, BrowserWindow, dialog, ipcMain, shell, type IpcMainInvokeEvent } from 'electron';
import path from 'node:path';
import { ipcResult } from './ipc-result';
import { WorkerClient } from './worker-client';
import { LibraryStore } from './library-store';
import { checkLibrary } from './library-check';
import { DesktopSession } from './desktop-session';
import type { WorkerEvent } from './types';

let window: BrowserWindow | null = null;
let shuttingDown = false;
let stopped = false;

const e2e = !app.isPackaged && process.env.PDFSEARCH_E2E === '1';
app.setName('PassagePin');
// Preserve the original profile, including indexes, library and Chromium theme
// storage. Display/package names can change without moving the user's data.
const profile = e2e && process.env.PDFSEARCH_E2E_DATA
  ? process.env.PDFSEARCH_E2E_DATA : path.join(app.getPath('appData'), 'pdfsearch-desktop');
app.setPath('userData', profile);
app.setPath('sessionData', profile);
const session = new DesktopSession(new LibraryStore(profile), path.join(profile, 'indexes'),
  () => new WorkerClient(workerPath()), publish, (indexPath, signal) => checkLibrary(workerPath(), indexPath, signal));

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
function handlers(): void {
  const handle = (channel: string, listener: Parameters<typeof ipcMain.handle>[1]) =>
    ipcMain.handle(channel, (event, ...args) => ipcResult(() => listener(event, ...args)));
  handle('state:get', (event) => { trusted(event); return session.state; });
  handle('library:choose', async (event) => {
    trusted(event);
    return session.chooseFolder(async () => {
      if (!window) throw new Error('Application window is unavailable.');
      if (e2e && process.env.PDFSEARCH_E2E_FOLDER) return process.env.PDFSEARCH_E2E_FOLDER;
      const result = await dialog.showOpenDialog(window, { title: 'Choose a folder of PDFs', properties: ['openDirectory'] });
      return result.canceled ? undefined : result.filePaths[0];
    });
  });
  handle('library:select', async (event, id: unknown) => {
    trusted(event); const key = text(id, 'library ID', 64);
    return session.selectLibrary(key);
  });
  handle('library:rename', async (event, id: unknown, name: unknown) => {
    trusted(event); const key = text(id, 'library ID', 64);
    return session.renameLibrary(key, name);
  });
  handle('library:remove', async (event, id: unknown) => {
    trusted(event); const key = text(id, 'library ID', 64);
    return session.removeLibrary(key);
  });
  handle('library:index', async (event, rebuild: unknown) => {
    trusted(event); if (typeof rebuild !== 'boolean') throw new Error('Invalid rebuild option.');
    return session.index(rebuild);
  });
  handle('library:search', async (event, query: unknown, top: unknown, minScore: unknown) => {
    trusted(event); const subject = text(query, 'subject'); const limit = integer(top, 'result limit', 500);
    if (typeof minScore !== 'number' || !Number.isFinite(minScore) || minScore < -1 || minScore > 1) throw new Error('Invalid similarity threshold.');
    return session.search(subject, limit, minScore);
  });
  handle('library:page', async (event, filename: unknown, page: unknown) => {
    trusted(event); const result = await session.page(text(filename, 'filename', 4096), integer(page, 'page number', 1_000_000));
    return { filename: result.filename, page: result.page, text: result.text };
  });
  handle('library:open-pdf', async (event, filename: unknown, page: unknown) => {
    trusted(event); const result = await session.resolvePDF(text(filename, 'filename', 4096), integer(page, 'page number', 1_000_000));
    const error = await shell.openPath(result.path); if (error) throw new Error(error);
  });
  handle('worker:cancel', async (event) => { trusted(event); await session.cancel(); });
  handle('worker:restart', async (event) => {
    trusted(event); return session.restart();
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
// Claim the profile before loading libraries or starting a window/worker. Atomic
// registry saves alone cannot prevent separate processes overwriting each other.
if (!app.requestSingleInstanceLock()) {
  app.quit();
} else {
  app.on('second-instance', () => {
    if (!window || window.isDestroyed()) { createWindow(); return; }
    const current = window;
    const focus = () => { current.show(); current.focus(); };
    if (current.isMinimized()) { current.once('restore', focus); current.restore(); }
    else focus();
  });
  app.whenReady().then(() => {
    handlers(); if (!window) createWindow(); void session.start();
    app.on('activate', () => { if (!window) createWindow(); });
  }).catch((error: unknown) => { console.error(error); app.quit(); });
  app.on('window-all-closed', () => { if (process.platform !== 'darwin') app.quit(); });
  app.on('before-quit', (event) => {
    if (stopped) return;
    event.preventDefault(); if (shuttingDown) return; shuttingDown = true;
    void (async () => { await session.stop(); stopped = true; app.quit(); })();
  });
}
