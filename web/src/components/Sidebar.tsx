import { useEffect, useRef, useState, type FormEvent } from 'react';
import type {
  Collection,
  CollectionShareRequest,
  Feed,
  PublicCollection,
} from '../types';

interface Props {
  open: boolean;
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
  // Shareable collections
  onCreateCollectionShare: (id: number) => void;
  collectionShare: { id: number; link: string } | null;
  // Public/private collection visibility + community directory
  publicCollections: PublicCollection[];
  onImportCollection: (id: number) => void;
  importCollectionPending: boolean;
  onRequestCollectionPublic: (id: number) => void;
  onRequestCollectionPrivate: (id: number) => void;
  onCancelCollectionVisibilityRequest: (id: number) => void;
  collectionVisibilityPending: boolean;
  // Admin queue for pending collection-visibility changes
  pendingCollectionShares: CollectionShareRequest[];
  onApproveCollectionShare: (id: number) => void;
  onRejectCollectionShare: (id: number) => void;
  collectionModerationPending: boolean;
}

export default function Sidebar({
  open,
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
  onCreateCollectionShare,
  collectionShare,
  publicCollections,
  onImportCollection,
  importCollectionPending,
  onRequestCollectionPublic,
  onRequestCollectionPrivate,
  onCancelCollectionVisibilityRequest,
  collectionVisibilityPending,
  pendingCollectionShares,
  onApproveCollectionShare,
  onRejectCollectionShare,
  collectionModerationPending,
}: Props) {
  const [url, setUrl] = useState('');
  const [bulkAddOpen, setBulkAddOpen] = useState(false);
  const [bulkAddText, setBulkAddText] = useState('');

  const [manageMode, setManageMode] = useState(false);
  const [selectedForDelete, setSelectedForDelete] = useState<Set<number>>(
    new Set(),
  );
  const [expandedFeedId, setExpandedFeedId] = useState<number | null>(null);
  const [collapsedCollections, setCollapsedCollections] = useState<Set<number>>(
    new Set(),
  );

  const [creatingCollection, setCreatingCollection] = useState(false);
  const [newCollectionName, setNewCollectionName] = useState('');
  const [renamingCollectionId, setRenamingCollectionId] = useState<
    number | null
  >(null);
  const [renameName, setRenameName] = useState('');

  // Rename/delete are two-tap: the first tap arms the button, the second
  // confirms, so a stray tap on a row can't rename or delete a collection.
  const [armedAction, setArmedAction] = useState<{
    id: number;
    action: 'rename' | 'delete';
  } | null>(null);
  const armTimer = useRef<number | null>(null);
  function arm(c: Collection, action: 'rename' | 'delete') {
    if (armTimer.current) window.clearTimeout(armTimer.current);
    setArmedAction({ id: c.id, action });
    armTimer.current = window.setTimeout(() => setArmedAction(null), 4000);
  }
  function disarm() {
    if (armTimer.current) window.clearTimeout(armTimer.current);
    setArmedAction(null);
  }
  useEffect(() => {
    if (!armedAction) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setArmedAction(null);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [armedAction]);
  useEffect(
    () => () => {
      if (armTimer.current) window.clearTimeout(armTimer.current);
    },
    [],
  );

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

  function toggleCollapsedCollection(id: number) {
    setCollapsedCollections((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  function renderFeed(f: Feed, depth: number, key: string) {
    const title = f.title || f.url;
    return (
      <div className="feed-block" key={key} style={{ paddingLeft: depth * 14 }}>
        <div className="feed-row">
          <button
            className={
              selectedFeedId === f.id ? 'feed-item active' : 'feed-item'
            }
            onClick={() => onSelectFeed(f.id)}
            title={f.url}
          >
            {title}
          </button>
          <button
            type="button"
            className="icon-btn"
            onClick={() => setExpandedFeedId((id) => (id === f.id ? null : f.id))}
            title="Organize into collections"
            aria-label={`Organize ${title}`}
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
    );
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

  // Describes the visibility pill for one of the user's collections.
  function collectionVisibilityMeta(c: Collection): {
    label: string;
    title: string;
    disabled: boolean;
    onClick: () => void;
  } {
    switch (c.visibility_status) {
      case 'public':
        return {
          label: 'public',
          title:
            'Public in the community directory. Click to request unpublication.',
          disabled: false,
          onClick: () => onRequestCollectionPrivate(c.id),
        };
      case 'pending':
        return {
          label: 'pending',
          title:
            (c.visibility_requested === 'private'
              ? 'Unpublishing awaits admin approval'
              : 'Publishing awaits admin approval') + ' · Click to cancel',
          disabled: false,
          onClick: () => onCancelCollectionVisibilityRequest(c.id),
        };
      default:
        return {
          label: 'private',
          title: 'Click to make public (needs admin approval)',
          disabled: false,
          onClick: () => onRequestCollectionPublic(c.id),
        };
    }
  }

  const feedsByCollection = new Map<number, Feed[]>();
  for (const c of collections) feedsByCollection.set(c.id, []);
  for (const f of feeds) {
    for (const cid of f.collection_ids) feedsByCollection.get(cid)?.push(f);
  }
  const collectionlessFeeds = feeds.filter(
    (f) => f.collection_ids.length === 0,
  );

  return (
    <aside className={open ? 'sidebar open' : 'sidebar'}>
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

      {/* Feeds: collections and feeds in one tree */}
      <section className="side-section">
        <div className="section-head">
          <span className="section-title">Feeds</span>
          <div className="head-actions">
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

        {manageMode ? (
          <nav className="feed-nav">
            {feeds.map((f) => (
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
            ))}
            {feeds.length === 0 && (
              <div className="muted pad">No feeds yet. Add one above.</div>
            )}
          </nav>
        ) : (
          <nav className="feed-nav">
            <button
              className={
                selectedFeedId === null && selectedCollectionId === null
                  ? 'feed-item active'
                  : 'feed-item'
              }
              onClick={() => onSelectFeed(null)}
            >
              All feeds
            </button>

            {collections.map((c) => {
              const isCollapsed = collapsedCollections.has(c.id);
              const feedsIn = feedsByCollection.get(c.id) ?? [];
              return (
                <div className="coll-tree" key={c.id}>
                  {renamingCollectionId === c.id ? (
                    <form
                      className="inline-form"
                      style={{ paddingLeft: 14 }}
                      onSubmit={submitRename}
                    >
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
                      <button
                        type="button"
                        className="btn tiny"
                        onClick={cancelRename}
                      >
                        Cancel
                      </button>
                    </form>
                  ) : (
                    <div className="coll-row">
                      <button
                        type="button"
                        className="chevron"
                        onClick={() => toggleCollapsedCollection(c.id)}
                        aria-label={
                          isCollapsed
                            ? `Expand ${c.name}`
                            : `Collapse ${c.name}`
                        }
                      >
                        {isCollapsed ? '▸' : '▾'}
                      </button>
                      <button
                        className={
                          selectedCollectionId === c.id
                            ? 'nav-item active'
                            : 'nav-item'
                        }
                        onClick={() => {
                          disarm();
                          onSelectCollection(c.id);
                        }}
                        title={c.name}
                      >
                        <span className="nav-label">{c.name}</span>
                        <span className="count">{c.feed_count}</span>
                      </button>
                      {(() => {
                        const v = collectionVisibilityMeta(c);
                        return (
                          <button
                            type="button"
                            className={`coll-vis ${c.visibility_status}`}
                            onClick={v.onClick}
                            title={v.title}
                            aria-label={`${v.label} — ${c.name}`}
                            disabled={v.disabled || collectionVisibilityPending}
                          >
                            {v.label}
                          </button>
                        );
                      })()}
                      <div className="row-actions">
                        <button
                          type="button"
                          className="icon-btn"
                          onClick={() => onCreateCollectionShare(c.id)}
                          title={
                            collectionShare?.id === c.id
                              ? `Link copied: ${collectionShare.link}`
                              : 'Copy a share link for this collection'
                          }
                          aria-label={`Share ${c.name}`}
                          disabled={collectionPending}
                        >
                          {collectionShare?.id === c.id ? '✓' : '⤴'}
                        </button>
                        {(() => {
                          const armed =
                            armedAction?.id === c.id &&
                            armedAction.action === 'rename';
                          return (
                            <button
                              type="button"
                              className={armed ? 'icon-btn confirm' : 'icon-btn'}
                              onClick={() => {
                                if (armed) {
                                  disarm();
                                  startRename(c);
                                } else {
                                  arm(c, 'rename');
                                }
                              }}
                              title={
                                armed
                                  ? `Click again to rename ${c.name}`
                                  : 'Rename collection (click twice to confirm)'
                              }
                              aria-label={`Rename ${c.name}`}
                            >
                              ✎
                            </button>
                          );
                        })()}
                        {(() => {
                          const armed =
                            armedAction?.id === c.id &&
                            armedAction.action === 'delete';
                          return (
                            <button
                              type="button"
                              className={
                                armed ? 'icon-btn danger confirm' : 'icon-btn danger'
                              }
                              onClick={() => {
                                if (armed) {
                                  disarm();
                                  onDeleteCollection(c.id);
                                } else {
                                  arm(c, 'delete');
                                }
                              }}
                              title={
                                armed
                                  ? `Click again to delete ${c.name}`
                                  : 'Delete collection (click twice to confirm)'
                              }
                              aria-label={`Delete ${c.name}`}
                            >
                              {armed ? '✓' : '×'}
                            </button>
                          );
                        })()}
                      </div>
                    </div>
                  )}
                  {!isCollapsed &&
                    renamingCollectionId !== c.id &&
                    feedsIn.map((f) => renderFeed(f, 2, `c${c.id}-${f.id}`))}
                </div>
              );
            })}

            {collectionlessFeeds.map((f) => renderFeed(f, 1, `root-${f.id}`))}

            {feeds.length === 0 && (
              <div className="muted pad">No feeds yet. Add one above.</div>
            )}
          </nav>
        )}
      </section>

      {/* Public collections: community directory (one-click import) */}
      <section className="side-section">
        <div className="section-head">
          <span className="section-title">Public Collections</span>
        </div>
        <div className="shared-list">
          {publicCollections.map((c) => (
            <div className="shared-row" key={c.id}>
              <div className="mod-info">
                <span className="feed-title" title={c.name}>
                  {c.name}
                </span>
                <span className="mod-meta">
                  {c.owner_name} · {c.feed_count} feeds
                </span>
              </div>
              <button
                type="button"
                className="btn tiny"
                onClick={() => onImportCollection(c.id)}
                disabled={importCollectionPending}
                title={`Import all feeds from ${c.name}`}
              >
                {importCollectionPending ? '…' : 'Import'}
              </button>
            </div>
          ))}
          {publicCollections.length === 0 && (
            <div className="muted small pad-x">No public collections yet.</div>
          )}
        </div>
      </section>

      {/* Admin moderation queue for pending collection visibility changes */}
      {pendingCollectionShares.length > 0 ? (
        <section className="side-section moderation">
          <div className="section-head">
            <span className="section-title">Approval queue</span>
            <span className="count">{pendingCollectionShares.length}</span>
          </div>
          <div className="mod-list">
            {pendingCollectionShares.map((p) => (
              <div className="mod-row" key={`coll-${p.collection_id}`}>
                <div className="mod-info">
                  <span className="feed-title" title={p.name}>
                    {p.name}
                  </span>
                  <span className="mod-meta">
                    collection ·{' '}
                    {p.requested === 'public'
                      ? 'wants to publish'
                      : 'wants to unpublish'}{' '}
                    · {p.owner_email}
                  </span>
                </div>
                <div className="mod-actions">
                  <button
                    type="button"
                    className="btn tiny"
                    onClick={() => onApproveCollectionShare(p.collection_id)}
                    disabled={collectionModerationPending}
                  >
                    Approve
                  </button>
                  <button
                    type="button"
                    className="btn tiny danger"
                    onClick={() => onRejectCollectionShare(p.collection_id)}
                    disabled={collectionModerationPending}
                  >
                    Reject
                  </button>
                </div>
              </div>
            ))}
          </div>
        </section>
      ) : null}
    </aside>
  );
}
