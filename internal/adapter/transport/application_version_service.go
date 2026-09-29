package transport

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	kratostransport "github.com/go-kratos/kratos/v2/transport"
	"google.golang.org/protobuf/types/known/timestamppb"

	applicationversionv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_version"
	"iwut-app-center/internal/shared"
	versiondomain "iwut-app-center/internal/version/domain"
	versionusecase "iwut-app-center/internal/version/usecase"
)

type CreateApplicationVersionHandler interface {
	Handle(
		ctx context.Context,
		identity versionusecase.DeveloperIdentity,
		command versionusecase.CreateApplicationVersionCommand,
	) (*versiondomain.ApplicationVersion, error)
}

type UpdateDraftApplicationVersionHandler interface {
	Handle(
		ctx context.Context,
		identity versionusecase.DeveloperIdentity,
		applicationID shared.ApplicationID,
		versionID versiondomain.ApplicationVersionID,
		command versionusecase.UpdateDraftApplicationVersionCommand,
	) (*versiondomain.ApplicationVersion, error)
}

type ApplicationVersionService struct {
	applicationversionv1.UnimplementedApplicationVersionServer
	createHandler CreateApplicationVersionHandler
	updateHandler UpdateDraftApplicationVersionHandler
}

var _ applicationversionv1.ApplicationVersionHTTPServer = (*ApplicationVersionService)(nil)
var _ applicationversionv1.ApplicationVersionServer = (*ApplicationVersionService)(nil)

func NewApplicationVersionService(
	createHandler CreateApplicationVersionHandler,
	updateHandler UpdateDraftApplicationVersionHandler,
) *ApplicationVersionService {
	return &ApplicationVersionService{createHandler: createHandler, updateHandler: updateHandler}
}

func (service *ApplicationVersionService) CreateApplicationVersion(
	ctx context.Context,
	request *applicationversionv1.CreateApplicationVersionRequest,
) (*applicationversionv1.CreateApplicationVersionResponse, error) {
	if service == nil || service.createHandler == nil {
		return nil, toTransportError(versiondomain.NewInternalError(nil))
	}
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(versiondomain.ErrDeveloperIdentityRequired)
	}

	version, err := service.createHandler.Handle(
		ctx,
		shared.DeveloperIdentity{
			AuthID:          identity.AuthID,
			DeveloperStatus: identity.DeveloperStatus,
		},
		versionusecase.CreateApplicationVersionCommand{
			ApplicationID:             request.GetApplicationId(),
			VersionLabel:              request.GetVersionLabel(),
			LaunchURL:                 request.GetLaunchUrl(),
			RPCApiMinVersion:          request.GetRpcApiMinVersion(),
			RPCApiMaxVersionExclusive: request.GetRpcApiMaxVersionExclusive(),
			RequiredCapabilities:      request.GetRequiredCapabilities(),
			RequiredScopes:            request.GetRequiredScopes(),
			OptionalScopes:            request.GetOptionalScopes(),
			PKCERedirectURIs:          request.GetOauthRedirects().GetPkceRedirectUris(),
			ConfidentialRedirectURIs:  request.GetOauthRedirects().GetConfidentialRedirectUris(),
		},
	)
	if err != nil {
		return nil, toTransportError(err)
	}
	if version == nil {
		return nil, toTransportError(versiondomain.NewInternalError(nil))
	}

	return applicationVersionResponse(version), nil
}

func (service *ApplicationVersionService) UpdateApplicationVersion(
	ctx context.Context,
	request *applicationversionv1.UpdateApplicationVersionRequest,
) (*applicationversionv1.UpdateApplicationVersionResponse, error) {
	if service == nil || service.updateHandler == nil || request == nil {
		return nil, toTransportError(versiondomain.NewInternalError(nil))
	}
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(versiondomain.ErrDeveloperIdentityRequired)
	}

	expectedRevision, err := expectedRevision(ctx, request.GetExpectedRevision())
	if err != nil {
		return nil, err
	}
	replacement := request.GetReplacement()
	if replacement == nil {
		replacement = &applicationversionv1.DraftApplicationVersionReplacement{}
	}
	applicationID, applicationIDOK := shared.ParseApplicationID(request.GetApplicationId())
	if !applicationIDOK {
		return nil, toTransportError(versiondomain.ErrInvalidApplicationID)
	}
	versionID := versiondomain.ApplicationVersionID(request.GetVersionId())
	if !versionID.IsValid() {
		return nil, toTransportError(versiondomain.ErrApplicationVersionNotFound)
	}

	version, err := service.updateHandler.Handle(
		ctx,
		shared.DeveloperIdentity{AuthID: identity.AuthID, DeveloperStatus: identity.DeveloperStatus},
		applicationID,
		versionID,
		versionusecase.UpdateDraftApplicationVersionCommand{
			ExpectedRevision:          expectedRevision,
			VersionLabel:              replacement.GetVersionLabel(),
			LaunchURL:                 replacement.GetLaunchUrl(),
			RPCApiMinVersion:          replacement.GetRpcApiMinVersion(),
			RPCApiMaxVersionExclusive: replacement.GetRpcApiMaxVersionExclusive(),
			RequiredCapabilities:      replacement.GetRequiredCapabilities(),
			RequiredScopes:            replacement.GetRequiredScopes(),
			OptionalScopes:            replacement.GetOptionalScopes(),
			PKCERedirectURIs:          replacement.GetOauthRedirects().GetPkceRedirectUris(),
			ConfidentialRedirectURIs:  replacement.GetOauthRedirects().GetConfidentialRedirectUris(),
		},
	)
	if err != nil {
		if isHTTP(ctx) && errors.Is(err, versiondomain.ErrApplicationVersionRevisionConflict) {
			return nil, kratoserrors.New(http.StatusPreconditionFailed, ReasonApplicationVersionRevisionConflict, "application version revision conflicts")
		}
		return nil, toTransportError(err)
	}
	if version == nil {
		return nil, toTransportError(versiondomain.NewInternalError(nil))
	}
	return updateApplicationVersionResponse(version), nil
}

