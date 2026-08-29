// Package store defines the persistence contract for feeds and items.
//
// A Store is implemented by the Postgres-backed store in postgres.go. Handlers
// depend on this interface (not the concrete type) so they can be unit-tested
// with an in-memory stub.
//
// Every operation is scoped to a user (userID int64). Feeds and collections
// belong to exactly one user; items are reached through their feed. A userID
// of 0 represents legacy (pre-multi-user) data before it has been claimed.
package store

import (
	"context"
	"time"
)

// Feed is a subscribed RSS/Atom feed.
type Feed struct {
	ID            int64      `json:"id"`
	UserID        int64      `json:"user_id"`
	URL           string     `json:"url"`
	Title         string     `json:"title"`
	SiteURL       string     `json:"site_url"`
	LastFetched   *time.Time `json:"last_fetched"`
	CreatedAt     time.Time  `json:"created_at"`
	CollectionIDs []int64    `json:"collection_ids"`
}

// Collection is a named group of feeds. A feed may belong to several
// collections (many-to-many via feed_collections).
type Collection struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	FeedCount int       `json:"feed_count"`
	CreatedAt time.Time `json:"created_at"`
}

// Item is a single entry from a feed.
type Item struct {
	ID          int64      `json:"id"`
	FeedID      int64      `json:"feed_id"`
	FeedTitle   string     `json:"feed_title"`
	GUID        string     `json:"guid"`
	Title       string     `json:"title"`
	Link        string     `json:"link"`
	Content     string     `json:"content"`
	Author      string     `json:"author"`
	PublishedAt *time.Time `json:"published_at"`
	FetchedAt   time.Time  `json:"fetched_at"`
	Read        bool       `json:"read"`
}

// User is a registered account. The password hash is internal and never
// serialized to clients.
type User struct {
	ID            int64     `json:"id"`
	Email         string    `json:"email"`
	DisplayName   string    `json:"display_name"`
	PasswordHash  string    `json:"-"`
	Role          string    `json:"role"`
	EmailVerified bool      `json:"email_verified"`
	CreatedAt     time.Time `json:"created_at"`
}

// Session is an authenticated session. The raw token only ever lives in the
// client cookie; the store keeps its SHA-256 hash.
type Session struct {
	TokenHash string    `json:"-"`
	UserID    int64     `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// IncomingItem is a feed entry as produced by the RSS parser, before it has a
// stable database identity. GUID must be unique within a feed.
type IncomingItem struct {
	GUID        string
	Title       string
	Link        string
	Content     string
	Author      string
	PublishedAt *time.Time
}

// ItemQuery filters and paginates ListItems results. All results are already
// restricted to the caller's feeds.
type ItemQuery struct {
	FeedID       *int64 // filter to one feed when set
	CollectionID *int64 // filter to feeds in one collection when set
	Unread       *bool  // when set, filter to unread (true) or read (false) items
	// Since/Until bound the time window. Items whose published_at is NULL are
	// compared by fetched_at instead (some feeds carry no publication date).
	Since  *time.Time
	Until  *time.Time
	Limit  int
	Offset int
}

// Store is the persistence interface used by the HTTP layer and the refresher.
// Methods that act on a single user's data take that user's ID; ListAllFeeds
// is the one cross-user read, used by the background refresher.
type Store interface {
	// CreateFeed subscribes a feed for the user. If the user already
	// subscribes to the URL, the existing feed is returned.
	CreateFeed(ctx context.Context, userID int64, url, title, siteURL string) (*Feed, error)
	// ListFeeds returns the user's feeds with their collection IDs populated.
	ListFeeds(ctx context.Context, userID int64) ([]Feed, error)
	// ListAllFeeds returns every feed across all users (no collection data),
	// used by the background refresher.
	ListAllFeeds(ctx context.Context) ([]Feed, error)
	DeleteFeed(ctx context.Context, userID, id int64) error
	// DeleteFeeds removes the user's feeds by ID in a single statement. IDs
	// not owned by the user are ignored; it reports how many rows were
	// deleted.
	DeleteFeeds(ctx context.Context, userID int64, ids []int64) (int, error)
	TouchFeed(ctx context.Context, userID, id int64) error

	// SetFeedCollections replaces the set of collections a feed belongs to.
	SetFeedCollections(ctx context.Context, userID, feedID int64, collectionIDs []int64) error

	// Collections
	CreateCollection(ctx context.Context, userID int64, name string) (*Collection, error)
	// GetCollection returns a single collection by ID (ErrNotFound when missing).
	GetCollection(ctx context.Context, userID, id int64) (*Collection, error)
	// ListCollections returns the user's collections, each with its feed count.
	ListCollections(ctx context.Context, userID int64) ([]Collection, error)
	RenameCollection(ctx context.Context, userID, id int64, name string) (*Collection, error)
	DeleteCollection(ctx context.Context, userID, id int64) error

	// UpsertItems inserts items for a feed, ignoring ones already seen
	// (matched by feed_id + guid). It returns the number of newly inserted
	// items. feedID already implies the owning user.
	UpsertItems(ctx context.Context, feedID int64, items []IncomingItem) (int, error)

	// ListItems returns the user's items matching the query, plus the total
	// count matching the query.
	ListItems(ctx context.Context, userID int64, q ItemQuery) ([]Item, int, error)
	SetItemRead(ctx context.Context, userID, id int64, read bool) error
	// SetItemsRead sets the read state for multiple of the user's items in a
	// single statement. IDs not owned by the user are ignored. It returns the
	// number of rows actually changed.
	SetItemsRead(ctx context.Context, userID int64, ids []int64, read bool) (int, error)
	// UnreadCount returns the number of the user's unread items.
	UnreadCount(ctx context.Context, userID int64) (int, error)

	// AssignLegacyData reassigns all legacy (user_id = 0) feeds and
	// collections to the given user. It is called once, when the first user
	// activates. It reports how many feeds and collections were reassigned.
	AssignLegacyData(ctx context.Context, userID int64) (feeds, collections int, err error)

	// Users
	// CreateUser registers a new account (ErrConflict when the email is taken).
	CreateUser(ctx context.Context, email, displayName, passwordHash string) (*User, error)
	// GetUserByEmail returns the user with the given email (ErrNotFound when
	// missing). Email comparison is case-insensitive.
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	// GetUser returns the user with the given ID (ErrNotFound when missing).
	GetUser(ctx context.Context, id int64) (*User, error)
	SetUserPasswordHash(ctx context.Context, id int64, passwordHash string) error
	// ActivateUser marks the user's email as verified. When this is the first
	// verified user, it is promoted to admin and all legacy data is
	// reassigned to it. It reports whether the user was the first.
	ActivateUser(ctx context.Context, id int64) (first bool, err error)

	// Sessions
	CreateSession(ctx context.Context, tokenHash string, userID int64, expiresAt time.Time) error
	// GetSession returns a session by token hash (ErrNotFound when missing).
	GetSession(ctx context.Context, tokenHash string) (*Session, error)
	DeleteSession(ctx context.Context, tokenHash string) error

	// Email verification
	CreateEmailVerification(ctx context.Context, email, tokenHash, purpose string, expiresAt time.Time) error
	// ConsumeEmailVerification atomically deletes an unexpired verification
	// record and reports the email it was issued for. It reports ok=false when
	// the token is unknown, already used, or expired.
	ConsumeEmailVerification(ctx context.Context, tokenHash, purpose string) (email string, ok bool, err error)
}
