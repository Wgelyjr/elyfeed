import { useEffect, useRef } from 'react';
import type { Item } from '../types';

interface Props {
  items: Item[];
  loading: boolean;
  error: string | null;
  onOpen: (id: number, item: Item) => void;
  hasMore: boolean;
  loadingMore: boolean;
  onLoadMore: () => void;
}

function formatDate(iso: string | null): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleString([], {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

export default function ItemList({
  items,
  loading,
  error,
  onOpen,
  hasMore,
  loadingMore,
  onLoadMore,
}: Props) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const sentinelRef = useRef<HTMLDivElement>(null);
  // Keep the latest load handler without re-creating the observer each render.
  const loadMoreRef = useRef(onLoadMore);
  loadMoreRef.current = onLoadMore;

  useEffect(() => {
    if (!hasMore) return;
    const sentinel = sentinelRef.current;
    const root = scrollRef.current;
    if (!sentinel) return;
    // Fire a bit before the sentinel scrolls into view so the next page is
    // usually ready by the time the user reaches the end.
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries[0].isIntersecting) loadMoreRef.current();
      },
      { root, rootMargin: '240px 0px' },
    );
    observer.observe(sentinel);
    return () => observer.disconnect();
  }, [hasMore]);

  if (loading) {
    return (
      <div className="items-scroll">
        <div className="muted pad">Loading…</div>
      </div>
    );
  }
  if (error) {
    return (
      <div className="items-scroll">
        <div className="error-bar pad">Failed to load: {error}</div>
      </div>
    );
  }
  if (items.length === 0) {
    return (
      <div className="items-scroll">
        <div className="muted pad">
          No items yet. Add a feed, then hit Refresh.
        </div>
      </div>
    );
  }

  return (
    <div className="items-scroll" ref={scrollRef}>
      <ul className="items">
        {items.map((item) => (
          <li key={item.id} className={item.read ? 'item' : 'item unread'}>
            <button className="item-main" onClick={() => onOpen(item.id, item)}>
              <span className="item-title">{item.title || item.link || item.guid}</span>
              <span className="item-meta">
                {item.feed_title}
                {item.author ? ` · ${item.author}` : ''}
                {item.published_at ? ` · ${formatDate(item.published_at)}` : ''}
              </span>
              {item.content && <span className="item-content">{item.content}</span>}
            </button>
          </li>
        ))}
      </ul>
      {hasMore && (
        <div className="sentinel" ref={sentinelRef}>
          {loadingMore && <span className="muted small">Loading…</span>}
        </div>
      )}
    </div>
  );
}
