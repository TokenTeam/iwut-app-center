package transport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	kerrors "github.com/go-kratos/kratos/v2/errors"
	"io"
	testerdomain "iwut-app-center/internal/tester/domain"
	"net/http"
	"strings"

	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/grpc/codes"

	applicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	applicationadmintransferv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_admin_transfer"
	applicationcatalogv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_catalog"
	applicationclosurev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_closure"
	filterv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_filter"
	profilereviewv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_review"
	profilev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_revision"
	publicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_publication"
	applicationreviewv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_review"
	applicationversionv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_version"
	catalogv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/catalog"
	oauthclientv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/oauth_client"
	runtimev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/runtime_resolution"
	testerjoinlinkv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/tester_join_link"
	testermembershipv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/tester_membership"
)

const (
	DecideApplicationProfileReviewInternalPath   = "/v1/applications/{application_id}/profile-revisions/{profile_revision_id}/reviews/{profile_review_id}/decision"
	DecideApplicationProfileReviewExternalPath   = ServicePrefix + DecideApplicationProfileReviewInternalPath
	DecideApplicationProfileReviewGRPCMethod     = profilereviewv1.OperationApplicationProfileReviewDecideApplicationProfileRevisionReview
	SubmitApplicationProfileReviewInternalPath   = "/v1/applications/{application_id}/profile-revisions/{profile_revision_id}/reviews"
	SubmitApplicationProfileReviewExternalPath   = ServicePrefix + SubmitApplicationProfileReviewInternalPath
	SubmitApplicationProfileReviewGRPCMethod     = profilereviewv1.OperationApplicationProfileReviewSubmitApplicationProfileRevisionReview
	UpdateApplicationProfileRevisionInternalPath = "/v1/applications/{application_id}/profile-revisions/{profile_revision_id}"
	UpdateApplicationProfileRevisionExternalPath = ServicePrefix + UpdateApplicationProfileRevisionInternalPath
	UpdateApplicationProfileRevisionGRPCMethod   = profilev1.OperationApplicationProfileRevisionUpdateApplicationProfileRevision
	CreateApplicationProfileRevisionInternalPath = "/v1/applications/{application_id}/profile-revisions"
	CreateApplicationProfileRevisionExternalPath = ServicePrefix + CreateApplicationProfileRevisionInternalPath
	CreateApplicationProfileRevisionGRPCMethod   = profilev1.OperationApplicationProfileRevisionCreateApplicationProfileRevision

	ResolveTestLaunchTargetInternalPath = "/v1/applications/{application_id}/test-launch:resolve"
	ResolveTestLaunchTargetExternalPath = ServicePrefix + ResolveTestLaunchTargetInternalPath
	ResolveTestLaunchTargetGRPCMethod   = catalogv1.OperationCatalogResolveTestLaunchTarget
	ResolveLaunchTargetInternalPath     = "/v1/applications/{application_id}/launch-target:resolve"
	ResolveLaunchTargetExternalPath     = ServicePrefix + ResolveLaunchTargetInternalPath
	ResolveLaunchTargetGRPCMethod       = runtimev1.OperationRuntimeResolutionServiceResolveLaunchTarget
	ListPublicApplicationsInternalPath  = "/v1/catalog/applications:search"
	ListPublicApplicationsExternalPath  = ServicePrefix + ListPublicApplicationsInternalPath
	ListPublicApplicationsGRPCMethod    = applicationcatalogv1.OperationApplicationCatalogServiceListPublicApplications
	GetPublicApplicationInternalPath    = "/v1/catalog/applications/{application_id}:get"
	GetPublicApplicationExternalPath    = ServicePrefix + GetPublicApplicationInternalPath
	GetPublicApplicationGRPCMethod      = applicationcatalogv1.OperationApplicationCatalogServiceGetPublicApplication

	RevokeTesterJoinLinkInternalPath           = "/v1/applications/{application_id}/tester-join-links/{join_link_id}"
	RevokeTesterJoinLinkExternalPath           = ServicePrefix + RevokeTesterJoinLinkInternalPath
	RevokeTesterJoinLinkGRPCMethod             = testerjoinlinkv1.OperationTesterJoinLinkRevokeTesterJoinLink
	RemoveApplicationTesterInternalPath        = "/v1/applications/{application_id}/tester-memberships/{membership_id}"
	RemoveApplicationTesterExternalPath        = ServicePrefix + RemoveApplicationTesterInternalPath
	RemoveApplicationTesterGRPCMethod          = testermembershipv1.OperationTesterMembershipRemoveApplicationTester
	JoinApplicationAsTesterInternalPath        = "/v1/tester-join-links/{join_link_id}/memberships"
	JoinApplicationAsTesterExternalPath        = ServicePrefix + JoinApplicationAsTesterInternalPath
	JoinApplicationAsTesterGRPCMethod          = testermembershipv1.OperationTesterMembershipJoinApplicationAsTester
	CreateOrRotateTesterJoinLinkInternalPath   = "/v1/applications/{application_id}/tester-join-links"
	CreateOrRotateTesterJoinLinkExternalPath   = ServicePrefix + CreateOrRotateTesterJoinLinkInternalPath
	CreateOrRotateTesterJoinLinkGRPCMethod     = testerjoinlinkv1.OperationTesterJoinLinkCreateOrRotateTesterJoinLink
	PlaceApprovedVersionInTestSlotInternalPath = "/v1/applications/{application_id}/publications/{rpc_api_major}/test-slot"
	PlaceApprovedVersionInTestSlotExternalPath = ServicePrefix + PlaceApprovedVersionInTestSlotInternalPath
	PlaceApprovedVersionInTestSlotGRPCMethod   = publicationv1.OperationApplicationPublicationPlaceApprovedVersionInTestSlot
	SetApprovedVersionInStableSlotInternalPath = "/v1/applications/{application_id}/publications/{rpc_api_major}/stable-slot"
	SetApprovedVersionInStableSlotExternalPath = ServicePrefix + SetApprovedVersionInStableSlotInternalPath
	SetApprovedVersionInStableSlotGRPCMethod   = publicationv1.OperationApplicationPublicationSetApprovedVersionInStableSlot
	ClearStableSlotInternalPath                = SetApprovedVersionInStableSlotInternalPath
	ClearStableSlotExternalPath                = SetApprovedVersionInStableSlotExternalPath
	ClearStableSlotGRPCMethod                  = publicationv1.OperationApplicationPublicationClearStableSlot
	SetGreyRolloutInternalPath                 = "/v1/applications/{application_id}/publications/{rpc_api_major}/grey-rollout"
	SetGreyRolloutExternalPath                 = ServicePrefix + SetGreyRolloutInternalPath
	SetGreyRolloutGRPCMethod                   = publicationv1.OperationApplicationPublicationSetGreyRollout
	ClearGreyRolloutInternalPath               = SetGreyRolloutInternalPath
	ClearGreyRolloutExternalPath               = SetGreyRolloutExternalPath
	ClearGreyRolloutGRPCMethod                 = publicationv1.OperationApplicationPublicationClearGreyRollout
	// ServicePrefix is the Gateway-only service prefix. It is not part of the
	// Proto HTTP annotation.
	ServicePrefix = "/app-center"
	// CreateApplicationInternalPath is the HTTP path declared by the Proto
	// google.api.http annotation.
	CreateApplicationInternalPath = "/v1/applications"
	// CreateApplicationExternalPath is the client-visible path after the Gateway
	// strips ServicePrefix.
	CreateApplicationExternalPath = ServicePrefix + CreateApplicationInternalPath
	// CreateApplicationGRPCMethod is the generated full method name.
	CreateApplicationGRPCMethod = applicationv1.OperationApplicationCreateApplication
	// CreateApplicationVersionInternalPath is declared by the UC-APP-002 Proto.
	CreateApplicationVersionInternalPath       = "/v1/applications/{application_id}/versions"
	CreateApplicationVersionExternalPath       = ServicePrefix + CreateApplicationVersionInternalPath
	CreateApplicationVersionGRPCMethod         = applicationversionv1.OperationApplicationVersionCreateApplicationVersion
	UpdateApplicationVersionInternalPath       = "/v1/applications/{application_id}/versions/{version_id}"
	UpdateApplicationVersionExternalPath       = ServicePrefix + UpdateApplicationVersionInternalPath
	UpdateApplicationVersionGRPCMethod         = applicationversionv1.OperationApplicationVersionUpdateApplicationVersion
	SubmitApplicationVersionReviewInternalPath = "/v1/applications/{application_id}/versions/{version_id}/reviews"
	SubmitApplicationVersionReviewExternalPath = ServicePrefix + SubmitApplicationVersionReviewInternalPath
	SubmitApplicationVersionReviewGRPCMethod   = applicationreviewv1.OperationApplicationReviewSubmitApplicationVersionReview
	RestoreApplicationVersionInternalPath      = "/v1/applications/{application_id}/versions/{version_id}/reviews/{review_id}/draft-restoration"
	RestoreApplicationVersionExternalPath      = ServicePrefix + RestoreApplicationVersionInternalPath
	RestoreApplicationVersionGRPCMethod        = applicationreviewv1.OperationApplicationReviewRestoreRejectedApplicationVersionToDraft
	DecideApplicationVersionReviewInternalPath = "/v1/applications/{application_id}/versions/{version_id}/reviews/{review_id}/decision"
	DecideApplicationVersionReviewExternalPath = ServicePrefix + DecideApplicationVersionReviewInternalPath
	DecideApplicationVersionReviewGRPCMethod   = applicationreviewv1.OperationApplicationReviewDecideApplicationVersionReview

	RegisterOAuthClientInternalPath              = "/v1/applications/{application_id}/oauth-registrations/{channel}/clients"
	RegisterOAuthClientExternalPath              = ServicePrefix + RegisterOAuthClientInternalPath
	RegisterOAuthClientGRPCMethod                = oauthclientv1.OperationOAuthClientServiceRegisterOAuthClient
	GetApplicationOAuthRegistrationInternalPath  = "/v1/applications/{application_id}/oauth-registrations/{channel}"
	GetApplicationOAuthRegistrationExternalPath  = ServicePrefix + GetApplicationOAuthRegistrationInternalPath
	GetApplicationOAuthRegistrationGRPCMethod    = oauthclientv1.OperationOAuthClientServiceGetApplicationOAuthRegistration
	SetOAuthClientStatusInternalPath             = "/v1/oauth-clients/{client_id}/status"
	SetOAuthClientStatusExternalPath             = ServicePrefix + SetOAuthClientStatusInternalPath
	SetOAuthClientStatusGRPCMethod               = oauthclientv1.OperationOAuthClientServiceSetOAuthClientStatus
	GetOAuthClientCredentialMetadataInternalPath = "/v1/oauth-clients/{client_id}/credential"
	GetOAuthClientCredentialMetadataExternalPath = ServicePrefix + GetOAuthClientCredentialMetadataInternalPath
	GetOAuthClientCredentialMetadataGRPCMethod   = oauthclientv1.OperationOAuthClientServiceGetOAuthClientCredentialMetadata
	RotateOAuthClientSecretInternalPath          = "/v1/oauth-clients/{client_id}/credential-rotations"
	RotateOAuthClientSecretExternalPath          = ServicePrefix + RotateOAuthClientSecretInternalPath
	RotateOAuthClientSecretGRPCMethod            = oauthclientv1.OperationOAuthClientServiceRotateOAuthClientSecret

	ApplicationFilterInternalPath    = "/v1/applications/{application_id}/filter"
	ApplicationFilterExternalPath    = ServicePrefix + ApplicationFilterInternalPath
	GetApplicationFilterGRPCMethod   = filterv1.OperationApplicationFilterServiceGetApplicationFilter
	SetApplicationFilterGRPCMethod   = filterv1.OperationApplicationFilterServiceSetApplicationFilter
	ClearApplicationFilterGRPCMethod = filterv1.OperationApplicationFilterServiceClearApplicationFilter
)

