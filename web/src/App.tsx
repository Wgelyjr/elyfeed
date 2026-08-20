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

  const feedsQuery = useQuery({ queryKey: ['feeds'], queryFn: api.listFeeds });

  const itemsQuery = useQuery({
    queryKey: ['items', selectedFeedId],
    queryFn: () =>
      api.listItems({ feed_id: selectedFeedId ?? undefined, limit: 200 }),
  });

  const unreadQuery = useQuery({
    queryKey: ['unread'],
    queryFn: api.unreadCount,
    refetchInterval: 30_000,
  });

  const invalidateAll = () => {
    queryClient.invalidateQueries({ queryKey: ['feeds'] });
    queryClient.invalidateQueries({ queryKey: ['items'] });
    queryClient.invalidateQueries({ queryKey: ['unread'] });
  };

  const addFeed = useMutation({
    mutationFn: (url: string) => api.addFeed(url),
    onSuccess: invalidateAll,
  });

  const deleteFeed = useMutation({
    mutationFn: (id: number) => api.deleteFeed(id),
    onSuccess: invalidateAll,
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

  function handleOpen(id: number, item: Item) {
    if (!item.read) setRead.mutate({ id, read: true });
    if (item.link) window.open(item.link, '_blank', 'noopener,noreferrer');
  }

  return (
    <div className="app">
      <Sidebar
        feeds={feedsQuery.data ?? []}
        selectedFeedId={selectedFeedId}
        onSelect={setSelectedFeedId}
        onAdd={(url) => addFeed.mutate(url)}
        addPending={addFeed.isPending}
        addError={addFeed.isError ? addFeed.error.message : null}
        onDelete={(id) => deleteFeed.mutate(id)}
      />

      <main className="main">
        <header className="topbar">
          <h1>{selectedFeed ? selectedFeed.title : 'All feeds'}</h1>
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
