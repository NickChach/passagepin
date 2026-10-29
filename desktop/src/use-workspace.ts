import { useEffect, useState } from "react";
import { DesktopError } from "../electron/ipc-result";
import type { DesktopAPI, SavedLibrary, SearchResult } from "../electron/types";
import { emptyIndexProgress } from "./index-progress";
import { useDesktopState } from "./use-desktop-state";
import { useSearchSession } from "./use-search-session";

const message = (error: unknown) => error instanceof Error ? error.message : String(error);

// Coordinates UI actions; the desktop subscription and search session own their
// respective state. All actions share one busy/cancellation boundary.
export function useWorkspace(api: DesktopAPI) {

	const [error, setError] = useState<string | null>(null);
	const { state, setState, progress, setProgress } = useDesktopState(api, setError);
	const { query, setQuery, lastQuery, results, selected, preview, top, setTop, minimum, setMinimum, clearSearch, readPage, runSearch } = useSearchSession(api);
	const [action, setAction] = useState<string | null>(null);

	const [notice, setNotice] = useState<string | null>(null);

	const [editing, setEditing] = useState<SavedLibrary | null>(null);
	const [nameDraft, setNameDraft] = useState("");
	const [removing, setRemoving] = useState<SavedLibrary | null>(null);
	const busy = action !== null || state.operation !== null;
	const ready = state.ready !== null && !state.error;
	const library = state.library;
	useEffect(() => {
		setRemoving(current => current && state.libraries.some(entry => entry.id === current.id) ? current : null);
		setEditing(current => current && state.libraries.some(entry => entry.id === current.id) ? current : null);
	}, [state.libraries]);

	function resetLibraryView() {
		clearSearch();
		setProgress(emptyIndexProgress());
		setEditing(null);
		setRemoving(null);
	}
	useEffect(() => {
		resetLibraryView();
	}, [library?.directory]);

	async function perform(name: string, work: () => Promise<void>) {
		setAction(name);
		setError(null);
		setNotice(null);
		try {
			await work();
		} catch (err) {
			const detail = message(err);
			if (err instanceof DesktopError && err.code === "cancelled")
				setNotice("Stopped. The last saved index is still available.");
			else setError(detail);
		} finally {
			setAction(null);
		}
	}
	async function chooseFolder() {
		await perform("opening", async () => {
			const chosen = await api.chooseFolder();
			if (!chosen) return;
			setState((s) => ({ ...s, library: chosen }));
			resetLibraryView();
		});
	}
	async function selectLibrary(id: string) {
		await perform("opening", async () => {
			const chosen = await api.selectLibrary(id);
			setState((s) => ({ ...s, library: chosen }));
			resetLibraryView();
		});
	}
	async function renameLibrary() {
		if (!editing || busy) return;
		await perform("renaming", async () => {
			setState(await api.renameLibrary(editing.id, nameDraft.trim()));
			setEditing(null);
		});
	}
	async function removeLibrary() {
		if (!removing || busy) return;
		await perform("removing", async () => {
			setState(await api.removeLibrary(removing.id));
			setRemoving(null);
			setNotice(`Removed “${removing.name}” and its cached index. Your PDFs are unchanged.`);
		});
	}
	async function indexFolder() {
		await perform("indexing", async () => {
			setProgress(emptyIndexProgress());
			const result = await api.index(Boolean(library?.issue));
			setState((s) => ({ ...s, library: result.library }));
			clearSearch();
			const summary =
				result.stats.pages > 0
					? `${result.stats.pages.toLocaleString()} searchable pages are ready.`
					: "No searchable pages were indexed.";
			setNotice(
				`${summary} ${result.stats.indexed} PDFs indexed, ${result.stats.reused} unchanged PDFs reused. ${result.stats.recovered || result.stats.skipped || result.stats.partial ? `${result.stats.partial ?? 0} partially indexed, ${result.stats.recovered ?? 0} indexed using recovery, ${result.stats.skipped ?? 0} skipped. See the indexing report for details.` : ""}`,
			);
		});
	}

	async function search() {
		if (!query.trim() || !library?.indexed || busy) return;
		await perform("searching", async () => {
			await runSearch();
		});
	}
	async function restart() {
		await perform("restarting", async () => {
			setState(await api.restart());
			clearSearch();
		});
	}

	const loading = action === "indexing" || state.operation === "indexing";
	const canSearch = ready && Boolean(library?.indexed && library.pages > 0 && query.trim());

	function cancel() {
		void api.cancel().catch((err: unknown) => setError(message(err)));
	}

	function startRename(entry: SavedLibrary) {
		setEditing(entry); setNameDraft(entry.name); setRemoving(null);
	}
	function startRemove(entry: SavedLibrary) {
		setRemoving(entry); setEditing(null);
	}
	async function selectPage(result: SearchResult) {
		await perform("reading", () => readPage(result));
	}
	async function openPDF() {
		if (selected) await perform("opening-pdf", () => api.openPDF(selected.filename, selected.page));
	}
	return {
		state, action, query, setQuery, lastQuery, results, selected, preview, progress,
		error, notice, top, setTop, minimum, setMinimum,
		editing, nameDraft, setNameDraft, removing, busy, ready, library, chooseFolder,
		selectLibrary, renameLibrary, removeLibrary, indexFolder, search, restart,
		loading, canSearch, cancel, selectPage, openPDF, startRename, startRemove,
		cancelRename: () => setEditing(null), cancelRemove: () => setRemoving(null),
	};
}
