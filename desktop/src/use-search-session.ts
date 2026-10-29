import { useState } from "react";
import type { DesktopAPI, PageText, SearchResult } from "../electron/types";

export function useSearchSession(api: DesktopAPI) {
	const [query, setQuery] = useState("");
	const [lastQuery, setLastQuery] = useState("");
	const [results, setResults] = useState<SearchResult[]>([]);
	const [selected, setSelected] = useState<SearchResult | null>(null);
	const [preview, setPreview] = useState<PageText | null>(null);
	const [top, setTop] = useState(20);
	const [minimum, setMinimum] = useState(0);
	function clearSearch() {
		setResults([]);
		setSelected(null);
		setPreview(null);
		setLastQuery("");
	}
	async function readPage(result: SearchResult) {
		setSelected(result);
		setPreview((current) => (current?.filename === result.filename && current.page === result.page ? current : null));
		const page = await api.page(result.filename, result.page);
		setPreview(page);
	}
	async function runSearch() {
		const found = await api.search(query.trim(), top, minimum);
		setResults(found.results);
		setLastQuery(found.query);
		if (found.results[0]) await readPage(found.results[0]);
		else {
			setSelected(null);
			setPreview(null);
		}
	}
	return { query, setQuery, lastQuery, results, selected, preview, top, setTop, minimum, setMinimum, clearSearch, readPage, runSearch };
}