func expectedRevision(ctx context.Context, grpcRevision int64) (int64, error) {
	transporter, ok := kratostransport.FromServerContext(ctx)
	if !ok || transporter.Kind() != kratostransport.KindHTTP {
		return grpcRevision, nil
	}
	value := transporter.RequestHeader().Get("If-Match")
	if value == "" {
		return 0, kratoserrors.New(http.StatusPreconditionRequired, ReasonApplicationVersionRevisionRequired, "If-Match is required")
	}
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' || strings.HasPrefix(value, "W/") {
		return 0, toTransportError(versiondomain.ErrApplicationVersionRevisionRequired)
	}
	revision, err := strconv.ParseInt(value[1:len(value)-1], 10, 64)
	if err != nil || revision < 1 {
		return 0, toTransportError(versiondomain.ErrApplicationVersionRevisionRequired)
	}
	return revision, nil
}

func isHTTP(ctx context.Context) bool {
	transporter, ok := kratostransport.FromServerContext(ctx)
	return ok && transporter.Kind() == kratostransport.KindHTTP
}

func applicationVersionResponse(version *versiondomain.ApplicationVersion) *applicationversionv1.CreateApplicationVersionResponse {
	capabilityValues := version.RequiredCapabilities()
	capabilities := make([]string, len(capabilityValues))
	for index, capability := range capabilityValues {
		capabilities[index] = string(capability)
	}
	requiredValues := version.RequiredScopes()
	requiredScopes := make([]string, len(requiredValues))
	for index, scope := range requiredValues {
		requiredScopes[index] = string(scope)
	}
	optionalValues := version.OptionalScopes()
	optionalScopes := make([]string, len(optionalValues))
	for index, scope := range optionalValues {
		optionalScopes[index] = string(scope)
	}
	redirects := version.OAuthRedirects()

	return &applicationversionv1.CreateApplicationVersionResponse{
		VersionId:                 version.ID().String(),
		ApplicationId:             version.ApplicationID().String(),
		Sequence:                  version.Sequence().Int32(),
		VersionLabel:              version.VersionLabel().String(),
		LaunchUrl:                 version.LaunchURL().String(),
		RpcApiMinVersion:          version.RPCApiRange().Minimum(),
		RpcApiMaxVersionExclusive: version.RPCApiRange().MaximumExclusive(),
		RequiredCapabilities:      capabilities,
		RequiredScopes:            requiredScopes,
		OptionalScopes:            optionalScopes,
		ReviewStatus:              string(version.ReviewStatus()),
		CreatedBy:                 version.CreatedBy().String(),
		CreatedAt:                 timestamppb.New(version.CreatedAt()),
		Revision:                  version.Revision(),
		UpdatedBy:                 version.UpdatedBy().String(),
		UpdatedAt:                 timestamppb.New(version.UpdatedAt()),
		OauthRedirects: &applicationversionv1.OAuthRedirectConfiguration{
			PkceRedirectUris:         redirects.PKCERedirectURIs(),
			ConfidentialRedirectUris: redirects.ConfidentialRedirectURIs(),
		},
	}
}

func updateApplicationVersionResponse(version *versiondomain.ApplicationVersion) *applicationversionv1.UpdateApplicationVersionResponse {
	created := applicationVersionResponse(version)
	return &applicationversionv1.UpdateApplicationVersionResponse{
		VersionId:                 created.GetVersionId(),
		ApplicationId:             created.GetApplicationId(),
		Sequence:                  created.GetSequence(),
		VersionLabel:              created.GetVersionLabel(),
		LaunchUrl:                 created.GetLaunchUrl(),
		RpcApiMinVersion:          created.GetRpcApiMinVersion(),
		RpcApiMaxVersionExclusive: created.GetRpcApiMaxVersionExclusive(),
		RequiredCapabilities:      created.GetRequiredCapabilities(),
		RequiredScopes:            created.GetRequiredScopes(),
		OptionalScopes:            created.GetOptionalScopes(),
		ReviewStatus:              created.GetReviewStatus(),
		CreatedBy:                 created.GetCreatedBy(),
		CreatedAt:                 created.GetCreatedAt(),
		Revision:                  created.GetRevision(),
		UpdatedBy:                 created.GetUpdatedBy(),
		UpdatedAt:                 created.GetUpdatedAt(),
		OauthRedirects:            created.GetOauthRedirects(),
	}
}
