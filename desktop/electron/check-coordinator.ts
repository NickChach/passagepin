import type { FileRefresh, LibraryChecker } from './library-check';
import type { LibraryCheck } from './types';

type PendingRefresh = { generation: number; files: FileRefresh[] };

// Owns check lifetimes and stale-result rejection. DesktopSession owns the
// operation guard and performs refresh writes through its serialized worker.
export class CheckCoordinator {
  private generation = 0;
  private checking: AbortController | null = null;
  private status: LibraryCheck | null = null;
  private pending: PendingRefresh | null = null;
  constructor(private readonly checker: LibraryChecker | undefined,
    private readonly publish: (data: LibraryCheck | null) => void,
    private readonly refreshReady: () => void) {}

  invalidate(): void {
    this.generation++; this.checking?.abort(); this.checking = null; this.pending = null;
    this.setStatus(null);
  }
  start(indexPath: string): void {
    if (!this.checker || this.checking || this.status) return;
    const generation = this.generation;
    const controller = new AbortController(); this.checking = controller;
    this.setStatus({ status: 'checking' });
    void this.checker(indexPath, controller.signal).then(result => {
      if (generation !== this.generation) return;
      this.checking = null;
      const changed = result.changed_pages + result.added_files + result.removed_files > 0 || result.errors.length > 0;
      this.setStatus({ status: changed ? 'changed' : 'current', changed_pages: result.changed_pages,
        added_files: result.added_files, removed_files: result.removed_files, errors: result.errors });
      if (result.refresh.length) {
        this.pending = { generation, files: result.refresh }; this.refreshReady();
      }
    }).catch((error: unknown) => {
      if (generation !== this.generation) return;
      this.checking = null;
      this.reportRefreshError(generation, error);
    });
  }
  takeRefresh(): PendingRefresh | null {
    const pending = this.pending; this.pending = null;
    return pending?.generation === this.generation ? pending : null;
  }
  reportRefreshError(generation: number, error: unknown): void {
    if (generation === this.generation)
      this.setStatus({ status: 'error', message: error instanceof Error ? error.message : String(error) });
  }
  private setStatus(status: LibraryCheck | null): void {
    this.status = status; this.publish(status);
  }
}

// Leave room for protocol framing below the worker's 1 MiB request limit.
export function* refreshBatches(files: FileRefresh[]): Generator<FileRefresh[]> {
  let batch: FileRefresh[] = []; let bytes = 0;
  for (const file of files) {
    const size = Buffer.byteLength(JSON.stringify(file)) + 1;
    if (batch.length && bytes + size > 512 * 1024) { yield batch; batch = []; bytes = 0; }
    batch.push(file); bytes += size;
  }
  if (batch.length) yield batch;
}
