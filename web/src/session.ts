import type { Persister } from '@tanstack/react-query-persist-client';

// Per-user query-cache persistence. Each account's data lives under its own
// localStorage key so different users never see each other's cached feeds and
// items.
const CACHE_PREFIX = 'elyfeed-cache-';

export function cacheKeyForUser(userId: number): string {
  return `${CACHE_PREFIX}${userId}`;
}

export function removeCacheForUser(userId: number): void {
  localStorage.removeItem(cacheKeyForUser(userId));
}

// A per-user localStorage persister plus a `deactivate` switch. On logout /
// session expiry we call `deactivate` so that a late query-cache event (e.g. a
// query settling into an error state) cannot re-persist the user's data after
// we have already deleted the cache entry.
export function createSessionPersister(userId: number): {
  persister: Persister;
  deactivate: () => void;
} {
  const key = cacheKeyForUser(userId);
  let active = true;
  const persister: Persister = {
    persistClient: (persistClient) => {
      if (active) localStorage.setItem(key, JSON.stringify(persistClient));
      return Promise.resolve();
    },
    restoreClient: () =>
      Promise.resolve(
        JSON.parse(localStorage.getItem(key) ?? 'null') ?? undefined,
      ),
    removeClient: () => Promise.resolve(localStorage.removeItem(key)),
  };
  return { persister, deactivate: () => { active = false; } };
}
