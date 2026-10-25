import type { DesktopAPI } from '../electron/types';
declare global { interface Window { pdfSearch: DesktopAPI } }
