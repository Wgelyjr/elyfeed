import type { Item } from '../types';

interface Props {
  items: Item[];
  loading: boolean;
  error: string | null;
  onOpen: (id: number, item: Item) => void;
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

export default function ItemList({ items, loading, error, onOpen }: Props) {
  if (loading) return <div className="muted pad">Loading…</div>;
  if (error) return <div className="error-bar pad">Failed to load: {error}</div>;
  if (items.length === 0) {
    return (
      <div className="muted pad">
        No items yet. Add a feed, then hit Refresh.
      </div>
    );
  }

  return (
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
  );
}
