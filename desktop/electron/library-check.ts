import { execFile } from 'node:child_process';

export interface FileRefresh { filename: string; before: string; after: string }
export interface CheckResult {
  changed_pages: number; added_files: number; removed_files: number;
  errors: string[]; refresh: FileRefresh[];
}
export type LibraryChecker = (indexPath: string, signal: AbortSignal) => Promise<CheckResult>;

export function checkLibrary(binary: string, indexPath: string, signal: AbortSignal): Promise<CheckResult> {
  return new Promise((resolve, reject) => {
    execFile(binary, ['check-index', '--index', indexPath], { signal, maxBuffer: 8 * 1024 * 1024, windowsHide: true }, (error, stdout, stderr) => {
      if (error) { reject(new Error(stderr.trim() || "The library check could not finish. Select the library again to retry.")); return; }
      try {
        const r = JSON.parse(stdout) as CheckResult;
        if (!r || !['changed_pages', 'added_files', 'removed_files'].every(key => Number.isSafeInteger(r[key as keyof CheckResult]) && Number(r[key as keyof CheckResult]) >= 0)
          || !Array.isArray(r.errors) || !r.errors.every(e => typeof e === 'string')
          || !Array.isArray(r.refresh) || !r.refresh.every(f => f && typeof f.filename === 'string' && /^[a-f0-9]{64}$/.test(f.before) && /^[a-f0-9]{64}$/.test(f.after)))
          throw new Error('Invalid library check result.');
        resolve(r);
      } catch (e) { reject(e); }
    });
  });
}
