package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCClient abstracts an OpenID Connect provider so the auth service can be
// tested without a real identity provider.
type OIDCClient interface {
	// AuthURL builds the provider's authorization redirect URL for the
	// given opaque state value.
	AuthURL(state string) string
	// Exchange validates the authorization code and returns the verified
	// email and (optional) display name of the remote user.
	Exchange(ctx context.Context, code string) (email, name string, err error)
}

// goOIDC is the production OIDCClient backed by coreos/go-oidc.
type goOIDC struct {
	authConfig *oauth2.Config
	verifier   *oidc.IDTokenVerifier
	httpc      *http.Client
}

// NewOIDCClient discovers the provider at issuer and returns a ready client.
// It fails if discovery fails or the configuration is invalid.
func NewOIDCClient(ctx context.Context, httpc *http.Client, issuer, clientID, clientSecret, redirectURL string, scopes []string) (OIDCClient, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "email", "profile"}
	}
	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       scopes,
	}
	verifier := provider.Verifier(&oidc.Config{ClientID: clientID})
	return &goOIDC{authConfig: cfg, verifier: verifier, httpc: httpc}, nil
}

func (c *goOIDC) AuthURL(state string) string {
	return c.authConfig.AuthCodeURL(state)
}

func (c *goOIDC) Exchange(ctx context.Context, code string) (string, string, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, c.httpc)
	token, err := c.authConfig.Exchange(ctx, code)
	if err != nil {
		return "", "", fmt.Errorf("oidc token exchange: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return "", "", errors.New("oidc: no id_token in token response")
	}
	idToken, err := c.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return "", "", fmt.Errorf("oidc id_token verification: %w", err)
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified *bool  `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return "", "", fmt.Errorf("oidc claims: %w", err)
	}
	if claims.Email == "" {
		return "", "", errors.New("oidc: provider did not provide an email")
	}
	if claims.EmailVerified != nil && !*claims.EmailVerified {
		return "", "", errors.New("oidc: provider reports the email as not verified")
	}
	return claims.Email, claims.Name, nil
}
