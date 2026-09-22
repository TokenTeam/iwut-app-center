package preflight

import (
	"context"
	"errors"
	publicationdomain "iwut-app-center/internal/publication/domain"
	publicationport "iwut-app-center/internal/publication/port"
	reviewdomain "iwut-app-center/internal/review/domain"
	reviewport "iwut-app-center/internal/review/port"
)

// PublicationLaunchURLSubmissionPolicy adapts the same DNS-only policy used
// during review; publication owns its port and records the policy revision.
type PublicationLaunchURLSubmissionPolicy struct{ policy *LaunchURLSubmissionPolicy }

func NewPublicationLaunchURLSubmissionPolicy(policy *LaunchURLSubmissionPolicy) *PublicationLaunchURLSubmissionPolicy {
	return &PublicationLaunchURLSubmissionPolicy{policy}
}
func (p *PublicationLaunchURLSubmissionPolicy) Inspect(ctx context.Context, url publicationdomain.LaunchURL) (publicationdomain.PreflightPolicyVersion, error) {
	if p == nil || p.policy == nil {
		return "", publicationport.ErrLaunchURLInspectionUnavailable
	}
	version, err := p.policy.Inspect(ctx, reviewdomain.LaunchURL(url))
	if err != nil {
		switch {
		case errors.Is(err, reviewport.ErrLaunchURLNotReviewable):
			return "", publicationport.ErrLaunchURLNotReviewable
		case errors.Is(err, reviewport.ErrLaunchURLInspectionUnavailable):
			return "", errors.Join(publicationport.ErrLaunchURLInspectionUnavailable, err)
		default:
			return "", err
		}
	}
	return publicationdomain.PreflightPolicyVersion(version), nil
}

var _ publicationport.LaunchURLSubmissionPolicy = (*PublicationLaunchURLSubmissionPolicy)(nil)
