package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres is a Store backed by a Postgres database.
type Postgres struct {
	pool *pgxpool.Pool
}

var _ Store = (*Postgres)(nil)

// NewPostgres wraps a connection pool with the Store implementation.
func NewPostgres(pool *pgxpool.Pool) *Postgres {
	return &Postgres{pool: pool}
}

func (s *Postgres) CreateFeed(ctx context.Context, url, title, siteURL string) (*Feed, error) {
	var f Feed
	err := s.pool.QueryRow(ctx,
		`INSERT INTO feeds (url, title, site_url) VALUES ($1, $2, $3)
		 ON CONFLICT (url) DO UPDATE SET title = EXCLUDED.title, site_url = EXCLUDED.site_url
		 RETURNING id, url, title, site_url, last_fetched, created_at`,
		url, title, siteURL,
	).Scan(&f.ID, &f.URL, &f.Title, &f.SiteURL, &f.LastFetched, &f.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create feed: %w", err)
	}
	return &f, nil
}

func (s *Postgres) ListFeeds(ctx context.Context) ([]Feed, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, url, title, site_url, last_fetched, created_at
		 FROM feeds ORDER BY title ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list feeds: %w", err)
	}
	defer rows.Close()

	var out []Feed
	for rows.Next() {
		var f Feed
		if err := rows.Scan(&f.ID, &f.URL, &f.Title, &f.SiteURL, &f.LastFetched, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan feed: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate feeds: %w", err)
	}
	return out, nil
}

func (s *Postgres) DeleteFeed(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM feeds WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete feed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Postgres) TouchFeed(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE feeds SET last_fetched = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("touch feed: %w", err)
	}
	return nil
}

func (s *Postgres) UpsertItems(ctx context.Context, feedID int64, items []IncomingItem) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op if committed

	const upsertSQL = `INSERT INTO items (feed_id, guid, title, link, content, author, published_at)
	 VALUES ($1, $2, $3, $4, $5, $6, $7)
	 ON CONFLICT (feed_id, guid) DO NOTHING`

	inserted := 0
	for _, it := range items {
		tag, err := tx.Exec(ctx, upsertSQL, feedID, it.GUID, it.Title, it.Link, it.Content, it.Author, it.PublishedAt)
		if err != nil {
			return 0, fmt.Errorf("upsert item: %w", err)
		}
		inserted += int(tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit tx: %w", err)
	}
	return inserted, nil
}

func (s *Postgres) ListItems(ctx context.Context, q ItemQuery) ([]Item, int, error) {
	var conds []string
	var args []any

	if q.FeedID != nil {
		conds = append(conds, fmt.Sprintf("i.feed_id = $%d", len(args)+1))
		args = append(args, *q.FeedID)
	}
	if q.Unread != nil {
		conds = append(conds, fmt.Sprintf("i.read = $%d", len(args)+1))
		args = append(args, *q.Unread)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	countSQL := `SELECT count(*) FROM items i` + where
	var total int
	if err := s.pool.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count items: %w", err)
	}

	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	limitArg := len(args) + 1
	offsetArg := len(args) + 2
	args = append(args, limit, q.Offset)

	listSQL := `
		SELECT i.id, i.feed_id, COALESCE(f.title, ''), i.guid, i.title, i.link,
		       i.content, i.author, i.published_at, i.fetched_at, i.read
		FROM items i
		LEFT JOIN feeds f ON f.id = i.feed_id` + where + `
		ORDER BY (i.published_at IS NULL) ASC, i.published_at DESC NULLS LAST, i.fetched_at DESC
		LIMIT $` + itoa(limitArg) + ` OFFSET $` + itoa(offsetArg)

	rows, err := s.pool.Query(ctx, listSQL, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list items: %w", err)
	}
	defer rows.Close()

	out := make([]Item, 0, limit)
	for rows.Next() {
		var it Item
		if err := rows.Scan(
			&it.ID, &it.FeedID, &it.FeedTitle, &it.GUID, &it.Title, &it.Link,
			&it.Content, &it.Author, &it.PublishedAt, &it.FetchedAt, &it.Read,
		); err != nil {
			return nil, 0, fmt.Errorf("scan item: %w", err)
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate items: %w", err)
	}
	return out, total, nil
}

func (s *Postgres) SetItemRead(ctx context.Context, id int64, read bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE items SET read = $2 WHERE id = $1`, id, read)
	if err != nil {
		return fmt.Errorf("set item read: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Postgres) UnreadCount(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM items WHERE read = false`).Scan(&n); err != nil {
		return 0, fmt.Errorf("unread count: %w", err)
	}
	return n, nil
}

// ErrNotFound is returned when an operation targets a row that does not exist.
var ErrNotFound = errors.New("not found")

// itoa is a tiny int-to-string helper used to build positional SQL parameters.
func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
