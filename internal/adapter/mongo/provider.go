package mongo

import (
	"github.com/goforj/wire"
	catalogport "iwut-app-center/internal/catalog/port"
	filterport "iwut-app-center/internal/filter/port"
	oauthclientport "iwut-app-center/internal/oauthclient/port"
	profileport "iwut-app-center/internal/profile/port"
	publicationport "iwut-app-center/internal/publication/port"
	testerport "iwut-app-center/internal/tester/port"

	"iwut-app-center/internal/application/port"
	reviewport "iwut-app-center/internal/review/port"
	versionport "iwut-app-center/internal/version/port"
)

// ProviderSet binds the transactional Application repository to its port. The
// database and client providers live in the composition root because they need
// validated configuration.
var ProviderSet = wire.NewSet(
	NewApplicationOperationsRepository,
	wire.Bind(new(port.ApplicationOperationsRepository), new(*ApplicationOperationsRepository)),
	NewApplicationClosureRepository,
	wire.Bind(new(port.ApplicationClosureRepository), new(*ApplicationClosureRepository)),
	NewApplicationAdminTransferRepository,
	wire.Bind(new(port.ApplicationAdminTransferRepository), new(*ApplicationAdminTransferRepository)),
	NewApplicationFilterRepository,
	wire.Bind(new(filterport.Repository), new(*ApplicationFilterRepository)),
	NewOAuthProviderRepository,
	wire.Bind(new(oauthclientport.ProviderRepository), new(*OAuthProviderRepository)),
	NewOAuthClientRepository,
	wire.Bind(new(oauthclientport.Repository), new(*OAuthClientRepository)),
	NewApplicationProfileRevisionRepository,
	wire.Bind(new(profileport.ApplicationProfileReviewDecisionRepository), new(*ApplicationProfileRevisionRepository)),
	wire.Bind(new(profileport.ApplicationProfileReviewRepository), new(*ApplicationProfileRevisionRepository)),
	wire.Bind(new(profileport.DraftApplicationProfileRevisionRepository), new(*ApplicationProfileRevisionRepository)),
	wire.Bind(new(profileport.ApplicationProfileRevisionRepository), new(*ApplicationProfileRevisionRepository)),
	NewTestLaunchResolver,
	wire.Bind(new(catalogport.TestLaunchResolver), new(*TestLaunchResolver)),
	NewUnifiedLaunchResolver,
	wire.Bind(new(catalogport.LaunchTargetResolver), new(*UnifiedLaunchResolver)),
	NewPublicCatalogRepository,
	wire.Bind(new(catalogport.PublicApplicationCatalogRepository), new(*PublicCatalogRepository)),
	NewApplicationTesterMembershipRepository,
	wire.Bind(new(testerport.ApplicationTesterMembershipRepository), new(*ApplicationTesterMembershipRepository)),
	wire.Bind(new(testerport.ApplicationTesterRemovalRepository), new(*ApplicationTesterMembershipRepository)),
	NewApplicationTesterJoinLinkRepository,
	wire.Bind(new(testerport.ApplicationTesterJoinLinkRepository), new(*ApplicationTesterJoinLinkRepository)),
	wire.Bind(new(testerport.ApplicationTesterRevocationRepository), new(*ApplicationTesterJoinLinkRepository)),
	NewApplicationPublicationRepository,
	wire.Bind(new(publicationport.ApplicationPublicationRepository), new(*ApplicationPublicationRepository)),
	wire.Bind(new(publicationport.StablePublicationRepository), new(*ApplicationPublicationRepository)),
	wire.Bind(new(publicationport.GreyPublicationRepository), new(*ApplicationPublicationRepository)),
	NewApplicationRepository,
	NewApplicationVersionRepository,
	NewApplicationReviewRepository,
	NewApplicationReviewRestorationRepository,
	NewApplicationReviewDecisionRepository,
	NewVersionReviewPolicyRepository,
	wire.Bind(new(port.ApplicationRepository), new(*ApplicationRepository)),
	wire.Bind(new(versionport.ApplicationVersionRepository), new(*ApplicationVersionRepository)),
	wire.Bind(new(reviewport.ApplicationReviewRepository), new(*ApplicationReviewRepository)),
	wire.Bind(new(reviewport.RejectedApplicationVersionRepository), new(*ApplicationReviewRestorationRepository)),
	wire.Bind(new(reviewport.ApplicationReviewDecisionRepository), new(*ApplicationReviewDecisionRepository)),
	wire.Bind(new(reviewport.ReviewPolicyProvider), new(*VersionReviewPolicyRepository)),
)
