package transport

import "github.com/goforj/wire"

// ProviderSet wires the transport adapters. The identity middleware is applied
// internally; callers only construct the verifier and the service.
var ProviderSet = wire.NewSet(
	NewIdentityVerifier,
	NewApplicationService,
	NewApplicationManagementQueryService,
	NewApplicationAdminTransferService,
	NewApplicationClosureService,
	NewApplicationOperationsService,
	NewApplicationVersionService,
	NewApplicationReviewService,
	NewApplicationReviewQueryService,
	NewApplicationPublicationServiceWithGrey,
	NewTesterJoinLinkService,
	NewTesterMembershipService,
	NewCatalogService,
	NewRuntimeResolutionService,
	NewApplicationCatalogService,
	NewApplicationProfileRevisionService,
	NewApplicationProfileReviewService,
	NewApplicationProfileReviewQueryService,
	NewApplicationFilterService,
	NewOAuthClientService,
	NewOAuthClientProviderService,
	NewServersWithApplicationCatalogAndOAuthProvider,
)
