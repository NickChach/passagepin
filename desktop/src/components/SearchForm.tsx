import Icon from "./Icon";

interface SearchFormProps {
	query: string;
	top: number;
	minimum: number;
	ready: boolean;
	busy: boolean;
	canSearch: boolean;
	searching: boolean;
	operation: string | null;
	onQueryChange: (query: string) => void;
	onTopChange: (top: number) => void;
	onMinimumChange: (minimum: number) => void;
	onSearch: () => void;
	onCancel: () => void;
}

export default function SearchForm({
	query,
	top,
	minimum,
	ready,
	busy,
	canSearch,
	searching,
	operation,
	onQueryChange,
	onTopChange,
	onMinimumChange,
	onSearch,
	onCancel,
}: SearchFormProps) {
	const cancellable = operation === "searching" || operation === "reading";
	return (
		<header className="search-header">
			<h1>Find where an idea is discussed.</h1>
			<form
				onSubmit={(event) => {
					event.preventDefault();
					onSearch();
				}}
			>
				<div className="query-control">
					<Icon name="search" size={23} />
					<input
						aria-label="Search subject"
						placeholder="Describe a subject, question, or idea…"
						value={query}
						onChange={(event) => onQueryChange(event.target.value)}
						disabled={!ready}
						maxLength={4000}
					/>
					<button
						className="button primary"
						disabled={!canSearch || busy}
						data-locked={(canSearch && busy) || undefined}
						type="submit"
					>
						{searching ? "Searching…" : "Search"}
					</button>
				</div>
				<div className="search-options">
					<div className="search-status">
						<span className="search-description" style={{ visibility: cancellable ? "hidden" : undefined }}>
							Search by meaning across the pages in your library.
						</span>
						{cancellable && (
							<div className="operation-status" role="status">
								<span>{operation === "reading" ? "Reading page…" : "Searching…"}</span>
								<button
									className="text-button"
									onClick={onCancel}
								>
									{operation === "reading" ? "Cancel reading" : "Cancel search"}
								</button>
							</div>
						)}
					</div>
					<details>
						<summary>Search options</summary>
						<div className="options-panel">
							<label>
								Maximum results
								<select value={top} disabled={busy} onChange={(event) => onTopChange(Number(event.target.value))}>
									<option value={10}>10 pages</option>
									<option value={20}>20 pages</option>
									<option value={50}>50 pages</option>
								</select>
							</label>
							<label>
								Minimum similarity
								<input
									aria-label="Minimum similarity"
									type="number"
									min="-1"
									max="1"
									step="0.05"
									value={minimum}
									disabled={busy}
									onChange={(event) => onMinimumChange(Number(event.target.value))}
								/>
							</label>
							<p>Similarity is a ranking score, not a probability.</p>
						</div>
					</details>
				</div>
			</form>
		</header>
	);
}
