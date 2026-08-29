package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"elyfeed/internal/auth"
	"elyfeed/internal/store"
)

// newEmptyServer builds a handler with no users, for auth-flow tests.
func newEmptyServer(t *testing.T) (http.Handler, *stubStore, *recordingMailer) {
	t.Helper()
	mail := &recordingMailer{}
	m := &stubStore{
		sessions:      map[string]*store.Session{},
		verifications: map[string]stubVerification{},
	}
	a := auth.NewService(m, mail, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), "", time.Hour, false)
	return New(m, nil, fstest.MapFS{}, a), m, mail
}

// sessionCookie extracts the session cookie set by a response.
func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	sc := rec.Header().Get("Set-Cookie")
	if sc == "" {
		t.Fatal("response set no cookie")
	}
	// Set-Cookie carries attributes after the first ';'.
	nameVal := sc
	if i := strings.IndexByte(sc, ';'); i >= 0 {
		nameVal = sc[:i]
	}
	cookies, err := http.ParseCookie(nameVal)
	if err != nil {
		t.Fatalf("parse Set-Cookie %q: %v", sc, err)
	}
	for _, c := range cookies {
		if c.Name == auth.SessionCookieName {
			return c
		}
	}
	t.Fatalf("Set-Cookie %q has no %s cookie", sc, auth.SessionCookieName)
	return nil
}

