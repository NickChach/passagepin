import { useEffect, useRef, useState } from 'react';
import type { DesktopAPI } from '../../electron/types';
import { chromiumNoticesText } from '../chromium-notices';

export default function About({ api, onClose }: { api: DesktopAPI; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [info, setInfo] = useState<{ version: string; platform: string } | null>(null);
  const [notices, setNotices] = useState<{ title: string; text: string } | null>(null);
  const [loading, setLoading] = useState<'licenses' | 'chromium' | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    dialog.current?.showModal();
    let active = true;
    void api.appInfo().then(value => { if (active) setInfo(value); }).catch(err => { if (active) setError(String(err)); });
    return () => { active = false; };
  }, [api]);
  async function showNotices(kind: 'licenses' | 'chromium') {
    setLoading(kind); setError(null);
    try {
      const text = kind === 'licenses' ? await api.licenses() : chromiumNoticesText(await api.chromiumNotices());
      setNotices({ title: kind === 'licenses' ? 'License notices' : 'Chromium notices', text });
    }
    catch (err) { setError(err instanceof Error ? err.message : String(err)); }
    finally { setLoading(null); }
  }
  return <dialog ref={dialog} className="about-dialog" aria-labelledby="about-title" closedby="any" onClose={onClose}>
    <h2 id="about-title">About PassagePin</h2>
    <p>{info ? `Version ${info.version} · ${info.platform}` : 'Loading version…'}</p>
    <p>Find relevant pages across your local PDF library. Indexing and searching work offline; your documents and queries stay on this computer.</p>
    <h3>Using PassagePin</h3>
    <ul>
      <li>Choose a folder, index it, then describe a subject or question.</li>
      <li>Check for changes after editing or adding PDFs. Update index processes changes; rebuilding retries every PDF from scratch.</li>
      <li>Scanned pages need OCR before indexing. Blank pages also have no searchable text.</li>
      <li>The search model is intended for English. Similarity scores rank pages and are not probabilities.</li>
      <li>PDF page numbers include covers and blank pages. Open PDF opens the document in your system viewer. Use the PDF page number to navigate there.</li>
    </ul>
    <p>Packaged support: Linux x86-64 with glibc 2.38 or newer. Other platforms are not yet validated.</p>
    <div className="about-actions">
      <button type="button" className="button secondary" disabled={loading !== null} onClick={() => { void showNotices('licenses'); }}>{loading === 'licenses' ? 'Loading notices…' : 'License notices'}</button>
      <button type="button" className="button secondary" disabled={loading !== null} onClick={() => { void showNotices('chromium'); }}>{loading === 'chromium' ? 'Loading notices…' : 'Chromium notices'}</button>
      <form method="dialog"><button autoFocus className="button primary">Close</button></form>
    </div>
    {error && <p role="alert">{error}</p>}
    {notices && <section aria-label={notices.title}><h3>{notices.title}</h3><pre tabIndex={0}>{notices.text}</pre></section>}
  </dialog>;
}
