package store

// These are integration tests: they exercise the real Postgres-backed Store
// against a live database, covering the collection-linking and sharing/
// visibility state machines that the in-memory stub in the server tests cannot
// reach. They are opt-in so that `go test ./...` stays green without a
// database; set DATABASE_URL to run them, e.g.:
//
//	DATABASE_URL=postgres://elyfeed:elyfeed@localhost:5432/elyfeed?sslmode=disable \
//	    go test ./internal/store/
//
// Each test creates its own throwaway user (random email) and removes it on
// completion, so the tests are safe to run against a shared or production
// database and do not clobber real data.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"

	"elyfeed/internal/db"
)

// requireTestStore connects to the database named by DATABASE_URL, applies any
// pending migrations, and returns a ready *Postgres. It skips the test when
// DATABASE_URL is unset and closes the pool on cleanup.
func requireTestStore(t *testing.T) *Postgres {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewPostgres(pool)
}

// newTestUser creates a throwaway account and registers a cleanup that removes
// the user's feeds, collections, and account (feed_collections and items are
// dropped by ON DELETE CASCADE). It returns the new user's ID.
func newTestUser(t *testing.T, st *Postgres) int64 {
	t.Helper()
	u, err := st.CreateUser(context.Background(),
		fmt.Sprintf("storetest-%s@example.com", randomHex(t)), "Store Test", "test-hash")
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, q := range []string{
			`DELETE FROM feeds WHERE user_id = $1`,
			`DELETE FROM collections WHERE user_id = $1`,
			`DELETE FROM users WHERE id = $1`,
		} {
			if _, err := st.pool.Exec(ctx, q, u.ID); err != nil {
				t.Logf("cleanup %q: %v", q, err)
			}
		}
	})
	return u.ID
}

// randomHex returns a short random hex string for unique test fixtures.
func randomHex(t *testing.T) string {
	t.Helper()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random: %v", err)
	}
	return hex.EncodeToString(b)
}

// assertCollections checks that feedID belongs to exactly the want collections
// (order-insensitive) as reported by ListFeeds.
func assertCollections(t *testing.T, st *Postgres, ctx context.Context, user, feedID int64, want []int64) {
	t.Helper()
	feeds, err := st.ListFeeds(ctx, user)
	if err != nil {
		t.Fatalf("list feeds: %v", err)
	}
	var got []int64
	found := false
	for _, f := range feeds {
		if f.ID == feedID {
			got, found = f.CollectionIDs, true
			break
		}
	}
	if !found {
		t.Fatalf("feed %d missing from user %d's feeds", feedID, user)
	}
	if !slices.Equal(sortedInt64(got), sortedInt64(want)) {
		t.Fatalf("feed %d collections = %v, want %v", feedID, got, want)
	}
}

func sortedInt64(v []int64) []int64 {
	out := append([]int64(nil), v...)
	slices.Sort(out)
	return out
}

