package transport

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/protobuf/types/known/timestamppb"

	publicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_publication"
	publicationdomain "iwut-app-center/internal/publication/domain"
	publicationusecase "iwut-app-center/internal/publication/usecase"
	"iwut-app-center/internal/shared"
)

type PlaceApprovedVersionInTestSlotHandler interface {
	Handle(context.Context, shared.DeveloperIdentity, shared.ApplicationID, int32, publicationusecase.PlaceApprovedVersionInTestSlotCommand) (*publicationdomain.PlaceInTestResult, error)
}
type SetApprovedVersionInStableSlotHandler interface {
	Handle(context.Context, shared.DeveloperIdentity, shared.ApplicationID, int32, publicationusecase.SetApprovedVersionInStableSlotCommand) (*publicationdomain.PlaceInTestResult, error)
}
type ClearStableSlotHandler interface {
	Handle(context.Context, shared.DeveloperIdentity, shared.ApplicationID, int32, publicationusecase.ClearStableSlotCommand) (*publicationdomain.PlaceInTestResult, error)
}
type SetGreyRolloutHandler interface {
	Handle(context.Context, shared.DeveloperIdentity, shared.ApplicationID, int32, publicationusecase.SetGreyRolloutCommand) (*publicationdomain.PlaceInTestResult, error)
}
type ClearGreyRolloutHandler interface {
	Handle(context.Context, shared.DeveloperIdentity, shared.ApplicationID, int32, publicationusecase.ClearGreyRolloutCommand) (*publicationdomain.PlaceInTestResult, error)
}

type ApplicationPublicationService struct {
	publicationv1.UnimplementedApplicationPublicationServer
	testHandler      PlaceApprovedVersionInTestSlotHandler
	stableHandler    SetApprovedVersionInStableSlotHandler
	clearHandler     ClearStableSlotHandler
	setGreyHandler   SetGreyRolloutHandler
	clearGreyHandler ClearGreyRolloutHandler
}

var _ publicationv1.ApplicationPublicationHTTPServer = (*ApplicationPublicationService)(nil)
var _ publicationv1.ApplicationPublicationServer = (*ApplicationPublicationService)(nil)

func NewApplicationPublicationService(handler PlaceApprovedVersionInTestSlotHandler) *ApplicationPublicationService {
	return &ApplicationPublicationService{testHandler: handler}
}
func NewApplicationPublicationServiceWithStable(test PlaceApprovedVersionInTestSlotHandler, stable SetApprovedVersionInStableSlotHandler, clear ClearStableSlotHandler) *ApplicationPublicationService {
	return &ApplicationPublicationService{testHandler: test, stableHandler: stable, clearHandler: clear}
}
func NewApplicationPublicationServiceWithGrey(test PlaceApprovedVersionInTestSlotHandler, stable SetApprovedVersionInStableSlotHandler, clearStable ClearStableSlotHandler, setGrey SetGreyRolloutHandler, clearGrey ClearGreyRolloutHandler) *ApplicationPublicationService {
	return &ApplicationPublicationService{testHandler: test, stableHandler: stable, clearHandler: clearStable, setGreyHandler: setGrey, clearGreyHandler: clearGrey}
}

func (service *ApplicationPublicationService) PlaceApprovedVersionInTestSlot(ctx context.Context, request *publicationv1.PlaceApprovedVersionInTestSlotRequest) (*publicationv1.PlaceApprovedVersionInTestSlotResponse, error) {
	if service == nil || service.testHandler == nil || request == nil {
		return nil, toTransportError(publicationdomain.NewInternalError(nil))
	}
	identity, applicationID, err := publicationRequestIdentity(ctx, request.GetApplicationId())
	if err != nil {
		return nil, err
	}
	command := publicationusecase.PlaceApprovedVersionInTestSlotCommand{}
	if body := request.GetCommand(); body != nil {
		command.VersionID = publicationdomain.ApplicationVersionID(body.GetVersionId())
		if body.ExpectedPublicationRevision != nil {
			value := body.GetExpectedPublicationRevision()
			command.ExpectedPublicationRevision = &value
		}
	}
	result, handleErr := service.testHandler.Handle(ctx, identity, applicationID, request.GetRpcApiMajor(), command)
	if handleErr != nil {
		return nil, publicationTransportError(ctx, handleErr)
	}
	publication, history, changed, convertErr := publicationResources(result)
	if convertErr != nil {
		return nil, convertErr
	}
	return &publicationv1.PlaceApprovedVersionInTestSlotResponse{Changed: changed, Publication: publication, History: history}, nil
}

