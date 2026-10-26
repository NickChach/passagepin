import type { Progress } from '../electron/types';

interface ActivePDF { filename: string; page: number; pages: number }
export interface IndexProgress {
  active: Map<string, ActivePDF>;
  completed: number;
  reused: number;
}

export const emptyIndexProgress = (): IndexProgress => ({ active: new Map(), completed: 0, reused: 0 });

export function updateIndexProgress(current: IndexProgress, event: Progress): IndexProgress {
  if (event.phase === 'discovering') return emptyIndexProgress();
  if (!event.filename) return current;
  const filename = event.filename;
  if (event.phase === 'embedding' && event.page !== undefined && event.pages !== undefined) {
    const active = new Map(current.active);
    // Updating an existing Map entry preserves its row's position.
    active.set(filename, { filename, page: Math.max(current.active.get(filename)?.page ?? 0, event.page), pages: event.pages });
    return { ...current, active };
  }
  if (event.phase === 'completed' || event.phase === 'reused') {
    const active = new Map(current.active);
    active.delete(filename);
    return {
      active,
      completed: current.completed + Number(event.phase === 'completed'),
      reused: current.reused + Number(event.phase === 'reused'),
    };
  }
  return current;
}