// TestSetFeedCollections covers replacing the set of collections a feed belongs
// to, including the multi-collection case that a placeholder collision once
// broke (binding collection IDs into the feed_id/user_id parameter slots).
func TestSetFeedCollections(t *testing.T) {
	st := requireTestStore(t)
	ctx := context.Background()
	user := newTestUser(t, st)
	other := newTestUser(t, st)

	f1, _ := st.CreateFeed(ctx, user, "https://example.com/a.xml", "Feed A", "")
	f2, _ := st.CreateFeed(ctx, user, "https://example.com/b.xml", "Feed B", "")
	c1, _ := st.CreateCollection(ctx, user, "Coll 1")
	c2, _ := st.CreateCollection(ctx, user, "Coll 2")
	foreign, _ := st.CreateCollection(ctx, other, "Foreign Coll")

	// Single collection.
	if err := st.SetFeedCollections(ctx, user, f1.ID, []int64{c1.ID}); err != nil {
		t.Fatalf("set one: %v", err)
	}
	assertCollections(t, st, ctx, user, f1.ID, []int64{c1.ID})

	// Multiple collections: the regression case.
	if err := st.SetFeedCollections(ctx, user, f1.ID, []int64{c1.ID, c2.ID}); err != nil {
		t.Fatalf("set two: %v", err)
	}
	assertCollections(t, st, ctx, user, f1.ID, []int64{c1.ID, c2.ID})

	// Replace, not append: switch to only c2.
	if err := st.SetFeedCollections(ctx, user, f1.ID, []int64{c2.ID}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	assertCollections(t, st, ctx, user, f1.ID, []int64{c2.ID})

	// Clear all memberships.
	if err := st.SetFeedCollections(ctx, user, f1.ID, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	assertCollections(t, st, ctx, user, f1.ID, nil)

	// A collection the user does not own is ignored, without error.
	if err := st.SetFeedCollections(ctx, user, f1.ID, []int64{foreign.ID}); err != nil {
		t.Fatalf("foreign collection: %v", err)
	}
	assertCollections(t, st, ctx, user, f1.ID, nil)

	// A feed the user does not own is rejected.
	of, _ := st.CreateFeed(ctx, other, "https://example.com/other.xml", "Other Feed", "")
	if err := st.SetFeedCollections(ctx, user, of.ID, []int64{c1.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-owned feed: got %v, want ErrNotFound", err)
	}

	// An untouched feed stays empty.
	assertCollections(t, st, ctx, user, f2.ID, nil)
}

// TestAddFeedsToCollection covers incrementally linking feeds to a collection
// without dropping existing memberships, plus the ownership guards.
func TestAddFeedsToCollection(t *testing.T) {
	st := requireTestStore(t)
	ctx := context.Background()
	user := newTestUser(t, st)
	other := newTestUser(t, st)

	f1, _ := st.CreateFeed(ctx, user, "https://example.com/a.xml", "A", "")
	f2, _ := st.CreateFeed(ctx, user, "https://example.com/b.xml", "B", "")
	c1, _ := st.CreateCollection(ctx, user, "Coll 1")

	n, err := st.AddFeedsToCollection(ctx, user, c1.ID, []int64{f1.ID, f2.ID})
	if err != nil {
		t.Fatalf("add two: %v", err)
	}
	if n != 2 {
		t.Fatalf("added = %d, want 2", n)
	}
	assertCollections(t, st, ctx, user, f1.ID, []int64{c1.ID})
	assertCollections(t, st, ctx, user, f2.ID, []int64{c1.ID})

	// Re-adding the same links creates nothing new.
	if n, err := st.AddFeedsToCollection(ctx, user, c1.ID, []int64{f1.ID, f2.ID}); err != nil {
		t.Fatalf("re-add: %v", err)
	} else if n != 0 {
		t.Fatalf("re-added = %d, want 0", n)
	}

	// A collection the user does not own is a no-op.
	oc, _ := st.CreateCollection(ctx, other, "Other Coll")
	if n, err := st.AddFeedsToCollection(ctx, user, oc.ID, []int64{f1.ID}); err != nil {
		t.Fatalf("foreign collection: %v", err)
	} else if n != 0 {
		t.Fatalf("foreign collection added = %d, want 0", n)
	}

	// A feed not owned by the user is ignored.
	of, _ := st.CreateFeed(ctx, other, "https://example.com/o.xml", "O", "")
	if n, err := st.AddFeedsToCollection(ctx, user, c1.ID, []int64{of.ID}); err != nil {
		t.Fatalf("foreign feed: %v", err)
	} else if n != 0 {
		t.Fatalf("foreign feed added = %d, want 0", n)
	}
}

// TestCollectionVisibilityLifecycle walks a collection through its visibility
// states (with a feed inside it), verifies the public directory + import path,
// and checks that the owner can cancel their own pending change.
func TestCollectionVisibilityLifecycle(t *testing.T) {
	st := requireTestStore(t)
	ctx := context.Background()
	user := newTestUser(t, st)
	other := newTestUser(t, st)

	f, _ := st.CreateFeed(ctx, user, "https://example.com/pub.xml", "Public Feed", "")
	c, _ := st.CreateCollection(ctx, user, "Public Coll")
	if err := st.SetFeedCollections(ctx, user, f.ID, []int64{c.ID}); err != nil {
		t.Fatalf("link feed: %v", err)
	}

	// private -> request public -> pending.
	got, err := st.SetCollectionVisibilityRequest(ctx, user, c.ID, "public")
	if err != nil {
		t.Fatalf("request public: %v", err)
	}
	if got.VisibilityStatus != "pending" || got.VisibilityRequested == nil || *got.VisibilityRequested != "public" {
		t.Fatalf("after request = (%q,%v), want (pending, public)", got.VisibilityStatus, got.VisibilityRequested)
	}

	// Owner cancels their own pending request -> back to private.
	if got, err = st.CancelCollectionVisibilityRequest(ctx, user, c.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	} else if got.VisibilityStatus != "private" || got.VisibilityRequested != nil {
		t.Fatalf("after cancel = (%q,%v), want (private, nil)", got.VisibilityStatus, got.VisibilityRequested)
	}

	// A private collection is not importable.
	if _, err := st.GetPublicCollectionForImport(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("import while private: got %v, want ErrNotFound", err)
	}

	// Request public again; admin approves -> public.
	if _, err := st.SetCollectionVisibilityRequest(ctx, user, c.ID, "public"); err != nil {
		t.Fatalf("re-request: %v", err)
	}
	if got, err = st.ResolveCollectionShare(ctx, c.ID, true); err != nil {
		t.Fatalf("approve: %v", err)
	} else if got.VisibilityStatus != "public" {
		t.Fatalf("after approve = %q, want public", got.VisibilityStatus)
	}

	// Now resolvable for import, with the feed URL attached.
	share, err := st.GetPublicCollectionForImport(ctx, c.ID)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(share.Feeds) != 1 || share.Feeds[0].URL != f.URL {
		t.Fatalf("import feeds = %+v, want one entry with %s", share.Feeds, f.URL)
	}

	// public -> request private -> pending; cancel -> back to public.
	if _, err := st.SetCollectionVisibilityRequest(ctx, user, c.ID, "private"); err != nil {
		t.Fatalf("request private: %v", err)
	}
	if got, err = st.CancelCollectionVisibilityRequest(ctx, user, c.ID); err != nil {
		t.Fatalf("cancel 2: %v", err)
	} else if got.VisibilityStatus != "public" {
		t.Fatalf("after cancel 2 = %q, want public", got.VisibilityStatus)
	}

	// A non-owner cannot cancel.
	if _, err := st.CancelCollectionVisibilityRequest(ctx, other, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-owner cancel: got %v, want ErrNotFound", err)
	}
}
