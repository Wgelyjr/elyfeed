package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	f.CollectionIDs = []int64{}
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

	out := make([]Feed, 0)
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

	ids := make([]int64, len(out))
	for i, f := range out {
		ids[i] = f.ID
	}
	byFeed, err := s.feedCollectionMap(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if cids := byFeed[out[i].ID]; len(cids) > 0 {
			out[i].CollectionIDs = cids
		} else {
			out[i].CollectionIDs = []int64{}
		}
	}
	return out, nil
}

// feedCollectionMap returns, for the given feed IDs, the collection IDs each
// belongs to (empty map entries mean no membership).
func (s *Postgres) feedCollectionMap(ctx context.Context, feedIDs []int64) (map[int64][]int64, error) {
	m := make(map[int64][]int64, len(feedIDs))
	if len(feedIDs) == 0 {
		return m, nil
	}
	ph := placeholders(len(feedIDs))
	rows, err := s.pool.Query(ctx,
		`SELECT feed_id, collection_id FROM feed_collections
		 WHERE feed_id IN (`+ph+`)`, toAny(feedIDs)...)
	if err != nil {
		return nil, fmt.Errorf("list feed collections: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var feedID, collID int64
		if err := rows.Scan(&feedID, &collID); err != nil {
			return nil, fmt.Errorf("scan feed collection: %w", err)
		}
		m[feedID] = append(m[feedID], collID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate feed collections: %w", err)
	}
	return m, nil
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

func (s *Postgres) DeleteFeeds(ctx context.Context, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	ph := placeholders(len(ids))
	tag, err := s.pool.Exec(ctx, `DELETE FROM feeds WHERE id IN (`+ph+`)`, toAny(ids)...)
	if err != nil {
		return 0, fmt.Errorf("delete feeds: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// SetFeedCollections replaces the collections a feed belongs to.
func (s *Postgres) SetFeedCollections(ctx context.Context, feedID int64, collectionIDs []int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op if committed

	if _, err := tx.Exec(ctx, `DELETE FROM feed_collections WHERE feed_id = $1`, feedID); err != nil {
		return fmt.Errorf("clear feed collections: %w", err)
	}
	for _, cid := range collectionIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO feed_collections (feed_id, collection_id) VALUES ($1, $2)
			 ON CONFLICT (feed_id, collection_id) DO NOTHING`, feedID, cid); err != nil {
			return fmt.Errorf("set feed collection: %w", err)
		}
	}
	return tx.Commit(ctx)
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
	if q.CollectionID != nil {
		conds = append(conds, fmt.Sprintf(
			"i.feed_id IN (SELECT feed_id FROM feed_collections WHERE collection_id = $%d)", len(args)+1))
		args = append(args, *q.CollectionID)
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

// --- collections ---

func (s *Postgres) CreateCollection(ctx context.Context, name string) (*Collection, error) {
	var c Collection
	err := s.pool.QueryRow(ctx,
		`INSERT INTO collections (name) VALUES ($1)
		 RETURNING id, name, created_at`, name,
	).Scan(&c.ID, &c.Name, &c.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w: %q already exists", ErrConflict, name)
		}
		return nil, fmt.Errorf("create collection: %w", err)
	}
	return &c, nil
}

func (s *Postgres) ListCollections(ctx context.Context) ([]Collection, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT c.id, c.name, c.created_at, count(f.feed_id)
		 FROM collections c
		 LEFT JOIN feed_collections f ON f.collection_id = c.id
		 GROUP BY c.id, c.name, c.created_at
		 ORDER BY c.name ASC, c.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	defer rows.Close()

	out := make([]Collection, 0)
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.Name, &c.CreatedAt, &c.FeedCount); err != nil {
			return nil, fmt.Errorf("scan collection: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate collections: %w", err)
	}
	return out, nil
}

func (s *Postgres) RenameCollection(ctx context.Context, id int64, name string) (*Collection, error) {
	var c Collection
	err := s.pool.QueryRow(ctx,
		`UPDATE collections AS c SET name = $2 WHERE c.id = $1
		 RETURNING c.id, c.name, c.created_at,
		 (SELECT count(fc.feed_id) FROM feed_collections fc WHERE fc.collection_id = c.id)`,
		id, name,
	).Scan(&c.ID, &c.Name, &c.CreatedAt, &c.FeedCount)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w: %q already exists", ErrConflict, name)
		}
		if rowsAffectedZero(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("rename collection: %w", err)
	}
	return &c, nil
}

func (s *Postgres) DeleteCollection(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM collections WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete collection: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ErrNotFound is returned when an operation targets a row that does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned when a uniqueness constraint would be violated
// (e.g. creating a collection whose name already exists).
var ErrConflict = errors.New("conflict")

// itoa is a tiny int-to-string helper used to build positional SQL parameters.
func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

// toAny converts []int64 to []any so it can be passed to pgx's variadic
// Query/Exec arguments.
func toAny(ids []int64) []any {
	out := make([]any, len(ids))
	for i, v := range ids {
		out[i] = v
	}
	return out
}

// placeholders builds "$1,$2,...,$n" for use in IN (...) clauses.
func placeholders(n int) string {
	b := make([]string, n)
	for i := range b {
		b[i] = "$" + itoa(i+1)
	}
	return strings.Join(b, ",")
}

// isUniqueViolation reports whether err is a Postgres unique-constraint error.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// rowsAffectedZero reports whether a QueryRow scan failed because no row was
// returned (pgx surfaces this as ErrNoRows).
func rowsAffectedZero(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
