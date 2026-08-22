import { useCallback, useEffect, useRef } from 'react';
import type { Item } from '../types';

interface Props {
  items: Item[];
  loading: boolean;
  error: string | null;
  onOpen: (id: number, item: Item) => void;
  onMarkRead: (ids: number[]) => void;
  // When false (the Unread queue view) items are only marked read by explicit
  // user action, not by scrolling.
  autoMark: boolean;
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

// How long to wait after the last item enters the viewport before sending
// the batched mark-read request.
const FLUSH_MS = 250;

export default function ItemList({
  items,
  loading,
  error,
  onOpen,
  onMarkRead,
  autoMark,
  hasMore,
  loadingMore,
  onLoadMore,
}: Props) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const sentinelRef = useRef<HTMLDivElement>(null);

  const hasList = !loading && !error && items.length > 0;

  // Keep the latest handlers without re-creating observers each render.
  const loadMoreRef = useRef(onLoadMore);
  loadMoreRef.current = onLoadMore;
  const markReadRef = useRef(onMarkRead);
  markReadRef.current = onMarkRead;

  // Read-detection state.
  const observerRef = useRef<IntersectionObserver | null>(null);
  const pendingRef = useRef<Set<number>>(new Set());
  const flushTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Fire the pending batch to the parent (marks them read).
  const flush = useCallback(() => {
    if (flushTimerRef.current) {
      clearTimeout(flushTimerRef.current);
      flushTimerRef.current = null;
    }
    const pending = pendingRef.current;
    if (pending.size === 0) return;
    const ids = Array.from(pending);
    pending.clear();
    markReadRef.current(ids);
  }, []);

  // Observe newly-mounted items as the list grows (infinite scroll).
  const itemRef = useCallback((el: HTMLLIElement | null) => {
    if (el && observerRef.current) observerRef.current.observe(el);
  }, []);

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

  // Mark items read once they enter the viewport, so the list always matches
  // what the reader has actually seen. Items skipped by a very fast scroll
  // are picked up once they end up fully above the visible top. Items still
  // below the fold are left unread. Disabled in the Unread queue view, where
  // scrolling should not silently drain the list.
  useEffect(() => {
    if (!hasList || !autoMark) return;
    const root = scrollRef.current;
    if (!root) return;

    const observer = new IntersectionObserver(
      (entries) => {
        let added = false;
        for (const entry of entries) {
          const el = entry.target as HTMLElement;
          if (!entry.isIntersecting || el.dataset.read === 'true') continue;
          const id = Number(el.dataset.id);
          if (!Number.isFinite(id)) continue;
          pendingRef.current.add(id);
          added = true;
        }
        // Catch-up sweep: the observer never reports an item that a fast
        // scroll carries from below the fold to above it without a sampled
        // intersection in between, so unread items that ended up fully above
        // the visible top are marked here as well.
        const rootTop = root.getBoundingClientRect().top;
        root.querySelectorAll('li[data-id]').forEach((node) => {
          const el = node as HTMLElement;
          if (el.dataset.read === 'true') return;
          if (el.getBoundingClientRect().bottom >= rootTop) return;
          const id = Number(el.dataset.id);
          if (!Number.isFinite(id)) return;
          pendingRef.current.add(id);
          added = true;
        });
        if (added) {
          if (flushTimerRef.current) clearTimeout(flushTimerRef.current);
          flushTimerRef.current = setTimeout(flush, FLUSH_MS);
        }
      },
      { root },
    );

    root.querySelectorAll('li[data-id]').forEach((el) => observer.observe(el));
    observerRef.current = observer;

    return () => {
      observer.disconnect();
      observerRef.current = null;
      if (flushTimerRef.current) {
        clearTimeout(flushTimerRef.current);
        flushTimerRef.current = null;
      }
      flush();
    };
  }, [hasList, autoMark, flush]);

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
          <li
            key={item.id}
            ref={itemRef}
            data-id={item.id}
            data-read={item.read ? 'true' : 'false'}
            className={item.read ? 'item' : 'item unread'}
          >
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
