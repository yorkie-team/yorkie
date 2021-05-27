package auth

import "context"

type key int

const tokenKey key = 0

// TokenFromCtx returns the tokenKey from the given context.
func TokenFromCtx(ctx context.Context) string {
	return ctx.Value(tokenKey).(string)
}

// CtxWithToken creates a new context with the given token.
func CtxWithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenKey, token)
}
