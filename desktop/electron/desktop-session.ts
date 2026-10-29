import { mkdir, realpath, rm } from 'node:fs/promises';
import path from 'node:path';
import { libraryID, type LibraryStore } from './library-store';
import type { WorkerClient } from './worker-client';
import type { Library, SavedLibrary, State, WorkerEvent, WorkerMethods, Progress } from './types';

export type WorkerConnection = Pick<WorkerClient, 'start' | 'stop' | 'cancel' | 'request'> & {
 on(event: 'progress', listener: (data: Progress) => void): unknown;
 on(event: 'crashed', listener: (error: Error) => void): unknown;
};
type Registry = Pick<LibraryStore, 'load' | 'snapshot' | 'entry' | 'get' | 'select' | 'rename' | 'remove'>;

// Owns the active library and worker lifecycle independently of Electron IPC.
export class DesktopSession {
  readonly state: State = { ready: null, error: null, operation: null, library: null, libraries: [], activeLibraryId: null, libraryError: null };
  private client: WorkerConnection | null = null;
  private startup: Promise<void> | null = null;
  constructor(private readonly libraries: Registry, private readonly indexDirectory: string,
    private readonly createWorker: () => WorkerConnection, private readonly publish: (event: WorkerEvent) => void) {}
  start(): Promise<void> { this.startup = this.initialize(); return this.startup; }
  async stop(): Promise<void> { await this.client?.stop(); }
  async restart(): Promise<State> {
    await this.startup;
    if (this.state.operation) throw new Error('Cancel the current operation before restarting.');
    await this.client?.stop(); await this.start(); return this.state;
  }
  async cancel(): Promise<void> { await this.client?.cancel(); }
  async chooseFolder(pick: () => Promise<string | undefined>): Promise<Library | null> {
    return this.operation('opening', async () => {
      const directory = await pick();
      return directory ? this.openLibrary(this.libraries.entry(await realpath(directory))) : null;
    });
  }
  async selectLibrary(id: string): Promise<Library> {
    return this.operation('opening', () => this.openLibrary(this.libraries.get(id)));
  }
  async renameLibrary(id: string, name: unknown): Promise<State> {
    return this.operation('renaming', async () => {
      await this.libraries.rename(id, name); this.publishLibraries(); return { ...this.state, operation: null };
    });
  }
  async removeLibrary(id: string): Promise<State> {
    return this.operation('removing', async () => {
      const active = this.state.activeLibraryId === id;
      await this.libraries.remove(id); this.publishLibraries();
      if (active) this.setLibrary(null);
      try { await rm(path.join(this.indexDirectory, id + '.json'), { force: true }); }
      catch { throw new Error('The library was removed, but its cached index could not be deleted.'); }
      return { ...this.state, operation: null };
    });
  }
  async index(rebuild: boolean) {
    return this.operation('indexing', async () => {
      this.requireLibrary();
      const result = await this.client!.request('index', { rebuild });
      this.setLibrary(result.library); return result;
    });
  }
  async search(query: string, top: number, min_score: number) {
    return this.libraryRequest('searching', 'search', { query, top, min_score });
  }
  async page(filename: string, page: number) {
    return this.libraryRequest('reading', 'page', { filename, page });
  }
  async resolvePDF(filename: string, page: number) {
    return this.libraryRequest('opening-pdf', 'resolve-pdf', { filename, page });
  }
  private async libraryRequest<M extends 'search' | 'page' | 'resolve-pdf'>(operation: string, method: M, params: WorkerMethods[M]['params']): Promise<WorkerMethods[M]['result']> {
    return this.operation(operation, () => {
      this.requireLibrary();
      return this.client!.request(method, params);
    });
  }
  private async operation<T>(name: string, action: () => Promise<T>): Promise<T> {
    await this.startup;
    if (!this.client || !this.state.ready || this.state.error) throw new Error(this.state.error || 'The search engine is still starting.');
    if (this.state.operation) throw new Error('Wait for the current operation or cancel it first.');
    this.state.operation = name; this.publish({ kind: 'operation', data: name });
    try { return await action(); }
    finally { this.state.operation = null; this.publish({ kind: 'operation', data: null }); }
  }
  private publishLibraries(): void {
    const saved = this.libraries.snapshot();
    Object.assign(this.state, saved);
    this.publish({ kind: 'libraries', data: saved });
  }
  private setLibrary(library: Library | null, error: string | null = null): void {
    this.state.library = library; this.state.libraryError = error;
    this.publish({ kind: 'library', data: library });
    this.publish({ kind: 'library-error', data: error });
  }
  private requireLibrary(): void {
    if (!this.state.library) throw new Error('Choose an available library first.');
  }
  private async loadLibrary(entry: SavedLibrary): Promise<Library> {
    if (!this.client) throw new Error('Search engine is unavailable.');
    const canonical = await realpath(entry.directory);
    if (libraryID(canonical) !== entry.id) throw new Error('This folder now points to a different location. Add it as a new library.');
    const indexDir = this.indexDirectory;
    await mkdir(indexDir, { recursive: true });
    return this.client.request('open', { directory: canonical, index_path: path.join(indexDir, entry.id + '.json') });
  }
  private async openLibrary(entry: SavedLibrary): Promise<Library> {
    const previous = this.state.activeLibraryId && this.state.library ? this.libraries.get(this.state.activeLibraryId) : null;
    const library = await this.loadLibrary(entry);
    try { await this.libraries.select(entry); }
    catch (error) {
      // A failed preference write must not leave the worker searching a different
      // folder from the one displayed by the renderer.
      if (previous) {
        try { this.setLibrary(await this.loadLibrary(previous)); }
        catch { this.setLibrary(null, 'Could not restore the previous library. Select it again to retry.'); }
      }
      throw error;
    }
    this.publishLibraries(); this.setLibrary(library);
    return library;
  }
  private async initialize(): Promise<void> {
    this.state.ready = null; this.state.error = null; this.setLibrary(null); this.publish({ kind: 'starting' });
    const next = this.createWorker(); this.client = next;
    next.on('progress', (data) => this.publish({ kind: 'progress', data }));
    next.on('crashed', (error: Error) => {
      if (this.client !== next) return;
      this.state.ready = null; this.state.error = error.message;
      this.publish({ kind: 'failed', data: error.message });
    });
    try {
      await this.libraries.load(); this.publishLibraries();
      this.state.ready = await next.start();
      if (this.state.activeLibraryId) {
        const previous = this.libraries.get(this.state.activeLibraryId);
        try { this.setLibrary(await this.loadLibrary(previous)); }
        catch { this.setLibrary(null, `Could not open “${previous.name}”. Reconnect its folder and select it to retry, or choose another library.`); }
      }
      if (!this.state.ready || this.state.error) throw new Error(this.state.error || 'The search engine stopped while opening the library.');
      this.publish({ kind: 'ready', data: this.state.ready });
    } catch (error) {
      this.state.ready = null;
      this.state.error = error instanceof Error ? error.message : String(error);
      this.publish({ kind: 'failed', data: this.state.error });
    }
  }
}
