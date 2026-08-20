import type { Feed, Item, ItemsResponse, UnreadCountResponse } from './types';

// Thin typed wrapper over the JSON API. All requests are same-origin (the Go
// server serves both the API and the app), so no credentials/CORS juggling.

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
    throw new Error(msg);
  }
  return (await res.json()) as T;
}

export interface ListItemsParams {
  feed_id?: number;
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

  listItems: (params: ListItemsParams): Promise<ItemsResponse> => {
    const qs = new URLSearchParams();
    if (params.feed_id != null) qs.set('feed_id', String(params.feed_id));
    if (params.unread) qs.set('unread', 'true');
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

  unreadCount: (): Promise<UnreadCountResponse> =>
    request<UnreadCountResponse>('/api/items/unread-count'),

  refresh: (): Promise<{ refreshed: number }> =>
    request<{ refreshed: number }>('/api/refresh', { method: 'POST' }),
};

export type { Item };
