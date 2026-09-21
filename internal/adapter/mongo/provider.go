package mongo

import (
	"github.com/goforj/wire"

	"iwut-app-center/internal/application/port"
	reviewport "iwut-app-center/internal/review/port"
	versionport "iwut-app-center/internal/version/port"
)

// ProviderSet binds the transactional Application repository to its port. The
// database and client providers live in the composition root because they need
// validated configuration.
var ProviderSet = wire.NewSet(
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
