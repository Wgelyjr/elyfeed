import { useState } from 'react';
import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { api } from './api';
import type { Item, ItemsResponse } from './types';
import Sidebar from './components/Sidebar';
import ItemList from './components/ItemList';

// How many items to fetch per scroll "page".
const PAGE_SIZE = 30;

// Filters the item list by read state.
type ReadFilter = 'all' | 'unread' | 'read';

const READ_FILTERS: { value: ReadFilter; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'unread', label: 'Unread' },
  { value: 'read', label: 'Read' },
];

// Applies a batch of read-state flips to a cached items query. When
// dropRead is set (the query is filtered to unread) the flipped items are
// removed outright and total is reduced so infinite scroll doesn't
// re-fetch offsets that are already loaded.
function applyMarkedRead(
  old: { pages: ItemsResponse[] } | undefined,
  ids: Set<number>,
  dropRead: boolean,
): { pages: ItemsResponse[] } | undefined {
  if (!old?.pages) return old;
  return {
    ...old,
    pages: old.pages.map((page) => ({
      ...page,
      items: page.items
        .map((it) => (ids.has(it.id) ? { ...it, read: true } : it))
        .filter((it) => !(dropRead && ids.has(it.id))),
      total: Math.max(
        0,
        page.total -
          (dropRead ? page.items.filter((it) => ids.has(it.id)).length : 0),
      ),
    })),
  };
}

