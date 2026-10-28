import type { IndexProgress } from "../index-progress";

interface IndexingProgressProps {
	progress: IndexProgress;
	onCancel: () => void;
}

export default function IndexingProgress({
	progress,
	onCancel,
}: IndexingProgressProps) {
	const activePDFs = [...progress.active.values()];
	return (
		<section className="index-progress" aria-label="Indexing progress">
			<strong role="status">Indexing PDFs…</strong>
			{activePDFs.length > 0 ? (
				<ul className="index-file-list" aria-label="PDFs being indexed">
					{activePDFs.map((file) => (
						<li key={file.filename} className="index-file">
							<strong className="index-file-name" title={file.filename}>
								{file.filename}
							</strong>
							<span className="index-file-page">
								Page {file.page} of {file.pages}
							</span>
							<progress max={file.pages} value={file.page} aria-label={`Indexing ${file.filename}`} />
						</li>
					))}
				</ul>
			) : (
				<p className="index-progress-message">
					{progress.completed || progress.reused || progress.skipped
						? "Updating your library…"
						: "Reading your PDFs…"}
				</p>
			)}
			{(progress.completed > 0 || progress.reused > 0 || progress.skipped > 0) && (
				<p className="index-progress-summary">
					{progress.completed} processed, {progress.reused} unchanged {progress.reused === 1 ? "PDF" : "PDFs"}{" "}
					reused{progress.partial > 0 ? `, ${progress.partial} partially indexed` : ""}
					{progress.skipped > 0 ? `, ${progress.skipped} skipped` : ""}
					{progress.recovered > 0 ? `, ${progress.recovered} using recovery` : ""}
				</p>
			)}
			<button
				className="text-button"
				onClick={onCancel}
			>
				Cancel indexing
			</button>
		</section>
	);
}
