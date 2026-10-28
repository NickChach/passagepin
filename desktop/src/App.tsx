import { desktopAPI } from './desktop-api';
import { DesktopError } from '../electron/ipc-result';
import { useEffect, useState } from "react";
import type { PageText, SavedLibrary, SearchResult, State } from "../electron/types";
import { saveTheme, type Theme } from "./theme";
import { emptyIndexProgress, updateIndexProgress } from "./index-progress";
import Sidebar from "./components/Sidebar";
import LibraryManager from "./components/LibraryManager";
import IndexPanel from "./components/IndexPanel";
import SidebarFooter from "./components/SidebarFooter";
import SearchForm from "./components/SearchForm";
import IndexReport from "./components/IndexReport";
import SearchResults from "./components/SearchResults";
import Welcome from "./components/Welcome";

const initial: State = {
	ready: null,
	error: null,
	operation: null,
	library: null,
	libraries: [],
	activeLibraryId: null,
	libraryError: null,
};
const api = () => desktopAPI(window.pdfSearch);
const message = (error: unknown) =>
	error instanceof Error ? error.message : String(error);

export default function App() {
	const [theme, setTheme] = useState<Theme>(() => (document.documentElement.dataset.theme === "dark" ? "dark" : "light"));
	const [state, setState] = useState<State>(initial);
	const [action, setAction] = useState<string | null>(null);
	const [query, setQuery] = useState("");
	const [lastQuery, setLastQuery] = useState("");
	const [results, setResults] = useState<SearchResult[]>([]);
	const [selected, setSelected] = useState<SearchResult | null>(null);
	const [preview, setPreview] = useState<PageText | null>(null);
	const [progress, setProgress] = useState(emptyIndexProgress);
	const [error, setError] = useState<string | null>(null);
	const [notice, setNotice] = useState<string | null>(null);
	const [top, setTop] = useState(20);
	const [minimum, setMinimum] = useState(0);
	const [editing, setEditing] = useState<SavedLibrary | null>(null);
	const [nameDraft, setNameDraft] = useState("");
	const [removing, setRemoving] = useState<SavedLibrary | null>(null);
	const busy = action !== null || state.operation !== null;
	const ready = state.ready !== null && !state.error;
	const library = state.library;

	function toggleTheme() {
		const next = theme === "dark" ? "light" : "dark";
		saveTheme(next);
		setTheme(next);
	}

	useEffect(() => {
		if (!window.pdfSearch) {
			setError("Open PassagePin as a desktop application.");
			return;
		}
		const unsubscribe = api().onEvent((event) => {
			if (event.kind === "ready") setState((s) => ({ ...s, ready: event.data, error: null }));
			if (event.kind === "starting") {
				setState((s) => ({ ...s, ready: null, error: null }));
				setProgress(emptyIndexProgress());
			}
			if (event.kind === "failed") {
				setState((s) => ({ ...s, ready: null, error: event.data }));
				setProgress(emptyIndexProgress());
			}
			if (event.kind === "operation") {
				setState((s) => ({ ...s, operation: event.data }));
				setProgress(emptyIndexProgress());
			}
			if (event.kind === "library") setState((s) => ({ ...s, library: event.data }));
			if (event.kind === "libraries") {
				setState((s) => ({ ...s, ...event.data }));
				setRemoving((current) => (current && event.data.libraries.some((entry) => entry.id === current.id) ? current : null));
				setEditing((current) => (current && event.data.libraries.some((entry) => entry.id === current.id) ? current : null));
			}
			if (event.kind === "library-error") setState((s) => ({ ...s, libraryError: event.data }));
			if (event.kind === "progress") setProgress((current) => updateIndexProgress(current, event.data));
		});
		void api()
			.getState()
			.then(setState)
			.catch((err: unknown) => setError(message(err)));
		return unsubscribe;
	}, []);

	function clearSearch() {
		setResults([]);
		setSelected(null);
		setPreview(null);
		setLastQuery("");
	}
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
			const chosen = await api().chooseFolder();
			if (!chosen) return;
			setState((s) => ({ ...s, library: chosen }));
			resetLibraryView();
		});
	}
	async function selectLibrary(id: string) {
		await perform("opening", async () => {
			const chosen = await api().selectLibrary(id);
			setState((s) => ({ ...s, library: chosen }));
			resetLibraryView();
		});
	}
	async function renameLibrary() {
		if (!editing || busy) return;
		await perform("renaming", async () => {
			setState(await api().renameLibrary(editing.id, nameDraft.trim()));
			setEditing(null);
		});
	}
	async function removeLibrary() {
		if (!removing || busy) return;
		await perform("removing", async () => {
			setState(await api().removeLibrary(removing.id));
			setRemoving(null);
			setNotice(`Removed “${removing.name}” and its cached index. Your PDFs are unchanged.`);
		});
	}
	async function indexFolder() {
		await perform("indexing", async () => {
			setProgress(emptyIndexProgress());
			const result = await api().index(Boolean(library?.issue));
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
	async function readPage(result: SearchResult) {
		setSelected(result);
		setPreview((current) => (current?.filename === result.filename && current.page === result.page ? current : null));
		const page = await api().page(result.filename, result.page);
		setPreview(page);
	}
	async function search() {
		if (!query.trim() || !library?.indexed || busy) return;
		await perform("searching", async () => {
			const found = await api().search(query.trim(), top, minimum);
			setResults(found.results);
			setLastQuery(found.query);
			if (found.results[0]) await readPage(found.results[0]);
			else {
				setSelected(null);
				setPreview(null);
			}
		});
	}
	async function restart() {
		await perform("restarting", async () => {
			setState(await api().restart());
			clearSearch();
		});
	}

	const loading = action === "indexing" || state.operation === "indexing";
	const canSearch = ready && Boolean(library?.indexed && library.pages > 0 && query.trim());

	function cancel() {
		void api().cancel().catch((err: unknown) => setError(message(err)));
	}

	return (
		<div className="app-shell">
			<Sidebar>
				<LibraryManager
					libraries={state.libraries}
					activeLibraryId={state.activeLibraryId}
					library={library}
					ready={ready}
					busy={busy}
					editing={editing}
					nameDraft={nameDraft}
					removing={removing}
					onSelect={selectLibrary}
					onChooseFolder={chooseFolder}
					onStartRename={(entry) => {
						setEditing(entry);
						setNameDraft(entry.name);
						setRemoving(null);
					}}
					onNameChange={setNameDraft}
					onRename={renameLibrary}
					onCancelRename={() => setEditing(null)}
					onStartRemove={(entry) => {
						setRemoving(entry);
						setEditing(null);
					}}
					onRemove={removeLibrary}
					onCancelRemove={() => setRemoving(null)}
				/>
				{library && (
					<IndexPanel
						library={library}
						ready={ready}
						busy={busy}
						loading={loading}
						progress={progress}
						onIndex={indexFolder}
						onCancel={cancel}
					/>
				)}
				<SidebarFooter
					theme={theme}
					ready={ready}
					busy={busy}
					engineError={state.error}
					onToggleTheme={toggleTheme}
					onRestart={restart}
				/>
			</Sidebar>
			<main className="workspace">
				<SearchForm
					query={query}
					top={top}
					minimum={minimum}
					ready={ready}
					busy={busy}
					canSearch={canSearch}
					searching={action === "searching"}
					operation={state.operation}
					onQueryChange={setQuery}
					onTopChange={setTop}
					onMinimumChange={setMinimum}
					onSearch={search}
					onCancel={cancel}
				/>
				{(error || state.error || state.libraryError) && (
					<div className="message error" role="alert">
						<strong>Couldn’t complete that action.</strong>
						<span>{error || state.error || state.libraryError}</span>
					</div>
				)}
				{notice && (
					<div className="message notice" role="status">
						{notice}
					</div>
				)}
				{!loading && <IndexReport issues={library?.issues ?? []} />}
				{lastQuery ? (
					<SearchResults
						lastQuery={lastQuery}
						results={results}
						selected={selected}
						preview={preview}
						busy={busy}
						openingPDF={action === "opening-pdf"}
						onSelect={(result) => void perform("reading", () => readPage(result))}
						onOpenPDF={() => {
							if (selected) void perform("opening-pdf", () => api().openPDF(selected.filename, selected.page));
						}}
					/>
				) : (
					<Welcome
						library={library}
						hasSavedLibraries={Boolean(state.libraries?.length)}
						ready={ready}
						busy={busy}
						onChooseFolder={chooseFolder}
						onIndex={indexFolder}
					/>
				)}
			</main>
		</div>
	);
}
