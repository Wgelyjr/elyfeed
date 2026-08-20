// Package refresh periodically fetches subscribed feeds and stores new items.
package refresh

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"elyfeed/internal/rss"
	"elyfeed/internal/store"
)

// Refresher fetches feeds and persists their entries.
type Refresher struct {
	store     store.Store
	client    *http.Client
	userAgent string
	interval  time.Duration
	log       *slog.Logger
}

// New builds a Refresher.
func New(st store.Store, client *http.Client, userAgent string, interval time.Duration, log *slog.Logger) *Refresher {
	if log == nil {
		log = slog.Default()
	}
	return &Refresher{
		store:     st,
		client:    client,
		userAgent: userAgent,
		interval:  interval,
		log:       log,
	}
}

// Start launches the background refresh loop. It returns immediately.
// Nothing is refreshed until the context is cancelled when interval is zero.
func (r *Refresher) Start(ctx context.Context) {
	if r.interval <= 0 {
		r.log.Info("background refresh disabled (REFRESH_INTERVAL=0)")
		return
	}
	go func() {
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()

		r.RefreshAll(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.RefreshAll(ctx)
			}
		}
	}()
}

// AddFeed validates and stores a new subscription, seeding it with the items
// from the first fetch. It returns an error if the URL is not a readable feed.
func (r *Refresher) AddFeed(ctx context.Context, url string) (*store.Feed, error) {
	feed, err := rss.Fetch(ctx, r.client, url, r.userAgent)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(feed.Title)
	if title == "" {
		title = url
	}
	dbFeed, err := r.store.CreateFeed(ctx, url, title, feed.SiteURL)
	if err != nil {
		return nil, err
	}
	if err := r.storeItems(ctx, dbFeed.ID, feed); err != nil {
		// The feed is subscribed; only the initial backfill failed.
		r.log.Warn("seed feed items", "url", url, "err", err)
	}
	return dbFeed, nil
}

// AddFeedResult reports the outcome of adding a single feed in a bulk add.
type AddFeedResult struct {
	URL   string      `json:"url"`
	Feed  *store.Feed `json:"feed,omitempty"`
	Error string      `json:"error,omitempty"`
}

// AddFeeds adds multiple feeds, validating and seeding each. Per-URL failures
// are captured in the returned results rather than aborting the whole batch.
// Blank and duplicate URLs are skipped.
func (r *Refresher) AddFeeds(ctx context.Context, urls []string) []AddFeedResult {
	seen := make(map[string]struct{}, len(urls))
	results := make([]AddFeedResult, 0, len(urls))
	for _, raw := range urls {
		u := strings.TrimSpace(raw)
		if u == "" {
			continue
		}
		if _, dup := seen[u]; dup {
			continue
		}
		seen[u] = struct{}{}

		feed, err := r.AddFeed(ctx, u)
		res := AddFeedResult{URL: u}
		if err != nil {
			res.Error = err.Error()
		} else {
			res.Feed = feed
		}
		results = append(results, res)
	}
	return results
}

// RefreshAll fetches every subscribed feed and upserts new items. It returns
// the number of feeds refreshed successfully.
func (r *Refresher) RefreshAll(ctx context.Context) (int, error) {
	feeds, err := r.store.ListFeeds(ctx)
	if err != nil {
		return 0, err
	}
	ok := 0
	for _, f := range feeds {
		if err := ctx.Err(); err != nil {
			return ok, err
		}
		if err := r.refreshOne(ctx, f); err != nil {
			r.log.Warn("refresh feed", "url", f.URL, "err", err)
			continue
		}
		ok++
	}
	return ok, nil
}

func (r *Refresher) refreshOne(ctx context.Context, f store.Feed) error {
	feed, err := rss.Fetch(ctx, r.client, f.URL, r.userAgent)
	if err != nil {
		return err
	}
	if err := r.storeItems(ctx, f.ID, feed); err != nil {
		return err
	}
	return r.store.TouchFeed(ctx, f.ID)
}

func (r *Refresher) storeItems(ctx context.Context, feedID int64, feed *rss.Feed) error {
	items := make([]store.IncomingItem, 0, len(feed.Items))
	for _, it := range feed.Items {
		items = append(items, store.IncomingItem{
			GUID:        it.GUID,
			Title:       it.Title,
			Link:        it.Link,
			Content:     it.Content,
			Author:      it.Author,
			PublishedAt: it.PublishedAt,
		})
	}
	if len(items) == 0 {
		return nil
	}
	_, err := r.store.UpsertItems(ctx, feedID, items)
	return err
}
