import { useEffect, useState } from 'react';
import type { LibraryCheck } from '../../electron/types';

export default function LibraryCheckNotice({ check, busy, onUpdate }: { check?: LibraryCheck | null; busy: boolean; onUpdate: () => void }) {
  const [later, setLater] = useState(false);
  useEffect(() => setLater(false), [check]);
  if (!check || check.status === 'current') return null;
  if (check.status === 'checking') return <div className="message notice" role="status">Checking library for changes… You can keep searching.</div>;
  if (check.status === 'error') return <div className="message notice" role="status">Couldn’t check this library. {check.message} Select the library again to retry, or update its index.</div>;

  const changes = [
    [check.changed_pages, 'changed page', 'changed pages'],
    [check.added_files, 'added PDF', 'added PDFs'],
    [check.removed_files, 'removed PDF', 'removed PDFs'],
  ] as const;
  const summary = changes.filter(([count]) => count > 0)
    .map(([count, singular, plural]) => `${count.toLocaleString()} ${count === 1 ? singular : plural}`).join(', ');

  return <div className="message library-check" role="status">
    <div className="library-check-content">
      <strong>{later ? 'Index may be outdated.' : 'Library changes found.'}</strong>
      {!later && <>
        {summary && <p className="library-check-summary">{summary}.</p>}
        <p className="library-check-hint">Results reflect the last saved index until you update.</p>
      </>}
      {Boolean(check.errors.length) && <details className="library-check-errors">
        <summary>Files needing attention ({check.errors.length})</summary>
        <ul>{check.errors.map((error, i) => <li key={i}>{error}</li>)}</ul>
      </details>}
    </div>
    <div className="library-check-actions">
      <button type="button" className="button primary" disabled={busy} data-locked={busy || undefined} onClick={onUpdate}>Update index</button>
      {!later && <button type="button" className="button secondary" onClick={() => setLater(true)}>Later</button>}
    </div>
  </div>;
}
