package transport

import (
	"context"

	"iwut-app-center/internal/shared"
)

type identityContextKey struct{}

func withDeveloperIdentity(ctx context.Context, identity shared.DeveloperIdentity) context.Context {
	return context.WithValue(ctx, identityContextKey{}, identity)
}

func developerIdentityFromContext(ctx context.Context) (shared.DeveloperIdentity, bool) {
	if ctx == nil {
		return shared.DeveloperIdentity{}, false
	}
	identity, ok := ctx.Value(identityContextKey{}).(shared.DeveloperIdentity)
	return identity, ok
}