func (service *ApplicationPublicationService) SetApprovedVersionInStableSlot(ctx context.Context, request *publicationv1.SetApprovedVersionInStableSlotRequest) (*publicationv1.SetApprovedVersionInStableSlotResponse, error) {
	if service == nil || service.stableHandler == nil || request == nil {
		return nil, toTransportError(publicationdomain.NewInternalError(nil))
	}
	identity, applicationID, err := publicationRequestIdentity(ctx, request.GetApplicationId())
	if err != nil {
		return nil, err
	}
	if httpRequest, ok := khttp.RequestFromServerContext(ctx); ok && (httpRequest.URL.RawQuery != "" || httpRequest.URL.ForceQuery) {
		return nil, toTransportError(publicationdomain.ErrInvalidApplicationPublicationRevision)
	}
	command := publicationusecase.SetApprovedVersionInStableSlotCommand{}
	if body := request.GetCommand(); body != nil {
		command.VersionID = publicationdomain.ApplicationVersionID(body.GetVersionId())
		if body.ExpectedPublicationRevision != nil {
			value := body.GetExpectedPublicationRevision()
			command.ExpectedPublicationRevision = &value
		}
	}
	result, handleErr := service.stableHandler.Handle(ctx, identity, applicationID, request.GetRpcApiMajor(), command)
	if handleErr != nil {
		return nil, publicationTransportError(ctx, handleErr)
	}
	publication, history, changed, convertErr := publicationResources(result)
	if convertErr != nil {
		return nil, convertErr
	}
	return &publicationv1.SetApprovedVersionInStableSlotResponse{Changed: changed, Publication: publication, History: history}, nil
}

func (service *ApplicationPublicationService) ClearStableSlot(ctx context.Context, request *publicationv1.ClearStableSlotRequest) (*publicationv1.ClearStableSlotResponse, error) {
	if service == nil || service.clearHandler == nil || request == nil {
		return nil, toTransportError(publicationdomain.NewInternalError(nil))
	}
	identity, applicationID, err := publicationRequestIdentity(ctx, request.GetApplicationId())
	if err != nil {
		return nil, err
	}
	if httpRequest, ok := khttp.RequestFromServerContext(ctx); ok && !validClearStableHTTPInput(httpRequest) {
		return nil, toTransportError(publicationdomain.ErrInvalidApplicationPublicationRevision)
	}
	result, handleErr := service.clearHandler.Handle(ctx, identity, applicationID, request.GetRpcApiMajor(), publicationusecase.ClearStableSlotCommand{ExpectedPublicationRevision: request.GetExpectedPublicationRevision()})
	if handleErr != nil {
		return nil, publicationTransportError(ctx, handleErr)
	}
	publication, history, changed, convertErr := publicationResources(result)
	if convertErr != nil {
		return nil, convertErr
	}
	return &publicationv1.ClearStableSlotResponse{Changed: changed, Publication: publication, History: history}, nil
}

