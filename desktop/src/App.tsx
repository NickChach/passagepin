import { useEffect, useState, type FormEvent, type ReactNode } from 'react';
import type { PageText, SavedLibrary, SearchResult, State } from '../electron/types';
import { saveTheme, type Theme } from './theme';
import { emptyIndexProgress, updateIndexProgress } from './index-progress';

function Icon({ name, size = 20 }: { name: 'folder' | 'search' | 'document' | 'refresh' | 'arrow' | 'lock' | 'moon'; size?: number }) {
  const paths: Record<string, ReactNode> = {
    folder: <path d="M3 7V5a1 1 0 0 1 1-1h5l2 3h9a1 1 0 0 1 1 1v11a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V7Z" />,
    search: <><circle cx="10.5" cy="10.5" r="6.5" /><path d="m16 16 5 5" /></>,
    document: <><path d="M6 3h8l4 4v14H6V3Z" /><path d="M14 3v5h4M9 12h6M9 16h6" /></>,
    refresh: <><path d="M20 7v5h-5M4 17v-5h5" /><path d="M5.2 8a7 7 0 0 1 11.4-3L20 9M4 15l3.4 4A7 7 0 0 0 18.8 16" /></>,
    arrow: <><path d="M5 19 19 5M7 5h12v12" /></>,
    lock: <><rect x="5" y="10" width="14" height="11" rx="2" /><path d="M8 10V6a4 4 0 0 1 8 0v4" /></>,
    moon: <path d="M20.5 13A8.5 8.5 0 0 1 11 3.5 8.5 8.5 0 1 0 20.5 13Z" />,
  };
  return <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{paths[name]}</svg>;
}
const initial: State = { ready: null, error: null, operation: null, library: null, libraries: [], activeLibraryId: null, libraryError: null };
const basename = (path: string) => path.split(/[\\/]/).filter(Boolean).at(-1) || path;
const message = (error: unknown) => error instanceof Error ? error.message.replace(/^Error invoking remote method '[^']+': (?:Error: )?/, '') : String(error);

