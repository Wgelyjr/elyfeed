// Package server exposes the HTTP API and serves the embedded frontend.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"elyfeed/internal/auth"
	"elyfeed/internal/refresh"
	"elyfeed/internal/store"
)

func init() {
	// Serve deterministic content types for the embedded frontend regardless of
	// the host's mime.types table (minimal container images often lack entries).
	mime.AddExtensionType(".webmanifest", "application/manifest+json")
	mime.AddExtensionType(".ico", "image/x-icon")
}

// Server wires the store, refresher and auth service behind HTTP handlers.
type Server struct {
	store     store.Store
	refresher *refresh.Refresher
	assets    fs.FS
	auth      *auth.Service
}

// New builds the HTTP handler: the JSON API under /api and the embedded
// single-page app for every other route. Every request passes through the
// auth middleware, which populates the request context when a valid session
// cookie is present, and through the security-headers middleware. /api
// request bodies are capped at maxRequestBodyBytes and mutating /api
// requests must carry a JSON content type.
func New(st store.Store, ref *refresh.Refresher, assets fs.FS, a *auth.Service) http.Handler {
	s := &Server{store: st, refresher: ref, assets: assets, auth: a}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/feeds", s.handleListFeeds)
	mux.HandleFunc("POST /api/feeds", s.handleAddFeed)
	mux.HandleFunc("POST /api/feeds/bulk", s.handleAddFeeds)
	mux.HandleFunc("POST /api/feeds/bulk-delete", s.handleBulkDeleteFeeds)
	mux.HandleFunc("PUT /api/feeds/{id}/collections", s.handleSetFeedCollections)
	mux.HandleFunc("DELETE /api/feeds/{id}", s.handleDeleteFeed)
	mux.HandleFunc("GET /api/collections", s.handleListCollections)
	mux.HandleFunc("POST /api/collections", s.handleCreateCollection)
	mux.HandleFunc("PATCH /api/collections/{id}", s.handleRenameCollection)
	mux.HandleFunc("DELETE /api/collections/{id}", s.handleDeleteCollection)
	mux.HandleFunc("POST /api/collections/{id}/share", s.handleCreateCollectionShare)
	mux.HandleFunc("GET /api/collection-shares/{token}", s.handleGetCollectionShare)
	mux.HandleFunc("POST /api/imports", s.handleImport)
	mux.HandleFunc("POST /api/collections/{id}/make-public", s.handleRequestCollectionPublic)
	mux.HandleFunc("POST /api/collections/{id}/make-private", s.handleRequestCollectionPrivate)
	mux.HandleFunc("POST /api/collections/{id}/cancel-visibility", s.handleCancelCollectionVisibilityRequest)
	mux.HandleFunc("POST /api/collections/{id}/import", s.handleImportCollection)
	mux.HandleFunc("GET /api/public-collections", s.handleListPublicCollections)
	mux.HandleFunc("GET /api/public-collections/pending", s.handleListPendingCollectionShares)
	mux.HandleFunc("POST /api/public-collections/{id}/approve", s.handleApproveCollectionShare)
	mux.HandleFunc("POST /api/public-collections/{id}/reject", s.handleRejectCollectionShare)
	mux.HandleFunc("GET /api/recommended-feeds", s.handleListRecommendedFeeds)
	mux.HandleFunc("POST /api/recommended-feeds", s.handleCreateRecommendedFeed)
	mux.HandleFunc("DELETE /api/recommended-feeds/{id}", s.handleDeleteRecommendedFeed)
	mux.HandleFunc("GET /api/items", s.handleListItems)
	mux.HandleFunc("GET /api/items/unread-count", s.handleUnreadCount)
	mux.HandleFunc("POST /api/items/{id}/read", s.handleSetItemRead)
	mux.HandleFunc("POST /api/items/bulk-read", s.handleBulkSetItemRead)
	mux.HandleFunc("GET /api/digest", s.handleDigest)
	mux.HandleFunc("POST /api/refresh", s.handleRefresh)
	mux.HandleFunc("POST /api/auth/register", s.handleRegister)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/auth/me", s.handleMe)
	mux.HandleFunc("GET /api/auth/verify", s.handleVerifyEmail)
	mux.HandleFunc("POST /api/auth/forgot-password", s.handleForgotPassword)
	mux.HandleFunc("POST /api/auth/reset-password", s.handleResetPassword)
	mux.HandleFunc("GET /api/auth/oidc", s.handleOIDCLogin)
	mux.HandleFunc("GET /api/auth/oidc/callback", s.handleOIDCCallback)
	mux.HandleFunc("GET /api/", s.handleAPIIndex)
	mux.Handle("GET /", spaHandler{assets: assets})

	return withSecurityHeaders(withAPIPolicy(a.Middleware(mux)))
}

