import type { PageText, SearchResult } from "../../electron/types";
import { basename } from "../path";
import Icon from "./Icon";

interface PagePreviewProps {
	selected: SearchResult | null;
	preview: PageText | null;
	busy: boolean;
	openingPDF: boolean;
	onOpenPDF: () => void;
}

export default function PagePreview({
	selected,
	preview,
	busy,
	openingPDF,
	onOpenPDF,
}: PagePreviewProps) {
	return (
		<article className="page-preview" aria-label="Selected PDF page">
			<header>
				<div>
					<span className="preview-page-number">PDF page {selected?.page}</span>
					<h2 title={selected?.filename}>{selected ? basename(selected.filename) : "Select a page"}</h2>
				</div>
				<button
					className="button secondary"
					disabled={busy || !selected}
					data-locked={(Boolean(selected) && busy) || undefined}
					aria-busy={openingPDF || undefined}
					title="Open the document in your default PDF viewer"
					onClick={onOpenPDF}
				>
					{openingPDF ? "Opening…" : "Open PDF"}
					<Icon name="arrow" size={15} />
				</button>
			</header>
			<div className="page-paper">
				{preview ? (
					<p>{preview.text || "This page has no extractable text."}</p>
				) : (
					<p className="muted" role="status">
						Reading page…
					</p>
				)}
			</div>
			<footer>
				PDF page numbers count from the start of the file, including covers and blank pages. They may differ
				from the book’s printed page numbers.
			</footer>
		</article>
	);
}