func (service *ApplicationPublicationService) SetGreyRollout(ctx context.Context, request *publicationv1.SetGreyRolloutRequest) (*publicationv1.SetGreyRolloutResponse, error) {
	if service == nil || service.setGreyHandler == nil || request == nil {
		return nil, toTransportError(publicationdomain.NewInternalError(nil))
	}
	identity, applicationID, err := publicationRequestIdentity(ctx, request.GetApplicationId())
	if err != nil {
		return nil, err
	}
	if httpRequest, ok := khttp.RequestFromServerContext(ctx); ok && (httpRequest.URL.RawQuery != "" || httpRequest.URL.ForceQuery) {
		return nil, toTransportError(publicationdomain.ErrInvalidApplicationPublicationRevision)
	}
	command := publicationusecase.SetGreyRolloutCommand{}
	if body := request.GetCommand(); body != nil {
		command.VersionID = publicationdomain.ApplicationVersionID(body.GetVersionId())
		command.ExposureBasisPoints = body.GetExposureBasisPoints()
		command.ExpectedPublicationRevision = body.GetExpectedPublicationRevision()
	}
	result, handleErr := service.setGreyHandler.Handle(ctx, identity, applicationID, request.GetRpcApiMajor(), command)
	if handleErr != nil {
		return nil, publicationTransportError(ctx, handleErr)
	}
	publication, history, changed, convertErr := publicationResources(result)
	if convertErr != nil {
		return nil, convertErr
	}
	return &publicationv1.SetGreyRolloutResponse{Changed: changed, Publication: publication, History: history}, nil
}

func (service *ApplicationPublicationService) ClearGreyRollout(ctx context.Context, request *publicationv1.ClearGreyRolloutRequest) (*publicationv1.ClearGreyRolloutResponse, error) {
	if service == nil || service.clearGreyHandler == nil || request == nil {
		return nil, toTransportError(publicationdomain.NewInternalError(nil))
	}
	identity, applicationID, err := publicationRequestIdentity(ctx, request.GetApplicationId())
	if err != nil {
		return nil, err
	}
	if httpRequest, ok := khttp.RequestFromServerContext(ctx); ok && !validPublicationClearHTTPInput(httpRequest) {
		return nil, toTransportError(publicationdomain.ErrInvalidApplicationPublicationRevision)
	}
	result, handleErr := service.clearGreyHandler.Handle(ctx, identity, applicationID, request.GetRpcApiMajor(), publicationusecase.ClearGreyRolloutCommand{ExpectedPublicationRevision: request.GetExpectedPublicationRevision()})
	if handleErr != nil {
		return nil, publicationTransportError(ctx, handleErr)
	}
	publication, history, changed, convertErr := publicationResources(result)
	if convertErr != nil {
		return nil, convertErr
	}
	return &publicationv1.ClearGreyRolloutResponse{Changed: changed, Publication: publication, History: history}, nil
}

func validClearStableHTTPInput(request *http.Request) bool {
	return validPublicationClearHTTPInput(request)
}
func validPublicationClearHTTPInput(request *http.Request) bool {
	query := request.URL.Query()
	if len(query) != 1 || len(query["expected_publication_revision"]) != 1 {
		return false
	}
	if request.Body == nil {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1025))
	return err == nil && len(body) <= 1024 && len(body) == 0
}

func publicationRequestIdentity(ctx context.Context, rawApplicationID string) (shared.DeveloperIdentity, shared.ApplicationID, error) {
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return shared.DeveloperIdentity{}, "", toTransportError(publicationdomain.ErrDeveloperIdentityRequired)
	}
	applicationID, valid := shared.ParseApplicationID(rawApplicationID)
	if !valid {
		return shared.DeveloperIdentity{}, "", toTransportError(publicationdomain.ErrApplicationVersionNotFound)
	}
	return identity, applicationID, nil
}

