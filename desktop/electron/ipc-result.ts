export type IPCResult<T> = { ok: true; value: T } | { ok: false; error: { code: string; message: string } };

export async function ipcResult<T>(work: () => T | Promise<T>): Promise<IPCResult<T>> {
  try { return { ok: true, value: await work() }; }
  catch (error) {
    return { ok: false, error: {
      code: error instanceof Error && 'code' in error && typeof error.code === 'string' ? error.code : 'operation_failed',
      message: error instanceof Error ? error.message : String(error),
    } };
  }
}

export class DesktopError extends Error {
  constructor(message: string, public readonly code: string) { super(message); }
}

export function unwrapResult<T>(result: IPCResult<T>): T {
  if (!result.ok) throw new DesktopError(result.error.message, result.error.code);
  return result.value;
}