export default function App() {
  const [theme, setTheme] = useState<Theme>(() => document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light');
  const [state, setState] = useState<State>(initial);
  const [action, setAction] = useState<string | null>(null);
  const [query, setQuery] = useState('');
  const [lastQuery, setLastQuery] = useState('');
  const [results, setResults] = useState<SearchResult[]>([]);
  const [selected, setSelected] = useState<SearchResult | null>(null);
  const [preview, setPreview] = useState<PageText | null>(null);
  const [progress, setProgress] = useState(emptyIndexProgress);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [top, setTop] = useState(20);
  const [minimum, setMinimum] = useState(0);
  const [editing, setEditing] = useState<SavedLibrary | null>(null);
  const [nameDraft, setNameDraft] = useState('');
  const [removing, setRemoving] = useState<SavedLibrary | null>(null);
  const busy = action !== null || state.operation !== null;
  const ready = state.ready !== null && !state.error;
  const library = state.library;

  function toggleTheme() {
    const next = theme === 'dark' ? 'light' : 'dark';
    saveTheme(next); setTheme(next);
  }

  useEffect(() => {
    if (!window.pdfSearch) { setError('Open PassagePin as a desktop application.'); return; }
    const unsubscribe = window.pdfSearch.onEvent((event) => {
      if (event.kind === 'ready') setState((s) => ({ ...s, ready: event.data, error: null }));
      if (event.kind === 'starting') { setState((s) => ({ ...s, ready: null, error: null })); setProgress(emptyIndexProgress()); }
      if (event.kind === 'failed') { setState((s) => ({ ...s, ready: null, error: event.data })); setProgress(emptyIndexProgress()); }
      if (event.kind === 'operation') { setState((s) => ({ ...s, operation: event.data })); setProgress(emptyIndexProgress()); }
      if (event.kind === 'library') setState((s) => ({ ...s, library: event.data }));
      if (event.kind === 'libraries') {
        setState((s) => ({ ...s, ...event.data }));
        setRemoving((current) => current && event.data.libraries.some((entry) => entry.id === current.id) ? current : null);
        setEditing((current) => current && event.data.libraries.some((entry) => entry.id === current.id) ? current : null);
      }
      if (event.kind === 'library-error') setState((s) => ({ ...s, libraryError: event.data }));
      if (event.kind === 'progress') setProgress((current) => updateIndexProgress(current, event.data));
    });
    void window.pdfSearch.getState().then(setState).catch((err: unknown) => setError(message(err)));
    return unsubscribe;
  }, []);

  function clearSearch() {
    setResults([]); setSelected(null); setPreview(null); setLastQuery(''); setProgress(emptyIndexProgress());
  }
  useEffect(() => {
    clearSearch(); setEditing(null); setRemoving(null);
  }, [library?.directory]);

  async function perform(name: string, work: () => Promise<void>) {
    setAction(name); setError(null); setNotice(null);
    try { await work(); }
    catch (err) {
      const detail = message(err);
      if (detail.includes('context canceled') || detail.includes('cancelled')) setNotice('Stopped. The last saved index is still available.');
      else setError(detail);
    } finally { setAction(null); }
  }
  async function chooseFolder() {
    await perform('opening', async () => {
      const chosen = await window.pdfSearch.chooseFolder();
      if (!chosen) return;
      setState((s) => ({ ...s, library: chosen })); clearSearch(); setEditing(null); setRemoving(null);
    });
  }
  async function selectLibrary(id: string) {
    await perform('opening', async () => {
      const chosen = await window.pdfSearch.selectLibrary(id);
      setState((s) => ({ ...s, library: chosen })); clearSearch(); setEditing(null); setRemoving(null);
    });
  }
  async function renameLibrary(event: FormEvent) {
    event.preventDefault();
    if (!editing || busy) return;
    await perform('renaming', async () => {
      setState(await window.pdfSearch.renameLibrary(editing.id, nameDraft.trim())); setEditing(null);
    });
  }
  async function removeLibrary() {
    if (!removing || busy) return;
    await perform('removing', async () => {
      setState(await window.pdfSearch.removeLibrary(removing.id)); setRemoving(null);
      setNotice(`Removed “${removing.name}” and its cached index. Your PDFs are unchanged.`);
    });
  }
  async function indexFolder() {
    await perform('indexing', async () => {
      setProgress(emptyIndexProgress());
      const result = await window.pdfSearch.index(Boolean(library?.issue));
      setState((s) => ({ ...s, library: result.library }));
      setResults([]); setSelected(null); setPreview(null); setLastQuery('');
      const summary = result.stats.pages > 0 ? `${result.stats.pages.toLocaleString()} searchable pages are ready.` : 'No searchable pages were indexed.';
      setNotice(`${summary} ${result.stats.indexed} PDFs indexed, ${result.stats.reused} unchanged PDFs reused. ${result.stats.recovered || result.stats.skipped ? `${result.stats.recovered ?? 0} indexed using recovery, ${result.stats.skipped ?? 0} skipped. See the indexing report for details.` : ''}`);
    });
  }
  async function readPage(result: SearchResult) {
    setSelected(result); setPreview(null);
    const page = await window.pdfSearch.page(result.filename, result.page);
    setPreview(page);
  }
  async function search(event: FormEvent) {
    event.preventDefault();
    if (!query.trim() || !library?.indexed || busy) return;
    await perform('searching', async () => {
      const found = await window.pdfSearch.search(query.trim(), top, minimum);
      setResults(found.results); setLastQuery(found.query); setSelected(null); setPreview(null);
      if (found.results[0]) await readPage(found.results[0]);
    });
  }
  async function restart() {
    await perform('restarting', async () => { setState(await window.pdfSearch.restart()); setResults([]); setSelected(null); setPreview(null); setLastQuery(''); });
  }
  const loading = action === 'indexing' || state.operation === 'indexing';
  const activePDFs = [...progress.active.values()];
  const fileIssues = library?.issues ?? [];
  const skippedFiles = fileIssues.filter((issue) => issue.kind === 'skipped').length;
  const recoveredFiles = fileIssues.filter((issue) => issue.kind === 'recovered').length;
  const cancellable = state.operation === 'searching' || state.operation === 'reading';
  const canSearch = ready && library?.indexed && library.pages > 0 && !busy && Boolean(query.trim());
  const statusLabel = state.error ? 'Search engine stopped' : ready ? 'Search engine ready' : 'Preparing search engine…';
  const updateText = library?.updated_at ? new Date(library.updated_at).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }) : '';

  return <div className="app-shell">
    <aside className="sidebar" aria-label="PDF library">
      <div className="brand"><span className="brand-mark"><Icon name="document" size={25} /></span><span>PassagePin</span></div>
      <div className="sidebar-section">
        <h2>Your libraries</h2>
        {state.libraries?.length > 0 ? <ul className="saved-libraries" aria-label="Saved libraries">{state.libraries.map((entry) => <li key={entry.id} className={state.activeLibraryId === entry.id ? 'current-library' : ''}>
          <button className="library-choice" aria-current={state.activeLibraryId === entry.id ? 'true' : undefined} disabled={!ready || busy} title={entry.directory} onClick={() => void selectLibrary(entry.id)}>
            <Icon name="folder" size={18} /><span><strong>{entry.name}</strong><span className="folder-path">{entry.directory}</span>{state.activeLibraryId === entry.id && <span className="library-current">{library ? 'Current library' : 'Folder unavailable'}</span>}</span>
          </button>
          <div className="library-actions"><button className="text-button" aria-label={`Rename library ${entry.name}`} disabled={!ready || busy} onClick={() => { setEditing(entry); setNameDraft(entry.name); setRemoving(null); }}>Rename</button><button className="text-button" aria-label={`Remove library ${entry.name}`} disabled={!ready || busy} onClick={() => { setRemoving(entry); setEditing(null); }}>Remove</button></div>
        </li>)}</ul> : library ? <div className="folder-summary"><Icon name="folder" size={28} /><strong title={library.directory}>{basename(library.directory)}</strong><p className="folder-path" title={library.directory}>{library.directory}</p></div>
          : <p className="muted">Start with a folder of PDFs on your computer.</p>}
        {editing && <form className="library-editor" aria-label="Rename library" onSubmit={(event) => void renameLibrary(event)}><label htmlFor="library-name">Library name</label><input id="library-name" value={nameDraft} maxLength={120} required autoFocus disabled={busy} onChange={(event) => setNameDraft(event.target.value)} onKeyDown={(event) => { if (event.key === 'Escape' && !busy) setEditing(null); }} /><div className="library-actions"><button type="submit" className="text-button" disabled={busy || !nameDraft.trim()}>Save name</button><button type="button" className="text-button" disabled={busy} onClick={() => setEditing(null)}>Cancel</button></div></form>}
        {removing && <section className="library-removal" aria-label="Remove saved library"><p>Remove <strong>{removing.name}</strong> and its cached index? Your PDF files will stay on this computer.</p><div className="library-actions"><button className="text-button danger" disabled={busy} onClick={() => void removeLibrary()}>Remove library</button><button className="text-button" autoFocus disabled={busy} onClick={() => setRemoving(null)}>Keep library</button></div></section>}
        <button className="button secondary wide" disabled={!ready || busy} onClick={() => void chooseFolder()}><Icon name="folder" size={17} />{state.libraries?.length || library ? 'Add library' : 'Choose folder'}</button>
      </div>
      {library && <div className="sidebar-section index-section">
        <div className="section-heading"><h2>Search index</h2><span className={`status-tag ${library.indexed ? 'available' : ''}`}>{library.indexed ? 'Ready' : 'Not indexed'}</span></div>
        {library.indexed && <><dl className="library-counts"><div><dt>PDFs</dt><dd>{library.documents.toLocaleString()}</dd></div><div><dt>Searchable pages</dt><dd>{library.pages.toLocaleString()}</dd></div></dl><p className="last-update">Updated {updateText}</p></>}
        {library.issue && <p className="small-warning">The saved index needs rebuilding before it can be searched.</p>}
        <button className="button primary wide" disabled={!ready || busy} onClick={() => void indexFolder()}><Icon name="refresh" size={17} />{library.issue ? 'Rebuild index' : library.indexed ? 'Update index' : 'Index folder'}</button>
        {library.empty_pages > 0 && <p className="small-warning">{library.empty_pages} {library.empty_pages === 1 ? 'page had' : 'pages had'} no extractable text. Scanned pages need OCR.</p>}
        {loading && <section className="index-progress" aria-label="Indexing progress">
          <strong role="status">Indexing PDFs…</strong>
          {activePDFs.length > 0 ? <ul className="index-file-list" aria-label="PDFs being indexed">{activePDFs.map((file) => <li key={file.filename} className="index-file">
            <strong className="index-file-name" title={file.filename}>{file.filename}</strong>
            <span className="index-file-page">Page {file.page} of {file.pages}</span>
            <progress max={file.pages} value={file.page} aria-label={`Indexing ${file.filename}`} />
          </li>)}</ul> : <p className="index-progress-message">{progress.completed || progress.reused || progress.skipped ? 'Updating your library…' : 'Reading your PDFs…'}</p>}
          {(progress.completed > 0 || progress.reused > 0 || progress.skipped > 0) && <p className="index-progress-summary">{progress.completed} processed, {progress.reused} unchanged {progress.reused === 1 ? 'PDF' : 'PDFs'} reused{progress.skipped > 0 ? `, ${progress.skipped} skipped` : ''}{progress.recovered > 0 ? `, ${progress.recovered} using recovery` : ''}</p>}
          <button className="text-button" onClick={() => void window.pdfSearch.cancel().catch((err: unknown) => setError(message(err)))}>Cancel indexing</button>
        </section>}
      </div>}
      <div className="sidebar-footer">
        <button type="button" className="theme-toggle" role="switch" aria-label="Dark mode" aria-checked={theme === 'dark'} title={`Switch to ${theme === 'dark' ? 'light' : 'dark'} mode`} onClick={toggleTheme}>
          <span className="theme-label"><Icon name="moon" size={17} />Dark mode</span><span className="theme-track" aria-hidden="true"><span className="theme-thumb" /></span>
        </button>
        <div className="engine-status" role="status"><span className={`status-dot ${ready ? 'ready' : state.error ? 'failed' : 'starting'}`} />{statusLabel}</div>{state.error && <button className="text-button" disabled={busy} onClick={() => void restart()}>Restart search engine</button>}<p><Icon name="lock" size={14} />Your documents stay on this computer.</p>
      </div>
    </aside>

    <main className="workspace">
      <header className="search-header">
        <h1>Find where an idea is discussed.</h1>
        <form onSubmit={(event) => void search(event)}>
          <div className="query-control"><Icon name="search" size={23} /><input aria-label="Search subject" placeholder="Describe a subject, question, or idea…" value={query} onChange={(event) => setQuery(event.target.value)} disabled={!ready} maxLength={4000} /><button className="button primary" disabled={!canSearch} type="submit">{action === 'searching' ? 'Searching…' : 'Search'}</button></div>
          <div className="search-options"><span>Search by meaning across the pages in your library.</span><details><summary>Search options</summary><div className="options-panel"><label>Maximum results<select value={top} disabled={busy} onChange={(event) => setTop(Number(event.target.value))}><option value={10}>10 pages</option><option value={20}>20 pages</option><option value={50}>50 pages</option></select></label><label>Minimum similarity<input aria-label="Minimum similarity" type="number" min="-1" max="1" step="0.05" value={minimum} disabled={busy} onChange={(event) => setMinimum(Number(event.target.value))} /></label><p>Similarity is a ranking score, not a probability.</p></div></details></div>
        </form>
      </header>
      {(error || state.error || state.libraryError) && <div className="message error" role="alert"><strong>Couldn’t complete that action.</strong><span>{error || state.error || state.libraryError}</span></div>}
      {notice && <div className="message notice" role="status">{notice}</div>}
      {!loading && fileIssues.length > 0 && <section className="index-report" aria-label="Indexing report"><details>
        <summary>Indexing report: {skippedFiles} skipped, {recoveredFiles} indexed using recovery</summary>
        <p>Skipped PDFs are excluded from search results. Original files are unchanged.</p>
        <ul>{fileIssues.map((issue) => <li key={`${issue.kind}:${issue.filename}`}>
          <strong>{issue.filename}</strong>
          <span>{issue.kind === 'skipped' ? 'Skipped — no pages indexed' : 'Indexed using recovery'}{issue.page ? ` (failed on page ${issue.page})` : ''}</span>
          <p>{issue.kind === 'recovered' ? 'Original reading issue: ' : ''}{issue.reason}</p>
        </li>)}</ul>
      </details></section>}
      {cancellable && <div className="results-heading" role="status"><span>{state.operation === 'reading' ? 'Reading page…' : 'Searching…'}</span><button className="text-button" onClick={() => void window.pdfSearch.cancel().catch((err: unknown) => setError(message(err)))}>{state.operation === 'reading' ? 'Cancel reading' : 'Cancel search'}</button></div>}

      {lastQuery ? <section className="search-content" aria-label="Search results">
        <div className="results-heading"><div><h2>{results.length} ranked {results.length === 1 ? 'page' : 'pages'}</h2><p>For “{lastQuery}”</p></div></div>
        {results.length === 0 ? <div className="empty-state"><Icon name="search" size={44} /><h2>No pages matched this subject.</h2><p>Try a broader description or lower the minimum similarity in search options.</p></div>
          : <div className="reading-layout"><div className="results-list" role="list" aria-label="Matching pages">{results.map((result, position) => {
            const active = selected?.filename === result.filename && selected.page === result.page;
            return <button role="listitem" aria-current={active ? 'true' : undefined} disabled={busy} key={`${result.filename}:${result.page}`} className={`result-row ${active ? 'selected' : ''}`} onClick={() => void perform('reading', () => readPage(result))}>
              <span className="result-position">{position + 1}</span><span className="result-document"><Icon name="document" size={23} /></span><span className="result-info"><strong>{basename(result.filename)}</strong><span title={result.filename}>{result.filename.includes('/') || result.filename.includes('\\') ? result.filename : 'PDF document'}</span><span className="result-meta">Page {result.page}<span>Similarity {result.score.toFixed(3)}</span></span></span>
            </button>;
          })}</div>
            <article className="page-preview" aria-label="Selected PDF page"><header><div><span className="preview-page-number">Page {selected?.page}</span><h2 title={selected?.filename}>{selected ? basename(selected.filename) : 'Select a page'}</h2></div><button className="button secondary" disabled={busy || !selected} title="Open the document in your default PDF viewer" onClick={() => selected && void perform('opening-pdf', () => window.pdfSearch.openPDF(selected.filename, selected.page))}>Open PDF<Icon name="arrow" size={15} /></button></header><div className="page-paper">{preview ? <p>{preview.text || 'This page has no extractable text.'}</p> : <p className="muted" role="status">Reading page…</p>}</div><footer>Extracted text from the current PDF. Page numbers include covers and blank pages.</footer></article>
          </div>}
      </section> : <section className="welcome" aria-label="Getting started"><div className="paper-illustration" aria-hidden="true"><div className="paper-shadow" /><div className="paper-sheet"><Icon name="document" size={33} /><i /><i /><i className="annotation-line" /><i /><i /></div><span className="paper-search"><Icon name="search" size={24} /></span></div><h2>{!library ? 'Your next answer is in your library.' : !library.indexed ? 'Make these PDFs searchable.' : library.pages === 0 ? 'No searchable pages yet.' : 'Your library is ready to explore.'}</h2><p>{!library ? state.libraries?.length ? 'Select a saved library in the sidebar, or add another folder of PDFs.' : 'Choose a folder of PDFs to get started. Everything is read and searched locally.' : !library.indexed ? 'Index this folder once, then search for the subjects you want to read about.' : library.pages === 0 ? 'Add text-based PDFs to this folder and update the index. Scanned documents need OCR first.' : `Describe a subject above to find relevant pages across ${library.documents} PDFs.`}</p>{!library && <button className="button secondary" disabled={!ready || busy} onClick={() => void chooseFolder()}><Icon name="folder" size={18} />{state.libraries?.length ? 'Add a library' : 'Choose a folder'}</button>}{library && !library.indexed && <button className="button secondary" disabled={!ready || busy} onClick={() => void indexFolder()}>Index this folder</button>}</section>}
    </main>
  </div>;
}
