import type { Theme } from "../theme";
import Icon from "./Icon";

interface SidebarFooterProps {
	theme: Theme;
	ready: boolean;
	busy: boolean;
	engineError: string | null;
	onToggleTheme: () => void;
	onRestart: () => void;
}

export default function SidebarFooter({
	theme,
	ready,
	busy,
	engineError,
	onToggleTheme,
	onRestart,
}: SidebarFooterProps) {
	const statusLabel = engineError ? "Search engine stopped" : ready ? "Search engine ready" : "Preparing search engine…";
	return (
		<div className="sidebar-footer">
			<button
				type="button"
				className="theme-toggle"
				role="switch"
				aria-label="Dark mode"
				aria-checked={theme === "dark"}
				title={`Switch to ${theme === "dark" ? "light" : "dark"} mode`}
				onClick={onToggleTheme}
			>
				<span className="theme-label">
					<Icon name="moon" size={17} />
					Dark mode
				</span>
				<span className="theme-track" aria-hidden="true">
					<span className="theme-thumb" />
				</span>
			</button>
			<div className="engine-status" role="status">
				<span className={`status-dot ${ready ? "ready" : engineError ? "failed" : "starting"}`} />
				{statusLabel}
			</div>
			{engineError && (
				<button className="text-button" disabled={busy} data-locked={busy || undefined} onClick={onRestart}>
					Restart search engine
				</button>
			)}
			<p>
				<Icon name="lock" size={14} />
				Your data stay on this computer.
			</p>
		</div>
	);
}
