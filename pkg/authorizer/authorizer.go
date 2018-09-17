package authorizer

import (
	"context"
)

type Interface interface {
	AuthorizeToken(token string) (*User, bool, error)
}

type User struct {
	ID     string
	Labels map[string]string
}

var userKey key

type key int

func WithUser(ctx context.Context, user *User) context.Context {
	return context.WithValue(ctx, userKey, user)
}

func FromContext(ctx context.Context) (*User, bool) {
	user, ok := ctx.Value(userKey).(*User)
	return user, ok
}
