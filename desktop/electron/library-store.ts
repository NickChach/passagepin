import { createHash, randomUUID } from 'node:crypto';
import { mkdir, readFile, realpath, rename, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import type { SavedLibrary, SavedLibraries } from './types';

export function libraryID(directory: string): string {
  return createHash('sha256').update(directory).digest('hex');
}

function libraryName(value: unknown): string {
  if (typeof value !== 'string' || !value.trim() || value.trim().length > 120 || /[\u0000-\u001f\u007f]/.test(value)) {
    throw new Error('Enter a library name between 1 and 120 characters.');
  }
  return value.trim();
}

function missing(error: unknown): boolean { return (error as NodeJS.ErrnoException).code === 'ENOENT'; }

// The registry owns names and selection; the existing path-derived index files
// remain compatible. Only commit in-memory changes after the atomic save succeeds.
export class LibraryStore {
  private data: SavedLibraries = { libraries: [], activeLibraryId: null };
  constructor(private readonly profile: string) {}

  snapshot(): SavedLibraries {
    return { libraries: this.data.libraries.map((entry) => ({ ...entry })), activeLibraryId: this.data.activeLibraryId };
  }

  async load(): Promise<void> {
    let raw: unknown;
    try { raw = JSON.parse(await readFile(path.join(this.profile, 'libraries.json'), 'utf8')); }
    catch (error) {
      if (!missing(error)) throw new Error('Could not read saved libraries. Check libraries.json in the app data folder.', { cause: error });
      // Migrate only when no registry exists, so an intentionally empty list
      // cannot resurrect the legacy selection after the last library is removed.
      let legacy: { directory?: unknown };
      try { legacy = JSON.parse(await readFile(path.join(this.profile, 'library.json'), 'utf8')); }
      catch (error) { if (missing(error) || error instanceof SyntaxError) return; throw error; }
      if (typeof legacy?.directory !== 'string' || !path.isAbsolute(legacy.directory)) return;
      let directory = path.resolve(legacy.directory);
      try { directory = await realpath(directory); } catch { /* Retain disconnected or inaccessible folders for retry/removal. */ }
      const entry = this.entry(directory);
      await this.select(entry);
      return;
    }
    const saved = raw as { version?: unknown; libraries?: unknown; activeLibraryId?: unknown } | null;
    if (!saved || saved.version !== 1 || !Array.isArray(saved.libraries)) throw new Error('Invalid saved libraries file.');
    const ids = new Set<string>();
    const libraries: SavedLibrary[] = saved.libraries.map((entry: SavedLibrary) => {
      if (!entry || typeof entry.directory !== 'string' || !path.isAbsolute(entry.directory)
        || entry.id !== libraryID(entry.directory) || ids.has(entry.id)) throw new Error('Invalid saved library.');
      ids.add(entry.id);
      return { id: entry.id, directory: entry.directory, name: libraryName(entry.name) };
    });
    if (saved.activeLibraryId !== null && (typeof saved.activeLibraryId !== 'string' || !ids.has(saved.activeLibraryId))) throw new Error('Invalid active library.');
    this.data = { libraries, activeLibraryId: saved.activeLibraryId as string | null };
  }

  get(id: string): SavedLibrary {
    const entry = this.data.libraries.find((item) => item.id === id);
    if (!entry) throw new Error('This library is no longer saved.');
    return { ...entry };
  }

  entry(canonical: string): SavedLibrary {
    const id = libraryID(canonical);
    return this.data.libraries.find((entry) => entry.id === id)
      ?? { id, directory: canonical, name: (path.basename(canonical).replace(/[\u0000-\u001f\u007f]/g, '').trim() || 'PDF library').slice(0, 120) };
  }

  async select(entry: SavedLibrary): Promise<void> {
    const libraries = this.data.libraries.some((item) => item.id === entry.id)
      ? this.data.libraries : [...this.data.libraries, entry];
    await this.save({ libraries, activeLibraryId: entry.id });
  }

  async rename(id: string, name: unknown): Promise<void> {
    this.get(id);
    const nextName = libraryName(name);
    await this.save({ ...this.data, libraries: this.data.libraries.map((entry) => entry.id === id ? { ...entry, name: nextName } : entry) });
  }

  async remove(id: string): Promise<void> {
    this.get(id);
    await this.save({ libraries: this.data.libraries.filter((entry) => entry.id !== id), activeLibraryId: this.data.activeLibraryId === id ? null : this.data.activeLibraryId });
  }

  private async save(next: SavedLibraries): Promise<void> {
    await mkdir(this.profile, { recursive: true });
    const temporary = path.join(this.profile, `libraries.${randomUUID()}.tmp`);
    try {
      await writeFile(temporary, JSON.stringify({ version: 1, ...next }), { mode: 0o600, flag: 'wx' });
      await rename(temporary, path.join(this.profile, 'libraries.json'));
      this.data = next;
    } finally { await rm(temporary, { force: true }); }
  }
}
