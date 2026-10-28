import type { PageText, SearchResult } from "../../electron/types";
import { basename } from "../path";
import Icon from "./Icon";
import PagePreview from "./PagePreview";

interface SearchResultsProps {
	lastQuery: string;
	results: SearchResult[];
	selected: SearchResult | null;
	preview: PageText | null;
	busy: boolean;
	openingPDF: boolean;
	onSelect: (result: SearchResult) => void;
	onOpenPDF: () => void;
}

export default function SearchResults({
	lastQuery,
	results,
	selected,
	preview,
	busy,
	openingPDF,
	onSelect,
	onOpenPDF,
}: SearchResultsProps) {
	return (
		<section className="search-content" aria-label="Search results">
			<div className="results-heading">
				<div>
					<h2>
						{results.length} ranked {results.length === 1 ? "page" : "pages"}
					</h2>
					<p>For “{lastQuery}”</p>
				</div>
			</div>
			{results.length === 0 ? (
				<div className="empty-state">
					<Icon name="search" size={44} />
					<h2>No pages matched this subject.</h2>
					<p>Try a broader description or lower the minimum similarity in search options.</p>
				</div>
			) : (
				<div className="reading-layout">
					<div className="results-list" role="list" aria-label="Matching pages">
						{results.map((result, position) => {
							const active = selected?.filename === result.filename && selected.page === result.page;
							return (
								<button
									role="listitem"
									aria-current={active ? "true" : undefined}
									disabled={busy}
									data-locked={busy || undefined}
									key={`${result.filename}:${result.page}`}
									className={`result-row ${active ? "selected" : ""}`}
									onClick={() => onSelect(result)}
								>
									<span className="result-position">{position + 1}</span>
									<span className="result-document">
										<Icon name="document" size={23} />
									</span>
									<span className="result-info">
										<strong>{basename(result.filename)}</strong>
										<span title={result.filename}>
											{result.filename.includes("/") || result.filename.includes("\\")
												? result.filename
												: "PDF document"}
										</span>
										<span className="result-meta">
											PDF page {result.page}
											<span>Similarity {result.score.toFixed(3)}</span>
										</span>
									</span>
								</button>
							);
						})}
					</div>
					<PagePreview selected={selected} preview={preview} busy={busy} openingPDF={openingPDF} onOpenPDF={onOpenPDF} />
				</div>
			)}
		</section>
	);
}
