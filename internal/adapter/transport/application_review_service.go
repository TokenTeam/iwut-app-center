package transport

import (
	"context"
	"errors"
	"net/http"

	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	"google.golang.org/protobuf/types/known/timestamppb"

	applicationreviewv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_review"
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

type RestoreRejectedApplicationVersionHandler interface {
	Handle(
		ctx context.Context,
		identity shared.DeveloperIdentity,
		applicationID shared.ApplicationID,
		versionID reviewdomain.ApplicationVersionID,
		reviewID reviewdomain.ApplicationReviewID,
		command reviewusecase.RestoreRejectedApplicationVersionCommand,
	) (*reviewdomain.RestoreRejectedVersionResult, error)
}

type DecideApplicationVersionReviewHandler interface {
	Handle(
		ctx context.Context,
		identity reviewusecase.ReviewerIdentity,
		applicationID shared.ApplicationID,
		versionID reviewdomain.ApplicationVersionID,
		reviewID reviewdomain.ApplicationReviewID,
		command reviewusecase.DecideApplicationVersionReviewCommand,
	) (*reviewdomain.ApplicationReviewDecisionResult, error)
}

type ApplicationReviewService struct {
	applicationreviewv1.UnimplementedApplicationReviewServer
	submitHandler  SubmitApplicationVersionReviewHandler
	restoreHandler RestoreRejectedApplicationVersionHandler
	decideHandler  DecideApplicationVersionReviewHandler
}

var _ applicationreviewv1.ApplicationReviewHTTPServer = (*ApplicationReviewService)(nil)
var _ applicationreviewv1.ApplicationReviewServer = (*ApplicationReviewService)(nil)

func NewApplicationReviewService(
	submitHandler SubmitApplicationVersionReviewHandler,
	restoreHandler RestoreRejectedApplicationVersionHandler,
	decideHandler DecideApplicationVersionReviewHandler,
) *ApplicationReviewService {
	return &ApplicationReviewService{
		submitHandler: submitHandler, restoreHandler: restoreHandler, decideHandler: decideHandler,
	}
}

func (service *ApplicationReviewService) DecideApplicationVersionReview(
	ctx context.Context,
	request *applicationreviewv1.DecideApplicationVersionReviewRequest,
) (*applicationreviewv1.DecideApplicationVersionReviewResponse, error) {
	if service == nil || service.decideHandler == nil || request == nil {
		return nil, toTransportError(reviewdomain.NewInternalError(nil))
	}
	trusted, ok := trustedIdentityFromContext(ctx)
	if !ok || !trusted.AuthID.IsValid() {
		return nil, toTransportError(reviewdomain.ErrReviewerIdentityRequired)
	}
	applicationID, valid := shared.ParseApplicationID(request.GetApplicationId())
	versionID := reviewdomain.ApplicationVersionID(request.GetVersionId())
	reviewID := reviewdomain.ApplicationReviewID(request.GetReviewId())
	if !valid || !versionID.IsValid() || !reviewID.IsValid() {
		return nil, toTransportError(reviewdomain.ErrApplicationReviewNotFound)
	}
	command := request.GetCommand()
	outcome := ""
	if command != nil {
		switch command.GetOutcome() {
		case applicationreviewv1.ReviewDecisionAction_APPROVE:
			outcome = "APPROVE"
		case applicationreviewv1.ReviewDecisionAction_REJECT:
			outcome = "REJECT"
		}
	}
	usecaseCommand := reviewusecase.DecideApplicationVersionReviewCommand{Outcome: outcome}
	if command != nil {
		usecaseCommand.ExpectedPolicyVersion = command.GetExpectedPolicyVersion()
		usecaseCommand.ConfirmedCheckIDs = append([]string{}, command.GetConfirmedCheckIds()...)
		usecaseCommand.Reason = command.GetReason()
	}
	result, err := service.decideHandler.Handle(
		ctx,
		reviewusecase.ReviewerIdentity{
			AuthID: trusted.AuthID, Permissions: append([]string{}, trusted.Permissions...),
		},
		applicationID,
		versionID,
		reviewID,
		usecaseCommand,
	)
	if err != nil {
		if isHTTP(ctx) && isReviewDecisionValidationError(err) {
			spec := reviewDomainErrorSpecs[reviewErrorCode(err)]
			return nil, kratoserrors.New(http.StatusUnprocessableEntity, spec.reason, spec.message)
		}
		return nil, toTransportError(err)
	}
	if result == nil || result.Review() == nil || result.Version() == nil {
		return nil, toTransportError(reviewdomain.NewInternalError(nil))
	}
	version := result.Version()
	return &applicationreviewv1.DecideApplicationVersionReviewResponse{
		Review: applicationReviewResource(result.Review()),
		Version: &applicationreviewv1.DecidedApplicationVersion{
			ApplicationId: version.ApplicationID().String(),
			VersionId:     version.VersionID().String(),
			ReviewStatus:  version.ReviewStatus().String(),
			Revision:      version.Revision(),
			UpdatedBy:     version.UpdatedBy().String(),
			UpdatedAt:     timestamppb.New(version.UpdatedAt()),
		},
	}, nil
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

func (service *ApplicationReviewService) RestoreRejectedApplicationVersionToDraft(
	ctx context.Context,
	request *applicationreviewv1.RestoreRejectedApplicationVersionToDraftRequest,
) (*applicationreviewv1.RestoreRejectedApplicationVersionToDraftResponse, error) {
	if service == nil || service.restoreHandler == nil || request == nil {
		return nil, toTransportError(reviewdomain.NewInternalError(nil))
	}
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(reviewdomain.ErrDeveloperIdentityRequired)
	}
	applicationID, valid := shared.ParseApplicationID(request.GetApplicationId())
	versionID := reviewdomain.ApplicationVersionID(request.GetVersionId())
	reviewID := reviewdomain.ApplicationReviewID(request.GetReviewId())
	if !valid || !versionID.IsValid() || !reviewID.IsValid() {
		return nil, toTransportError(reviewdomain.ErrApplicationReviewNotFound)
	}
	expectedRevision := int64(0)
	if command := request.GetCommand(); command != nil {
		expectedRevision = command.GetExpectedVersionRevision()
	}
	result, err := service.restoreHandler.Handle(
		ctx,
		identity,
		applicationID,
		versionID,
		reviewID,
		reviewusecase.RestoreRejectedApplicationVersionCommand{ExpectedVersionRevision: expectedRevision},
	)
	if err != nil {
		return nil, toTransportError(err)
	}
	if result == nil || result.Review() == nil || result.Version() == nil {
		return nil, toTransportError(reviewdomain.NewInternalError(nil))
	}
	review := result.Review()
	version := result.Version()
	return &applicationreviewv1.RestoreRejectedApplicationVersionToDraftResponse{
		Review: applicationReviewResource(review),
		Version: &applicationreviewv1.RestoredApplicationVersion{
			ApplicationId: version.ApplicationID().String(),
			VersionId:     version.VersionID().String(),
			ReviewStatus:  version.ReviewStatus(),
			Revision:      version.Revision(),
			UpdatedBy:     version.UpdatedBy().String(),
			UpdatedAt:     timestamppb.New(version.UpdatedAt()),
		},
	}, nil
}

func applicationReviewSubmissionResponse(result *reviewdomain.ReviewSubmissionResult) *applicationreviewv1.SubmitApplicationVersionReviewResponse {
	review := result.Review()
	version := result.Version()
	return &applicationreviewv1.SubmitApplicationVersionReviewResponse{
		Review: applicationReviewResource(review),
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

func applicationReviewResource(review *reviewdomain.ApplicationReview) *applicationreviewv1.ApplicationReviewResource {
	if review == nil {
		return nil
	}
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
	resource := &applicationreviewv1.ApplicationReviewResource{
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
	}
	if restoration := review.DraftRestoration(); restoration != nil {
		resource.DraftRestoration = &applicationreviewv1.ApplicationReviewDraftRestoration{
			RestoredBy:            restoration.RestoredBy().String(),
			RestoredAt:            timestamppb.New(restoration.RestoredAt()),
			ResultVersionRevision: restoration.ResultVersionRevision(),
		}
	}
	if decision := review.Decision(); decision != nil {
		confirmed := decision.ConfirmedCheckIDs()
		confirmedValues := make([]string, len(confirmed))
		for index, id := range confirmed {
			confirmedValues[index] = id.String()
		}
		resource.Decision = &applicationreviewv1.ApplicationReviewDecision{
			Outcome:             decision.Outcome().String(),
			ReviewPolicyVersion: decision.ReviewPolicyVersion().String(),
			ConfirmedCheckIds:   confirmedValues,
			DecidedBy:           decision.DecidedBy().String(),
			DecidedAt:           timestamppb.New(decision.DecidedAt()),
		}
		if reason := decision.Reason(); reason != "" {
			resource.Decision.Reason = &reason
		}
		if validation := decision.ApprovalValidation(); validation != nil {
			resource.Decision.ApprovalValidation = &applicationreviewv1.ApplicationReviewApprovalValidation{
				ScopeCatalogRevision:   validation.ScopeCatalogRevision().Int64(),
				PreflightPolicyVersion: validation.PreflightPolicyVersion().String(),
			}
		}
	}
	return resource
}

func isReviewDecisionValidationError(err error) bool {
	return errors.Is(err, reviewdomain.ErrInvalidApplicationReviewOutcome) ||
		errors.Is(err, reviewdomain.ErrInvalidApplicationReviewPolicyVersion) ||
		errors.Is(err, reviewdomain.ErrApplicationReviewChecksIncomplete) ||
		errors.Is(err, reviewdomain.ErrInvalidApplicationReviewChecks) ||
		errors.Is(err, reviewdomain.ErrInvalidApplicationReviewReason) ||
		errors.Is(err, reviewdomain.ErrInvalidApplicationScope) ||
		errors.Is(err, reviewdomain.ErrApplicationLaunchURLNotReviewable)
}