// ServerConfig carries the two listen addresses validated at startup.
type ServerConfig struct {
	HTTPAddr                  string
	GRPCAddr                  string
	ApplicationClosureEnabled bool
}

// Servers groups the two Kratos transports that share one ApplicationService.
type Servers struct {
	HTTP *khttp.Server
	GRPC *kgrpc.Server
}

func NewServersWithOAuthProvider(
	config ServerConfig,
	verifier *IdentityVerifier,
	service *ApplicationService,
	versionService *ApplicationVersionService,
	reviewService *ApplicationReviewService,
	publicationService *ApplicationPublicationService,
	testerJoinLinkService *TesterJoinLinkService,
	testerMembershipService *TesterMembershipService,
	catalogService *CatalogService,
	profileService *ApplicationProfileRevisionService,
	profileReviewService *ApplicationProfileReviewService,
	oauthClientService *OAuthClientService,
	filterService *ApplicationFilterService,
	serviceVerifier *ServiceIdentityVerifier,
	oauthProviderService *OAuthClientProviderService,
) (*Servers, error) {
	return NewServers(config, verifier, service, versionService, reviewService, publicationService, testerJoinLinkService, testerMembershipService, catalogService, profileService, profileReviewService, oauthClientService, filterService, serviceVerifier, oauthProviderService)
}

