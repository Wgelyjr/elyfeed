import { useState, type FormEvent } from 'react';
import type { Feed } from '../types';

interface Props {
  feeds: Feed[];
  selectedFeedId: number | null;
  onSelect: (id: number | null) => void;
  onAdd: (url: string) => void;
  addPending: boolean;
  addError: string | null;
  onDelete: (id: number) => void;
}

export default function Sidebar({
  feeds,
  selectedFeedId,
  onSelect,
  onAdd,
  addPending,
  addError,
  onDelete,
}: Props) {
  const [url, setUrl] = useState('');

  function submit(e: FormEvent) {
    e.preventDefault();
    const trimmed = url.trim();
    if (!trimmed || addPending) return;
    onAdd(trimmed);
    setUrl('');
  }

  return (
    <aside className="sidebar">
      <div className="brand">elyfeed</div>

      <form className="add-feed" onSubmit={submit}>
        <input
          type="url"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          placeholder="Add feed URL"
          aria-label="Feed URL"
        />
        <button type="submit" className="btn" disabled={addPending || !url.trim()}>
          {addPending ? 'Adding…' : 'Add'}
        </button>
      </form>
      {addError && <div className="form-error">{addError}</div>}

      <nav className="feed-nav">
        <button
          className={selectedFeedId === null ? 'feed-item active' : 'feed-item'}
          onClick={() => onSelect(null)}
        >
          All feeds
        </button>

        {feeds.map((f) => (
          <div className="feed-row" key={f.id}>
            <button
              className={
                selectedFeedId === f.id ? 'feed-item active' : 'feed-item'
              }
              onClick={() => onSelect(f.id)}
              title={f.url}
            >
              {f.title || f.url}
            </button>
            <button
              className="feed-delete"
              onClick={() => onDelete(f.id)}
              title="Remove feed"
              aria-label={`Remove ${f.title || f.url}`}
            >
              ×
            </button>
          </div>
        ))}

        {feeds.length === 0 && (
          <div className="muted pad">No feeds yet. Add one above.</div>
        )}
      </nav>
    </aside>
  );
}
