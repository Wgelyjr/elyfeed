import { useState, type FormEvent } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../api';

// First-run experience for a brand-new account (no feeds yet). It offers the
// admin-curated recommendations, the community shared-feed directory, direct
// URL add, and import of a shared collection. Adding a feed (or importing)
// flips the app out of onboarding because the parent watches the feed count.
interface Props {
  onDismiss: () => void;
}

// Accepts a raw token or a full share link and returns the token portion.
function extractToken(input: string): string {
  const t = input.trim();
  if (!t) return '';
  if (/^[a-f0-9]{16,}$/i.test(t)) return t;
  try {
    const u = new URL(t);
    const q = u.searchParams.get('share') || u.searchParams.get('import');
    if (q) return q;
    return u.pathname.split('/').filter(Boolean).pop() ?? '';
  } catch {
    return t;
  }
}

export default function Onboarding({ onDismiss }: Props) {
  const queryClient = useQueryClient();

  const recommendedQuery = useQuery({
    queryKey: ['recommended-feeds'],
    queryFn: api.listRecommendedFeeds,
  });
  const sharedQuery = useQuery({
    queryKey: ['shared-feeds'],
    queryFn: api.listSharedFeeds,
  });

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ['feeds'] });
    queryClient.invalidateQueries({ queryKey: ['collections'] });
    queryClient.invalidateQueries({ queryKey: ['items'] });
    queryClient.invalidateQueries({ queryKey: ['unread'] });
  };

  const addFeed = useMutation({
    mutationFn: (url: string) => api.addFeed(url),
    onSuccess: invalidate,
  });
  const importCollection = useMutation({
    mutationFn: ({ token, name }: { token: string; name?: string }) =>
      api.importCollection(token, name),
    onSuccess: invalidate,
  });

  const [url, setUrl] = useState('');
  const [token, setToken] = useState('');
  const [importName, setImportName] = useState('');

  function submitUrl(e: FormEvent) {
    e.preventDefault();
    const u = url.trim();
    if (!u || addFeed.isPending) return;
    addFeed.mutate(u);
    setUrl('');
  }

  function submitImport(e: FormEvent) {
    e.preventDefault();
    const t = extractToken(token);
    if (!t || importCollection.isPending) return;
    importCollection.mutate({ token: t, name: importName.trim() || undefined });
    setToken('');
    setImportName('');
  }

  const addError = addFeed.isError ? addFeed.error.message : null;
  const importError = importCollection.isError
    ? importCollection.error.message
    : null;

  return (
    <div className="onboard">
      <div className="onboard-card">
        <div className="onboard-brand">elyfeed</div>
        <h2>Welcome to elyfeed</h2>
        <p className="onboard-sub">
          Add a feed by URL, pick from the recommendations, browse what the
          community has shared, or import a shared collection.
        </p>

        <form className="onboard-add" onSubmit={submitUrl}>
          <input
            type="url"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            placeholder="https://example.com/feed.xml"
            aria-label="Feed URL"
          />
          <button
            type="submit"
            className="btn"
            disabled={addFeed.isPending || !url.trim()}
          >
            {addFeed.isPending ? 'Adding…' : 'Add feed'}
          </button>
        </form>
        {addError && <div className="form-error">{addError}</div>}

        <section className="onboard-section">
          <h3>Recommended</h3>
          <ul className="onboard-list">
            {recommendedQuery.data?.map((f) => (
              <li key={f.id} className="onboard-row">
                <span className="onboard-feed-title" title={f.url}>
                  {f.title || f.url}
                </span>
                <button
                  className="btn tiny"
                  disabled={addFeed.isPending}
                  onClick={() => addFeed.mutate(f.url)}
                >
                  Add
                </button>
              </li>
            ))}
            {!recommendedQuery.isLoading &&
              (recommendedQuery.data?.length ?? 0) === 0 && (
                <li className="muted small">No recommended feeds yet.</li>
              )}
          </ul>
        </section>

        <section className="onboard-section">
          <h3>Shared by the community</h3>
          <ul className="onboard-list">
            {sharedQuery.data?.map((f) => (
              <li key={f.id} className="onboard-row">
                <span className="onboard-feed-title" title={f.url}>
                  {f.title || f.url}
                </span>
                <button
                  className="btn tiny"
                  disabled={addFeed.isPending}
                  onClick={() => addFeed.mutate(f.url)}
                >
                  Add
                </button>
              </li>
            ))}
            {!sharedQuery.isLoading && (sharedQuery.data?.length ?? 0) === 0 && (
              <li className="muted small">No shared feeds yet.</li>
            )}
          </ul>
        </section>

        <section className="onboard-section">
          <h3>Import a shared collection</h3>
          <form className="onboard-import" onSubmit={submitImport}>
            <input
              value={token}
              onChange={(e) => setToken(e.target.value)}
              placeholder="Paste a share link or token"
              aria-label="Share link or token"
            />
            <input
              value={importName}
              onChange={(e) => setImportName(e.target.value)}
              placeholder="Collection name (optional)"
              aria-label="Collection name"
            />
            <button
              type="submit"
              className="btn"
              disabled={importCollection.isPending || !token.trim()}
            >
              {importCollection.isPending ? 'Importing…' : 'Import'}
            </button>
          </form>
          {importError && <div className="form-error">{importError}</div>}
        </section>

        <div className="onboard-footer">
          <button type="button" className="link" onClick={onDismiss}>
            Skip for now
          </button>
        </div>
      </div>
    </div>
  );
}
