// Package auth manages user identity and authentication.
//
// The authenticated user's ID is carried in the request context. Handlers read
// it with UserIDFromContext; the middleware that populates it is wired in the
// HTTP layer.
package auth

import "context"

type ctxKey struct{}

// WithUserID returns a context carrying the authenticated user's ID.
func WithUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, ctxKey{}, userID)
}

// UserIDFromContext returns the authenticated user's ID and whether an
// authenticated user is present in the context.
func UserIDFromContext(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ctxKey{}).(int64)
	return id, ok
}
