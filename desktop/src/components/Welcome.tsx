import type { Library } from "../../electron/types";
import Icon from "./Icon";

interface WelcomeProps {
	library: Library | null;
	hasSavedLibraries: boolean;
	ready: boolean;
	busy: boolean;
	onChooseFolder: () => void;
	onIndex: () => void;
}

export default function Welcome({
	library,
	hasSavedLibraries,
	ready,
	busy,
	onChooseFolder,
	onIndex,
}: WelcomeProps) {
	return (
		<section className="welcome" aria-label="Getting started">
			<div className="paper-illustration" aria-hidden="true">
				<div className="paper-shadow" />
				<div className="paper-sheet">
					<Icon name="document" size={33} />
					<i />
					<i />
					<i className="annotation-line" />
					<i />
					<i />
				</div>
				<span className="paper-search">
					<Icon name="search" size={24} />
				</span>
			</div>
			<h2>
				{!library
					? "Your next answer is in your library."
					: !library.indexed
						? "Make these PDFs searchable."
						: library.pages === 0
							? "No searchable pages yet."
							: "Your library is ready to be explored."}
			</h2>
			<p>
				{!library
					? hasSavedLibraries
						? "Select a saved library in the sidebar, or add another folder of PDFs."
						: "Choose a folder of PDFs to get started. Everything is read and searched locally."
					: !library.indexed
						? "Index this folder once, then search for the subjects you want to read about."
						: library.pages === 0
							? "Add text-based PDFs to this folder and update the index. Scanned documents need OCR first."
							: `Describe a subject above to find relevant pages across ${library.documents} PDFs.`}
			</p>
			{!library && (
				<button
					className="button secondary"
					disabled={!ready || busy}
					data-locked={(ready && busy) || undefined}
					onClick={onChooseFolder}
				>
					<Icon name="folder" size={18} />
					{hasSavedLibraries ? "Add a library" : "Choose a folder"}
				</button>
			)}
			{library && !library.indexed && (
				<button
					className="button secondary"
					disabled={!ready || busy}
					data-locked={(ready && busy) || undefined}
					onClick={onIndex}
				>
					Index this folder
				</button>
			)}
		</section>
	);
}
