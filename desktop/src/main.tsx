import React from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';
import { applyTheme, readTheme } from './theme';
import './style.css';
applyTheme(readTheme());
createRoot(document.getElementById('root')!).render(<React.StrictMode><App /></React.StrictMode>);
