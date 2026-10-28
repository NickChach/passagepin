import type { FileIssue } from "../../electron/types";

interface IndexReportProps {
	issues: FileIssue[];
}

export default function IndexReport({ issues }: IndexReportProps) {
	if (issues.length === 0) return null;
	const skippedFiles = issues.filter((issue) => issue.kind === "skipped").length;
	const recoveredFiles = issues.filter((issue) => issue.kind === "recovered").length;
	const partialFiles = issues.filter((issue) => issue.kind === "partial").length;
	return (
		<section className="index-report" aria-label="Indexing report">
			<details>
				<summary>
					Indexing report: {partialFiles > 0 ? `${partialFiles} partially indexed, ` : ""}
					{skippedFiles} skipped, {recoveredFiles} indexed using recovery
				</summary>
				<p>
					Readable pages from partially indexed PDFs are searchable. Failed pages and skipped PDFs are excluded.
					Original files are unchanged.
				</p>
				<ul>
					{issues.map((issue) => (
						<li key={`${issue.kind}:${issue.filename}`}>
							<strong>{issue.filename}</strong>
							<span>
								{issue.kind === "partial"
									? `Partially indexed — ${issue.indexed_pages} searchable ${issue.indexed_pages === 1 ? "page" : "pages"}; ${issue.failures?.length ?? 0} ${issue.failures?.length === 1 ? "page" : "pages"} failed`
									: issue.kind === "skipped"
										? "Skipped — no pages indexed"
										: "Indexed using recovery"}
								{issue.kind === "skipped" && issue.page ? ` (failed on page ${issue.page})` : ""}
							</span>
							{issue.failures?.length ? (
								<details>
									<summary>Failed pages ({issue.failures.length})</summary>
									<ul>
										{issue.failures.map((failure) => (
											<li key={failure.page}>
												Page {failure.page}: {failure.reason}
											</li>
										))}
									</ul>
								</details>
							) : (
								<p>
									{issue.kind === "recovered" ? "Original reading issue: " : ""}
									{issue.reason}
								</p>
							)}
						</li>
					))}
				</ul>
			</details>
		</section>
	);
}
