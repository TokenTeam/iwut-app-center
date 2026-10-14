package transport

import "github.com/goforj/wire"

// ProviderSet wires the transport adapters. The identity middleware is applied
// internally; callers only construct the verifier and the service.
var ProviderSet = wire.NewSet(
	NewTestApplicationCatalogService,
	NewApplicationTesterManagementService,
	NewIdentityVerifier,
	NewApplicationService,
	NewApplicationManagementQueryService,
	NewApplicationAdminTransferService,
	NewApplicationClosureService,
	NewApplicationOperationsService,
	NewApplicationArchivalService,
	NewApplicationVersionServiceWithQueryAndAbandonment,
	NewApplicationReviewService,
	NewApplicationReviewQueryService,
	NewApplicationPublicationServiceWithTestClear,
	NewTesterJoinLinkService,
	NewTesterMembershipService,
	NewCatalogService,
	NewRuntimeResolutionService,
	NewApplicationCatalogService,
	NewApplicationProfileRevisionServiceWithQuery,
	NewProfileManagementResponseSizer,
	NewApplicationProfileReviewService,
	NewApplicationProfileReviewQueryService,
	NewApplicationFilterService,
	NewOAuthClientService,
	NewOAuthClientProviderService,
	NewServersWithApplicationCatalogAndOAuthProvider,
)
