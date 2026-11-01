import type { ReactNode } from "react";
import appIcon from "../../assets/icon.svg";

interface SidebarProps {
	children: ReactNode;
}

export default function Sidebar({ children }: SidebarProps) {
	return (
		<aside className="sidebar" aria-label="PDF library">
			<div className="brand">
				<img className="brand-mark" src={appIcon} alt="" />
				<span>PassagePin</span>
			</div>
			{children}
		</aside>
	);
}