func NewServersWithRuntimeResolutionAndOAuthProvider(
	config ServerConfig,
	verifier *IdentityVerifier,
	service *ApplicationService,
	versionService *ApplicationVersionService,
	reviewService *ApplicationReviewService,
	publicationService *ApplicationPublicationService,
	testerJoinLinkService *TesterJoinLinkService,
	testerMembershipService *TesterMembershipService,
	catalogService *CatalogService,
	runtimeResolutionService *RuntimeResolutionService,
	profileService *ApplicationProfileRevisionService,
	profileReviewService *ApplicationProfileReviewService,
	oauthClientService *OAuthClientService,
	filterService *ApplicationFilterService,
	serviceVerifier *ServiceIdentityVerifier,
	oauthProviderService *OAuthClientProviderService,
) (*Servers, error) {
	return NewServers(config, verifier, service, versionService, reviewService, publicationService, testerJoinLinkService, testerMembershipService, catalogService, profileService, profileReviewService, oauthClientService, runtimeResolutionService, filterService, serviceVerifier, oauthProviderService)
}

func NewServersWithApplicationCatalogAndOAuthProvider(config ServerConfig, verifier *IdentityVerifier, service *ApplicationService, versionService *ApplicationVersionService, reviewService *ApplicationReviewService, publicationService *ApplicationPublicationService, testerJoinLinkService *TesterJoinLinkService, testerMembershipService *TesterMembershipService, catalogService *CatalogService, runtimeResolutionService *RuntimeResolutionService, applicationCatalogService *ApplicationCatalogService, profileService *ApplicationProfileRevisionService, profileReviewService *ApplicationProfileReviewService, oauthClientService *OAuthClientService, filterService *ApplicationFilterService, transferService *ApplicationAdminTransferService, closureService *ApplicationClosureService, serviceVerifier *ServiceIdentityVerifier, oauthProviderService *OAuthClientProviderService) (*Servers, error) {
	return NewServers(config, verifier, service, versionService, reviewService, publicationService, testerJoinLinkService, testerMembershipService, catalogService, profileService, profileReviewService, oauthClientService, runtimeResolutionService, applicationCatalogService, filterService, transferService, closureService, serviceVerifier, oauthProviderService)
}

