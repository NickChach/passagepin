import type { Progress } from './types';

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
