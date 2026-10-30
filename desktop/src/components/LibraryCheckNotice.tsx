import { useEffect, useState } from 'react';
import type { LibraryCheck } from '../../electron/types';

export default function LibraryCheckNotice({ check, busy, onUpdate }: { check?: LibraryCheck | null; busy: boolean; onUpdate: () => void }) {
  const [later, setLater] = useState(false);
  useEffect(() => setLater(false), [check]);
  if (!check || check.status === 'current') return null;
  if (check.status === 'checking') return <div className="message notice" role="status">Checking library for changes… You can keep searching.</div>;
  if (check.status === 'error') return <div className="message notice" role="status">Couldn’t check this library. {check.message} Select the library again to retry, or update its index.</div>;
  return <div className="message notice" role="status">
    <strong>{later ? 'Index may be outdated.' : 'Library changes found.'}</strong>
    {!later && <span>{check.changed_pages ?? 0} changed pages, {check.added_files ?? 0} added PDFs, {check.removed_files ?? 0} removed PDFs. Results reflect the last saved index until you update.</span>}
    {Boolean(check.errors?.length) && <details><summary>Files needing attention ({check.errors!.length})</summary><ul>{check.errors!.map((error, i) => <li key={i}>{error}</li>)}</ul></details>}
    <div><button className="button secondary" disabled={busy} onClick={onUpdate}>Update index</button>{!later && <button className="text-button" onClick={() => setLater(true)}>Later</button>}</div>
  </div>;
}
