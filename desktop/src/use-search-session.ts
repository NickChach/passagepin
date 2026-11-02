import { useState } from "react";
import { DesktopError } from "../electron/ipc-result";
import type { DesktopAPI, PageText, SearchResult } from "../electron/types";

export function useSearchSession(api: DesktopAPI) {
	const [query, setQuery] = useState("");
	const [lastQuery, setLastQuery] = useState("");
	const [results, setResults] = useState<SearchResult[]>([]);
	const [selected, setSelected] = useState<SearchResult | null>(null);
	const [preview, setPreview] = useState<PageText | null>(null);
	const [previewStatus, setPreviewStatus] = useState<"idle" | "reading" | "ready" | "cancelled" | "error">("idle");
	const [previewError, setPreviewError] = useState<string | null>(null);
	const [top, setTop] = useState(20);
	const [minimum, setMinimum] = useState(0);
	function clearSearch() {
		setResults([]);
		setSelected(null);
		setPreview(null);
		setLastQuery("");
		setPreviewStatus("idle");
		setPreviewError(null);
	}
	async function readPage(result: SearchResult) {
		setSelected(result);
		setPreview((current) => (current?.filename === result.filename && current.page === result.page ? current : null));
		setPreviewStatus("reading");
		setPreviewError(null);
		try {
			const page = await api.page(result.filename, result.page);
			setPreview(page);
			setPreviewStatus("ready");
		} catch (error) {
			setPreview(null);
			setPreviewStatus(error instanceof DesktopError && error.code === "cancelled" ? "cancelled" : "error");
			setPreviewError(error instanceof Error ? error.message : String(error));
			throw error;
		}
	}
	async function runSearch() {
		const found = await api.search(query.trim(), top, minimum);
		setResults(found.results);
		setLastQuery(found.query);
		if (found.results[0]) await readPage(found.results[0]);
		else {
			setSelected(null);
			setPreview(null);
			setPreviewStatus("idle");
			setPreviewError(null);
		}
	}
	return { query, setQuery, lastQuery, results, selected, preview, previewStatus, previewError, top, setTop, minimum, setMinimum, clearSearch, readPage, runSearch };
}