func NewServers(
	config ServerConfig,
	verifier *IdentityVerifier,
	service *ApplicationService,
	versionService *ApplicationVersionService,
	reviewService *ApplicationReviewService,
	publicationService *ApplicationPublicationService,
	testerJoinLinkService *TesterJoinLinkService,
	testerMembershipService *TesterMembershipService,
	catalogService *CatalogService,
	profileService *ApplicationProfileRevisionService,
	profileReviewService *ApplicationProfileReviewService,
	oauthClientService *OAuthClientService,
	providerRuntime ...any,
) (*Servers, error) {
	if verifier == nil {
		return nil, errors.New("transport servers: identity verifier is required")
	}
	if service == nil {
		return nil, errors.New("transport servers: application service is required")
	}
	if versionService == nil {
		return nil, errors.New("transport servers: application version service is required")
	}
	if reviewService == nil {
		return nil, errors.New("transport servers: application review service is required")
	}

	if publicationService == nil {
		return nil, errors.New("transport servers: application publication service is required")
	}
	if testerJoinLinkService == nil {
		return nil, errors.New("transport servers: tester join link service is required")
	}
	if testerMembershipService == nil {
		return nil, errors.New("transport servers: tester membership service is required")
	}
	if catalogService == nil {
		return nil, errors.New("transport servers: catalog service is required")
	}
	if profileService == nil {
		return nil, errors.New("transport servers: application profile revision service is required")
	}
	if profileReviewService == nil {
		return nil, errors.New("transport servers: application profile review service is required")
	}
	if oauthClientService == nil {
		return nil, errors.New("transport servers: OAuth client service is required")
	}
	var serviceVerifier *ServiceIdentityVerifier
	var oauthProviderService *OAuthClientProviderService
	var filterService *ApplicationFilterService
	var runtimeResolutionService *RuntimeResolutionService
	var applicationCatalogService *ApplicationCatalogService
	var transferService *ApplicationAdminTransferService
	var closureService *ApplicationClosureService
	for _, runtime := range providerRuntime {
		switch value := runtime.(type) {
		case *RuntimeResolutionService:
			if value == nil || runtimeResolutionService != nil {
				return nil, errors.New("transport servers: runtime resolution service is invalid")
			}
			runtimeResolutionService = value
		case *ApplicationCatalogService:
			if value == nil || applicationCatalogService != nil {
				return nil, errors.New("transport servers: application catalog service is invalid")
			}
			applicationCatalogService = value
		case *ApplicationFilterService:
			if value == nil || filterService != nil {
				return nil, errors.New("transport servers: application filter service is invalid")
			}
			filterService = value
		case *ApplicationAdminTransferService:
			if value == nil || transferService != nil {
				return nil, errors.New("transport servers: application administrator transfer service is invalid")
			}
			transferService = value
		case *ApplicationClosureService:
			if value == nil || closureService != nil {
				return nil, errors.New("transport servers: application closure service is invalid")
			}
			closureService = value
		case *ServiceIdentityVerifier:
			if value == nil || serviceVerifier != nil {
				return nil, errors.New("transport servers: service identity verifier is invalid")
			}
			serviceVerifier = value
		case *OAuthClientProviderService:
			if value == nil || oauthProviderService != nil {
				return nil, errors.New("transport servers: OAuth provider service is invalid")
			}
			oauthProviderService = value
		default:
			return nil, errors.New("transport servers: invalid optional runtime")
		}
	}
	httpServer := khttp.NewServer(
		khttp.Address(config.HTTPAddr),
		khttp.Filter(testerMembershipCredentialFilter(verifier)),
		khttp.Middleware(identityMiddleware(verifier)),
		khttp.ResponseEncoder(createdResponseEncoder),
		khttp.ErrorEncoder(credentialSafeErrorEncoder),
	)
	profilereviewv1.RegisterApplicationProfileReviewHTTPServer(httpServer, profileReviewService)
	profilev1.RegisterApplicationProfileRevisionHTTPServer(httpServer, profileService)
	catalogv1.RegisterCatalogHTTPServer(httpServer, catalogService)
	if runtimeResolutionService != nil {
		runtimev1.RegisterRuntimeResolutionServiceHTTPServer(httpServer, runtimeResolutionService)
	}
	if applicationCatalogService != nil {
		applicationcatalogv1.RegisterApplicationCatalogServiceHTTPServer(httpServer, applicationCatalogService)
	}
	testermembershipv1.RegisterTesterMembershipHTTPServer(httpServer, testerMembershipService)
	testerjoinlinkv1.RegisterTesterJoinLinkHTTPServer(httpServer, testerJoinLinkService)
	publicationv1.RegisterApplicationPublicationHTTPServer(httpServer, publicationService)
	applicationv1.RegisterApplicationHTTPServer(httpServer, service)
	applicationversionv1.RegisterApplicationVersionHTTPServer(httpServer, versionService)
	applicationreviewv1.RegisterApplicationReviewHTTPServer(httpServer, reviewService)
	oauthclientv1.RegisterOAuthClientServiceHTTPServer(httpServer, oauthClientService)
	if transferService != nil {
		applicationadmintransferv1.RegisterApplicationAdminTransferServiceHTTPServer(httpServer, transferService)
	}
	if closureService != nil && config.ApplicationClosureEnabled {
		applicationclosurev1.RegisterApplicationClosureServiceHTTPServer(httpServer, closureService)
	}
	if filterService != nil {
		filterv1.RegisterApplicationFilterServiceHTTPServer(httpServer, filterService)
	}
	grpcMiddleware := identityMiddleware(verifier)
	if serviceVerifier != nil {
		grpcMiddleware = authenticationMiddleware(verifier, serviceVerifier)
	}
	grpcServer := kgrpc.NewServer(kgrpc.Address(config.GRPCAddr), kgrpc.Middleware(grpcMiddleware))
	profilereviewv1.RegisterApplicationProfileReviewServer(grpcServer, profileReviewService)
	profilev1.RegisterApplicationProfileRevisionServer(grpcServer, profileService)
	catalogv1.RegisterCatalogServer(grpcServer, catalogService)
	if runtimeResolutionService != nil {
		runtimev1.RegisterRuntimeResolutionServiceServer(grpcServer, runtimeResolutionService)
	}
	if applicationCatalogService != nil {
		applicationcatalogv1.RegisterApplicationCatalogServiceServer(grpcServer, applicationCatalogService)
	}
	testermembershipv1.RegisterTesterMembershipServer(grpcServer, testerMembershipService)
	testerjoinlinkv1.RegisterTesterJoinLinkServer(grpcServer, testerJoinLinkService)
	publicationv1.RegisterApplicationPublicationServer(grpcServer, publicationService)
	applicationv1.RegisterApplicationServer(grpcServer, service)
	applicationversionv1.RegisterApplicationVersionServer(grpcServer, versionService)
	applicationreviewv1.RegisterApplicationReviewServer(grpcServer, reviewService)
	oauthclientv1.RegisterOAuthClientServiceServer(grpcServer, oauthClientService)
	if transferService != nil {
		applicationadmintransferv1.RegisterApplicationAdminTransferServiceServer(grpcServer, transferService)
	}
	if closureService != nil && config.ApplicationClosureEnabled {
		applicationclosurev1.RegisterApplicationClosureServiceServer(grpcServer, closureService)
	}
	if filterService != nil {
		filterv1.RegisterApplicationFilterServiceServer(grpcServer, filterService)
	}
	if oauthProviderService != nil {
		oauthclientv1.RegisterOAuthClientProviderServiceServer(grpcServer, oauthProviderService)
	}

	return &Servers{HTTP: httpServer, GRPC: grpcServer}, nil
}