func publicationTransportError(ctx context.Context, err error) error {
	if errors.Is(err, publicationdomain.ErrApplicationProfileStateInconsistent) || errors.Is(err, publicationdomain.ErrApplicationPublicationStateInconsistent) {
		reason := ReasonApplicationProfileStateInconsistent
		if errors.Is(err, publicationdomain.ErrApplicationPublicationStateInconsistent) {
			reason = ReasonApplicationPublicationStateInconsistent
		}
		slog.ErrorContext(ctx, "application publication invariant failed", "reason", reason)
	}
	if isHTTP(ctx) && (errors.Is(err, publicationdomain.ErrApplicationVersionNotApproved) || errors.Is(err, publicationdomain.ErrApplicationVersionRpcApiIncompatible) || errors.Is(err, publicationdomain.ErrInvalidApplicationScope) || errors.Is(err, publicationdomain.ErrApplicationLaunchURLNotReviewable) || errors.Is(err, publicationdomain.ErrOAuthClientRegistrationRequired) || errors.Is(err, publicationdomain.ErrApplicationProfileRequired) || errors.Is(err, publicationdomain.ErrStablePublicationRequiredByGrey) || errors.Is(err, publicationdomain.ErrGreyStableBaselineRequired)) {
		var domainError *publicationdomain.Error
		if errors.As(err, &domainError) {
			spec := publicationDomainErrorSpecs[domainError.Code()]
			return kratoserrors.New(http.StatusUnprocessableEntity, spec.reason, spec.message)
		}
	}
	return toTransportError(err)
}

func publicationResources(result *publicationdomain.PlaceInTestResult) (*publicationv1.ApplicationPublicationResource, *publicationv1.ApplicationPublicationHistoryResource, bool, error) {
	if result == nil || result.Publication() == nil || result.Changed() != (result.History() != nil) {
		return nil, nil, false, toTransportError(publicationdomain.NewInternalError(nil))
	}
	p := result.Publication()
	resource := &publicationv1.ApplicationPublicationResource{PublicationId: p.PublicationID().String(), ApplicationId: p.ApplicationID().String(), RpcApiMajor: p.RPCAPIMajor(), Revision: p.Revision(), CreatedBy: p.CreatedBy().String(), CreatedAt: timestamppb.New(p.CreatedAt()), UpdatedBy: p.UpdatedBy().String(), UpdatedAt: timestamppb.New(p.UpdatedAt())}
	if value := p.TestVersionIDPtr(); value != nil {
		text := value.String()
		resource.TestVersionId = &text
	}
	if value := p.StableVersionIDPtr(); value != nil {
		text := value.String()
		resource.StableVersionId = &text
	}
	if value := p.GreyRollout(); value != nil {
		resource.GreyRollout = &publicationv1.GreyRolloutResource{RolloutId: value.RolloutID().String(), VersionId: value.VersionID().String(), ExposureBasisPoints: value.ExposureBasisPoints().Int32()}
	}
	var historyResource *publicationv1.ApplicationPublicationHistoryResource
	if h := result.History(); h != nil {
		historyResource = &publicationv1.ApplicationPublicationHistoryResource{HistoryId: h.HistoryID().String(), PublicationId: h.PublicationID().String(), ApplicationId: h.ApplicationID().String(), RpcApiMajor: h.RPCAPIMajor(), PublicationRevision: h.PublicationRevision(), Action: string(h.Action()), ChangedBy: h.ChangedBy().String(), ChangedAt: timestamppb.New(h.ChangedAt())}
		if value := h.PreviousVersionID(); value != nil {
			text := value.String()
			historyResource.PreviousVersionId = &text
		}
		if value := h.NewVersionIDPtr(); value != nil {
			text := value.String()
			historyResource.NewVersionId = &text
		}
		if value := h.ApprovedReviewIDPtr(); value != nil {
			text := value.String()
			historyResource.ApprovedReviewId = &text
		}
		if value := h.ScopeCatalogRevisionPtr(); value != nil {
			revision := value.Int64()
			historyResource.ScopeCatalogRevision = &revision
		}
		if value := h.PreflightPolicyVersionPtr(); value != nil {
			text := value.String()
			historyResource.PreflightPolicyVersion = &text
		}
		if value := h.GreyRolloutIDPtr(); value != nil {
			text := value.String()
			historyResource.GreyRolloutId = &text
		}
		if value := h.PreviousExposureBasisPointsPtr(); value != nil {
			points := value.Int32()
			historyResource.PreviousExposureBasisPoints = &points
		}
		if value := h.NewExposureBasisPointsPtr(); value != nil {
			points := value.Int32()
			historyResource.NewExposureBasisPoints = &points
		}
	}
	return resource, historyResource, result.Changed(), nil
}
