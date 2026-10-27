import { contextBridge, ipcRenderer } from 'electron';
import type { DesktopAPI, WorkerEvent } from './types';
const api: DesktopAPI = {
  getState: () => ipcRenderer.invoke('state:get'),
  chooseFolder: () => ipcRenderer.invoke('library:choose'),
  selectLibrary: (id) => ipcRenderer.invoke('library:select', id),
  renameLibrary: (id, name) => ipcRenderer.invoke('library:rename', id, name),
  removeLibrary: (id) => ipcRenderer.invoke('library:remove', id),
  index: (rebuild) => ipcRenderer.invoke('library:index', rebuild),
  search: (query, top, minScore) => ipcRenderer.invoke('library:search', query, top, minScore),
  page: (filename, page) => ipcRenderer.invoke('library:page', filename, page),
  openPDF: (filename, page) => ipcRenderer.invoke('library:open-pdf', filename, page),
  cancel: () => ipcRenderer.invoke('worker:cancel'),
  restart: () => ipcRenderer.invoke('worker:restart'),
  onEvent: (listener) => {
    const receive = (_event: Electron.IpcRendererEvent, data: WorkerEvent) => listener(data);
    ipcRenderer.on('worker:event', receive);
    return () => ipcRenderer.removeListener('worker:event', receive);
  },
};
contextBridge.exposeInMainWorld('pdfSearch', api);