// createdResponseEncoder turns the generated handler's default 200 into the
// contract's 201 Created without touching the shared gRPC service. Kratos
// stores the status in a response writer whose WriteHeader only records the
// code, so overriding it here is safe and transport-local.
func createdResponseEncoder(w http.ResponseWriter, r *http.Request, v any) error {
	switch response := v.(type) {
	case *profilereviewv1.DecideApplicationProfileRevisionReviewResponse:
		w.Header().Set("ETag", fmt.Sprintf("\"%d\"", response.GetProfileRevision().GetRevision()))
	case *profilereviewv1.SubmitApplicationProfileRevisionReviewResponse:
		w.Header().Set("ETag", fmt.Sprintf("\"%d\"", response.GetProfileRevision().GetRevision()))
		w.WriteHeader(http.StatusCreated)
	case *profilev1.UpdateApplicationProfileRevisionResponse:
		w.Header().Set("ETag", fmt.Sprintf("\"%d\"", response.GetRevision()))
	case *profilev1.CreateApplicationProfileRevisionResponse:
		w.Header().Set("ETag", fmt.Sprintf("\"%d\"", response.GetRevision()))
		w.WriteHeader(http.StatusCreated)
	case *catalogv1.TestLaunchDescriptor:
		w.Header().Set("Cache-Control", "private, no-store")
	case *runtimev1.LaunchTargetDescriptor:
		w.Header().Set("Cache-Control", "private, no-store")
	case *applicationcatalogv1.PublicApplicationCatalogPage, *applicationcatalogv1.PublicApplicationCatalogItem:
		w.Header().Set("Cache-Control", "private, no-store")
	case *testerjoinlinkv1.RevokeTesterJoinLinkResponse:
		w.Header().Set("Cache-Control", "no-store")
	case *testermembershipv1.RemoveApplicationTesterResponse:
		w.Header().Set("Cache-Control", "no-store")
	case *testermembershipv1.JoinApplicationAsTesterResponse:
		w.Header().Set("Cache-Control", "no-store")
		if response.GetJoined() {
			w.WriteHeader(http.StatusCreated)
		}
	case *testerjoinlinkv1.CreateOrRotateTesterJoinLinkResponse:
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusCreated)
	case *publicationv1.PlaceApprovedVersionInTestSlotResponse:
		if response.GetChanged() && response.GetPublication().GetRevision() == 1 {
			w.WriteHeader(http.StatusCreated)
		}
	case *publicationv1.SetApprovedVersionInStableSlotResponse:
		if response.GetChanged() && response.GetPublication().GetRevision() == 1 {
			w.WriteHeader(http.StatusCreated)
		}
	case *applicationv1.CreateApplicationResponse:
		w.WriteHeader(http.StatusCreated)
	case *applicationversionv1.CreateApplicationVersionResponse:
		w.Header().Set("ETag", fmt.Sprintf("\"%d\"", response.GetRevision()))
		w.WriteHeader(http.StatusCreated)
	case *applicationversionv1.UpdateApplicationVersionResponse:
		w.Header().Set("ETag", fmt.Sprintf("\"%d\"", response.GetRevision()))
	case *applicationreviewv1.SubmitApplicationVersionReviewResponse:
		if review := response.GetReview(); review != nil {
			w.Header().Set("Location", fmt.Sprintf(
				"/v1/applications/%s/versions/%s/reviews/%s",
				review.GetApplicationId(), review.GetVersionId(), review.GetReviewId(),
			))
		}
		w.WriteHeader(http.StatusCreated)
	case *oauthclientv1.RegisterOAuthClientResponse:
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusCreated)
	case *applicationadmintransferv1.InitiateApplicationAdminTransferResponse:
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusCreated)
	case *applicationadmintransferv1.GetApplicationOwnershipResponse,
		*applicationadmintransferv1.GetApplicationAdminTransferResponse,
		*applicationadmintransferv1.AcceptApplicationAdminTransferResponse,
		*applicationadmintransferv1.RejectApplicationAdminTransferResponse,
		*applicationadmintransferv1.CancelApplicationAdminTransferResponse:
		w.Header().Set("Cache-Control", "no-store")
	case *applicationclosurev1.CloseApplicationResponse:
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusAccepted)
	case *applicationclosurev1.GetApplicationClosurePreviewResponse, *applicationclosurev1.GetApplicationClosureResponse:
		w.Header().Set("Cache-Control", "no-store")
	case *oauthclientv1.GetApplicationOAuthRegistrationResponse,
		*oauthclientv1.SetOAuthClientStatusResponse,
		*oauthclientv1.GetOAuthClientCredentialMetadataResponse,
		*oauthclientv1.RotateOAuthClientSecretResponse:
		w.Header().Set("Cache-Control", "no-store")
	case *filterv1.GetApplicationFilterResponse:
		w.Header().Set("ETag", fmt.Sprintf("\"%d\"", response.GetFilter().GetRevision()))
	case *filterv1.SetApplicationFilterResponse:
		w.Header().Set("ETag", fmt.Sprintf("\"%d\"", response.GetFilter().GetRevision()))
		if response.GetChanged() && response.GetFilter().GetRevision() == 1 {
			w.WriteHeader(http.StatusCreated)
		}
	case *filterv1.ClearApplicationFilterResponse:
		w.Header().Set("ETag", fmt.Sprintf("\"%d\"", response.GetFilter().GetRevision()))
	}
	return khttp.DefaultResponseEncoder(w, r, v)
}

