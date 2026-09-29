package transport

import (
	"context"
	"errors"
	"net/http"

	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	"google.golang.org/protobuf/types/known/timestamppb"

	publicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_publication"
	publicationdomain "iwut-app-center/internal/publication/domain"
	publicationusecase "iwut-app-center/internal/publication/usecase"
	"iwut-app-center/internal/shared"
)

type PlaceApprovedVersionInTestSlotHandler interface {
	Handle(context.Context, shared.DeveloperIdentity, shared.ApplicationID, int32, publicationusecase.PlaceApprovedVersionInTestSlotCommand) (*publicationdomain.PlaceInTestResult, error)
}

type ApplicationPublicationService struct {
	publicationv1.UnimplementedApplicationPublicationServer
	handler PlaceApprovedVersionInTestSlotHandler
}

var _ publicationv1.ApplicationPublicationHTTPServer = (*ApplicationPublicationService)(nil)
var _ publicationv1.ApplicationPublicationServer = (*ApplicationPublicationService)(nil)

func NewApplicationPublicationService(handler PlaceApprovedVersionInTestSlotHandler) *ApplicationPublicationService {
	return &ApplicationPublicationService{handler: handler}
}

func (service *ApplicationPublicationService) PlaceApprovedVersionInTestSlot(ctx context.Context, request *publicationv1.PlaceApprovedVersionInTestSlotRequest) (*publicationv1.PlaceApprovedVersionInTestSlotResponse, error) {
	if service == nil || service.handler == nil || request == nil {
		return nil, toTransportError(publicationdomain.NewInternalError(nil))
	}
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(publicationdomain.ErrDeveloperIdentityRequired)
	}
	applicationID, valid := shared.ParseApplicationID(request.GetApplicationId())
	if !valid {
		return nil, toTransportError(publicationdomain.ErrApplicationVersionNotFound)
	}
	command := publicationusecase.PlaceApprovedVersionInTestSlotCommand{}
	if body := request.GetCommand(); body != nil {
		command.VersionID = publicationdomain.ApplicationVersionID(body.GetVersionId())
		if body.ExpectedPublicationRevision != nil {
			revision := body.GetExpectedPublicationRevision()
			command.ExpectedPublicationRevision = &revision
		}
	}
	result, err := service.handler.Handle(ctx, identity, applicationID, request.GetRpcApiMajor(), command)
	if err != nil {
		if isHTTP(ctx) && (errors.Is(err, publicationdomain.ErrApplicationVersionNotApproved) || errors.Is(err, publicationdomain.ErrApplicationVersionRpcApiIncompatible) || errors.Is(err, publicationdomain.ErrInvalidApplicationScope) || errors.Is(err, publicationdomain.ErrApplicationLaunchURLNotReviewable) || errors.Is(err, publicationdomain.ErrOAuthClientRegistrationRequired)) {
			var domainError *publicationdomain.Error
			errors.As(err, &domainError)
			spec := publicationDomainErrorSpecs[domainError.Code()]
			return nil, kratoserrors.New(http.StatusUnprocessableEntity, spec.reason, spec.message)
		}
		return nil, toTransportError(err)
	}
	if result == nil || result.Publication() == nil || result.Changed() != (result.History() != nil) {
		return nil, toTransportError(publicationdomain.NewInternalError(nil))
	}
	publication := result.Publication()
	response := &publicationv1.PlaceApprovedVersionInTestSlotResponse{Changed: result.Changed(), Publication: &publicationv1.ApplicationPublicationResource{
		PublicationId: publication.PublicationID().String(), ApplicationId: publication.ApplicationID().String(), RpcApiMajor: publication.RPCAPIMajor(), TestVersionId: publication.TestVersionID().String(), Revision: publication.Revision(), CreatedBy: publication.CreatedBy().String(), CreatedAt: timestamppb.New(publication.CreatedAt()), UpdatedBy: publication.UpdatedBy().String(), UpdatedAt: timestamppb.New(publication.UpdatedAt()),
	}}
	if history := result.History(); history != nil {
		response.History = &publicationv1.ApplicationPublicationHistoryResource{
			HistoryId: history.HistoryID().String(), PublicationId: history.PublicationID().String(), ApplicationId: history.ApplicationID().String(), RpcApiMajor: history.RPCAPIMajor(), PublicationRevision: history.PublicationRevision(), Action: string(history.Action()), NewVersionId: history.NewVersionID().String(), ApprovedReviewId: history.ApprovedReviewID().String(), ScopeCatalogRevision: history.ScopeCatalogRevision().Int64(), PreflightPolicyVersion: history.PreflightPolicyVersion().String(), ChangedBy: history.ChangedBy().String(), ChangedAt: timestamppb.New(history.ChangedAt()),
		}
		if previous := history.PreviousVersionID(); previous != nil {
			value := previous.String()
			response.History.PreviousVersionId = &value
		}
	}
	return response, nil
}
