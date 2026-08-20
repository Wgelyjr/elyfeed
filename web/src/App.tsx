import { useState } from 'react';
import {
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { api } from './api';
import type { Item } from './types';
import Sidebar from './components/Sidebar';
import ItemList from './components/ItemList';

export default function App() {
  const queryClient = useQueryClient();
  const [selectedFeedId, setSelectedFeedId] = useState<number | null>(null);
  const [selectedCollectionId, setSelectedCollectionId] = useState<
    number | null
  >(null);

  const feedsQuery = useQuery({ queryKey: ['feeds'], queryFn: api.listFeeds });
  const collectionsQuery = useQuery({
    queryKey: ['collections'],
    queryFn: api.listCollections,
  });

  // Exactly one of feed/collection selection is active at a time.
  const itemsQuery = useQuery({
    queryKey: ['items', selectedFeedId, selectedCollectionId],
    queryFn: () =>
      api.listItems({
        feed_id: selectedFeedId ?? undefined,
        collection_id: selectedCollectionId ?? undefined,
        limit: 200,
      }),
  });

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

  const refresh = useMutation({
    mutationFn: () => api.refresh(),
    onSuccess: invalidateAll,
  });

  const selectedFeed =
    feedsQuery.data?.find((f) => f.id === selectedFeedId) ?? null;
  const selectedCollection =
    collectionsQuery.data?.find((c) => c.id === selectedCollectionId) ?? null;

  // Selecting a feed clears the active collection, and vice versa, so the two
  // never compete for the item list.
  const handleSelectFeed = (id: number | null) => {
    setSelectedFeedId(id);
    setSelectedCollectionId(null);
  };
  const handleSelectCollection = (id: number | null) => {
    setSelectedCollectionId(id);
    setSelectedFeedId(null);
  };

  function handleOpen(id: number, item: Item) {
    if (!item.read) setRead.mutate({ id, read: true });
    if (item.link) window.open(item.link, '_blank', 'noopener,noreferrer');
  }

  return (
    <div className="app">
      <Sidebar
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
          <h1>
            {selectedCollection
              ? selectedCollection.name
              : selectedFeed
                ? selectedFeed.title
                : 'All feeds'}
          </h1>
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

        <ItemList
          items={itemsQuery.data?.items ?? []}
          loading={itemsQuery.isLoading}
          error={itemsQuery.isError ? itemsQuery.error.message : null}
          onOpen={handleOpen}
        />
      </main>
    </div>
  );
}