// HTTP binding failures occur before the service and can include raw JSON
// values. Never return those parser details on the credential-bearing route.
func credentialSafeErrorEncoder(w http.ResponseWriter, r *http.Request, err error) {
	if isApplicationClosureRequest(r) {
		applicationClosureSafeErrorEncoder(w, r, err)
		return
	}
	if isApplicationAdminTransferRequest(r) {
		applicationAdminTransferSafeErrorEncoder(w, r, err)
		return
	}
	if isApplicationFilterRequest(r) {
		applicationFilterSafeErrorEncoder(w, r, err)
		return
	}
	if isOAuthClientManagementRequest(r) {
		w.Header().Set("Cache-Control", "no-store")
	}
	if isDecideApplicationProfileReviewRequest(r) {
		profileDecisionErrorEncoder(w, r, err)
		return
	}
	if isSubmitApplicationProfileReviewRequest(r) {
		profileSubmissionErrorEncoder(w, r, err)
		return
	}
	if isCreateApplicationProfileRevisionRequest(r) || isUpdateApplicationProfileRevisionRequest(r) {
		profileSafeErrorEncoder(w, r, err)
		return
	}
	if isTestLaunchResolutionRequest(r) {
		catalogSafeErrorEncoder(w, r, err)
		return
	}
	if isRuntimeResolutionRequest(r) {
		runtimeResolutionSafeErrorEncoder(w, r, err)
		return
	}
	if isPublicCatalogRequest(r) {
		applicationCatalogSafeErrorEncoder(w, r, err)
		return
	}
	if isTesterJoinLinkRevocationRequest(r) {
		w.Header().Set("Cache-Control", "no-store")
		if _, ok := testerjoinlinkv1.ErrorReason_value[kerrors.FromError(err).Reason]; !ok {
			if kerrors.FromError(err).Code == http.StatusBadRequest {
				err = invalidRevokeTesterJoinLinkRequest()
			} else {
				err = toTransportError(testerdomain.NewInternalError(nil))
			}
		}
	}
	if isTesterRemovalRequest(r) {
		w.Header().Set("Cache-Control", "no-store")
		if _, ok := testermembershipv1.ErrorReason_value[kerrors.FromError(err).Reason]; !ok {
			if kerrors.FromError(err).Code == http.StatusBadRequest {
				err = invalidRemoveTesterRequest()
			} else {
				err = toTransportError(testerdomain.NewInternalError(nil))
			}
		}
	}
	if isTesterMembershipRequest(r) {
		w.Header().Set("Cache-Control", "no-store")
		if _, ok := testermembershipv1.ErrorReason_value[kerrors.FromError(err).Reason]; !ok {
			if kerrors.FromError(err).Code == http.StatusBadRequest {
				err = toTransportError(testerdomain.ErrInvalidTesterJoinSecret)
			} else {
				err = toTransportError(testerdomain.NewInternalError(nil))
			}
		}
	}
	khttp.DefaultErrorEncoder(w, r, err)
}

func isApplicationClosureRequest(r *http.Request) bool {
	path := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 4 && parts[0] == "v1" && parts[1] == "applications" && (parts[3] == "closure" || parts[3] == "closure-preview") {
		return r.Method == http.MethodGet
	}
	return len(parts) == 3 && parts[0] == "v1" && parts[1] == "applications" && strings.HasSuffix(parts[2], ":close") && r.Method == http.MethodPost
}

func applicationClosureOperation(r *http.Request) string {
	if strings.HasSuffix(r.URL.Path, ":close") {
		return applicationclosurev1.OperationApplicationClosureServiceCloseApplication
	}
	if strings.HasSuffix(r.URL.Path, "/closure-preview") {
		return applicationclosurev1.OperationApplicationClosureServiceGetApplicationClosurePreview
	}
	return applicationclosurev1.OperationApplicationClosureServiceGetApplicationClosure
}

func applicationClosureSafeErrorEncoder(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "no-store")
	converted := kerrors.FromError(err)
	if _, ok := applicationclosurev1.ErrorReason_value[converted.Reason]; !ok {
		reason, message := "ERROR_REASON_INVALID_APPLICATION_ID", "application closure request is invalid"
		if strings.HasSuffix(r.URL.Path, ":close") {
			reason, message = "ERROR_REASON_INVALID_CLOSE_CONFIRMATION", "application close command is invalid"
		}
		if converted.Code != http.StatusBadRequest {
			reason, message = "ERROR_REASON_INTERNAL", "internal failure"
		}
		err = kerrors.New(int(converted.Code), reason, message)
	}
	khttp.DefaultErrorEncoder(w, r, err)
}

func applicationAdminTransferSafeErrorEncoder(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "no-store")
	converted := kerrors.FromError(err)
	if _, ok := applicationadmintransferv1.ErrorReason_value[converted.Reason]; !ok {
		reason := "ERROR_REASON_INVALID_TRANSFER_ID"
		message := "application administrator transfer request is invalid"
		if strings.HasSuffix(r.URL.Path, ":accept") {
			reason = "ERROR_REASON_INVALID_CONFIDENTIAL_CREDENTIAL_HANDLING"
		} else if strings.HasSuffix(r.URL.Path, "/admin-transfers") {
			reason = "ERROR_REASON_INVALID_TARGET_AUTH_ID"
		}
		if converted.Code != http.StatusBadRequest {
			reason, message = "ERROR_REASON_INTERNAL", "internal failure"
		}
		err = kerrors.New(int(converted.Code), reason, message)
		converted = kerrors.FromError(err)
	}
	switch converted.Reason {
	case "ERROR_REASON_APPLICATION_QUOTA_EXCEEDED",
		"ERROR_REASON_APPLICATION_ADMIN_TRANSFER_EXPIRED",
		"ERROR_REASON_APPLICATION_ADMIN_TRANSFER_NOT_PENDING",
		"ERROR_REASON_ACCOUNT_OWNER_EXIT_IN_PROGRESS":
		err = kerrors.New(http.StatusConflict, converted.Reason, converted.Message)
	case "ERROR_REASON_SOURCE_DEVELOPER_INELIGIBLE", "ERROR_REASON_TARGET_DEVELOPER_INELIGIBLE":
		err = kerrors.New(http.StatusUnprocessableEntity, converted.Reason, converted.Message)
	}
	khttp.DefaultErrorEncoder(w, r, err)
}

