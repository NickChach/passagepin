import type { Progress, WorkerMethods } from './types';

export function parseProgress(data: unknown): Progress {
  if (!data || typeof data !== 'object') throw new Error('Invalid search engine progress.');
  const p = data as Record<string, unknown>;
  const positive = (n: unknown) => typeof n === 'number' && Number.isSafeInteger(n) && n > 0;
  const flags = (p.recovered === undefined || typeof p.recovered === 'boolean') &&
    (p.partial === undefined || typeof p.partial === 'boolean');
  if (p.phase === 'discovering') return { phase: 'discovering' };
  if (typeof p.filename !== 'string' || !p.filename || !flags) throw new Error('Invalid search engine progress.');
  switch (p.phase) {
    case 'embedding': case 'completed':
      if (positive(p.page) && positive(p.pages) && (p.page as number) <= (p.pages as number))
        return data as Progress;
      break;
    case 'reused':
      if (positive(p.pages)) return data as Progress;
      break;
    case 'skipped':
      if (typeof p.reason === 'string' && p.reason && (p.page === undefined || positive(p.page)))
        return data as Progress;
  }
  throw new Error('Invalid search engine progress.');
}

// JSON arrives as unknown even when request() has a typed signature. Validate
// the method's response before resolving it into application state.
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Invalid search engine response.');
  return value as Record<string, unknown>;
}
const count = (n: unknown) => typeof n === 'number' && Number.isSafeInteger(n) && n >= 0;
const nonempty = (s: unknown) => typeof s === 'string' && s.length > 0;
function failures(value: unknown): boolean {
  return Array.isArray(value) && value.every(item => {
    const f = object(item);
    return count(f.page) && Number(f.page) > 0 && nonempty(f.reason) && (f.feature === undefined || typeof f.feature === 'string');
  });
}
function issues(value: unknown): boolean {
  return value === undefined || Array.isArray(value) && value.every(item => {
    const issue = object(item);
    return nonempty(issue.filename) && typeof issue.kind === 'string' && ['recovered', 'partial', 'skipped'].includes(issue.kind) && nonempty(issue.reason)
      && (issue.page === undefined || count(issue.page)) && (issue.feature === undefined || typeof issue.feature === 'string')
      && (issue.indexed_pages === undefined || count(issue.indexed_pages)) && (issue.failures === undefined || failures(issue.failures));
  });
}
function library(value: unknown): boolean {
  const l = object(value);
  return typeof l.directory === 'string' && typeof l.indexed === 'boolean'
    && ['documents', 'pages', 'empty_pages'].every(key => count(l[key]))
    && (l.failed_pages === undefined || count(l.failed_pages))
    && (l.updated_at === null || typeof l.updated_at === 'string' && Number.isFinite(Date.parse(l.updated_at)))
    && (l.issue === undefined || typeof l.issue === 'string') && issues(l.issues);
}
export function parseWorkerResult<M extends keyof WorkerMethods>(method: M, value: unknown): WorkerMethods[M]['result'] {
  const r = object(value);
  let valid = false;
  switch (method) {
    case 'open': case 'status': valid = library(r); break;
    case 'search':
      valid = typeof r.query === 'string' && Array.isArray(r.results) && r.results.every(item => {
        const hit = object(item);
        return nonempty(hit.filename) && count(hit.page) && Number(hit.page) > 0
          && typeof hit.score === 'number' && Number.isFinite(hit.score) && hit.score >= -1 && hit.score <= 1;
      }); break;
    case 'page':
      valid = nonempty(r.filename) && count(r.page) && Number(r.page) > 0 && typeof r.text === 'string' && nonempty(r.path); break;
    case 'resolve-pdf': valid = nonempty(r.path); break;
    case 'cancel': valid = typeof r.cancelled === 'boolean'; break;
    case 'shutdown': valid = typeof r.stopped === 'boolean'; break;
    case 'index': {
      const stats = object(r.stats); const timings = object(stats.timings);
      valid = library(r.library) && ['indexed', 'reused', 'pages', 'empty_pages', 'recovered', 'skipped', 'partial', 'failed_pages'].every(key => count(stats[key]))
        && issues(stats.issues) && ['hashing_ns', 'extraction_ns', 'embedding_ns', 'total_ns', 'saving_ns'].every(key => typeof timings[key] === 'number' && Number.isFinite(timings[key]) && Number(timings[key]) >= 0);
      break;
    }
  }
  if (!valid) throw new Error(`Invalid search engine response for ${method}.`);
  return value as WorkerMethods[M]['result'];
}
