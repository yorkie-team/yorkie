package users

import (
	"context"

	"github.com/yorkie-team/yorkie/api/types"
)

// userKey is the key for the context.Context.
type userKey struct{}

// From returns the user from the context.
func From(ctx context.Context) *types.User {
	return ctx.Value(userKey{}).(*types.User)
}

// With returns a new context with the given user.
func With(ctx context.Context, user *types.User) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}
