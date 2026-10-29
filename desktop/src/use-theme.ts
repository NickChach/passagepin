import { useState } from "react";
import { saveTheme, type Theme } from "./theme";

export function useTheme() {
	const [theme, setTheme] = useState<Theme>(() => (document.documentElement.dataset.theme === "dark" ? "dark" : "light"));
	function toggleTheme() {
		const next = theme === "dark" ? "light" : "dark";
		saveTheme(next);
		setTheme(next);
	}

	return { theme, toggleTheme };
}
