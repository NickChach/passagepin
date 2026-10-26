export interface Library {
  directory: string;
  indexed: boolean;
  documents: number;
  pages: number;
  empty_pages: number;
  updated_at: string | null;
  issue?: string;
  issues?: FileIssue[];
}
export interface FileIssue { filename: string; kind: 'recovered' | 'skipped'; reason: string; page?: number }
export interface SearchResult { filename: string; page: number; score: number }
export interface PageText { filename: string; page: number; text: string }
export interface Progress { phase: string; filename?: string; page?: number; pages?: number; reason?: string; recovered?: boolean }
export interface WorkerReady { protocol: number; pid: number; model_loads: number }
export type WorkerEvent =
  | { kind: 'starting' }
  | { kind: 'ready'; data: WorkerReady }
  | { kind: 'progress'; data: Progress }
  | { kind: 'operation'; data: string | null }
  | { kind: 'library'; data: Library }
  | { kind: 'failed'; data: string };
export interface State { ready: WorkerReady | null; error: string | null; operation: string | null; library: Library | null }
export interface DesktopAPI {
  getState(): Promise<State>;
  chooseFolder(): Promise<Library | null>;
  index(rebuild: boolean): Promise<{ library: Library; stats: { indexed: number; reused: number; pages: number; empty_pages: number; recovered?: number; skipped?: number; issues?: FileIssue[] } }>;
  search(query: string, top: number, minScore: number): Promise<{ results: SearchResult[]; query: string }>;
  page(filename: string, page: number): Promise<PageText>;
  openPDF(filename: string, page: number): Promise<void>;
  cancel(): Promise<void>;
  restart(): Promise<State>;
  onEvent(listener: (event: WorkerEvent) => void): () => void;
}
