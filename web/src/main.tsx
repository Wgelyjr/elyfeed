import React from 'react';
import ReactDOM from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  persistQueryClient,
  type Persister,
} from '@tanstack/react-query-persist-client';
import { registerSW } from 'virtual:pwa-register';
import App from './App';
import './main.css';

// Register the service worker for offline app-shell loading.
registerSW({ immediate: true });

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 60 * 1000,
      gcTime: 7 * 24 * 60 * 60 * 1000,
      retry: false,
      refetchOnWindowFocus: true,
    },
  },
});

// Minimal localStorage persister so recently fetched data survives reloads and
// is available immediately (including while offline).
const PERSISTER_KEY = 'elyfeed-query-cache';
const persister: Persister = {
  persistClient: (persistClient) =>
    Promise.resolve(
      localStorage.setItem(PERSISTER_KEY, JSON.stringify(persistClient)),
    ),
  restoreClient: () =>
    Promise.resolve(
      JSON.parse(localStorage.getItem(PERSISTER_KEY) ?? 'null') ?? undefined,
    ),
  removeClient: () => Promise.resolve(localStorage.removeItem(PERSISTER_KEY)),
};

async function bootstrap() {
  // Rehydrate the query cache from localStorage before first paint.
  await persistQueryClient({ queryClient, persister });

  ReactDOM.createRoot(document.getElementById('root')!).render(
    <React.StrictMode>
      <QueryClientProvider client={queryClient}>
        <App />
      </QueryClientProvider>
    </React.StrictMode>,
  );
}

void bootstrap();
