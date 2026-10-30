import { useEffect, useState } from "react";
import type { DesktopAPI, State } from "../electron/types";
import { emptyIndexProgress, updateIndexProgress } from "./index-progress";
const initial: State = {
	ready: null,
	error: null,
	operation: null,
	library: null,
	libraries: [],
	activeLibraryId: null,
	libraryError: null,
};

export function useDesktopState(api: DesktopAPI, onError: (message: string) => void) {
	const [state, setState] = useState<State>(initial);
	const [progress, setProgress] = useState(emptyIndexProgress);
	useEffect(() => {
		if (!window.pdfSearch) {
			onError("Open PassagePin as a desktop application.");
			return;
		}
		const unsubscribe = api.onEvent((event) => {
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
			if (event.kind === "check") setState((s) => ({ ...s, check: event.data }));
			if (event.kind === "library") setState((s) => ({ ...s, library: event.data }));
			if (event.kind === "libraries") {
				setState((s) => ({ ...s, ...event.data }));
			}
			if (event.kind === "library-error") setState((s) => ({ ...s, libraryError: event.data }));
			if (event.kind === "progress") setProgress((current) => updateIndexProgress(current, event.data));
		});
		void api
			.getState()
			.then(setState)
			.catch((err: unknown) => onError(err instanceof Error ? err.message : String(err)));
		return unsubscribe;
	}, [api, onError]);

	return { state, setState, progress, setProgress };
}
