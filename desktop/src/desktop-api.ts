import type { DesktopAPI, DesktopTransport } from '../electron/types';
import { unwrapResult } from '../electron/ipc-result';

// Unwrap in the renderer: custom Error properties do not survive Electron's
// IPC or contextBridge error serialization.
export function desktopAPI(transport: DesktopTransport): DesktopAPI {
  return {
    getState: async () => unwrapResult(await transport.getState()),
    chooseFolder: async () => unwrapResult(await transport.chooseFolder()),
    selectLibrary: async (id) => unwrapResult(await transport.selectLibrary(id)),
    renameLibrary: async (id, name) => unwrapResult(await transport.renameLibrary(id, name)),
    removeLibrary: async (id) => unwrapResult(await transport.removeLibrary(id)),
    index: async (rebuild) => unwrapResult(await transport.index(rebuild)),
    search: async (query, top, minimum) => unwrapResult(await transport.search(query, top, minimum)),
    page: async (filename, page) => unwrapResult(await transport.page(filename, page)),
    openPDF: async (filename, page) => unwrapResult(await transport.openPDF(filename, page)),
    cancel: async () => unwrapResult(await transport.cancel()),
    restart: async () => unwrapResult(await transport.restart()),
    onEvent: (listener) => transport.onEvent(listener),
  };
}
