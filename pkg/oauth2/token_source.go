package oauth2

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

type passwordCredentialsTokenSource struct {
	ctx                context.Context
	cfg                *oauth2.Config
	username, password string

	mu                sync.Mutex // protects the fields below
	refreshToken      *oauth2.Token
	accessTokenSource oauth2.TokenSource
}

// NewPasswordCredentialsTokenSource returns an oauth2.TokenSource that
// creates an access and refresh token pair
// using the given resource owner username and password
// according to https://tools.ietf.org/html/rfc6749#section-4.3.
//
// The access token is reused until it expires.
// It is automatically refreshed as long as the refresh token is valid.
//
// The refresh token is reused until it expires.
// Once expired, a new token pair is created
// using the given resource owner and password.
//
// It is safe for concurrent use.
func NewPasswordCredentialsTokenSource(ctx context.Context, cfg *oauth2.Config, username, password string) *passwordCredentialsTokenSource {
	return &passwordCredentialsTokenSource{
		ctx:      ctx,
		username: username,
		password: password,
		cfg:      cfg,
	}
}

func (c *passwordCredentialsTokenSource) Token() (*oauth2.Token, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var (
		tok *oauth2.Token
		err error
	)

	if c.refreshToken.Valid() {
		tok, err = c.accessTokenSource.Token()

		rerr, ok := err.(*oauth2.RetrieveError)
		if ok && rerr.Response != nil && rerr.Response.StatusCode == http.StatusBadRequest {
			return c.passwordCredentialsToken()
		}

		if err != nil {
			return nil, fmt.Errorf("access token source failed: %v", err)
		}

		// Usually a new refresh token is issued when the access token was refreshed.
		// If it is the same, return immediately.
		if tok.RefreshToken == c.refreshToken.RefreshToken {
			return tok, nil
		}

		err = c.setRefreshToken(tok)
		if err != nil {
			return nil, err
		}

		return tok, nil
	}

	return c.passwordCredentialsToken()
}

func (c *passwordCredentialsTokenSource) passwordCredentialsToken() (*oauth2.Token, error) {
	tok, err := c.cfg.PasswordCredentialsToken(c.ctx, c.username, c.password)
	if err != nil {
		return nil, fmt.Errorf("password credentials token source failed: %v", err)
	}

	c.accessTokenSource = c.cfg.TokenSource(c.ctx, tok)

	err = c.setRefreshToken(tok)
	if err != nil {
		return nil, err
	}

	return tok, nil
}

func (c *passwordCredentialsTokenSource) setRefreshToken(tok *oauth2.Token) error {
	expires, ok := tok.Extra("refresh_expires_in").(float64)
	if !ok {
		return fmt.Errorf("refresh_expires_in is not a float64, but %T", tok.Extra("refresh_expires_in"))
	}

	// create a dummy access token to reuse calculation logic for the Valid() method
	c.refreshToken = &oauth2.Token{
		AccessToken:  tok.RefreshToken,
		RefreshToken: tok.RefreshToken,
		Expiry:       time.Now().Add(time.Duration(int64(expires)) * time.Second),
	}

	return nil
}
