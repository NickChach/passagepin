import { desktopAPI } from './desktop-api';
import About from './components/About';
import { useState, useMemo } from 'react';
import { useWorkspace } from './use-workspace';
import { useTheme } from './use-theme';
import Sidebar from "./components/Sidebar";
import LibraryManager from "./components/LibraryManager";
import IndexPanel from "./components/IndexPanel";
import SidebarFooter from "./components/SidebarFooter";
import SearchForm from "./components/SearchForm";
import IndexReport from "./components/IndexReport";
import SearchResults from "./components/SearchResults";
import LibraryCheckNotice from "./components/LibraryCheckNotice";
import Welcome from "./components/Welcome";

export default function App() {
	const [aboutOpen, setAboutOpen] = useState(false);
	const api = useMemo(() => desktopAPI(window.pdfSearch), []);
	const { theme, toggleTheme } = useTheme();
	const {
		state, action, query, setQuery, lastQuery, results,
		selected, preview, previewStatus, previewError, progress, error, notice, top,
		setTop, minimum, setMinimum, editing, nameDraft, setNameDraft,
		removing, busy, ready, library, chooseFolder, selectLibrary,
		renameLibrary, removeLibrary, indexFolder, checkLibrary, search, restart,
		loading, canSearch, cancel, selectPage, openPDF, startRename, startRemove, cancelRename, cancelRemove,
	} = useWorkspace(api);
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
					onStartRename={startRename}
					onNameChange={setNameDraft}
					onRename={renameLibrary}
					onCancelRename={cancelRename}
					onStartRemove={startRemove}
					onRemove={removeLibrary}
					onCancelRemove={cancelRemove}
				/>
				{library && (
					<IndexPanel
						library={library}
						ready={ready}
						busy={busy}
						loading={loading}
						progress={progress}
						onIndex={() => indexFolder()}
						onRebuild={() => indexFolder(true)}
						onCheck={checkLibrary}
						checking={state.check?.status === "checking"}
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
						onAbout={() => setAboutOpen(true)}
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
				<LibraryCheckNotice check={state.check} busy={busy} onUpdate={() => indexFolder()} />
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
						previewStatus={previewStatus}
						previewError={previewError}
						onRetry={() => { if (selected) void selectPage(selected); }}
						busy={busy}
						openingPDF={action === "opening-pdf"}
						onSelect={selectPage}
						onOpenPDF={openPDF}
					/>
				) : (
					<Welcome
						library={library}
						hasSavedLibraries={Boolean(state.libraries?.length)}
						ready={ready}
						busy={busy}
						onChooseFolder={chooseFolder}
						onIndex={() => indexFolder()}
					/>
				)}
			</main>
			{aboutOpen && <About api={api} onClose={() => setAboutOpen(false)} />}
		</div>
	);
}
