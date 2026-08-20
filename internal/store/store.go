// Package store defines the persistence contract for feeds and items.
//
// A Store is implemented by the Postgres-backed store in postgres.go. Handlers
// depend on this interface (not the concrete type) so they can be unit-tested
// with an in-memory stub.
package store

import (
	"context"
	"time"
)

// Feed is a subscribed RSS/Atom feed.
type Feed struct {
	ID            int64      `json:"id"`
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

// ItemQuery filters and paginates ListItems results.
type ItemQuery struct {
	FeedID       *int64 // filter to one feed when set
	CollectionID *int64 // filter to feeds in one collection when set
	Unread       *bool  // filter by read state when set
	Limit        int
	Offset       int
}

// Store is the persistence interface used by the HTTP layer and the refresher.
type Store interface {
	CreateFeed(ctx context.Context, url, title, siteURL string) (*Feed, error)
	// ListFeeds returns feeds with their collection IDs populated.
	ListFeeds(ctx context.Context) ([]Feed, error)
	DeleteFeed(ctx context.Context, id int64) error
	// DeleteFeeds removes feeds by ID in a single statement. Missing IDs are
	// ignored; it reports how many rows were deleted.
	DeleteFeeds(ctx context.Context, ids []int64) (int, error)
	TouchFeed(ctx context.Context, id int64) error

	// SetFeedCollections replaces the set of collections a feed belongs to.
	SetFeedCollections(ctx context.Context, feedID int64, collectionIDs []int64) error

	// Collections
	CreateCollection(ctx context.Context, name string) (*Collection, error)
	// ListCollections returns all collections, each with its feed count.
	ListCollections(ctx context.Context) ([]Collection, error)
	RenameCollection(ctx context.Context, id int64, name string) (*Collection, error)
	DeleteCollection(ctx context.Context, id int64) error

	// UpsertItems inserts items for a feed, ignoring ones already seen
	// (matched by feed_id + guid). It returns the number of newly inserted
	// items.
	UpsertItems(ctx context.Context, feedID int64, items []IncomingItem) (int, error)

	ListItems(ctx context.Context, q ItemQuery) ([]Item, int, error)
	SetItemRead(ctx context.Context, id int64, read bool) error
	UnreadCount(ctx context.Context) (int, error)
}
