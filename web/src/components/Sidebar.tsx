import { useState, type FormEvent } from 'react';
import type { Collection, Feed } from '../types';

interface Props {
  feeds: Feed[];
  collections: Collection[];
  selectedFeedId: number | null;
  selectedCollectionId: number | null;
  onSelectFeed: (id: number | null) => void;
  onSelectCollection: (id: number | null) => void;
  onAdd: (url: string) => void;
  addPending: boolean;
  addError: string | null;
  onBulkAdd: (urls: string[]) => void;
  bulkAddPending: boolean;
  bulkAddError: string | null;
  onBulkDelete: (ids: number[]) => void;
  bulkDeletePending: boolean;
  onSetFeedCollections: (feedId: number, collectionIds: number[]) => void;
  onCreateCollection: (name: string) => void;
  onRenameCollection: (id: number, name: string) => void;
  onDeleteCollection: (id: number) => void;
  collectionPending: boolean;
}

export default function Sidebar({
  feeds,
  collections,
  selectedFeedId,
  selectedCollectionId,
  onSelectFeed,
  onSelectCollection,
  onAdd,
  addPending,
  addError,
  onBulkAdd,
  bulkAddPending,
  bulkAddError,
  onBulkDelete,
  bulkDeletePending,
  onSetFeedCollections,
  onCreateCollection,
  onRenameCollection,
  onDeleteCollection,
  collectionPending,
}: Props) {
  const [url, setUrl] = useState('');
  const [bulkAddOpen, setBulkAddOpen] = useState(false);
  const [bulkAddText, setBulkAddText] = useState('');

  const [manageMode, setManageMode] = useState(false);
  const [selectedForDelete, setSelectedForDelete] = useState<Set<number>>(
    new Set(),
  );
  const [expandedFeedId, setExpandedFeedId] = useState<number | null>(null);

  const [creatingCollection, setCreatingCollection] = useState(false);
  const [newCollectionName, setNewCollectionName] = useState('');
  const [renamingCollectionId, setRenamingCollectionId] = useState<
    number | null
  >(null);
  const [renameName, setRenameName] = useState('');

  function submit(e: FormEvent) {
    e.preventDefault();
    const trimmed = url.trim();
    if (!trimmed || addPending) return;
    onAdd(trimmed);
    setUrl('');
  }

  function submitBulk(e: FormEvent) {
    e.preventDefault();
    const urls = bulkAddText
      .split('\n')
      .map((s) => s.trim())
      .filter(Boolean);
    if (!urls.length || bulkAddPending) return;
    onBulkAdd(urls);
    setBulkAddText('');
  }

  function toggleDeleteSelection(id: number) {
    setSelectedForDelete((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  function deleteSelected() {
    const ids = [...selectedForDelete];
    if (!ids.length) return;
    onBulkDelete(ids);
    setSelectedForDelete(new Set());
    setManageMode(false);
  }

  function toggleFeedCollection(feed: Feed, collId: number) {
    const has = feed.collection_ids.includes(collId);
    const next = has
      ? feed.collection_ids.filter((id) => id !== collId)
      : [...feed.collection_ids, collId];
    onSetFeedCollections(feed.id, next);
  }

  function submitCreateCollection(e: FormEvent) {
    e.preventDefault();
    const name = newCollectionName.trim();
    if (!name || collectionPending) return;
    onCreateCollection(name);
    setNewCollectionName('');
    setCreatingCollection(false);
  }

  function startRename(c: Collection) {
    setRenamingCollectionId(c.id);
    setRenameName(c.name);
  }

  function cancelRename() {
    setRenamingCollectionId(null);
    setRenameName('');
  }

  function submitRename(e: FormEvent) {
    e.preventDefault();
    const name = renameName.trim();
    if (!name || renamingCollectionId == null || collectionPending) return;
    onRenameCollection(renamingCollectionId, name);
    setRenamingCollectionId(null);
    setRenameName('');
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

      {bulkAddOpen ? (
        <form className="bulk-add" onSubmit={submitBulk}>
          <textarea
            value={bulkAddText}
            onChange={(e) => setBulkAddText(e.target.value)}
            placeholder={'Paste feed URLs, one per line'}
            aria-label="Feed URLs, one per line"
            rows={4}
          />
          <div className="bulk-actions">
            <button
              type="submit"
              className="btn"
              disabled={bulkAddPending || !bulkAddText.trim()}
            >
              {bulkAddPending ? 'Adding…' : 'Add all'}
            </button>
            <button
              type="button"
              className="btn"
              onClick={() => {
                setBulkAddOpen(false);
                setBulkAddText('');
              }}
            >
              Close
            </button>
          </div>
        </form>
      ) : (
        <div className="bulk-toggle">
          <button
            type="button"
            className="btn small"
            onClick={() => setBulkAddOpen(true)}
          >
            + Bulk add
          </button>
        </div>
      )}
      {bulkAddError && <div className="form-error">{bulkAddError}</div>}

      {/* Collections */}
      <section className="side-section">
        <div className="section-head">
          <span className="section-title">Collections</span>
          <button
            type="button"
            className="icon-btn"
            onClick={() => setCreatingCollection(true)}
            title="New collection"
            aria-label="New collection"
            disabled={creatingCollection}
          >
            +
          </button>
        </div>

        {creatingCollection && (
          <form className="inline-form" onSubmit={submitCreateCollection}>
            <input
              value={newCollectionName}
              onChange={(e) => setNewCollectionName(e.target.value)}
              placeholder="Collection name"
              aria-label="New collection name"
              autoFocus
              onKeyDown={(e) => {
                if (e.key === 'Escape') {
                  setCreatingCollection(false);
                  setNewCollectionName('');
                }
              }}
            />
            <button type="submit" className="btn tiny" disabled={collectionPending}>
              Add
            </button>
            <button
              type="button"
              className="btn tiny"
              onClick={() => {
                setCreatingCollection(false);
                setNewCollectionName('');
              }}
            >
              Cancel
            </button>
          </form>
        )}

        <div className="coll-list">
          {collections.map((c) =>
            renamingCollectionId === c.id ? (
              <form className="inline-form" key={c.id} onSubmit={submitRename}>
                <input
                  value={renameName}
                  onChange={(e) => setRenameName(e.target.value)}
                  aria-label={`Rename ${c.name}`}
                  autoFocus
                  onKeyDown={(e) => {
                    if (e.key === 'Escape') cancelRename();
                  }}
                />
                <button type="submit" className="btn tiny" disabled={collectionPending}>
                  Save
                </button>
                <button type="button" className="btn tiny" onClick={cancelRename}>
                  Cancel
                </button>
              </form>
            ) : (
              <div className="coll-row" key={c.id}>
                <button
                  className={
                    selectedCollectionId === c.id
                      ? 'nav-item active'
                      : 'nav-item'
                  }
                  onClick={() => onSelectCollection(c.id)}
                  title={c.name}
                >
                  <span className="nav-label">{c.name}</span>
                  <span className="count">{c.feed_count}</span>
                </button>
                <div className="row-actions">
                  <button
                    type="button"
                    className="icon-btn"
                    onClick={() => startRename(c)}
                    title="Rename collection"
                    aria-label={`Rename ${c.name}`}
                  >
                    ✎
                  </button>
                  <button
                    type="button"
                    className="icon-btn danger"
                    onClick={() => onDeleteCollection(c.id)}
                    title="Delete collection"
                    aria-label={`Delete ${c.name}`}
                  >
                    ×
                  </button>
                </div>
              </div>
            ),
          )}
          {collections.length === 0 && !creatingCollection && (
            <div className="muted small pad-x">No collections yet.</div>
          )}
        </div>
      </section>

      {/* Feeds */}
      <section className="side-section">
        <div className="section-head">
          <span className="section-title">Feeds</span>
          <button
            type="button"
            className="btn tiny"
            onClick={() => {
              setManageMode((m) => !m);
              setSelectedForDelete(new Set());
            }}
          >
            {manageMode ? 'Done' : 'Manage'}
          </button>
        </div>

        {manageMode && selectedForDelete.size > 0 && (
          <button
            className="btn danger small"
            onClick={deleteSelected}
            disabled={bulkDeletePending}
          >
            {bulkDeletePending
              ? 'Deleting…'
              : `Delete ${selectedForDelete.size} selected`}
          </button>
        )}

        <nav className="feed-nav">
          <button
            className={
              !manageMode && selectedFeedId === null && selectedCollectionId === null
                ? 'feed-item active'
                : 'feed-item'
            }
            onClick={() => onSelectFeed(null)}
          >
            All feeds
          </button>

          {feeds.map((f) =>
            manageMode ? (
              <label className="feed-row manage" key={f.id}>
                <input
                  type="checkbox"
                  checked={selectedForDelete.has(f.id)}
                  onChange={() => toggleDeleteSelection(f.id)}
                  aria-label={`Select ${f.title || f.url}`}
                />
                <span className="feed-title" title={f.url}>
                  {f.title || f.url}
                </span>
              </label>
            ) : (
              <div className="feed-block" key={f.id}>
                <div className="feed-row">
                  <button
                    className={
                      selectedFeedId === f.id ? 'feed-item active' : 'feed-item'
                    }
                    onClick={() => onSelectFeed(f.id)}
                    title={f.url}
                  >
                    {f.title || f.url}
                  </button>
                  <button
                    type="button"
                    className="icon-btn"
                    onClick={() =>
                      setExpandedFeedId((id) => (id === f.id ? null : f.id))
                    }
                    title="Organize into collections"
                    aria-label={`Organize ${f.title || f.url}`}
                  >
                    {expandedFeedId === f.id ? '▾' : '▸'}
                  </button>
                </div>
                {expandedFeedId === f.id && (
                  <div className="assign-panel">
                    {collections.length === 0 ? (
                      <div className="muted small pad-x">No collections yet.</div>
                    ) : (
                      collections.map((c) => {
                        const checked = f.collection_ids.includes(c.id);
                        return (
                          <label className="assign-row" key={c.id}>
                            <input
                              type="checkbox"
                              checked={checked}
                              onChange={() => toggleFeedCollection(f, c.id)}
                            />
                            <span>{c.name}</span>
                          </label>
                        );
                      })
                    )}
                  </div>
                )}
              </div>
            ),
          )}

          {feeds.length === 0 && (
            <div className="muted pad">No feeds yet. Add one above.</div>
          )}
        </nav>
      </section>
    </aside>
  );
}