func isApplicationAdminTransferRequest(r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) == 4 && parts[0] == "v1" && parts[1] == "applications" {
		return r.Method == http.MethodGet && parts[3] == "ownership" || r.Method == http.MethodPost && parts[3] == "admin-transfers"
	}
	if len(parts) != 3 || parts[0] != "v1" || parts[1] != "application-admin-transfers" {
		return false
	}
	return r.Method == http.MethodGet && !strings.Contains(parts[2], ":") ||
		r.Method == http.MethodPost && (strings.HasSuffix(parts[2], ":accept") || strings.HasSuffix(parts[2], ":reject") || strings.HasSuffix(parts[2], ":cancel"))
}

func applicationAdminTransferOperation(r *http.Request) string {
	switch {
	case strings.HasSuffix(r.URL.Path, "/ownership"):
		return applicationadmintransferv1.OperationApplicationAdminTransferServiceGetApplicationOwnership
	case strings.HasSuffix(r.URL.Path, "/admin-transfers"):
		return applicationadmintransferv1.OperationApplicationAdminTransferServiceInitiateApplicationAdminTransfer
	case strings.HasSuffix(r.URL.Path, ":accept"):
		return applicationadmintransferv1.OperationApplicationAdminTransferServiceAcceptApplicationAdminTransfer
	case strings.HasSuffix(r.URL.Path, ":reject"):
		return applicationadmintransferv1.OperationApplicationAdminTransferServiceRejectApplicationAdminTransfer
	case strings.HasSuffix(r.URL.Path, ":cancel"):
		return applicationadmintransferv1.OperationApplicationAdminTransferServiceCancelApplicationAdminTransfer
	default:
		return applicationadmintransferv1.OperationApplicationAdminTransferServiceGetApplicationAdminTransfer
	}
}

