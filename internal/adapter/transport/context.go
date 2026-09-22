package transport

import (
	"context"

	"iwut-app-center/internal/shared"
)

type identityContextKey struct{}

func withDeveloperIdentity(ctx context.Context, identity shared.DeveloperIdentity) context.Context {
	return withTrustedIdentity(ctx, shared.TrustedIdentity{
		AuthID: identity.AuthID, DeveloperStatus: identity.DeveloperStatus,
	})
}

func withTrustedIdentity(ctx context.Context, identity shared.TrustedIdentity) context.Context {
	identity.Permissions = append([]string{}, identity.Permissions...)
	return context.WithValue(ctx, identityContextKey{}, identity)
}

func trustedIdentityFromContext(ctx context.Context) (shared.TrustedIdentity, bool) {
	if ctx == nil {
		return shared.TrustedIdentity{}, false
	}
	identity, ok := ctx.Value(identityContextKey{}).(shared.TrustedIdentity)
	identity.Permissions = append([]string{}, identity.Permissions...)
	return identity, ok
}

func developerIdentityFromContext(ctx context.Context) (shared.DeveloperIdentity, bool) {
	identity, ok := trustedIdentityFromContext(ctx)
	if !ok {
		return shared.DeveloperIdentity{}, false
	}
	return shared.DeveloperIdentity{AuthID: identity.AuthID, DeveloperStatus: identity.DeveloperStatus}, true
}

func authenticatedUserIdentityFromContext(ctx context.Context) (shared.AuthenticatedUserIdentity, bool) {
	identity, ok := trustedIdentityFromContext(ctx)
	if !ok || !identity.AuthID.IsValid() {
		return shared.AuthenticatedUserIdentity{}, false
	}
	return shared.AuthenticatedUserIdentity{AuthID: identity.AuthID}, true
}