// doWithCookie issues a request carrying the given cookie (nil for none).
// Mutating methods carry the JSON content type the server now requires.
func doWithCookie(t *testing.T, h http.Handler, method, target string, c *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if c != nil {
		req.AddCookie(c)
	}
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// registerAndVerify registers an account and follows the verification link,
// returning the session cookie the verify step sets.
func registerAndVerify(t *testing.T, h http.Handler, mail *recordingMailer, email, password string) *http.Cookie {
	t.Helper()
	rec := doJSON(t, h, "POST", "/api/auth/register", map[string]string{
		"email": email, "name": "Tester", "password": password,
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("register: status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	if len(mail.messages) != 1 {
		t.Fatalf("register: mailer has %d messages, want 1", len(mail.messages))
	}
	token := tokenFromEmail(t, mail.messages[0].body)
	rec = do(t, h, "GET", "/api/auth/verify?token="+token)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("verify: status = %d, location = %q, want 302 -> /", rec.Code, rec.Header().Get("Location"))
	}
	return sessionCookie(t, rec)
}

func TestAuthFullLifecycle(t *testing.T) {
	h, m, mail := newEmptyServer(t)

	c := registerAndVerify(t, h, mail, "New@Example.com", "supersecret1")

	// The first verified user is the admin; the email is normalized.
	var me store.User
	rec := doWithCookie(t, h, "GET", "/api/auth/me", c)
	if rec.Code != http.StatusOK {
		t.Fatalf("me: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.Email != "new@example.com" || me.Role != "admin" || !me.EmailVerified {
		t.Fatalf("me = %+v, want normalized email, admin role, verified", me)
	}
	if len(m.sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(m.sessions))
	}

	// Data endpoints accept the new session.
	if rec := doWithCookie(t, h, "GET", "/api/feeds", c); rec.Code != http.StatusOK {
		t.Fatalf("feeds: status = %d, want 200", rec.Code)
	}

	// Logout kills the session.
	if rec := doWithCookie(t, h, "POST", "/api/auth/logout", c); rec.Code != http.StatusOK {
		t.Fatalf("logout: status = %d, want 200", rec.Code)
	}
	if rec := doWithCookie(t, h, "GET", "/api/auth/me", c); rec.Code != http.StatusUnauthorized {
		t.Fatalf("me after logout: status = %d, want 401", rec.Code)
	}

	// Password login works after verification.
	rec = doJSON(t, h, "POST", "/api/auth/login", map[string]string{
		"email": "new@example.com", "password": "supersecret1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	c2 := sessionCookie(t, rec)
	if rec := doWithCookie(t, h, "GET", "/api/auth/me", c2); rec.Code != http.StatusOK {
		t.Fatalf("me after login: status = %d, want 200", rec.Code)
	}
}

func TestAuthRegisterValidation(t *testing.T) {
	h, _, _ := newTestServer(t) // fixture already has a verified admin@example.com

	cases := []struct {
		body map[string]string
		want int
	}{
		{map[string]string{"email": "not-an-email", "password": "supersecret1"}, http.StatusBadRequest},
		{map[string]string{"email": "x@example.com", "password": "short"}, http.StatusBadRequest},
		{map[string]string{"email": "admin@example.com", "password": "supersecret1"}, http.StatusConflict},
	}
	for _, tc := range cases {
		rec := doJSON(t, h, "POST", "/api/auth/register", tc.body)
		if rec.Code != tc.want {
			t.Fatalf("%v: status = %d, want %d: %s", tc.body, rec.Code, tc.want, rec.Body.String())
		}
	}
}

func TestAuthRegisterResendsForUnverified(t *testing.T) {
	h, _, mail := newEmptyServer(t)

	for i := 1; i <= 2; i++ {
		rec := doJSON(t, h, "POST", "/api/auth/register", map[string]string{
			"email": "u@example.com", "password": "supersecret1",
		})
		if rec.Code != http.StatusAccepted {
			t.Fatalf("register %d: status = %d, want 202: %s", i, rec.Code, rec.Body.String())
		}
		if len(mail.messages) != i {
			t.Fatalf("after register %d: mailer has %d messages, want %d", i, len(mail.messages), i)
		}
	}
}

func TestAuthLoginBeforeVerify(t *testing.T) {
	h, _, _ := newEmptyServer(t)

	rec := doJSON(t, h, "POST", "/api/auth/register", map[string]string{
		"email": "u@example.com", "password": "supersecret1",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("register: status = %d, want 202", rec.Code)
	}

	rec = doJSON(t, h, "POST", "/api/auth/login", map[string]string{
		"email": "u@example.com", "password": "supersecret1",
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("login before verify: status = %d, want 422: %s", rec.Code, rec.Body.String())
	}

	// Unknown email is indistinguishable from a wrong password.
	rec = doJSON(t, h, "POST", "/api/auth/login", map[string]string{
		"email": "ghost@example.com", "password": "supersecret1",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("login unknown email: status = %d, want 401", rec.Code)
	}
}

func TestAuthLoginRateLimited(t *testing.T) {
	h, _, _ := newEmptyServer(t)

	rec := doJSON(t, h, "POST", "/api/auth/register", map[string]string{
		"email": "u@example.com", "password": "supersecret1",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("register: status = %d, want 202", rec.Code)
	}

	for i := 1; i <= 5; i++ {
		rec := doJSON(t, h, "POST", "/api/auth/login", map[string]string{
			"email": "u@example.com", "password": "wrongpassword",
		})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i, rec.Code)
		}
	}
	rec = doJSON(t, h, "POST", "/api/auth/login", map[string]string{
		"email": "u@example.com", "password": "wrongpassword",
	})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt 6: status = %d, want 429", rec.Code)
	}
}

func TestAuthVerifyInvalidAndSingleUse(t *testing.T) {
	h, _, mail := newEmptyServer(t)

	rec := doJSON(t, h, "POST", "/api/auth/register", map[string]string{
		"email": "u@example.com", "password": "supersecret1",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("register: status = %d, want 202", rec.Code)
	}
	token := tokenFromEmail(t, mail.messages[0].body)

	if rec := do(t, h, "GET", "/api/auth/verify?token=bogus"); rec.Code != http.StatusBadRequest {
		t.Fatalf("verify bogus: status = %d, want 400", rec.Code)
	}
	if rec := do(t, h, "GET", "/api/auth/verify?token="+token); rec.Code != http.StatusFound {
		t.Fatalf("verify: status = %d, want 302", rec.Code)
	}
	if rec := do(t, h, "GET", "/api/auth/verify?token="+token); rec.Code != http.StatusBadRequest {
		t.Fatalf("verify twice: status = %d, want 400", rec.Code)
	}
}

func TestAuthForgotResetFlow(t *testing.T) {
	h, _, mail := newEmptyServer(t)

	c := registerAndVerify(t, h, mail, "u@example.com", "oldpassword1")
	if rec := doWithCookie(t, h, "GET", "/api/auth/me", c); rec.Code != http.StatusOK {
		t.Fatalf("me: status = %d, want 200", rec.Code)
	}

	// Unknown emails get the same response and no email.
	before := len(mail.messages)
	if rec := doJSON(t, h, "POST", "/api/auth/forgot-password", map[string]string{"email": "ghost@example.com"}); rec.Code != http.StatusOK {
		t.Fatalf("forgot (unknown): status = %d, want 200", rec.Code)
	}
	if len(mail.messages) != before {
		t.Fatalf("forgot (unknown) sent a mail")
	}

	if rec := doJSON(t, h, "POST", "/api/auth/forgot-password", map[string]string{"email": "u@example.com"}); rec.Code != http.StatusOK {
		t.Fatalf("forgot: status = %d, want 200", rec.Code)
	}
	if len(mail.messages) != before+1 {
		t.Fatalf("forgot: mailer has %d messages, want %d", len(mail.messages), before+1)
	}
	resetToken := tokenFromEmail(t, mail.messages[before].body)

	// A verification token cannot be used for a reset, and vice versa.
	verifToken := tokenFromEmail(t, mail.messages[0].body)
	if rec := doJSON(t, h, "POST", "/api/auth/reset-password", map[string]string{
		"token": verifToken, "password": "newpassword1",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("reset with verify token: status = %d, want 400", rec.Code)
	}
	if rec := doJSON(t, h, "POST", "/api/auth/reset-password", map[string]string{
		"token": resetToken, "password": "short",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("reset weak password: status = %d, want 400", rec.Code)
	}
	if rec := doJSON(t, h, "POST", "/api/auth/reset-password", map[string]string{
		"token": resetToken, "password": "newpassword1",
	}); rec.Code != http.StatusOK {
		t.Fatalf("reset: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, h, "POST", "/api/auth/reset-password", map[string]string{
		"token": resetToken, "password": "newpassword1",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("reset twice: status = %d, want 400", rec.Code)
	}

	// Old password is dead, new one works.
	if rec := doJSON(t, h, "POST", "/api/auth/login", map[string]string{
		"email": "u@example.com", "password": "oldpassword1",
	}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("login with old password: status = %d, want 401", rec.Code)
	}
	if rec := doJSON(t, h, "POST", "/api/auth/login", map[string]string{
		"email": "u@example.com", "password": "newpassword1",
	}); rec.Code != http.StatusOK {
		t.Fatalf("login with new password: status = %d, want 200", rec.Code)
	}
}

func TestAuthOIDCDisabled(t *testing.T) {
	h, _, _ := newEmptyServer(t)

	for _, target := range []string{
		"/api/auth/oidc",
		"/api/auth/oidc/callback?code=x&state=y",
	} {
		if rec := do(t, h, "GET", target); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", target, rec.Code)
		}
	}
}

func TestDataEndpointsRequireAuth(t *testing.T) {
	h, _, _ := newTestServer(t)

	if rec := do(t, h, "GET", "/api/feeds"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("feeds unauthenticated: status = %d, want 401", rec.Code)
	}
	if rec := doAuth(t, h, "GET", "/api/feeds"); rec.Code != http.StatusOK {
		t.Fatalf("feeds authenticated: status = %d, want 200", rec.Code)
	}
	if rec := do(t, h, "GET", "/api/auth/me"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("me unauthenticated: status = %d, want 401", rec.Code)
	}
	// The API index stays public.
	if rec := do(t, h, "GET", "/api/"); rec.Code != http.StatusOK {
		t.Fatalf("api index: status = %d, want 200", rec.Code)
	}
}
