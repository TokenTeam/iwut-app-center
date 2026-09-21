package transport

import (
	"context"
	"errors"
	"net/http"

	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	"google.golang.org/protobuf/types/known/timestamppb"

	applicationreviewv1 "iwut-app-center/api/gen/go/app_center/v1/application_review"
	reviewdomain "iwut-app-center/internal/review/domain"
	reviewusecase "iwut-app-center/internal/review/usecase"
	"iwut-app-center/internal/shared"
)

type SubmitApplicationVersionReviewHandler interface {
	Handle(
		ctx context.Context,
		identity reviewusecase.DeveloperIdentity,
		applicationID shared.ApplicationID,
		versionID reviewdomain.ApplicationVersionID,
		command reviewusecase.SubmitApplicationVersionReviewCommand,
	) (*reviewdomain.ReviewSubmissionResult, error)
}

type ApplicationReviewService struct {
	applicationreviewv1.UnimplementedApplicationReviewServer
	submitHandler SubmitApplicationVersionReviewHandler
}

var _ applicationreviewv1.ApplicationReviewHTTPServer = (*ApplicationReviewService)(nil)
var _ applicationreviewv1.ApplicationReviewServer = (*ApplicationReviewService)(nil)

func NewApplicationReviewService(handler SubmitApplicationVersionReviewHandler) *ApplicationReviewService {
	return &ApplicationReviewService{submitHandler: handler}
}

func (service *ApplicationReviewService) SubmitApplicationVersionReview(
	ctx context.Context,
	request *applicationreviewv1.SubmitApplicationVersionReviewRequest,
) (*applicationreviewv1.SubmitApplicationVersionReviewResponse, error) {
	if service == nil || service.submitHandler == nil || request == nil {
		return nil, toTransportError(reviewdomain.NewInternalError(nil))
	}
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(reviewdomain.ErrDeveloperIdentityRequired)
	}
	applicationID, valid := shared.ParseApplicationID(request.GetApplicationId())
	if !valid {
		return nil, toTransportError(reviewdomain.ErrApplicationVersionNotFound)
	}
	versionID := reviewdomain.ApplicationVersionID(request.GetVersionId())
	if !versionID.IsValid() {
		return nil, toTransportError(reviewdomain.ErrApplicationVersionNotFound)
	}
	command := request.GetCommand()
	expectedRevision := int64(0)
	if command != nil {
		expectedRevision = command.GetExpectedRevision()
	}

	result, err := service.submitHandler.Handle(
		ctx,
		shared.DeveloperIdentity{AuthID: identity.AuthID, DeveloperStatus: identity.DeveloperStatus},
		applicationID,
		versionID,
		reviewusecase.SubmitApplicationVersionReviewCommand{ExpectedRevision: expectedRevision},
	)
	if err != nil {
		if isHTTP(ctx) && (errors.Is(err, reviewdomain.ErrApplicationLaunchURLNotReviewable) ||
			errors.Is(err, reviewdomain.ErrInvalidApplicationScope)) {
			reason := ReasonInvalidApplicationScope
			message := "application scope request is invalid"
			if errors.Is(err, reviewdomain.ErrApplicationLaunchURLNotReviewable) {
				reason = ReasonApplicationLaunchURLNotReviewable
				message = "application launch URL is not reviewable"
			}
			return nil, kratoserrors.New(http.StatusUnprocessableEntity, reason, message)
		}
		return nil, toTransportError(err)
	}
	if result == nil || result.Review() == nil || result.Version() == nil {
		return nil, toTransportError(reviewdomain.NewInternalError(nil))
	}
	return applicationReviewSubmissionResponse(result), nil
}

func applicationReviewSubmissionResponse(result *reviewdomain.ReviewSubmissionResult) *applicationreviewv1.SubmitApplicationVersionReviewResponse {
	review := result.Review()
	version := result.Version()
	snapshot := review.Snapshot()
	requiredCapabilities := append([]string{}, snapshot.RequiredCapabilities()...)
	requiredScopeValues := snapshot.RequiredScopes()
	requiredScopes := make([]string, len(requiredScopeValues))
	for index, scope := range requiredScopeValues {
		requiredScopes[index] = string(scope)
	}
	optionalScopeValues := snapshot.OptionalScopes()
	optionalScopes := make([]string, len(optionalScopeValues))
	for index, scope := range optionalScopeValues {
		optionalScopes[index] = string(scope)
	}
	return &applicationreviewv1.SubmitApplicationVersionReviewResponse{
		Review: &applicationreviewv1.ApplicationReviewResource{
			ReviewId:              review.ReviewID().String(),
			ApplicationId:         review.ApplicationID().String(),
			VersionId:             review.VersionID().String(),
			Attempt:               review.Attempt().Int32(),
			SourceVersionRevision: review.SourceVersionRevision(),
			Status:                string(review.Status()),
			Snapshot: &applicationreviewv1.ApplicationVersionReviewSnapshot{
				VersionLabel:              snapshot.VersionLabel(),
				LaunchUrl:                 string(snapshot.LaunchURL()),
				RpcApiMinVersion:          snapshot.RPCAPIMinVersion(),
				RpcApiMaxVersionExclusive: snapshot.RPCAPIMaxVersionExclusive(),
				RequiredCapabilities:      requiredCapabilities,
				RequiredScopes:            requiredScopes,
				OptionalScopes:            optionalScopes,
			},
			ScopeCatalogRevision:   review.ScopeCatalogRevision().Int64(),
			PreflightPolicyVersion: review.PreflightPolicyVersion().String(),
			SubmittedBy:            review.SubmittedBy().String(),
			SubmittedAt:            timestamppb.New(review.SubmittedAt()),
		},
		Version: &applicationreviewv1.SubmittedApplicationVersion{
			ApplicationId: version.ApplicationID().String(),
			VersionId:     version.VersionID().String(),
			ReviewStatus:  version.ReviewStatus(),
			Revision:      version.Revision(),
			UpdatedBy:     version.UpdatedBy().String(),
			UpdatedAt:     timestamppb.New(version.UpdatedAt()),
		},
	}
}
