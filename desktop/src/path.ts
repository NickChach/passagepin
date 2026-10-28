export const basename = (path: string) => path.split(/[\\/]/).filter(Boolean).at(-1) || path;
