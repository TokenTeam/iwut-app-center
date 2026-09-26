package transport

import "github.com/goforj/wire"

// ProviderSet wires the transport adapters. The identity middleware is applied
// internally; callers only construct the verifier and the service.
var ProviderSet = wire.NewSet(
	NewIdentityVerifier,
	NewApplicationService,
	NewApplicationVersionService,
	NewApplicationReviewService,
	NewApplicationPublicationService,
	NewTesterJoinLinkService,
	NewTesterMembershipService,
	NewCatalogService,
	NewApplicationProfileRevisionService,
	NewServers,
)