func validApplicationAdminTransferHTTPInput(r *http.Request) bool {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		return false
	}
	if r.Method == http.MethodGet {
		return validTesterRemovalHTTPInput(r)
	}
	if !strings.HasSuffix(r.URL.Path, ":reject") && !strings.HasSuffix(r.URL.Path, ":cancel") {
		return true
	}
	if r.Body == nil {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	if err != nil || len(body) > 4096 {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if strings.TrimSpace(string(body)) == "" {
		return true
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(body, &value) != nil || len(value) > 1 {
		return false
	}
	raw, present := value["transferId"]
	if !present {
		return len(value) == 0
	}
	var bodyID string
	if json.Unmarshal(raw, &bodyID) != nil {
		return false
	}
	pathID := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[2]
	pathID = strings.TrimSuffix(strings.TrimSuffix(pathID, ":reject"), ":cancel")
	return bodyID == pathID
}

func isTesterMembershipRequest(r *http.Request) bool {
	return r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/tester-join-links/") && strings.HasSuffix(r.URL.Path, "/memberships") && strings.Count(r.URL.Path, "/") == 4
}

func isOAuthClientManagementRequest(r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "v1" && parts[1] == "oauth-clients" {
		return true
	}
	return len(parts) >= 4 && parts[0] == "v1" && parts[1] == "applications" && parts[3] == "oauth-registrations"
}

func isTesterRemovalRequest(r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	return r.Method == http.MethodDelete && len(parts) == 5 && parts[0] == "v1" && parts[1] == "applications" && parts[3] == "tester-memberships"
}

func isTesterJoinLinkRevocationRequest(r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	return r.Method == http.MethodDelete && len(parts) == 5 && parts[0] == "v1" && parts[1] == "applications" && parts[3] == "tester-join-links"
}

func validTesterRemovalHTTPInput(r *http.Request) bool {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		return false
	}
	if r.Body == nil {
		return true
	}
	// The operation has no body. A small bound avoids consuming unbounded
	// whitespace; read failures and any data beyond the bound fail closed.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1025))
	return err == nil && len(body) <= 1024 && strings.TrimSpace(string(body)) == ""
}

// Generated bindings decode sensitive JSON before invoking Kratos middleware.
// This narrow filter establishes authentication first; the shared middleware
// still verifies and supplies the trusted use-case identity for both protocols.
func testerMembershipCredentialFilter(verifier *IdentityVerifier) khttp.FilterFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isDecideApplicationProfileReviewRequest(r) || isSubmitApplicationProfileReviewRequest(r) || isTesterMembershipRequest(r) || isTesterRemovalRequest(r) || isTesterJoinLinkRevocationRequest(r) || isTestLaunchResolutionRequest(r) || isRuntimeResolutionRequest(r) || isPublicCatalogRequest(r) || isCreateApplicationProfileRevisionRequest(r) || isUpdateApplicationProfileRevisionRequest(r) || isOAuthClientManagementRequest(r) || isApplicationFilterRequest(r) || isApplicationAdminTransferRequest(r) || isApplicationClosureRequest(r) {
				w.Header().Set("Cache-Control", "no-store")
				if isTestLaunchResolutionRequest(r) || isRuntimeResolutionRequest(r) || isPublicCatalogRequest(r) {
					w.Header().Set("Cache-Control", "private, no-store")
				}
				var identityErr error
				for _, key := range legacyIdentityHeaders {
					for _, value := range r.Header.Values(key) {
						if strings.TrimSpace(value) != "" {
							identityErr = errIdentityInvalid
						}
					}
				}
				if identityErr == nil && (isRuntimeResolutionRequest(r) || isPublicCatalogRequest(r)) {
					values := r.Header.Values(IdentityHeader)
					switch {
					case len(values) == 0:
					case len(values) != 1 || strings.TrimSpace(values[0]) == "":
						identityErr = errIdentityInvalid
					default:
						_, identityErr = verifier.Verify(strings.TrimSpace(values[0]))
					}
				} else if identityErr == nil {
					values := r.Header.Values(IdentityHeader)
					switch {
					case len(values) == 0:
						identityErr = errIdentityRequired
					case len(values) != 1:
						identityErr = errIdentityInvalid
					case strings.TrimSpace(values[0]) == "":
						identityErr = errIdentityRequired
					default:
						_, identityErr = verifier.Verify(strings.TrimSpace(values[0]))
					}
				}
				if identityErr != nil {
					operation := JoinApplicationAsTesterGRPCMethod
					if isOAuthClientManagementRequest(r) {
						operation = RegisterOAuthClientGRPCMethod
					}
					if isApplicationFilterRequest(r) {
						switch r.Method {
						case http.MethodGet:
							operation = GetApplicationFilterGRPCMethod
						case http.MethodPut:
							operation = SetApplicationFilterGRPCMethod
						case http.MethodDelete:
							operation = ClearApplicationFilterGRPCMethod
						}
					}
					if isDecideApplicationProfileReviewRequest(r) {
						operation = DecideApplicationProfileReviewGRPCMethod
					}
					if isSubmitApplicationProfileReviewRequest(r) {
						operation = SubmitApplicationProfileReviewGRPCMethod
					}
					if isUpdateApplicationProfileRevisionRequest(r) {
						operation = UpdateApplicationProfileRevisionGRPCMethod
					}
					if isCreateApplicationProfileRevisionRequest(r) {
						operation = CreateApplicationProfileRevisionGRPCMethod
					}
					if isTestLaunchResolutionRequest(r) {
						operation = ResolveTestLaunchTargetGRPCMethod
					}
					if isRuntimeResolutionRequest(r) {
						operation = ResolveLaunchTargetGRPCMethod
					}
					if isPublicCatalogListRequest(r) {
						operation = ListPublicApplicationsGRPCMethod
					} else if isPublicCatalogRequest(r) {
						operation = GetPublicApplicationGRPCMethod
					}
					if isTesterRemovalRequest(r) {
						operation = RemoveApplicationTesterGRPCMethod
					}
					if isTesterJoinLinkRevocationRequest(r) {
						operation = RevokeTesterJoinLinkGRPCMethod
					}
					if isApplicationAdminTransferRequest(r) {
						operation = applicationAdminTransferOperation(r)
					}
					if isApplicationClosureRequest(r) {
						operation = applicationClosureOperation(r)
					}
					credentialSafeErrorEncoder(w, r, toIdentityTransportError(identityErr, operation))
					return
				}
				if isApplicationAdminTransferRequest(r) && !validApplicationAdminTransferHTTPInput(r) {
					credentialSafeErrorEncoder(w, r, transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_TRANSFER_ID", "application administrator transfer request is invalid"))
					return
				}
				if isDecideApplicationProfileReviewRequest(r) && !validProfileDecisionHTTPInput(r) {
					credentialSafeErrorEncoder(w, r, invalidProfileDecision())
					return
				}
				if isSubmitApplicationProfileReviewRequest(r) && !validProfileSubmissionHTTPInput(r) {
					credentialSafeErrorEncoder(w, r, invalidProfileSubmission())
					return
				}
				if isUpdateApplicationProfileRevisionRequest(r) {
					if _, err := parseProfileIfMatch(r.Header.Values("If-Match")); err != nil {
						credentialSafeErrorEncoder(w, r, err)
						return
					}
					if !validCreateApplicationProfileRevisionHTTPInput(r) {
						credentialSafeErrorEncoder(w, r, invalidUpdateApplicationProfileRevisionRequest())
						return
					}
				}
				if isCreateApplicationProfileRevisionRequest(r) && !validCreateApplicationProfileRevisionHTTPInput(r) {
					credentialSafeErrorEncoder(w, r, invalidCreateApplicationProfileRevisionRequest())
					return
				}
				if isTestLaunchResolutionRequest(r) && !validTestLaunchHTTPInput(r) {
					credentialSafeErrorEncoder(w, r, invalidResolveTestLaunchRequest())
					return
				}
				if isRuntimeResolutionRequest(r) && !validRuntimeResolutionHTTPInput(r) {
					credentialSafeErrorEncoder(w, r, invalidResolveLaunchTargetRequest())
					return
				}
				if isPublicCatalogRequest(r) && !validApplicationCatalogHTTPInput(r) {
					credentialSafeErrorEncoder(w, r, invalidApplicationCatalogRequest())
					return
				}
				if isTesterJoinLinkRevocationRequest(r) && !validTesterRemovalHTTPInput(r) {
					credentialSafeErrorEncoder(w, r, invalidRevokeTesterJoinLinkRequest())
					return
				}
				if isTesterRemovalRequest(r) && !validTesterRemovalHTTPInput(r) {
					credentialSafeErrorEncoder(w, r, invalidRemoveTesterRequest())
					return
				}
				if isApplicationFilterRequest(r) && !validApplicationFilterHTTPInput(r) {
					applicationFilterSafeErrorEncoder(w, r, toTransportError(filterInvalidApplicationFilter()))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
