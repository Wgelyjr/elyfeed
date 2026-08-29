import React, { useCallback, useEffect, useRef, useState } from 'react';
import ReactDOM from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { persistQueryClient } from '@tanstack/react-query-persist-client';
import { registerSW } from 'virtual:pwa-register';
import { api, setOnUnauthorized } from './api';
import App from './App';
import LoginScreen from './components/LoginScreen';
import { createSessionPersister, removeCacheForUser } from './session';
import type { User } from './types';
import './main.css';

// Register the service worker for offline app-shell loading.
registerSW({ immediate: true });

const QUERY_CLIENT_DEFAULTS = {
  queries: {
    staleTime: 60 * 1000,
    gcTime: 7 * 24 * 60 * 60 * 1000,
    retry: false,
    refetchOnWindowFocus: true,
  },
};

// App shell: resolves the current session, owns the per-user QueryClient,
// and gates the UI on the login screen.
function Root({ initialUser }: { initialUser: User | null }) {
  const [user, setUser] = useState<User | null>(initialUser);
  // Non-null only once the user's persisted cache has been restored, so the
  // app never paints data belonging to a different user.
  const [session, setSession] = useState<QueryClient | null>(null);

  const userRef = useRef(user);
  userRef.current = user;
  const deactivateRef = useRef<(() => void) | null>(null);

  // End the local session: stop the persister, delete the user's cached
  // data, and show the login screen. Idempotent, so it is safe to call from
  // the 401 interceptor when several requests fail at once.
  const endSession = useCallback(() => {
    const uid = userRef.current?.id;
    deactivateRef.current?.();
    deactivateRef.current = null;
    if (uid != null) removeCacheForUser(uid);
    setUser(null);
  }, []);

  // A non-auth 401 means the session is gone (expired, revoked, or logged
  // out elsewhere): end the local session.
  useEffect(() => {
    setOnUnauthorized(endSession);
  }, [endSession]);

  // Build the per-user QueryClient and rehydrate that user's persisted cache
  // before the app is allowed to render.
  useEffect(() => {
    const uid = user?.id;
    if (uid == null) {
      setSession(null);
      return;
    }
    let disposed = false;
    let unsubscribe: (() => void) | null = null;
    const { persister, deactivate } = createSessionPersister(uid);
    deactivateRef.current = deactivate;
    const client = new QueryClient({
      defaultOptions: QUERY_CLIENT_DEFAULTS,
    });
    const [unsub, restored] = persistQueryClient({
      queryClient: client,
      persister,
    });
    unsubscribe = unsub;
    void restored
      .catch(() => {
        // Corrupt persisted cache: the library has discarded it, so start
        // with an empty (unpersisted) cache for this session.
      })
      .then(() => {
        if (disposed) {
          unsub();
          return;
        }
        setSession(client);
      });
    return () => {
      disposed = true;
      unsubscribe?.();
    };
  }, [user?.id]);

  if (!user) {
    return <LoginScreen onLogin={setUser} />;
  }
  if (!session) {
    return (
      <div className="app-loading">
        <span className="muted">Loading…</span>
      </div>
    );
  }
  return (
    <QueryClientProvider client={session}>
      <App user={user} onLogout={endSession} />
    </QueryClientProvider>
  );
}

async function bootstrap() {
  let user: User | null = null;
  try {
    user = await api.me();
  } catch {
    // Unreachable API or network failure: treat as signed out rather than
    // showing a broken page.
    user = null;
  }
  ReactDOM.createRoot(document.getElementById('root')!).render(
    <React.StrictMode>
      <Root initialUser={user} />
    </React.StrictMode>,
  );
}

void bootstrap();
