import type { ReactNode } from "react";
import Icon from "./Icon";

interface SidebarProps {
	children: ReactNode;
}

export default function Sidebar({ children }: SidebarProps) {
	return (
		<aside className="sidebar" aria-label="PDF library">
			<div className="brand">
				<span className="brand-mark">
					<Icon name="document" size={25} />
				</span>
				<span>PassagePin</span>
			</div>
			{children}
		</aside>
	);
}