export default function App() {
  const queryClient = useQueryClient();
  const [selectedFeedId, setSelectedFeedId] = useState<number | null>(null);
  const [selectedCollectionId, setSelectedCollectionId] = useState<
    number | null
  >(null);
  const [readFilter, setReadFilter] = useState<ReadFilter>('all');
  // Sidebar starts open on desktop, closed on phones (where it's an overlay).
  const [sidebarOpen, setSidebarOpen] = useState(
    () => (typeof window === 'undefined' ? true : window.innerWidth >= 768),
  );

  const feedsQuery = useQuery({ queryKey: ['feeds'], queryFn: api.listFeeds });
  const collectionsQuery = useQuery({
    queryKey: ['collections'],
    queryFn: api.listCollections,
  });

  // Exactly one of feed/collection selection is active at a time. Items load in
  // pages and are appended as the user scrolls (infinite scroll).
  const itemsQuery = useInfiniteQuery({
    queryKey: ['items', selectedFeedId, selectedCollectionId, readFilter],
    queryFn: ({ pageParam }) =>
      api.listItems({
        feed_id: selectedFeedId ?? undefined,
        collection_id: selectedCollectionId ?? undefined,
        unread: readFilter === 'all' ? undefined : readFilter === 'unread',
        limit: PAGE_SIZE,
        offset: pageParam,
      }),
    initialPageParam: 0,
    getNextPageParam: (lastPage, allPages) => {
      const loaded = allPages.reduce((n, p) => n + p.items.length, 0);
      return loaded < lastPage.total ? loaded : undefined;
    },
  });

  const items = itemsQuery.data?.pages.flatMap((p) => p.items) ?? [];

  const unreadQuery = useQuery({
    queryKey: ['unread'],
    queryFn: api.unreadCount,
    refetchInterval: 30_000,
  });

  const invalidateAll = () => {
    queryClient.invalidateQueries({ queryKey: ['feeds'] });
    queryClient.invalidateQueries({ queryKey: ['collections'] });
    queryClient.invalidateQueries({ queryKey: ['items'] });
    queryClient.invalidateQueries({ queryKey: ['unread'] });
  };

  const addFeed = useMutation({
    mutationFn: (url: string) => api.addFeed(url),
    onSuccess: invalidateAll,
  });

  const addFeeds = useMutation({
    mutationFn: (urls: string[]) => api.bulkAddFeeds(urls),
    onSuccess: invalidateAll,
  });

  const bulkDeleteFeeds = useMutation({
    mutationFn: (ids: number[]) => api.bulkDeleteFeeds(ids),
    onSuccess: invalidateAll,
  });

  const setFeedCollections = useMutation({
    mutationFn: ({
      feedId,
      collectionIds,
    }: {
      feedId: number;
      collectionIds: number[];
    }) => api.setFeedCollections(feedId, collectionIds),
    onSuccess: invalidateAll,
  });

  const createCollection = useMutation({
    mutationFn: (name: string) => api.createCollection(name),
    onSuccess: invalidateAll,
  });

  const renameCollection = useMutation({
    mutationFn: ({ id, name }: { id: number; name: string }) =>
      api.renameCollection(id, name),
    onSuccess: invalidateAll,
  });

  const deleteCollection = useMutation({
    mutationFn: (id: number) => api.deleteCollection(id),
    onSuccess: (_data, deletedId) => {
      if (selectedCollectionId === deletedId) setSelectedCollectionId(null);
      invalidateAll();
    },
  });

  const setRead = useMutation({
    mutationFn: ({ id, read }: { id: number; read: boolean }) =>
      api.setItemRead(id, read),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['items'] });
      queryClient.invalidateQueries({ queryKey: ['unread'] });
    },
  });

  // Bulk mark-read fired as items scroll fully past the viewport (all/read
  // views) or via the "Mark visible as read" button (unread queue view). We
  // flip the read flag in the cache immediately (no refetch) so the dot
  // disappears as soon as an item is passed, then persist in the background.
  const markRead = useMutation({
    mutationFn: (ids: number[]) => api.bulkSetItemsRead(ids, true),
    onMutate: (ids) => {
      const set = new Set(ids);
      queryClient.setQueriesData(
        { queryKey: ['items'] },
        (old: { pages: ItemsResponse[] } | undefined) =>
          applyMarkedRead(old, set, false),
      );
      if (readFilter === 'unread') {
        queryClient.setQueryData(
          ['items', selectedFeedId, selectedCollectionId, 'unread'],
          (old: { pages: ItemsResponse[] } | undefined) =>
            applyMarkedRead(old, set, true),
        );
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['unread'] });
    },
    onError: () => {
      queryClient.invalidateQueries({ queryKey: ['items'] });
      queryClient.invalidateQueries({ queryKey: ['unread'] });
    },
  });

  const refresh = useMutation({
    mutationFn: () => api.refresh(),
    onSuccess: invalidateAll,
  });

  const selectedFeed =
    feedsQuery.data?.find((f) => f.id === selectedFeedId) ?? null;
  const selectedCollection =
    collectionsQuery.data?.find((c) => c.id === selectedCollectionId) ?? null;

  // Selecting a feed clears the active collection, and vice versa, so the two
  // never compete for the item list. On phones the nav is an overlay, so pick a
  // destination and slide it away.
  const closeSidebarOnMobile = () => {
    if (typeof window !== 'undefined' && window.innerWidth < 768) {
      setSidebarOpen(false);
    }
  };
  const handleSelectFeed = (id: number | null) => {
    setSelectedFeedId(id);
    setSelectedCollectionId(null);
    closeSidebarOnMobile();
  };
  const handleSelectCollection = (id: number | null) => {
    setSelectedCollectionId(id);
    setSelectedFeedId(null);
    closeSidebarOnMobile();
  };

  function handleOpen(id: number, item: Item) {
    if (!item.read) setRead.mutate({ id, read: true });
    if (item.link) window.open(item.link, '_blank', 'noopener,noreferrer');
  }

  // Explicitly mark the unread items currently in the list's viewport as
  // read (used by the Unread queue view, where auto-marking is off).
  function markVisibleRead() {
    const root = document.querySelector<HTMLElement>('.items-scroll');
    if (!root) return;
    const rootBox = root.getBoundingClientRect();
    const ids: number[] = [];
    root.querySelectorAll<HTMLElement>('li[data-id]').forEach((el) => {
      if (el.dataset.read === 'true') return;
      const b = el.getBoundingClientRect();
      if (b.bottom <= rootBox.top || b.top >= rootBox.bottom) return;
      const id = Number(el.dataset.id);
      if (Number.isFinite(id)) ids.push(id);
    });
    if (ids.length > 0) markRead.mutate(ids);
  }

  return (
    <div className="app">
      <Sidebar
        open={sidebarOpen}
        feeds={feedsQuery.data ?? []}
        collections={collectionsQuery.data ?? []}
        selectedFeedId={selectedFeedId}
        selectedCollectionId={selectedCollectionId}
        onSelectFeed={handleSelectFeed}
        onSelectCollection={handleSelectCollection}
        onAdd={(url) => addFeed.mutate(url)}
        addPending={addFeed.isPending}
        addError={addFeed.isError ? addFeed.error.message : null}
        onBulkAdd={(urls) => addFeeds.mutate(urls)}
        bulkAddPending={addFeeds.isPending}
        bulkAddError={addFeeds.isError ? addFeeds.error.message : null}
        onBulkDelete={(ids) => bulkDeleteFeeds.mutate(ids)}
        bulkDeletePending={bulkDeleteFeeds.isPending}
        onSetFeedCollections={(feedId, collectionIds) =>
          setFeedCollections.mutate({ feedId, collectionIds })
        }
        onCreateCollection={(name) => createCollection.mutate(name)}
        onRenameCollection={(id, name) => renameCollection.mutate({ id, name })}
        onDeleteCollection={(id) => deleteCollection.mutate(id)}
        collectionPending={
          createCollection.isPending ||
          renameCollection.isPending ||
          deleteCollection.isPending
        }
      />

      <main className="main">
        <header className="topbar">
          <button
            className="menu-btn"
            onClick={() => setSidebarOpen((o) => !o)}
            aria-label={sidebarOpen ? 'Close menu' : 'Open menu'}
            aria-expanded={sidebarOpen}
          >
            ☰
          </button>
          <h1>
            {selectedCollection
              ? selectedCollection.name
              : selectedFeed
                ? selectedFeed.title
                : 'All feeds'}
          </h1>
          <div
            className="read-filter"
            role="group"
            aria-label="Filter items by read state"
          >
            {READ_FILTERS.map(({ value, label }) => (
              <button
                key={value}
                className={readFilter === value ? 'active' : ''}
                aria-pressed={readFilter === value}
                onClick={() => setReadFilter(value)}
              >
                {label}
              </button>
            ))}
          </div>
          <div className="topbar-actions">
            <span className="unread-badge" title="Total unread items">
              {unreadQuery.data ? unreadQuery.data.count : 0} unread
            </span>
            <button
              className="btn"
              onClick={() => refresh.mutate()}
              disabled={refresh.isPending}
            >
              {refresh.isPending ? 'Refreshing…' : 'Refresh'}
            </button>
          </div>
        </header>

        {refresh.isError && (
          <div className="error-bar">
            Refresh failed: {refresh.error.message}
          </div>
        )}

        {readFilter === 'unread' && (
          <div className="queue-bar">
            <button className="btn" onClick={markVisibleRead}>
              Mark visible as read
            </button>
          </div>
        )}
        <ItemList
          items={items}
          loading={itemsQuery.isLoading}
          error={itemsQuery.isError ? itemsQuery.error.message : null}
          onOpen={handleOpen}
          onMarkRead={(ids) => markRead.mutate(ids)}
          autoMark={readFilter !== 'unread'}
          hasMore={itemsQuery.hasNextPage}
          loadingMore={itemsQuery.isFetchingNextPage}
          onLoadMore={() => {
            if (itemsQuery.hasNextPage && !itemsQuery.isFetchingNextPage) {
              itemsQuery.fetchNextPage();
            }
          }}
        />
      </main>

      {sidebarOpen && (
        <div className="backdrop" onClick={() => setSidebarOpen(false)} />
      )}
    </div>
  );
}
