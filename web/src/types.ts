// Types mirror the JSON produced by the Go API (see internal/store/store.go).

export interface Feed {
  id: number;
  url: string;
  title: string;
  site_url: string;
  last_fetched: string | null;
  created_at: string;
  collection_ids: number[];
}

export interface Collection {
  id: number;
  name: string;
  feed_count: number;
  created_at: string;
}

export interface AddFeedResult {
  url: string;
  feed?: Feed;
  error?: string;
}

export interface Item {
  id: number;
  feed_id: number;
  feed_title: string;
  guid: string;
  title: string;
  link: string;
  content: string;
  author: string;
  published_at: string | null;
  fetched_at: string;
  read: boolean;
}

export interface ItemsResponse {
  items: Item[];
  total: number;
}

export interface UnreadCountResponse {
  count: number;
}

export interface User {
  id: number;
  email: string;
  display_name: string;
  role: string;
  email_verified: boolean;
  created_at: string;
}
