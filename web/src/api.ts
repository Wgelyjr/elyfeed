import type {
  AddFeedResult,
  Collection,
  Feed,
  Item,
  ItemsResponse,
  UnreadCountResponse,
  User,
} from './types';

// Thin typed wrapper over the JSON API. All requests are same-origin (the Go
// server serves both the API and the app), so no credentials/CORS juggling.

export class ApiError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

// Global handler for 401 responses (session expired / logged out elsewhere).
// The app registers it once; it must be idempotent because several in-flight
// requests can 401 at the same time.
let onUnauthorized: (() => void) | null = null;

export function setOnUnauthorized(handler: () => void): void {
  onUnauthorized = handler;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body.error) msg = body.error;
    } catch {
      // non-JSON error body; keep the status message
    }
    // Auth endpoints surface their own errors to the caller (the login form
    // needs to show "invalid credentials"); only non-auth 401s mean the
    // session is gone.
    if (res.status === 401 && !path.startsWith('/api/auth/')) {
      onUnauthorized?.();
    }
    throw new ApiError(res.status, msg);
  }
  return (await res.json()) as T;
}

export interface ListItemsParams {
  feed_id?: number;
  collection_id?: number;
  unread?: boolean;
  limit?: number;
  offset?: number;
}

export const api = {
  listFeeds: (): Promise<Feed[]> => request<Feed[]>('/api/feeds'),

  addFeed: (url: string): Promise<Feed> =>
    request<Feed>('/api/feeds', { method: 'POST', body: JSON.stringify({ url }) }),

  deleteFeed: (id: number): Promise<{ ok: boolean }> =>
    request<{ ok: boolean }>(`/api/feeds/${id}`, { method: 'DELETE' }),

  bulkAddFeeds: (urls: string[]): Promise<{ results: AddFeedResult[] }> =>
    request<{ results: AddFeedResult[] }>('/api/feeds/bulk', {
      method: 'POST',
      body: JSON.stringify({ urls }),
    }),

  bulkDeleteFeeds: (ids: number[]): Promise<{ deleted: number }> =>
    request<{ deleted: number }>('/api/feeds/bulk-delete', {
      method: 'POST',
      body: JSON.stringify({ ids }),
    }),

  setFeedCollections: (
    id: number,
    collectionIds: number[],
  ): Promise<{ ok: boolean }> =>
    request<{ ok: boolean }>(`/api/feeds/${id}/collections`, {
      method: 'PUT',
      body: JSON.stringify({ collection_ids: collectionIds }),
    }),

  listCollections: (): Promise<Collection[]> =>
    request<Collection[]>('/api/collections'),

  createCollection: (name: string): Promise<Collection> =>
    request<Collection>('/api/collections', {
      method: 'POST',
      body: JSON.stringify({ name }),
    }),

  renameCollection: (id: number, name: string): Promise<Collection> =>
    request<Collection>(`/api/collections/${id}`, {
      method: 'PATCH',
      body: JSON.stringify({ name }),
    }),

  deleteCollection: (id: number): Promise<{ ok: boolean }> =>
    request<{ ok: boolean }>(`/api/collections/${id}`, { method: 'DELETE' }),

  listItems: (params: ListItemsParams): Promise<ItemsResponse> => {
    const qs = new URLSearchParams();
    if (params.feed_id != null) qs.set('feed_id', String(params.feed_id));
    if (params.collection_id != null)
      qs.set('collection_id', String(params.collection_id));
    if (params.unread != null) qs.set('unread', String(params.unread));
    if (params.limit != null) qs.set('limit', String(params.limit));
    if (params.offset != null) qs.set('offset', String(params.offset));
    const q = qs.toString();
    return request<ItemsResponse>(`/api/items${q ? `?${q}` : ''}`);
  },

  setItemRead: (id: number, read: boolean): Promise<{ ok: boolean }> =>
    request<{ ok: boolean }>(`/api/items/${id}/read`, {
      method: 'POST',
      body: JSON.stringify({ read }),
    }),

  bulkSetItemsRead: (
    ids: number[],
    read: boolean,
  ): Promise<{ ok: boolean; updated: number }> =>
    request<{ ok: boolean; updated: number }>('/api/items/bulk-read', {
      method: 'POST',
      body: JSON.stringify({ ids, read }),
    }),

  unreadCount: (): Promise<UnreadCountResponse> =>
    request<UnreadCountResponse>('/api/items/unread-count'),

  refresh: (): Promise<{ refreshed: number }> =>
    request<{ refreshed: number }>('/api/refresh', { method: 'POST' }),

  // Auth. `me` returns null when not signed in instead of throwing, since a
  // 401 on this call just means "no session".
  me: async (): Promise<User | null> => {
    try {
      return await request<User>('/api/auth/me');
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) return null;
      throw err;
    }
  },

  login: (email: string, password: string): Promise<User> =>
    request<User>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ email, password }),
    }),

  register: (email: string, name: string, password: string): Promise<{ message: string }> =>
    request<{ message: string }>('/api/auth/register', {
      method: 'POST',
      body: JSON.stringify({ email, name, password }),
    }),

  logout: (): Promise<{ ok: boolean }> =>
    request<{ ok: boolean }>('/api/auth/logout', { method: 'POST' }),

  forgotPassword: (email: string): Promise<{ ok: boolean }> =>
    request<{ ok: boolean }>('/api/auth/forgot-password', {
      method: 'POST',
      body: JSON.stringify({ email }),
    }),
};

export type { Item };
