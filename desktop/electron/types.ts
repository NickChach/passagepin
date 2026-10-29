export interface Library {
  directory: string;
  indexed: boolean;
  documents: number;
  pages: number;
  empty_pages: number;
  failed_pages?: number;
  updated_at: string | null;
  issue?: string;
  issues?: FileIssue[];
}
export interface SavedLibrary { id: string; name: string; directory: string }
export interface SavedLibraries { libraries: SavedLibrary[]; activeLibraryId: string | null }
export interface PageFailure { page: number; reason: string; feature?: string }
export interface FileIssue { filename: string; kind: 'recovered' | 'partial' | 'skipped'; reason: string; page?: number; feature?: string; indexed_pages?: number; failures?: PageFailure[] }
export interface SearchResult { filename: string; page: number; score: number }
export interface PageText { filename: string; page: number; text: string }
export type Progress =
  | { phase: 'discovering'; filename?: never; page?: never; pages?: never; reason?: never; recovered?: never; partial?: never }
  | { phase: 'embedding' | 'completed'; filename: string; page: number; pages: number; reason?: never; recovered?: boolean; partial?: boolean }
  | { phase: 'reused'; filename: string; pages: number; page?: never; reason?: never; recovered?: boolean; partial?: boolean }
  | { phase: 'skipped'; filename: string; reason: string; page?: number; pages?: never; recovered?: never; partial?: never };
export interface WorkerReady { protocol: number; pid: number; model_loads: number }
export type WorkerEvent =
  | { kind: 'starting' }
  | { kind: 'ready'; data: WorkerReady }
  | { kind: 'progress'; data: Progress }
  | { kind: 'operation'; data: string | null }
  | { kind: 'library'; data: Library | null }
  | { kind: 'libraries'; data: SavedLibraries }
  | { kind: 'library-error'; data: string | null }
  | { kind: 'failed'; data: string };
export interface State extends SavedLibraries { ready: WorkerReady | null; error: string | null; operation: string | null; library: Library | null; libraryError: string | null }
export interface DesktopAPI {
  getState(): Promise<State>;
  chooseFolder(): Promise<Library | null>;
  selectLibrary(id: string): Promise<Library>;
  renameLibrary(id: string, name: string): Promise<State>;
  removeLibrary(id: string): Promise<State>;
  index(rebuild: boolean): Promise<IndexResult>;
  search(query: string, top: number, minScore: number): Promise<SearchResponse>;
  page(filename: string, page: number): Promise<PageText>;
  openPDF(filename: string, page: number): Promise<void>;
  cancel(): Promise<void>;
  restart(): Promise<State>;
  onEvent(listener: (event: WorkerEvent) => void): () => void;
}

export interface IndexStats {
  indexed: number; reused: number; pages: number; empty_pages: number;
  recovered: number; skipped: number; partial: number; failed_pages: number;
  issues?: FileIssue[];
  timings: { hashing_ns: number; extraction_ns: number; embedding_ns: number; total_ns: number; saving_ns: number };
}
export interface IndexResult { library: Library; stats: IndexStats }
export interface SearchResponse { results: SearchResult[]; query: string }
export interface WorkerMethods {
  open: { params: { directory: string; index_path: string }; result: Library };
  status: { params: Record<string, never>; result: Library };
  index: { params: { rebuild?: boolean }; result: IndexResult };
  search: { params: { query: string; top?: number; min_score?: number }; result: SearchResponse };
  'resolve-pdf': { params: { filename: string; page: number }; result: { path: string } };
  page: { params: { filename: string; page: number }; result: PageText & { path: string } };
  cancel: { params: { target: string }; result: { cancelled: boolean } };
  shutdown: { params: Record<string, never>; result: { stopped: boolean } };
}
export type DesktopTransport = {
  [K in keyof DesktopAPI]: DesktopAPI[K] extends (...args: infer A) => Promise<infer R>
    ? (...args: A) => Promise<import('./ipc-result').IPCResult<R>> : DesktopAPI[K];
};
