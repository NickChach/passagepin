import type { ReactNode } from "react";

export default function Icon({ name, size = 20 }: { name: "folder" | "search" | "document" | "refresh" | "arrow" | "lock" | "moon"; size?: number }) {
	const paths: Record<string, ReactNode> = {
		folder: <path d="M3 7V5a1 1 0 0 1 1-1h5l2 3h9a1 1 0 0 1 1 1v11a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V7Z" />,
		search: (
			<>
				<circle cx="10.5" cy="10.5" r="6.5" />
				<path d="m16 16 5 5" />
			</>
		),
		document: (
			<>
				<path d="M6 3h8l4 4v14H6V3Z" />
				<path d="M14 3v5h4M9 12h6M9 16h6" />
			</>
		),
		refresh: (
			<>
				<path d="M20 7v5h-5M4 17v-5h5" />
				<path d="M5.2 8a7 7 0 0 1 11.4-3L20 9M4 15l3.4 4A7 7 0 0 0 18.8 16" />
			</>
		),
		arrow: (
			<>
				<path d="M5 19 19 5M7 5h12v12" />
			</>
		),
		lock: (
			<>
				<rect x="5" y="10" width="14" height="11" rx="2" />
				<path d="M8 10V6a4 4 0 0 1 8 0v4" />
			</>
		),
		moon: <path d="M20.5 13A8.5 8.5 0 0 1 11 3.5 8.5 8.5 0 1 0 20.5 13Z" />,
	};
	return (
		<svg
			width={size}
			height={size}
			viewBox="0 0 24 24"
			fill="none"
			stroke="currentColor"
			strokeWidth="1.6"
			strokeLinecap="round"
			strokeLinejoin="round"
			aria-hidden="true"
		>
			{paths[name]}
		</svg>
	);
}