// currentUser returns the authenticated user's ID for the request, or false
// when the request carries no authenticated user.
func (s *Server) currentUser(r *http.Request) (int64, bool) {
	return auth.UserIDFromContext(r.Context())
}

// requireAdmin authenticates the request and checks the caller is an admin.
// It writes a 401 (unauthenticated) or 403 (not an admin) and returns ok=false
// otherwise.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (int64, bool) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return 0, false
	}
	u, err := s.store.GetUser(r.Context(), userID)
	if err != nil || u.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin access required")
		return 0, false
	}
	return userID, true
}

// --- feeds ---

func (s *Server) handleListFeeds(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	feeds, err := s.store.ListFeeds(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if feeds == nil {
		feeds = []store.Feed{}
	}
	writeJSON(w, http.StatusOK, feeds)
}

func (s *Server) handleAddFeed(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	u := strings.TrimSpace(req.URL)
	if u == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	parsed, err := url.Parse(u)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		writeError(w, http.StatusBadRequest, "url must be a valid http(s) URL")
		return
	}

	feed, err := s.refresher.AddFeed(r.Context(), userID, u)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not add feed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, feed)
}

func (s *Server) handleDeleteFeed(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	if err := s.store.DeleteFeed(r.Context(), userID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "feed not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAddFeeds adds several feeds at once (one per URL). Each URL is
// validated and seeded independently; failures are reported per URL.
func (s *Server) handleAddFeeds(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req struct {
		URLs []string `json:"urls"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.URLs) == 0 {
		writeError(w, http.StatusBadRequest, "urls is required")
		return
	}
	results := s.refresher.AddFeeds(r.Context(), userID, req.URLs)
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// handleBulkDeleteFeeds removes several feeds by ID.
func (s *Server) handleBulkDeleteFeeds(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids is required")
		return
	}
	n, err := s.store.DeleteFeeds(r.Context(), userID, req.IDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

// handleSetFeedCollections replaces the collections a feed belongs to.
func (s *Server) handleSetFeedCollections(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		CollectionIDs []int64 `json:"collection_ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.store.SetFeedCollections(r.Context(), userID, id, req.CollectionIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "feed not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- collections ---

func (s *Server) handleListCollections(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	collections, err := s.store.ListCollections(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if collections == nil {
		collections = []store.Collection{}
	}
	writeJSON(w, http.StatusOK, collections)
}

func (s *Server) handleCreateCollection(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	collection, err := s.store.CreateCollection(r.Context(), userID, name)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, collection)
}

func (s *Server) handleRenameCollection(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	collection, err := s.store.RenameCollection(r.Context(), userID, id, name)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrConflict):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "collection not found")
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, collection)
}

func (s *Server) handleDeleteCollection(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	if err := s.store.DeleteCollection(r.Context(), userID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "collection not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- recommended feeds ---

func (s *Server) handleListRecommendedFeeds(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	feeds, err := s.store.ListRecommendedFeeds(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if feeds == nil {
		feeds = []store.RecommendedFeed{}
	}
	writeJSON(w, http.StatusOK, feeds)
}

func (s *Server) handleCreateRecommendedFeed(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var req struct {
		URL     string `json:"url"`
		Title   string `json:"title"`
		SiteURL string `json:"site_url"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	u := strings.TrimSpace(req.URL)
	if u == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	parsed, err := url.Parse(u)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		writeError(w, http.StatusBadRequest, "url must be a valid http(s) URL")
		return
	}
	rf, err := s.store.CreateRecommendedFeed(r.Context(), u, strings.TrimSpace(req.Title), strings.TrimSpace(req.SiteURL))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, rf)
}

func (s *Server) handleDeleteRecommendedFeed(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	if err := s.store.DeleteRecommendedFeed(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "recommended feed not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- shareable collections + import ---

// handleCreateCollectionShare generates (or regenerates) a collection's share
// token and returns it.
func (s *Server) handleCreateCollectionShare(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	token, err := auth.NewToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.CreateCollectionShare(r.Context(), userID, id, token); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "collection not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token})
}

// handleGetCollectionShare previews a share token (name + feed URLs) without
// importing it.
func (s *Server) handleGetCollectionShare(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	cs, err := s.store.GetCollectionShare(r.Context(), r.PathValue("token"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "share link not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

// importFeedsIntoNewCollection creates a (collision-free) collection for the
// user named name, adds the given feed URLs to the account, links the added
// feeds to the new collection, and returns the collection plus per-URL results.
func (s *Server) importFeedsIntoNewCollection(ctx context.Context, userID int64, name string, urls []string) (*store.Collection, []refresh.AddFeedResult, error) {
	collection, err := s.store.CreateCollection(ctx, userID, s.uniqueCollectionName(ctx, userID, name))
	if err != nil {
		return nil, nil, err
	}
	results := s.refresher.AddFeeds(ctx, userID, urls)
	addedIDs := make([]int64, 0, len(results))
	for _, res := range results {
		if res.Feed != nil {
			addedIDs = append(addedIDs, res.Feed.ID)
		}
	}
	if len(addedIDs) > 0 {
		if _, err := s.store.AddFeedsToCollection(ctx, userID, collection.ID, addedIDs); err != nil {
			return nil, nil, err
		}
	}
	return collection, results, nil
}

// feedURLsFromShare collects the non-blank feed URLs from a shared collection.
func feedURLsFromShare(cs *store.CollectionShare) []string {
	urls := make([]string, 0, len(cs.Feeds))
	for _, f := range cs.Feeds {
		if u := strings.TrimSpace(f.URL); u != "" {
			urls = append(urls, u)
		}
	}
	return urls
}

// writeImportResult serializes the standard import response.
func writeImportResult(w http.ResponseWriter, collection *store.Collection, urls []string, results []refresh.AddFeedResult) {
	added := 0
	for _, res := range results {
		if res.Feed != nil {
			added++
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"collection": collection,
		"added":      added,
		"total":      len(urls),
		"results":    results,
	})
}

// handleImport imports a shared collection: it creates a (collision-free)
// collection for the user, adds the shared feeds to the account, and links them
// to the new collection.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req struct {
		Token string `json:"token"`
		Name  string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}
	cs, err := s.store.GetCollectionShare(r.Context(), token)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "share link not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = cs.Name
	}
	if name == "" {
		name = "Imported"
	}
	urls := feedURLsFromShare(cs)

	collection, results, err := s.importFeedsIntoNewCollection(r.Context(), userID, name, urls)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeImportResult(w, collection, urls, results)
}

// handleImportCollection imports a public collection by ID: it creates a
// (collision-free) collection for the user and adds all of the source
// collection's feeds to the account.
func (s *Server) handleImportCollection(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	cs, err := s.store.GetPublicCollectionForImport(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "collection not found or not public")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	urls := feedURLsFromShare(cs)
	collection, results, err := s.importFeedsIntoNewCollection(r.Context(), userID, cs.Name, urls)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeImportResult(w, collection, urls, results)
}

// --- public collections (admin-approved visibility) ---

// handleRequestCollectionPublic marks the user's private collection as
// pending-publish.
func (s *Server) handleRequestCollectionPublic(w http.ResponseWriter, r *http.Request) {
	s.setCollectionVisibilityRequest(w, r, "public")
}

// handleRequestCollectionPrivate marks the user's public collection as
// pending-unpublish.
func (s *Server) handleRequestCollectionPrivate(w http.ResponseWriter, r *http.Request) {
	s.setCollectionVisibilityRequest(w, r, "private")
}

func (s *Server) setCollectionVisibilityRequest(w http.ResponseWriter, r *http.Request, want string) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	collection, err := s.store.SetCollectionVisibilityRequest(r.Context(), userID, id, want)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "collection not found")
			return
		}
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, collection)
}

// handleListPublicCollections is the community directory of public collections.
func (s *Server) handleListPublicCollections(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	cols, err := s.store.ListPublicCollections(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cols == nil {
		cols = []store.PublicCollection{}
	}
	writeJSON(w, http.StatusOK, cols)
}

// handleListPendingCollectionShares returns the admin queue of pending
// collection-visibility changes.
func (s *Server) handleListPendingCollectionShares(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	reqs, err := s.store.ListPendingCollectionShares(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if reqs == nil {
		reqs = []store.CollectionShareRequest{}
	}
	writeJSON(w, http.StatusOK, reqs)
}

func (s *Server) handleApproveCollectionShare(w http.ResponseWriter, r *http.Request) {
	s.resolveCollectionShare(w, r, true)
}

func (s *Server) handleRejectCollectionShare(w http.ResponseWriter, r *http.Request) {
	s.resolveCollectionShare(w, r, false)
}

func (s *Server) resolveCollectionShare(w http.ResponseWriter, r *http.Request, approve bool) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	collection, err := s.store.ResolveCollectionShare(r.Context(), id, approve)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no pending change for this collection")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, collection)
}

// handleCancelCollectionVisibilityRequest lets the owner withdraw their own
// pending visibility change, reverting the collection to its pre-request state.
func (s *Server) handleCancelCollectionVisibilityRequest(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	collection, err := s.store.CancelCollectionVisibilityRequest(r.Context(), userID, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no pending change for this collection")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, collection)
}

// uniqueCollectionName returns name if it is free for the user, otherwise a
// suffixed variant.
func (s *Server) uniqueCollectionName(ctx context.Context, userID int64, name string) string {
	cols, err := s.store.ListCollections(ctx, userID)
	if err != nil {
		return name
	}
	taken := make(map[string]bool, len(cols))
	for _, c := range cols {
		taken[strings.ToLower(c.Name)] = true
	}
	if !taken[strings.ToLower(name)] {
		return name
	}
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s (%d)", name, i)
		if !taken[strings.ToLower(cand)] {
			return cand
		}
	}
}

// --- items ---

func (s *Server) handleListItems(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	q := store.ItemQuery{}

	if fid := r.URL.Query().Get("feed_id"); fid != "" {
		id, err := strconv.ParseInt(fid, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "feed_id must be an integer")
			return
		}
		q.FeedID = &id
	}
	if cid := r.URL.Query().Get("collection_id"); cid != "" {
		id, err := strconv.ParseInt(cid, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "collection_id must be an integer")
			return
		}
		q.CollectionID = &id
	}
	switch r.URL.Query().Get("unread") {
	case "true":
		q.Unread = boolPtr(true)
	case "false":
		q.Unread = boolPtr(false)
	}
	since, err := parseTimeParam("since", r.URL.Query().Get("since"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	q.Since = since
	until, err := parseTimeParam("until", r.URL.Query().Get("until"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	q.Until = until

	q.Limit = queryInt(r, "limit", 50, 1, 200)
	q.Offset = queryInt(r, "offset", 0, 0, 1<<30)

	items, total, err := s.store.ListItems(r.Context(), userID, q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []store.Item{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) handleUnreadCount(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	n, err := s.store.UnreadCount(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n})
}

func (s *Server) handleSetItemRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		Read bool `json:"read"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.store.SetItemRead(r.Context(), userID, id, req.Read); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "item not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleBulkSetItemRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req struct {
		IDs  []int64 `json:"ids"`
		Read bool    `json:"read"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.IDs) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "updated": 0})
		return
	}
	n, err := s.store.SetItemsRead(r.Context(), userID, req.IDs, req.Read)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "updated": n})
}

// handleDigest returns the recent items of one collection as a single
// LLM-ready payload: a markdown digest (default) or the raw item list as JSON.
// Query params: collection_id (required), since, until (RFC3339; defaults to
// the last 24h), format (markdown|json), limit.
func (s *Server) handleDigest(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	q := r.URL.Query()

	cid, err := strconv.ParseInt(q.Get("collection_id"), 10, 64)
	if err != nil || cid <= 0 {
		writeError(w, http.StatusBadRequest, "collection_id must be a positive integer")
		return
	}
	collection, err := s.store.GetCollection(r.Context(), userID, cid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "collection not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	now := time.Now().UTC()
	since := now.Add(-24 * time.Hour)
	if raw := q.Get("since"); raw != "" {
		t, err := parseTimeParam("since", raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		since = *t
	}
	until := now
	if raw := q.Get("until"); raw != "" {
		t, err := parseTimeParam("until", raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		until = *t
	}

	items, _, err := s.store.ListItems(r.Context(), userID, store.ItemQuery{
		CollectionID: &cid,
		Since:        &since,
		Until:        &until,
		Limit:        queryInt(r, "limit", 50, 1, 200),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []store.Item{}
	}

	format := q.Get("format")
	if format == "" {
		format = "markdown"
	}
	switch format {
	case "markdown":
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, renderDigestMarkdown(collection.Name, since, until, items))
	case "json":
		writeJSON(w, http.StatusOK, map[string]any{
			"collection": collection.Name,
			"since":      since.UTC().Format(time.RFC3339),
			"until":      until.UTC().Format(time.RFC3339),
			"count":      len(items),
			"items":      items,
		})
	default:
		writeError(w, http.StatusBadRequest, "format must be markdown or json")
	}
}

// --- api index ---

// apiEndpoints is the discovery document served at GET /api (and listed in
// the README). Keep it in sync with the routes registered in New.
var apiEndpoints = []struct {
	Method      string
	Path        string
	Description string
	Auth        bool
}{
	{"GET", "/api", "This index: all available endpoints", false},
	{"GET", "/api/feeds", "List feeds (with collection IDs)", true},
	{"POST", "/api/feeds", "Add a feed (fetches + seeds it), body {\"url\": \"...\"}", true},
	{"POST", "/api/feeds/bulk", "Add many feeds, body {\"urls\": [\"...\", ...]}", true},
	{"POST", "/api/feeds/bulk-delete", "Delete feeds by ID, body {\"ids\": [1, 2]}", true},
	{"PUT", "/api/feeds/{id}/collections", "Set a feed's collections, body {\"collection_ids\": [1]}", true},
	{"DELETE", "/api/feeds/{id}", "Delete a feed and its items", true},
	{"GET", "/api/collections", "List collections (with feed counts)", true},
	{"POST", "/api/collections", "Create a collection, body {\"name\": \"...\"}", true},
	{"PATCH", "/api/collections/{id}", "Rename a collection, body {\"name\": \"...\"}", true},
	{"DELETE", "/api/collections/{id}", "Delete a collection", true},
	{"POST", "/api/collections/{id}/share", "Create or regenerate a collection's share token; returns {\"token\": \"...\"}", true},
	{"GET", "/api/collection-shares/{token}", "Preview a share token: the collection's name and feed URLs", true},
	{"POST", "/api/imports", "Import a shared collection, body {\"token\": \"...\", \"name\": \"...\" (optional)}", true},
	{"POST", "/api/collections/{id}/make-public", "Request to make a collection public (moves it to pending admin review)", true},
	{"POST", "/api/collections/{id}/make-private", "Request to make a collection private (moves it to pending admin review)", true},
	{"POST", "/api/collections/{id}/cancel-visibility", "Cancel your own pending collection visibility change (reverts to pre-request state)", true},
	{"POST", "/api/collections/{id}/import", "Import a public collection by ID (creates a new collection with its feeds)", true},
	{"GET", "/api/public-collections", "Community directory of public collections (with their feed URLs)", true},
	{"GET", "/api/public-collections/pending", "Pending collection visibility changes awaiting admin review", true},
	{"POST", "/api/public-collections/{id}/approve", "Approve a pending collection visibility change", true},
	{"POST", "/api/public-collections/{id}/reject", "Reject a pending collection visibility change", true},
	{"GET", "/api/recommended-feeds", "Admin-curated starter feeds shown during onboarding", true},
	{"POST", "/api/recommended-feeds", "Add (or update) a recommended feed, body {\"url\": \"...\", \"title\": \"...\", \"site_url\": \"...\"}", true},
	{"DELETE", "/api/recommended-feeds/{id}", "Remove a recommended feed", true},
	{"GET", "/api/items", "List items. Query: feed_id, collection_id, unread (true|false), since, until (RFC3339), limit (1-200), offset", true},
	{"GET", "/api/items/unread-count", "Total unread item count", true},
	{"POST", "/api/items/{id}/read", "Set an item's read state, body {\"read\": true}", true},
	{"POST", "/api/items/bulk-read", "Set read state for many items, body {\"ids\": [1, 2], \"read\": true}", true},
	{"GET", "/api/digest", "LLM-ready digest of a collection's recent items. Query: collection_id (required), since, until (RFC3339; default: last 24h), format (markdown|json), limit", true},
	{"POST", "/api/refresh", "Refresh all feeds now", true},
	{"POST", "/api/auth/register", "Register an account, body {\"email\": \"...\", \"name\": \"...\", \"password\": \"...\"}; sends a verification email", false},
	{"POST", "/api/auth/login", "Log in, body {\"email\": \"...\", \"password\": \"...\"}; sets the session cookie", false},
	{"POST", "/api/auth/logout", "Log out; clears the session cookie", false},
	{"GET", "/api/auth/me", "The authenticated user (401 when logged out)", true},
	{"GET", "/api/auth/verify", "Consume an email verification link. Query: token", false},
	{"POST", "/api/auth/forgot-password", "Email a password reset link, body {\"email\": \"...\"} (always 200)", false},
	{"POST", "/api/auth/reset-password", "Set a new password, body {\"token\": \"...\", \"password\": \"...\"}", false},
	{"GET", "/api/auth/oidc", "Start the OIDC login flow (302 to the provider)", false},
	{"GET", "/api/auth/oidc/callback", "OIDC redirect target. Query: code, state", false},
}

// handleAPIIndex is the catch-all for GET /api/*. It serves the endpoint
// index at the API base (GET /api, GET /api/) so the surface is discoverable
// by humans, LLMs, and other automation, and 404s for anything else.
func (s *Server) handleAPIIndex(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimSuffix(path.Clean(r.URL.Path), "/")
	if p != "/api" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	endpoints := make([]struct {
		Method      string `json:"method"`
		Path        string `json:"path"`
		Description string `json:"description"`
		Auth        bool   `json:"auth"`
	}, 0, len(apiEndpoints))
	for _, e := range apiEndpoints {
		endpoints = append(endpoints, struct {
			Method      string `json:"method"`
			Path        string `json:"path"`
			Description string `json:"description"`
			Auth        bool   `json:"auth"`
		}{e.Method, e.Path, e.Description, e.Auth})
	}
	var user any
	if userID, ok := s.currentUser(r); ok {
		if u, err := s.store.GetUser(r.Context(), userID); err == nil {
			user = u
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":        "elyfeed",
		"description": "Multi-user RSS reader. Authenticate with a session (POST /api/auth/login or OIDC), then pull recent items by time window (GET /api/items?since=...&until=...) or get a ready-to-use digest per collection (GET /api/digest).",
		"user":        user,
		"endpoints":   endpoints,
	})
}

// --- refresh ---

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	refreshed, err := s.refresher.RefreshUser(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"refreshed": refreshed})
}

// --- auth ---

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.auth.Register(r.Context(), r, req.Email, req.Name, req.Password); err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "verification email sent"})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	user, token, err := s.auth.Login(r.Context(), r, req.Email, req.Password)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	s.auth.SetSessionCookie(w, token)
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	var token string
	if c, err := r.Cookie(auth.SessionCookieName); err == nil {
		token = c.Value
	}
	s.auth.Logout(r.Context(), token)
	s.auth.ClearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	user, err := s.store.GetUser(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// handleVerifyEmail is the target of the email verification link. It
// activates the account, starts a session and sends the browser to the app.
func (s *Server) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	_, token, err := s.auth.VerifyEmail(r.Context(), r.URL.Query().Get("token"))
	if err != nil {
		writeAuthError(w, err)
		return
	}
	s.auth.SetSessionCookie(w, token)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.auth.ForgotPassword(r.Context(), r, req.Email); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Always report success (even for unknown emails) so the endpoint cannot
	// be used to enumerate accounts.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.auth.ResetPassword(r.Context(), req.Token, req.Password); err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	authURL, err := s.auth.OIDCAuthURL()
	if err != nil {
		writeAuthError(w, err)
		return
	}
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	_, token, err := s.auth.OIDCCallback(r.Context(), q.Get("code"), q.Get("state"))
	if err != nil {
		writeAuthError(w, err)
		return
	}
	s.auth.SetSessionCookie(w, token)
	http.Redirect(w, r, "/", http.StatusFound)
}

// writeAuthError maps auth service errors to HTTP status codes.
func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, auth.ErrUserNotVerified):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, auth.ErrRateLimited):
		writeError(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, auth.ErrInvalidToken), errors.Is(err, auth.ErrInvalidEmail),
		errors.Is(err, auth.ErrWeakPassword), errors.Is(err, auth.ErrOIDCDisabled):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// renderDigestMarkdown renders items as a markdown digest grouped by feed
// (alphabetical), newest first within each feed.
func renderDigestMarkdown(collection string, since, until time.Time, items []store.Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Digest — %s (%s → %s)\n\n",
		collection, since.UTC().Format(time.RFC3339), until.UTC().Format(time.RFC3339))
	if len(items) == 0 {
		b.WriteString("_No items in this window._\n")
		return b.String()
	}

	sorted := make([]store.Item, len(items))
	copy(sorted, items)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].FeedTitle != sorted[j].FeedTitle {
			return sorted[i].FeedTitle < sorted[j].FeedTitle
		}
		return itemTime(sorted[i]).After(itemTime(sorted[j]))
	})

	lastFeed := ""
	for _, it := range sorted {
		if it.FeedTitle != lastFeed {
			lastFeed = it.FeedTitle
			fmt.Fprintf(&b, "## %s\n\n", it.FeedTitle)
		}
		line := fmt.Sprintf("- [%s](%s)", it.Title, it.Link)
		if it.Author != "" {
			line += " — " + it.Author
		}
		line += ", " + itemTime(it).UTC().Format("2006-01-02 15:04 UTC")
		b.WriteString(line + "\n")
		if excerpt := digestExcerpt(it.Content); excerpt != "" {
			b.WriteString("  > " + excerpt + "\n")
		}
	}
	return b.String()
}

// itemTime is the timestamp shown for an item: its publication date, falling
// back to the fetch time when the feed provides none.
func itemTime(it store.Item) time.Time {
	if it.PublishedAt != nil {
		return *it.PublishedAt
	}
	return it.FetchedAt
}

// digestExcerpt trims item content to a short single-line excerpt so digests
// stay small enough for LLM context windows. Whitespace (including newlines)
// is collapsed so the excerpt never breaks the markdown line it lives on.
func digestExcerpt(content string) string {
	c := strings.Join(strings.Fields(content), " ")
	const maxRunes = 300
	if len([]rune(c)) <= maxRunes {
		return c
	}
	r := []rune(c)[:maxRunes]
	if i := strings.LastIndex(string(r), " "); i > 0 {
		r = r[:i]
	}
	return string(r) + "…"
}

// --- SPA fallback ---

type spaHandler struct {
	assets fs.FS
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p == "" || p == "." {
		p = "index.html"
	}
	if f, err := h.assets.Open(p); err == nil {
		f.Close()
		http.ServeFileFS(w, r, h.assets, p)
		return
	}
	http.ServeFileFS(w, r, h.assets, "index.html")
}

// --- helpers ---

// maxRequestBodyBytes caps /api request bodies so a client cannot exhaust
// memory with an oversized payload.
const maxRequestBodyBytes = int64(1 << 20) // 1 MB

// contentSecurityPolicy is applied to every response. The built frontend
// loads only same-origin assets (Vite emits external scripts and
// stylesheets, no inline scripts); 'unsafe-inline' in style-src covers the
// style attributes React sets at runtime, and remote image sources are
// allowed for feed content.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: https:; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"frame-ancestors 'none'"

// withSecurityHeaders adds the security headers to every response: a strict
// Content-Security-Policy, nosniff, and a referrer policy that never leaks
// the request URL to third parties.
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// withAPIPolicy hardens /api request bodies: a maxRequestBodyBytes size cap,
// and a JSON content type on every mutating request (the same-origin SPA
// always sends one; cross-origin form submissions cannot).
func withAPIPolicy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api") {
			next.ServeHTTP(w, r)
			return
		}
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if mediaType(r.Header.Get("Content-Type")) != "application/json" {
				writeError(w, http.StatusUnsupportedMediaType,
					"mutating requests require Content-Type: application/json")
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// mediaType returns the MIME type of a Content-Type header value, or "" when
// the header is absent or malformed.
func mediaType(header string) string {
	if header == "" {
		return ""
	}
	t, _, err := mime.ParseMediaType(header)
	if err != nil {
		return ""
	}
	return t
}

// decodeJSON decodes the request body into v, writing an error response and
// returning false on failure: 413 when the body exceeds maxRequestBodyBytes,
// 400 for anything else.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
		}
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func parseID(w http.ResponseWriter, raw string) (int64, bool) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

// parseTimeParam parses an optional RFC3339 timestamp query parameter. It
// returns nil when the parameter is absent and an error when it is present
// but malformed.
func parseTimeParam(key, raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be an RFC3339 timestamp (e.g. 2026-08-22T00:00:00Z)", key)
	}
	return &t, nil
}

func boolPtr(b bool) *bool { return &b }

func queryInt(r *http.Request, key string, def, min, max int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}
