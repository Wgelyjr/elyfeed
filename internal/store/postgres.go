package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

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

// feedColumns is the SELECT/RETURNING column list for a full feed row,
// including its sharing state.
const feedColumns = `id, user_id, url, title, site_url, last_fetched, created_at, share_status, share_requested`

// scanFeed scans a full feed row (see feedColumns); share_requested is NULL
// unless the feed is pending.
func scanFeed(s rowScanner, f *Feed) error {
	var req sql.NullString
	if err := s.Scan(&f.ID, &f.UserID, &f.URL, &f.Title, &f.SiteURL, &f.LastFetched, &f.CreatedAt, &f.ShareStatus, &req); err != nil {
		return err
	}
	if req.Valid {
		f.ShareRequested = &req.String
	} else {
		f.ShareRequested = nil
	}
	return nil
}

// nullableString converts a scanned NULL-able string column to a pointer (nil
// when NULL).
func nullableString(nsql sql.NullString) *string {
	if nsql.Valid {
		return &nsql.String
	}
	return nil
}

func (s *Postgres) CreateFeed(ctx context.Context, userID int64, url, title, siteURL string) (*Feed, error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO feeds (user_id, url, title, site_url) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (user_id, url) DO UPDATE SET title = EXCLUDED.title, site_url = EXCLUDED.site_url
		 RETURNING `+feedColumns,
		userID, url, title, siteURL,
	)
	var f Feed
	if err := scanFeed(row, &f); err != nil {
		return nil, fmt.Errorf("create feed: %w", err)
	}
	f.CollectionIDs = []int64{}
	return &f, nil
}

func (s *Postgres) ListFeeds(ctx context.Context, userID int64) ([]Feed, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+feedColumns+`
		 FROM feeds WHERE user_id = $1 ORDER BY title ASC, id ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list feeds: %w", err)
	}
	defer rows.Close()

	out := make([]Feed, 0)
	for rows.Next() {
		var f Feed
		if err := scanFeed(rows, &f); err != nil {
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

// ListAllFeeds returns every feed across all users, with no collection data.
func (s *Postgres) ListAllFeeds(ctx context.Context) ([]Feed, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, url, title, site_url, last_fetched, created_at FROM feeds`)
	if err != nil {
		return nil, fmt.Errorf("list all feeds: %w", err)
	}
	defer rows.Close()

	out := make([]Feed, 0)
	for rows.Next() {
		var f Feed
		if err := rows.Scan(&f.ID, &f.UserID, &f.URL, &f.Title, &f.SiteURL, &f.LastFetched, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan feed: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate feeds: %w", err)
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

func (s *Postgres) DeleteFeed(ctx context.Context, userID, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM feeds WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete feed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Postgres) DeleteFeeds(ctx context.Context, userID int64, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	ph := placeholders(len(ids))
	sql := `DELETE FROM feeds WHERE user_id = $` + itoa(len(ids)+1) + ` AND id IN (` + ph + `)`
	args := append(toAny(ids), userID)
	tag, err := s.pool.Exec(ctx, sql, args...)
	if err != nil {
		return 0, fmt.Errorf("delete feeds: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// SetFeedCollections replaces the collections a feed belongs to. Only
// collections owned by the user are linked; foreign IDs are ignored.
func (s *Postgres) SetFeedCollections(ctx context.Context, userID, feedID int64, collectionIDs []int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op if committed

	var ok bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM feeds WHERE id = $1 AND user_id = $2)`, feedID, userID,
	).Scan(&ok); err != nil {
		return fmt.Errorf("check feed owner: %w", err)
	}
	if !ok {
		return ErrNotFound
	}

	if _, err := tx.Exec(ctx, `DELETE FROM feed_collections WHERE feed_id = $1`, feedID); err != nil {
		return fmt.Errorf("clear feed collections: %w", err)
	}
	if len(collectionIDs) > 0 {
		ph := placeholders(len(collectionIDs))
		sql := `INSERT INTO feed_collections (feed_id, collection_id)
			 SELECT $1, id FROM collections WHERE user_id = $2 AND id IN (`+ph+`)
			 ON CONFLICT (feed_id, collection_id) DO NOTHING`
		args := append([]any{feedID, userID}, toAny(collectionIDs)...)
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			return fmt.Errorf("set feed collection: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// AddFeedsToCollection links the user's feeds to the user's collection without
// removing existing memberships. Feeds not owned by the user are ignored, and
// nothing happens if the collection is not owned by the user.
func (s *Postgres) AddFeedsToCollection(ctx context.Context, userID int64, collectionID int64, feedIDs []int64) (int, error) {
	if len(feedIDs) == 0 {
		return 0, nil
	}
	// $1 = collection id, $2 = user id, $3.. = feed ids.
	ph := make([]string, len(feedIDs))
	for i := range feedIDs {
		ph[i] = "$" + itoa(i+3)
	}
	sql := `INSERT INTO feed_collections (feed_id, collection_id)
		 SELECT f.id, $1
		 FROM feeds f
		 WHERE f.user_id = $2 AND f.id IN (`+strings.Join(ph, ",")+`)
		   AND $1 IN (SELECT id FROM collections WHERE user_id = $2)
		 ON CONFLICT (feed_id, collection_id) DO NOTHING`
	args := append([]any{collectionID, userID}, toAny(feedIDs)...)
	tag, err := s.pool.Exec(ctx, sql, args...)
	if err != nil {
		return 0, fmt.Errorf("add feeds to collection: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// --- feed sharing ---

// SetShareRequest moves a feed to pending with the given target ("shared" from
// a private feed, "private" from a shared feed).
func (s *Postgres) SetShareRequest(ctx context.Context, userID, feedID int64, want string) (*Feed, error) {
	var from, to string
	switch want {
	case "shared":
		from, to = "private", "shared"
	case "private":
		from, to = "shared", "private"
	default:
		return nil, fmt.Errorf("invalid share target %q", want)
	}

	row := s.pool.QueryRow(ctx,
		`UPDATE feeds
		 SET share_status = 'pending', share_requested = $4
		 WHERE id = $1 AND user_id = $2 AND share_status = $3
		 RETURNING `+feedColumns,
		feedID, userID, from, to,
	)
	var f Feed
	if err := scanFeed(row, &f); err != nil {
		if rowsAffectedZero(err) {
			// Distinguish "not owned" from "already in another state".
			var cur string
			if err := s.pool.QueryRow(ctx,
				`SELECT share_status FROM feeds WHERE id = $1 AND user_id = $2`, feedID, userID,
			).Scan(&cur); err != nil {
				if rowsAffectedZero(err) {
					return nil, ErrNotFound
				}
				return nil, fmt.Errorf("get share state: %w", err)
			}
			return nil, fmt.Errorf("feed is currently %s", cur)
		}
		return nil, fmt.Errorf("set share request: %w", err)
	}
	return &f, nil
}

// ListPendingShares returns every feed whose sharing change awaits review.
func (s *Postgres) ListPendingShares(ctx context.Context) ([]ShareRequest, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT f.id, f.url, f.title, f.share_requested, u.id, u.display_name, u.email
		 FROM feeds f JOIN users u ON u.id = f.user_id
		 WHERE f.share_status = 'pending'
		 ORDER BY f.title ASC, f.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list pending shares: %w", err)
	}
	defer rows.Close()

	out := make([]ShareRequest, 0)
	for rows.Next() {
		var r ShareRequest
		if err := rows.Scan(&r.FeedID, &r.URL, &r.Title, &r.Requested, &r.OwnerID, &r.OwnerName, &r.OwnerEmail); err != nil {
			return nil, fmt.Errorf("scan pending share: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending shares: %w", err)
	}
	return out, nil
}

// ResolveShare applies (approve) or reverts (reject) a pending change.
func (s *Postgres) ResolveShare(ctx context.Context, feedID int64, approve bool) (*Feed, error) {
	set := `SET share_status = share_requested, share_requested = NULL`
	if !approve {
		set = `SET share_status = CASE share_requested WHEN 'shared' THEN 'private' ELSE 'shared' END,
		       share_requested = NULL`
	}
	row := s.pool.QueryRow(ctx,
		`UPDATE feeds `+set+`
		 WHERE id = $1 AND share_status = 'pending' AND share_requested IS NOT NULL
		 RETURNING `+feedColumns,
		feedID,
	)
	var f Feed
	if err := scanFeed(row, &f); err != nil {
		if rowsAffectedZero(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("resolve share: %w", err)
	}
	return &f, nil
}

// ListSharedFeeds returns the community directory of shared feeds.
func (s *Postgres) ListSharedFeeds(ctx context.Context) ([]SharedFeed, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT f.url, f.title, f.site_url, u.display_name
		 FROM feeds f JOIN users u ON u.id = f.user_id
		 WHERE f.share_status = 'shared'
		 ORDER BY f.title ASC, f.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list shared feeds: %w", err)
	}
	defer rows.Close()

	out := make([]SharedFeed, 0)
	for rows.Next() {
		var sf SharedFeed
		if err := rows.Scan(&sf.URL, &sf.Title, &sf.SiteURL, &sf.OwnerName); err != nil {
			return nil, fmt.Errorf("scan shared feed: %w", err)
		}
		out = append(out, sf)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate shared feeds: %w", err)
	}
	return out, nil
}

func (s *Postgres) TouchFeed(ctx context.Context, userID, id int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE feeds SET last_fetched = now() WHERE id = $1 AND user_id = $2`, id, userID)
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

func (s *Postgres) ListItems(ctx context.Context, userID int64, q ItemQuery) ([]Item, int, error) {
	// Every result is scoped to the caller's feeds; this condition is always
	// present and must stay first so its $1 placeholder is stable.
	conds := []string{"f.user_id = $1"}
	args := []any{userID}

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
		args = append(args, !*q.Unread)
	}
	// Undated items (published_at NULL) are bounded by fetched_at.
	if q.Since != nil {
		conds = append(conds, fmt.Sprintf(
			"(i.published_at >= $%d OR (i.published_at IS NULL AND i.fetched_at >= $%d))",
			len(args)+1, len(args)+2))
		args = append(args, *q.Since, *q.Since)
	}
	if q.Until != nil {
		conds = append(conds, fmt.Sprintf(
			"(i.published_at <= $%d OR (i.published_at IS NULL AND i.fetched_at <= $%d))",
			len(args)+1, len(args)+2))
		args = append(args, *q.Until, *q.Until)
	}
	where := " WHERE " + strings.Join(conds, " AND ")
	from := " FROM items i LEFT JOIN feeds f ON f.id = i.feed_id"

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*)`+from+where, args...).Scan(&total); err != nil {
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
		` + from + where + `
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

func (s *Postgres) SetItemRead(ctx context.Context, userID, id int64, read bool) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE items SET read = $3
		 WHERE id = $1 AND feed_id IN (SELECT id FROM feeds WHERE user_id = $2)`,
		id, userID, read)
	if err != nil {
		return fmt.Errorf("set item read: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Postgres) SetItemsRead(ctx context.Context, userID int64, ids []int64, read bool) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	ph := placeholders(len(ids))
	sql := `UPDATE items SET read = $` + itoa(len(ids)+1) + `
		 WHERE id IN (` + ph + `)
		   AND feed_id IN (SELECT id FROM feeds WHERE user_id = $` + itoa(len(ids)+2) + `)`
	args := append(toAny(ids), read, userID)
	tag, err := s.pool.Exec(ctx, sql, args...)
	if err != nil {
		return 0, fmt.Errorf("set items read: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (s *Postgres) UnreadCount(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM items i
		 JOIN feeds f ON f.id = i.feed_id
		 WHERE f.user_id = $1 AND i.read = false`, userID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("unread count: %w", err)
	}
	return n, nil
}

// AssignLegacyData reassigns unclaimed (user_id = 0) feeds and collections to
// the given user. Rows that would collide with an existing (user_id, url) or
// (user_id, name) pair are left unclaimed.
func (s *Postgres) AssignLegacyData(ctx context.Context, userID int64) (int, int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op if committed

	feedTag, err := tx.Exec(ctx,
		`UPDATE feeds SET user_id = $1
		 WHERE user_id = 0
		   AND NOT EXISTS (SELECT 1 FROM feeds f2 WHERE f2.user_id = $1 AND f2.url = feeds.url)`,
		userID)
	if err != nil {
		return 0, 0, fmt.Errorf("assign legacy feeds: %w", err)
	}
	collTag, err := tx.Exec(ctx,
		`UPDATE collections SET user_id = $1
		 WHERE user_id = 0
		   AND NOT EXISTS (SELECT 1 FROM collections c2 WHERE c2.user_id = $1 AND c2.name = collections.name)`,
		userID)
	if err != nil {
		return 0, 0, fmt.Errorf("assign legacy collections: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("commit tx: %w", err)
	}
	return int(feedTag.RowsAffected()), int(collTag.RowsAffected()), nil
}

// --- users ---

// userColumns is the SELECT list shared by the user lookups.
const userColumns = `id, email, display_name, password_hash, role, email_verified, created_at`

// rowScanner is satisfied by both pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanUser scans a user row; password_hash may be NULL for OIDC-only users.
func scanUser(s rowScanner, u *User) error {
	var hash sql.NullString
	if err := s.Scan(&u.ID, &u.Email, &u.DisplayName, &hash, &u.Role, &u.EmailVerified, &u.CreatedAt); err != nil {
		return err
	}
	u.PasswordHash = hash.String
	return nil
}

func (s *Postgres) CreateUser(ctx context.Context, email, displayName, passwordHash string) (*User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash) VALUES ($1, $2, $3)
		 RETURNING `+userColumns,
		email, displayName, passwordHash,
	).Scan(&u.ID, &u.Email, &u.DisplayName, &u.PasswordHash, &u.Role, &u.EmailVerified, &u.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w: email already in use", ErrConflict)
		}
		return nil, fmt.Errorf("create user: %w", err)
	}
	return &u, nil
}

func (s *Postgres) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE lower(email) = lower($1)`, email,
	).Scan(&u.ID, &u.Email, &u.DisplayName, &u.PasswordHash, &u.Role, &u.EmailVerified, &u.CreatedAt)
	if err != nil {
		if rowsAffectedZero(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	return &u, nil
}

func (s *Postgres) GetUser(ctx context.Context, id int64) (*User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.DisplayName, &u.PasswordHash, &u.Role, &u.EmailVerified, &u.CreatedAt)
	if err != nil {
		if rowsAffectedZero(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get user: %w", err)
	}
	return &u, nil
}

func (s *Postgres) SetUserPasswordHash(ctx context.Context, id int64, passwordHash string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, id, passwordHash)
	if err != nil {
		return fmt.Errorf("set user password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ActivateUser marks the user's email as verified. When no other verified
// user exists, this user becomes the first: it is promoted to admin and all
// legacy (user_id = 0) data is claimed by it.
func (s *Postgres) ActivateUser(ctx context.Context, id int64) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op if committed

	var verified int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE email_verified = true`).Scan(&verified); err != nil {
		return false, fmt.Errorf("count verified users: %w", err)
	}
	first := verified == 0

	update := `UPDATE users SET email_verified = true WHERE id = $1`
	if first {
		update = `UPDATE users SET email_verified = true, role = 'admin' WHERE id = $1`
	}
	tag, err := tx.Exec(ctx, update, id)
	if err != nil {
		return false, fmt.Errorf("activate user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, ErrNotFound
	}

	if first {
		if _, err := tx.Exec(ctx,
			`UPDATE feeds SET user_id = $1
			 WHERE user_id = 0
			   AND NOT EXISTS (SELECT 1 FROM feeds f2 WHERE f2.user_id = $1 AND f2.url = feeds.url)`,
			id); err != nil {
			return false, fmt.Errorf("assign legacy feeds: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE collections SET user_id = $1
			 WHERE user_id = 0
			   AND NOT EXISTS (SELECT 1 FROM collections c2 WHERE c2.user_id = $1 AND c2.name = collections.name)`,
			id); err != nil {
			return false, fmt.Errorf("assign legacy collections: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit tx: %w", err)
	}
	return first, nil
}

// --- sessions ---

func (s *Postgres) CreateSession(ctx context.Context, tokenHash string, userID int64, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, userID, expiresAt)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *Postgres) GetSession(ctx context.Context, tokenHash string) (*Session, error) {
	var sess Session
	err := s.pool.QueryRow(ctx,
		`SELECT token_hash, user_id, created_at, expires_at FROM sessions WHERE token_hash = $1`,
		tokenHash,
	).Scan(&sess.TokenHash, &sess.UserID, &sess.CreatedAt, &sess.ExpiresAt)
	if err != nil {
		if rowsAffectedZero(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get session: %w", err)
	}
	return &sess, nil
}

func (s *Postgres) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// --- email verification ---

func (s *Postgres) CreateEmailVerification(ctx context.Context, email, tokenHash, purpose string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO email_verifications (email, token_hash, purpose, expires_at) VALUES ($1, $2, $3, $4)`,
		email, tokenHash, purpose, expiresAt)
	if err != nil {
		return fmt.Errorf("create email verification: %w", err)
	}
	return nil
}

// ConsumeEmailVerification atomically deletes an unexpired verification
// record; the DELETE ... RETURNING makes reuse impossible.
func (s *Postgres) ConsumeEmailVerification(ctx context.Context, tokenHash, purpose string) (string, bool, error) {
	var email string
	err := s.pool.QueryRow(ctx,
		`DELETE FROM email_verifications
		 WHERE token_hash = $1 AND purpose = $2 AND expires_at > now()
		 RETURNING email`,
		tokenHash, purpose,
	).Scan(&email)
	if err != nil {
		if rowsAffectedZero(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("consume email verification: %w", err)
	}
	return email, true, nil
}

// --- collections ---

func (s *Postgres) CreateCollection(ctx context.Context, userID int64, name string) (*Collection, error) {
	var c Collection
	var req sql.NullString
	err := s.pool.QueryRow(ctx,
		`INSERT INTO collections (user_id, name) VALUES ($1, $2)
		 RETURNING id, name, created_at, visibility_status, visibility_requested`, userID, name,
	).Scan(&c.ID, &c.Name, &c.CreatedAt, &c.VisibilityStatus, &req)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w: %q already exists", ErrConflict, name)
		}
		return nil, fmt.Errorf("create collection: %w", err)
	}
	c.VisibilityRequested = nullableString(req)
	return &c, nil
}

func (s *Postgres) GetCollection(ctx context.Context, userID, id int64) (*Collection, error) {
	var c Collection
	var req sql.NullString
	err := s.pool.QueryRow(ctx,
		`SELECT c.id, c.name, c.created_at, count(f.feed_id), c.visibility_status, c.visibility_requested
		 FROM collections c
		 LEFT JOIN feed_collections f ON f.collection_id = c.id
		 WHERE c.id = $1 AND c.user_id = $2
		 GROUP BY c.id, c.name, c.created_at, c.visibility_status, c.visibility_requested`, id, userID,
	).Scan(&c.ID, &c.Name, &c.CreatedAt, &c.FeedCount, &c.VisibilityStatus, &req)
	if err != nil {
		if rowsAffectedZero(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get collection: %w", err)
	}
	c.VisibilityRequested = nullableString(req)
	return &c, nil
}

func (s *Postgres) ListCollections(ctx context.Context, userID int64) ([]Collection, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT c.id, c.name, c.created_at, count(f.feed_id), c.visibility_status, c.visibility_requested
		 FROM collections c
		 LEFT JOIN feed_collections f ON f.collection_id = c.id
		 WHERE c.user_id = $1
		 GROUP BY c.id, c.name, c.created_at, c.visibility_status, c.visibility_requested
		 ORDER BY c.name ASC, c.id ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	defer rows.Close()

	out := make([]Collection, 0)
	for rows.Next() {
		var c Collection
		var req sql.NullString
		if err := rows.Scan(&c.ID, &c.Name, &c.CreatedAt, &c.FeedCount, &c.VisibilityStatus, &req); err != nil {
			return nil, fmt.Errorf("scan collection: %w", err)
		}
		c.VisibilityRequested = nullableString(req)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate collections: %w", err)
	}
	return out, nil
}

func (s *Postgres) RenameCollection(ctx context.Context, userID, id int64, name string) (*Collection, error) {
	var c Collection
	var req sql.NullString
	err := s.pool.QueryRow(ctx,
		`UPDATE collections AS c SET name = $3 WHERE c.id = $1 AND c.user_id = $2
		 RETURNING c.id, c.name, c.created_at,
		 (SELECT count(fc.feed_id) FROM feed_collections fc WHERE fc.collection_id = c.id),
		 c.visibility_status, c.visibility_requested`,
		id, userID, name,
	).Scan(&c.ID, &c.Name, &c.CreatedAt, &c.FeedCount, &c.VisibilityStatus, &req)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w: %q already exists", ErrConflict, name)
		}
		if rowsAffectedZero(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("rename collection: %w", err)
	}
	c.VisibilityRequested = nullableString(req)
	return &c, nil
}

func (s *Postgres) DeleteCollection(ctx context.Context, userID, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM collections WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete collection: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateCollectionShare creates (or regenerates) the share token for the user's
// collection, keyed by collection id so regenerating replaces the old token.
func (s *Postgres) CreateCollectionShare(ctx context.Context, userID, collectionID int64, token string) error {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO collection_shares (collection_id, token)
		 SELECT $1, $2 FROM collections WHERE id = $1 AND user_id = $3
		 ON CONFLICT (collection_id) DO UPDATE
		   SET token = EXCLUDED.token, created_at = now()
		 RETURNING token`,
		collectionID, token, userID,
	)
	var got string
	if err := row.Scan(&got); err != nil {
		if rowsAffectedZero(err) {
			return ErrNotFound
		}
		return fmt.Errorf("create collection share: %w", err)
	}
	return nil
}

// GetCollectionShare resolves a share token to the collection's name and feed
// URLs.
func (s *Postgres) GetCollectionShare(ctx context.Context, token string) (*CollectionShare, error) {
	var cs CollectionShare
	if err := s.pool.QueryRow(ctx,
		`SELECT c.name
		 FROM collections c JOIN collection_shares cs ON cs.collection_id = c.id
		 WHERE cs.token = $1`, token,
	).Scan(&cs.Name); err != nil {
		if rowsAffectedZero(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get collection share: %w", err)
	}

	rows, err := s.pool.Query(ctx,
		`SELECT f.url, f.title
		 FROM collection_shares cs
		 JOIN collections c ON c.id = cs.collection_id
		 JOIN feed_collections fc ON fc.collection_id = c.id
		 JOIN feeds f ON f.id = fc.feed_id
		 WHERE cs.token = $1
		 ORDER BY f.title ASC, f.id ASC`, token)
	if err != nil {
		return nil, fmt.Errorf("list shared feeds: %w", err)
	}
	defer rows.Close()

	feeds := make([]SharedFeedURL, 0)
	for rows.Next() {
		var u SharedFeedURL
		if err := rows.Scan(&u.URL, &u.Title); err != nil {
			return nil, fmt.Errorf("scan shared feed: %w", err)
		}
		feeds = append(feeds, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate shared feeds: %w", err)
	}
	cs.Feeds = feeds
	return &cs, nil
}

// --- collection visibility (public/private with admin approval) ---

// SetCollectionVisibilityRequest moves a collection to pending with the given
// target ("public" from a private collection, "private" from a public one).
func (s *Postgres) SetCollectionVisibilityRequest(ctx context.Context, userID, collectionID int64, want string) (*Collection, error) {
	var from, to string
	switch want {
	case "public":
		from, to = "private", "public"
	case "private":
		from, to = "public", "private"
	default:
		return nil, fmt.Errorf("invalid visibility target %q", want)
	}

	row := s.pool.QueryRow(ctx,
		`UPDATE collections
		 SET visibility_status = 'pending', visibility_requested = $4
		 WHERE id = $1 AND user_id = $2 AND visibility_status = $3
		 RETURNING id, name, created_at, visibility_status, visibility_requested`,
		collectionID, userID, from, to,
	)
	var c Collection
	var req sql.NullString
	if err := row.Scan(&c.ID, &c.Name, &c.CreatedAt, &c.VisibilityStatus, &req); err != nil {
		if rowsAffectedZero(err) {
			// Distinguish "not owned" from "already in another state".
			var cur string
			if err := s.pool.QueryRow(ctx,
				`SELECT visibility_status FROM collections WHERE id = $1 AND user_id = $2`, collectionID, userID,
			).Scan(&cur); err != nil {
				if rowsAffectedZero(err) {
					return nil, ErrNotFound
				}
				return nil, fmt.Errorf("get visibility state: %w", err)
			}
			return nil, fmt.Errorf("collection is currently %s", cur)
		}
		return nil, fmt.Errorf("set collection visibility request: %w", err)
	}
	c.VisibilityRequested = nullableString(req)
	return &c, nil
}

// ListPendingCollectionShares returns every collection whose visibility change
// awaits review.
func (s *Postgres) ListPendingCollectionShares(ctx context.Context) ([]CollectionShareRequest, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT c.id, c.name, c.visibility_requested, u.id, u.display_name, u.email
		 FROM collections c JOIN users u ON u.id = c.user_id
		 WHERE c.visibility_status = 'pending'
		 ORDER BY c.name ASC, c.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list pending collection shares: %w", err)
	}
	defer rows.Close()

	out := make([]CollectionShareRequest, 0)
	for rows.Next() {
		var r CollectionShareRequest
		if err := rows.Scan(&r.CollectionID, &r.Name, &r.Requested, &r.OwnerID, &r.OwnerName, &r.OwnerEmail); err != nil {
			return nil, fmt.Errorf("scan pending collection share: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending collection shares: %w", err)
	}
	return out, nil
}

// ResolveCollectionShare applies (approve) or reverts (reject) a pending
// collection-visibility change.
func (s *Postgres) ResolveCollectionShare(ctx context.Context, collectionID int64, approve bool) (*Collection, error) {
	set := `SET visibility_status = visibility_requested, visibility_requested = NULL`
	if !approve {
		set = `SET visibility_status = CASE visibility_requested WHEN 'public' THEN 'private' ELSE 'public' END,
		       visibility_requested = NULL`
	}
	row := s.pool.QueryRow(ctx,
		`UPDATE collections `+set+`
		 WHERE id = $1 AND visibility_status = 'pending' AND visibility_requested IS NOT NULL
		 RETURNING id, name, created_at, visibility_status, visibility_requested`,
		collectionID,
	)
	var c Collection
	var req sql.NullString
	if err := row.Scan(&c.ID, &c.Name, &c.CreatedAt, &c.VisibilityStatus, &req); err != nil {
		if rowsAffectedZero(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("resolve collection share: %w", err)
	}
	c.VisibilityRequested = nullableString(req)
	return &c, nil
}

// ListPublicCollections returns the community directory of public collections,
// each with the feed URLs it holds.
func (s *Postgres) ListPublicCollections(ctx context.Context) ([]PublicCollection, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT c.id, c.name, u.display_name, count(fc.feed_id)
		 FROM collections c
		 JOIN users u ON u.id = c.user_id
		 LEFT JOIN feed_collections fc ON fc.collection_id = c.id
		 WHERE c.visibility_status = 'public'
		 GROUP BY c.id, c.name, u.display_name
		 ORDER BY c.name ASC, c.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list public collections: %w", err)
	}
	defer rows.Close()

	out := make([]PublicCollection, 0)
	for rows.Next() {
		var pc PublicCollection
		if err := rows.Scan(&pc.ID, &pc.Name, &pc.OwnerName, &pc.FeedCount); err != nil {
			return nil, fmt.Errorf("scan public collection: %w", err)
		}
		out = append(out, pc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate public collections: %w", err)
	}

	// Attach each collection's feed URLs for the directory preview + import.
	for i := range out {
		frows, err := s.pool.Query(ctx,
			`SELECT f.url, f.title
			 FROM feed_collections fc JOIN feeds f ON f.id = fc.feed_id
			 WHERE fc.collection_id = $1
			 ORDER BY f.title ASC, f.id ASC`, out[i].ID)
		if err != nil {
			return nil, fmt.Errorf("list public collection feeds: %w", err)
		}
		feeds := make([]SharedFeedURL, 0)
		for frows.Next() {
			var u SharedFeedURL
			if err := frows.Scan(&u.URL, &u.Title); err != nil {
				frows.Close()
				return nil, fmt.Errorf("scan public collection feed: %w", err)
			}
			feeds = append(feeds, u)
		}
		frows.Close()
		if err := frows.Err(); err != nil {
			return nil, fmt.Errorf("iterate public collection feeds: %w", err)
		}
		out[i].Feeds = feeds
	}
	return out, nil
}

// GetPublicCollectionForImport resolves a public collection (by ID) to its name
// and feed URLs so another user can import it.
func (s *Postgres) GetPublicCollectionForImport(ctx context.Context, collectionID int64) (*CollectionShare, error) {
	var cs CollectionShare
	if err := s.pool.QueryRow(ctx,
		`SELECT c.name FROM collections c
		 WHERE c.id = $1 AND c.visibility_status = 'public'`,
		collectionID,
	).Scan(&cs.Name); err != nil {
		if rowsAffectedZero(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get public collection: %w", err)
	}

	rows, err := s.pool.Query(ctx,
		`SELECT f.url, f.title
		 FROM feed_collections fc JOIN feeds f ON f.id = fc.feed_id
		 WHERE fc.collection_id = $1
		 ORDER BY f.title ASC, f.id ASC`, collectionID)
	if err != nil {
		return nil, fmt.Errorf("list public collection feeds: %w", err)
	}
	defer rows.Close()

	feeds := make([]SharedFeedURL, 0)
	for rows.Next() {
		var u SharedFeedURL
		if err := rows.Scan(&u.URL, &u.Title); err != nil {
			return nil, fmt.Errorf("scan public collection feed: %w", err)
		}
		feeds = append(feeds, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate public collection feeds: %w", err)
	}
	cs.Feeds = feeds
	return &cs, nil
}

// ListRecommendedFeeds returns the admin-curated onboarding catalog.
func (s *Postgres) ListRecommendedFeeds(ctx context.Context) ([]RecommendedFeed, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, url, title, site_url, created_at
		 FROM recommended_feeds ORDER BY title ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list recommended feeds: %w", err)
	}
	defer rows.Close()

	out := make([]RecommendedFeed, 0)
	for rows.Next() {
		var rf RecommendedFeed
		if err := rows.Scan(&rf.ID, &rf.URL, &rf.Title, &rf.SiteURL, &rf.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan recommended feed: %w", err)
		}
		out = append(out, rf)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recommended feeds: %w", err)
	}
	return out, nil
}

// CreateRecommendedFeed adds a recommended feed, or updates title/site by URL.
func (s *Postgres) CreateRecommendedFeed(ctx context.Context, url, title, siteURL string) (*RecommendedFeed, error) {
	var rf RecommendedFeed
	err := s.pool.QueryRow(ctx,
		`INSERT INTO recommended_feeds (url, title, site_url) VALUES ($1, $2, $3)
		 ON CONFLICT (url) DO UPDATE SET title = EXCLUDED.title, site_url = EXCLUDED.site_url
		 RETURNING id, url, title, site_url, created_at`,
		url, title, siteURL,
	).Scan(&rf.ID, &rf.URL, &rf.Title, &rf.SiteURL, &rf.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create recommended feed: %w", err)
	}
	return &rf, nil
}

func (s *Postgres) DeleteRecommendedFeed(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM recommended_feeds WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete recommended feed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ErrNotFound is returned when an operation targets a row that does not exist
// or is not owned by the caller.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned when a uniqueness constraint would be violated
// (e.g. creating a collection whose name already exists for the user).
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
