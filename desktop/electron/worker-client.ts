import { spawn, type ChildProcessWithoutNullStreams, type SpawnOptionsWithoutStdio } from 'node:child_process';
import { createInterface } from 'node:readline';
import { EventEmitter } from 'node:events';
import { parseProgress } from './worker-protocol';
import type { WorkerReady, WorkerMethods } from './types';

interface Reply { id?: string; event?: string; data?: unknown; result?: unknown; error?: { code: string; message: string } }
interface Pending { resolve(value: unknown): void; reject(error: Error): void }
type WorkerLauncher = (binary: string, args: string[], options: SpawnOptionsWithoutStdio) => ChildProcessWithoutNullStreams;

export class WorkerError extends Error {
  constructor(message: string, public readonly code: string) { super(message); }
}

export class WorkerClient extends EventEmitter {
  private child: ChildProcessWithoutNullStreams | null = null;
  private pending = new Map<string, Pending>();
  private sequence = 0;
  private activeID: string | null = null;
  private readyPromise: Promise<WorkerReady> | null = null;
  private stopping = false;
  private stderr = '';
  readonly binary: string;
  constructor(binary: string, private readonly args: string[] = ['worker', '--threads', '1'], private readonly launch: WorkerLauncher = spawn) { super(); this.binary = binary; }

  start(): Promise<WorkerReady> {
    if (this.readyPromise) return this.readyPromise;
    this.stopping = false;
    this.readyPromise = new Promise((resolve, reject) => {
      const child = this.launch(this.binary, this.args, { stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true, shell: false });
      this.child = child;
      let initialized = false;
      const timer = setTimeout(() => { child.kill(); reject(new Error('The search engine did not start within 90 seconds.')); }, 90_000);
      child.stderr.on('data', (chunk: Buffer) => { this.stderr = (this.stderr + chunk.toString()).slice(-16_000); });
      child.stdin.on('error', (error) => this.fail(error));
      const lines = createInterface({ input: child.stdout });
      lines.on('line', (line) => {
        try {
          if (line.length > 32 * 1024 * 1024) throw new Error('Search engine response is too large.');
          const message = JSON.parse(line) as Reply;
          if (message.event === 'ready') {
            const ready = message.data as WorkerReady;
            if (ready.protocol !== 1 || !Number.isSafeInteger(ready.pid)) throw new Error('Incompatible search engine protocol.');
            if (initialized) throw new Error('Search engine initialized twice.');
            initialized = true; clearTimeout(timer); this.emit('ready', ready); resolve(ready); return;
          }
          if (message.event === 'progress') { this.emit('progress', parseProgress(message.data)); return; }
          if (!message.id) return;
          const pending = this.pending.get(message.id);
          if (!pending) return;
          this.pending.delete(message.id);
          if (this.activeID === message.id) this.activeID = null;
          if (message.error) pending.reject(new WorkerError(message.error.message, message.error.code));
          else pending.resolve(message.result);
        } catch (error) { child.kill(); this.fail(error instanceof Error ? error : new Error(String(error))); }
      });
      child.once('error', (error) => { clearTimeout(timer); reject(error); this.fail(error); });
      child.once('exit', (code, signal) => {
        clearTimeout(timer); lines.close(); this.child = null;
        const detail = this.stderr.trim();
        const error = new Error(`Search engine stopped (${signal ?? code}).${detail ? ` ${detail}` : ''}`);
        if (!initialized) reject(error);
        this.fail(error);
        if (!this.stopping) this.emit('crashed', error);
      });
    });
    return this.readyPromise;
  }

  async request<M extends keyof WorkerMethods>(method: M, ...args: {} extends WorkerMethods[M]['params'] ? [params?: WorkerMethods[M]['params']] : [params: WorkerMethods[M]['params']]): Promise<WorkerMethods[M]['result']> {
    await this.start();
    const child = this.child;
    if (!child || child.stdin.destroyed) throw new Error('The search engine is not running. Restart it to continue.');
    const id = String(++this.sequence);
    const params = args[0] ?? {};
    return new Promise<WorkerMethods[M]['result']>((resolve, reject) => {
      this.pending.set(id, { resolve: (value) => resolve(value as WorkerMethods[M]['result']), reject });
      if (method === 'index' || method === 'search' || method === 'page') this.activeID = id;
      child.stdin.write(JSON.stringify({ id, method, params }) + '\n', (error) => {
        if (error) { this.pending.delete(id); if (this.activeID === id) this.activeID = null; reject(error); }
      });
    });
  }

  async cancel(): Promise<void> {
    if (this.activeID) await this.request('cancel', { target: this.activeID });
  }

  async stop(): Promise<void> {
    this.stopping = true;
    const child = this.child;
    if (!child) return;
    const exited = new Promise<void>((resolve) => child.once('exit', () => resolve()));
    // EOF cancels an active request, waits for it, and releases ONNX cleanly.
    child.stdin.end();
    const timer = setTimeout(() => child.kill('SIGKILL'), 5000);
    await exited; clearTimeout(timer);
  }

  private fail(error: Error): void {
    for (const request of this.pending.values()) request.reject(error);
    this.pending.clear(); this.activeID = null;
  }
}
