// Types mirror the JSON produced by the Go API (see internal/store/store.go).

export type ShareStatus = 'private' | 'pending' | 'shared';

export type VisibilityStatus = 'private' | 'pending' | 'public';

export interface Feed {
  id: number;
  url: string;
  title: string;
  site_url: string;
  last_fetched: string | null;
  created_at: string;
  collection_ids: number[];
  share_status: ShareStatus;
  share_requested?: 'shared' | 'private' | null;
}

export interface SharedFeed {
  url: string;
  title: string;
  site_url: string;
  owner_name: string;
}

export interface ShareRequest {
  feed_id: number;
  title: string;
  url: string;
  owner_name: string;
  owner_email: string;
  requested: 'shared' | 'private';
}

export interface RecommendedFeed {
  id: number;
  url: string;
  title: string;
  site_url: string;
}

export interface SharedFeedURL {
  url: string;
  title: string;
}

export interface CollectionShare {
  id: number;
  name: string;
  feeds: SharedFeedURL[];
}

export interface ImportResult {
  collection: Collection;
  added: number;
  total: number;
  results: AddFeedResult[];
}

export interface Collection {
  id: number;
  name: string;
  feed_count: number;
  created_at: string;
  visibility_status: VisibilityStatus;
  visibility_requested: 'private' | 'public' | null;
}

export interface PublicCollection {
  id: number;
  name: string;
  owner_name: string;
  feed_count: number;
  feeds: SharedFeedURL[];
}

export interface CollectionShareRequest {
  collection_id: number;
  owner_id: number;
  owner_name: string;
  owner_email: string;
  requested: 'private' | 'public';
  name: string;
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
