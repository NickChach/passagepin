import type { Library, SavedLibrary } from "../../electron/types";
import { basename } from "../path";
import Icon from "./Icon";

interface LibraryManagerProps {
	libraries: SavedLibrary[];
	activeLibraryId: string | null;
	library: Library | null;
	ready: boolean;
	busy: boolean;
	editing: SavedLibrary | null;
	nameDraft: string;
	removing: SavedLibrary | null;
	onSelect: (id: string) => void;
	onChooseFolder: () => void;
	onStartRename: (library: SavedLibrary) => void;
	onNameChange: (name: string) => void;
	onRename: () => void;
	onCancelRename: () => void;
	onStartRemove: (library: SavedLibrary) => void;
	onRemove: () => void;
	onCancelRemove: () => void;
}

export default function LibraryManager({
	libraries,
	activeLibraryId,
	library,
	ready,
	busy,
	editing,
	nameDraft,
	removing,
	onSelect,
	onChooseFolder,
	onStartRename,
	onNameChange,
	onRename,
	onCancelRename,
	onStartRemove,
	onRemove,
	onCancelRemove,
}: LibraryManagerProps) {
	return (
		<div className="sidebar-section">
			<h2>Your libraries</h2>
			{libraries?.length > 0 ? (
				<ul className="saved-libraries" aria-label="Saved libraries">
					{libraries.map((entry) => (
						<li key={entry.id} className={activeLibraryId === entry.id ? "current-library" : ""}>
							<button
								className="library-choice"
								aria-current={activeLibraryId === entry.id ? "true" : undefined}
								disabled={!ready || busy}
								data-locked={(ready && busy) || undefined}
								title={entry.directory}
								onClick={() => onSelect(entry.id)}
							>
								<Icon name="folder" size={18} />
								<span>
									<strong>{entry.name}</strong>
									<span className="folder-path">{entry.directory}</span>
									{activeLibraryId === entry.id && (
										<span className="library-current">
											{library ? "Current library" : "Folder unavailable"}
										</span>
									)}
								</span>
							</button>
							<div className="library-actions">
								<button
									className="text-button"
									aria-label={`Rename library ${entry.name}`}
									disabled={!ready || busy}
									data-locked={(ready && busy) || undefined}
									onClick={() => onStartRename(entry)}
								>
									Rename
								</button>
								<button
									className="text-button"
									aria-label={`Remove library ${entry.name}`}
									disabled={!ready || busy}
									data-locked={(ready && busy) || undefined}
									onClick={() => onStartRemove(entry)}
								>
									Remove
								</button>
							</div>
						</li>
					))}
				</ul>
			) : library ? (
				<div className="folder-summary">
					<Icon name="folder" size={28} />
					<strong title={library.directory}>{basename(library.directory)}</strong>
					<p className="folder-path" title={library.directory}>
						{library.directory}
					</p>
				</div>
			) : (
				<p className="muted">Start with a folder of PDFs on your computer.</p>
			)}
			{editing && (
				<form
					className="library-editor"
					aria-label="Rename library"
					onSubmit={(event) => {
						event.preventDefault();
						onRename();
					}}
				>
					<label htmlFor="library-name">Library name</label>
					<input
						id="library-name"
						value={nameDraft}
						maxLength={120}
						required
						autoFocus
						disabled={busy}
						onChange={(event) => onNameChange(event.target.value)}
						onKeyDown={(event) => {
							if (event.key === "Escape" && !busy) onCancelRename();
						}}
					/>
					<div className="library-actions">
						<button
							type="submit"
							className="text-button"
							disabled={busy || !nameDraft.trim()}
							data-locked={(busy && Boolean(nameDraft.trim())) || undefined}
						>
							Save name
						</button>
						<button
							type="button"
							className="text-button"
							disabled={busy}
							data-locked={busy || undefined}
							onClick={onCancelRename}
						>
							Cancel
						</button>
					</div>
				</form>
			)}
			{removing && (
				<section className="library-removal" aria-label="Remove saved library">
					<p>
						Remove <strong>{removing.name}</strong> and its cached index? Your PDF files will stay on this computer.
					</p>
					<div className="library-actions">
						<button
							className="text-button danger"
							disabled={busy}
							data-locked={busy || undefined}
							onClick={onRemove}
						>
							Remove library
						</button>
						<button
							className="text-button"
							autoFocus
							disabled={busy}
							data-locked={busy || undefined}
							onClick={onCancelRemove}
						>
							Keep library
						</button>
					</div>
				</section>
			)}
			<button
				className="button secondary wide"
				disabled={!ready || busy}
				data-locked={(ready && busy) || undefined}
				onClick={onChooseFolder}
			>
				<Icon name="folder" size={17} />
				{libraries?.length || library ? "Add library" : "Choose folder"}
			</button>
		</div>
	);
}
