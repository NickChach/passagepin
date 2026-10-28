import type { Library } from "../../electron/types";
import type { IndexProgress } from "../index-progress";
import Icon from "./Icon";
import IndexingProgress from "./IndexingProgress";

interface IndexPanelProps {
	library: Library;
	ready: boolean;
	busy: boolean;
	loading: boolean;
	progress: IndexProgress;
	onIndex: () => void;
	onCancel: () => void;
}

export default function IndexPanel({
	library,
	ready,
	busy,
	loading,
	progress,
	onIndex,
	onCancel,
}: IndexPanelProps) {
	const updateText = library.updated_at
		? new Date(library.updated_at).toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" })
		: "";
	return (
		<div className="sidebar-section index-section">
			<div className="section-heading">
				<h2>Search index</h2>
				<span className={`status-tag ${library.indexed ? "available" : ""}`}>
					{library.indexed ? "Ready" : "Not indexed"}
				</span>
			</div>
			{library.indexed && (
				<>
					<dl className="library-counts">
						<div>
							<dt>PDFs</dt>
							<dd>{library.documents.toLocaleString()}</dd>
						</div>
						<div>
							<dt>Searchable pages</dt>
							<dd>{library.pages.toLocaleString()}</dd>
						</div>
					</dl>
					<p className="last-update">Updated {updateText}</p>
				</>
			)}
			{library.issue && <p className="small-warning">The saved index needs rebuilding before it can be searched.</p>}
			<button
				className="button primary wide"
				disabled={!ready || busy}
				data-locked={(ready && busy) || undefined}
				onClick={onIndex}
			>
				<Icon name="refresh" size={17} />
				{library.issue ? "Rebuild index" : library.indexed ? "Update index" : "Index folder"}
			</button>
			{library.empty_pages > 0 && (
				<p className="small-warning">
					{library.empty_pages} {library.empty_pages === 1 ? "page had" : "pages had"} no extractable text. Scanned
					pages need OCR.
				</p>
			)}
			{loading && <IndexingProgress progress={progress} onCancel={onCancel} />}
		</div>
	);
}
