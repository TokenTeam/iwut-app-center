package transport

import (
	"encoding/json"
	"errors"
	catalogdomain "iwut-app-center/internal/catalog/domain"
	filterdomain "iwut-app-center/internal/filter/domain"
	managementdomain "iwut-app-center/internal/management/domain"
	oauthclientdomain "iwut-app-center/internal/oauthclient/domain"
	profiledomain "iwut-app-center/internal/profile/domain"
	"iwut-app-center/internal/shared"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"iwut-app-center/internal/application/domain"
	publicationdomain "iwut-app-center/internal/publication/domain"
	reviewdomain "iwut-app-center/internal/review/domain"
	testerdomain "iwut-app-center/internal/tester/domain"
	versiondomain "iwut-app-center/internal/version/domain"
)

// Stable Proto error reasons. They are the machine-readable contract clients
// branch on; the accompanying message is never parsed. Values mirror the
// ErrorReason enums in the formal v1 capability packages and are asserted
// mechanically by API contract tests.
const (
	ReasonInvalidProfileReviewReason                     = "ERROR_REASON_INVALID_PROFILE_REVIEW_REASON"
	ReasonInvalidApplicationProfileReviewQuery           = "ERROR_REASON_INVALID_APPLICATION_PROFILE_REVIEW_QUERY"
	ReasonInvalidApplicationProfileReviewPageToken       = "ERROR_REASON_INVALID_APPLICATION_PROFILE_REVIEW_PAGE_TOKEN"
	ReasonProfileReviewChecksIncomplete                  = "ERROR_REASON_PROFILE_REVIEW_CHECKS_INCOMPLETE"
	ReasonProfileReviewPolicyUnavailable                 = "ERROR_REASON_PROFILE_REVIEW_POLICY_UNAVAILABLE"
	ReasonApplicationProfileReviewStateInconsistent      = "ERROR_REASON_APPLICATION_PROFILE_REVIEW_STATE_INCONSISTENT"
	ReasonApplicationProfilePublicationConflict          = "ERROR_REASON_APPLICATION_PROFILE_PUBLICATION_CONFLICT"
	ReasonApplicationProfileReviewStateConflict          = "ERROR_REASON_APPLICATION_PROFILE_REVIEW_STATE_CONFLICT"
	ReasonApplicationProfileReviewAlreadyDecided         = "ERROR_REASON_APPLICATION_PROFILE_REVIEW_ALREADY_DECIDED"
	ReasonApplicationProfileReviewNotFound               = "ERROR_REASON_APPLICATION_PROFILE_REVIEW_NOT_FOUND"
	ReasonInvalidApplicationProfileReviewDecision        = "ERROR_REASON_INVALID_APPLICATION_PROFILE_REVIEW_DECISION"
	ReasonApplicationProfileReviewConflictOfInterest     = "ERROR_REASON_APPLICATION_PROFILE_REVIEW_CONFLICT_OF_INTEREST"
	ReasonApplicationProfileReviewPermissionRequired     = "ERROR_REASON_APPLICATION_PROFILE_REVIEW_PERMISSION_REQUIRED"
	ReasonInvalidApplicationProfileReviewSubmission      = "ERROR_REASON_INVALID_APPLICATION_PROFILE_REVIEW_SUBMISSION"
	ReasonInvalidApplicationProfileContent               = "ERROR_REASON_INVALID_APPLICATION_PROFILE_CONTENT"
	ReasonInvalidApplicationProfileRevisionID            = "ERROR_REASON_INVALID_APPLICATION_PROFILE_REVISION_ID"
	ReasonApplicationProfileExpectedRevisionRequired     = "ERROR_REASON_APPLICATION_PROFILE_EXPECTED_REVISION_REQUIRED"
	ReasonApplicationProfileRevisionNotFound             = "ERROR_REASON_APPLICATION_PROFILE_REVISION_NOT_FOUND"
	ReasonApplicationProfileRevisionNotDraft             = "ERROR_REASON_APPLICATION_PROFILE_REVISION_NOT_DRAFT"
	ReasonApplicationProfileRevisionConflict             = "ERROR_REASON_APPLICATION_PROFILE_REVISION_CONFLICT"
	ReasonInvalidUpdateApplicationProfileRevisionRequest = "ERROR_REASON_INVALID_UPDATE_APPLICATION_PROFILE_REVISION_REQUEST"
	ReasonInvalidCreateApplicationProfileRevisionRequest = "ERROR_REASON_INVALID_CREATE_APPLICATION_PROFILE_REVISION_REQUEST"
	ReasonApplicationProfileStateInconsistent            = "ERROR_REASON_APPLICATION_PROFILE_STATE_INCONSISTENT"
	ReasonApplicationProfileWorkRevisionAlreadyExists    = "ERROR_REASON_APPLICATION_PROFILE_WORK_REVISION_ALREADY_EXISTS"
	ReasonInvalidApplicationIcon                         = "ERROR_REASON_INVALID_APPLICATION_ICON"
	ReasonInvalidApplicationDescription                  = "ERROR_REASON_INVALID_APPLICATION_DESCRIPTION"
	ReasonInvalidApplicationDisplayName                  = "ERROR_REASON_INVALID_APPLICATION_DISPLAY_NAME"
	ReasonInvalidHostRPCAPIMajor                         = "ERROR_REASON_INVALID_HOST_RPC_API_MAJOR"
	ReasonInvalidHostCapabilities                        = "ERROR_REASON_INVALID_HOST_CAPABILITIES"
	ReasonApplicationTesterRequired                      = "ERROR_REASON_APPLICATION_TESTER_REQUIRED"
	ReasonApplicationTestTargetUnavailable               = "ERROR_REASON_APPLICATION_TEST_TARGET_UNAVAILABLE"
	ReasonHostCapabilitiesInsufficient                   = "ERROR_REASON_HOST_CAPABILITIES_INSUFFICIENT"
	ReasonApplicationTestPublicationInconsistent         = "ERROR_REASON_APPLICATION_TEST_PUBLICATION_INCONSISTENT"
	ReasonInvalidResolveTestLaunchRequest                = "ERROR_REASON_INVALID_RESOLVE_TEST_LAUNCH_REQUEST"
	ReasonApplicationLaunchTargetUnavailable             = "ERROR_REASON_APPLICATION_LAUNCH_TARGET_UNAVAILABLE"
	ReasonApplicationRuntimeStateInconsistent            = "ERROR_REASON_APPLICATION_RUNTIME_STATE_INCONSISTENT"
	ReasonInvalidResolveLaunchTargetRequest              = "ERROR_REASON_INVALID_RESOLVE_LAUNCH_TARGET_REQUEST"
	ReasonInvalidPageSize                                = "ERROR_REASON_INVALID_PAGE_SIZE"
	ReasonInvalidPageToken                               = "ERROR_REASON_INVALID_PAGE_TOKEN"
	ReasonPublicApplicationNotFound                      = "ERROR_REASON_PUBLIC_APPLICATION_NOT_FOUND"
	ReasonApplicationCatalogStateInconsistent            = "ERROR_REASON_APPLICATION_CATALOG_STATE_INCONSISTENT"
	ReasonInvalidApplicationCatalogRequest               = "ERROR_REASON_INVALID_APPLICATION_CATALOG_REQUEST"

	ReasonApplicationTesterJoinLinkStateInconsistent = "ERROR_REASON_APPLICATION_TESTER_JOIN_LINK_STATE_INCONSISTENT"
	ReasonInvalidRevokeTesterJoinLinkRequest         = "ERROR_REASON_INVALID_REVOKE_TESTER_JOIN_LINK_REQUEST"
	ReasonInvalidRemoveTesterRequest                 = "ERROR_REASON_INVALID_REMOVE_TESTER_REQUEST"
	ReasonInvalidTesterMembershipId                  = "ERROR_REASON_INVALID_TESTER_MEMBERSHIP_ID"
	ReasonApplicationTesterMembershipNotFound        = "ERROR_REASON_APPLICATION_TESTER_MEMBERSHIP_NOT_FOUND"
	ReasonApplicationTesterStateInconsistent         = "ERROR_REASON_APPLICATION_TESTER_STATE_INCONSISTENT"
	ReasonAuthenticatedUserRequired                  = "ERROR_REASON_AUTHENTICATED_USER_REQUIRED"
	ReasonInvalidAuthenticatedUser                   = "ERROR_REASON_INVALID_AUTHENTICATED_USER"
	ReasonInvalidTesterJoinSecret                    = "ERROR_REASON_INVALID_TESTER_JOIN_SECRET"
	ReasonTesterJoinLinkInvalid                      = "ERROR_REASON_TESTER_JOIN_LINK_INVALID"
	ReasonApplicationTesterLimitReached              = "ERROR_REASON_APPLICATION_TESTER_LIMIT_REACHED"
	ReasonInvalidTesterJoinLinkId                    = "ERROR_REASON_INVALID_TESTER_JOIN_LINK_ID"
	ReasonApplicationTesterJoinLinkAlreadyExists     = "ERROR_REASON_APPLICATION_TESTER_JOIN_LINK_ALREADY_EXISTS"
	ReasonApplicationTesterJoinLinkNotFound          = "ERROR_REASON_APPLICATION_TESTER_JOIN_LINK_NOT_FOUND"
	ReasonApplicationTesterJoinLinkChanged           = "ERROR_REASON_APPLICATION_TESTER_JOIN_LINK_CHANGED"

	ReasonInvalidRpcApiMajor                      = "ERROR_REASON_INVALID_RPC_API_MAJOR"
	ReasonInvalidApplicationVersionId             = "ERROR_REASON_INVALID_APPLICATION_VERSION_ID"
	ReasonInvalidApplicationPublicationRevision   = "ERROR_REASON_INVALID_APPLICATION_PUBLICATION_REVISION"
	ReasonApplicationVersionNotApproved           = "ERROR_REASON_APPLICATION_VERSION_NOT_APPROVED"
	ReasonApplicationVersionRpcApiIncompatible    = "ERROR_REASON_APPLICATION_VERSION_RPC_API_INCOMPATIBLE"
	ReasonApplicationPublicationAlreadyExists     = "ERROR_REASON_APPLICATION_PUBLICATION_ALREADY_EXISTS"
	ReasonApplicationPublicationNotFound          = "ERROR_REASON_APPLICATION_PUBLICATION_NOT_FOUND"
	ReasonApplicationPublicationRevisionConflict  = "ERROR_REASON_APPLICATION_PUBLICATION_REVISION_CONFLICT"
	ReasonOAuthClientRegistrationRequired         = "ERROR_REASON_OAUTH_CLIENT_REGISTRATION_REQUIRED"
	ReasonApplicationProfileRequired              = "ERROR_REASON_APPLICATION_PROFILE_REQUIRED"
	ReasonStablePublicationRequiredByGrey         = "ERROR_REASON_STABLE_PUBLICATION_REQUIRED_BY_GREY"
	ReasonApplicationPublicationStateInconsistent = "ERROR_REASON_APPLICATION_PUBLICATION_STATE_INCONSISTENT"
	ReasonGreyStableBaselineRequired              = "ERROR_REASON_GREY_STABLE_BASELINE_REQUIRED"

	ReasonInvalidApplicationName                 = "ERROR_REASON_INVALID_APPLICATION_NAME"
	ReasonDeveloperIdentityRequired              = "ERROR_REASON_DEVELOPER_IDENTITY_REQUIRED"
	ReasonInvalidDeveloperIdentity               = "ERROR_REASON_INVALID_DEVELOPER_IDENTITY"
	ReasonDeveloperApprovalRequired              = "ERROR_REASON_DEVELOPER_APPROVAL_REQUIRED"
	ReasonApplicationNameAlreadyExists           = "ERROR_REASON_APPLICATION_NAME_ALREADY_EXISTS"
	ReasonApplicationQuotaExceeded               = "ERROR_REASON_APPLICATION_QUOTA_EXCEEDED"
	ReasonInvalidApplicationID                   = "ERROR_REASON_INVALID_APPLICATION_ID"
	ReasonInvalidVersionLabel                    = "ERROR_REASON_INVALID_VERSION_LABEL"
	ReasonInvalidApplicationLaunchURL            = "ERROR_REASON_INVALID_APPLICATION_LAUNCH_URL"
	ReasonInvalidRPCApiRange                     = "ERROR_REASON_INVALID_RPC_API_RANGE"
	ReasonInvalidRequiredCapability              = "ERROR_REASON_INVALID_REQUIRED_CAPABILITY"
	ReasonInvalidApplicationScope                = "ERROR_REASON_INVALID_APPLICATION_SCOPE"
	ReasonInvalidOAuthRedirectConfiguration      = "ERROR_REASON_INVALID_OAUTH_REDIRECT_CONFIGURATION"
	ReasonApplicationNotFound                    = "ERROR_REASON_APPLICATION_NOT_FOUND"
	ReasonApplicationAdminRequired               = "ERROR_REASON_APPLICATION_ADMIN_REQUIRED"
	ReasonApplicationVersionLabelExists          = "ERROR_REASON_APPLICATION_VERSION_LABEL_ALREADY_EXISTS"
	ReasonScopeCatalogUnavailable                = "ERROR_REASON_SCOPE_CATALOG_UNAVAILABLE"
	ReasonApplicationVersionRevisionRequired     = "ERROR_REASON_APPLICATION_VERSION_REVISION_REQUIRED"
	ReasonApplicationVersionNotFound             = "ERROR_REASON_APPLICATION_VERSION_NOT_FOUND"
	ReasonApplicationVersionNotDraft             = "ERROR_REASON_APPLICATION_VERSION_NOT_DRAFT"
	ReasonApplicationVersionNotRejected          = "ERROR_REASON_APPLICATION_VERSION_NOT_REJECTED"
	ReasonApplicationVersionRevisionConflict     = "ERROR_REASON_APPLICATION_VERSION_REVISION_CONFLICT"
	ReasonApplicationReviewNotFound              = "ERROR_REASON_APPLICATION_REVIEW_NOT_FOUND"
	ReasonApplicationReviewNotLatest             = "ERROR_REASON_APPLICATION_REVIEW_NOT_LATEST"
	ReasonApplicationReviewAlreadyRestored       = "ERROR_REASON_APPLICATION_REVIEW_ALREADY_RESTORED"
	ReasonApplicationReviewStateInconsistent     = "ERROR_REASON_APPLICATION_REVIEW_STATE_INCONSISTENT"
	ReasonApplicationLaunchURLNotReviewable      = "ERROR_REASON_APPLICATION_LAUNCH_URL_NOT_REVIEWABLE"
	ReasonLaunchURLInspectionUnavailable         = "ERROR_REASON_LAUNCH_URL_INSPECTION_UNAVAILABLE"
	ReasonReviewerIdentityRequired               = "ERROR_REASON_REVIEWER_IDENTITY_REQUIRED"
	ReasonInvalidReviewerIdentity                = "ERROR_REASON_INVALID_REVIEWER_IDENTITY"
	ReasonApplicationReviewPermissionRequired    = "ERROR_REASON_APPLICATION_REVIEW_PERMISSION_REQUIRED"
	ReasonApplicationReviewAlreadyDecided        = "ERROR_REASON_APPLICATION_REVIEW_ALREADY_DECIDED"
	ReasonApplicationReviewConflictOfInterest    = "ERROR_REASON_APPLICATION_REVIEW_CONFLICT_OF_INTEREST"
	ReasonInvalidApplicationReviewOutcome        = "ERROR_REASON_INVALID_APPLICATION_REVIEW_OUTCOME"
	ReasonInvalidApplicationReviewPolicyVersion  = "ERROR_REASON_INVALID_APPLICATION_REVIEW_POLICY_VERSION"
	ReasonApplicationReviewPolicyChanged         = "ERROR_REASON_APPLICATION_REVIEW_POLICY_CHANGED"
	ReasonApplicationReviewChecksIncomplete      = "ERROR_REASON_APPLICATION_REVIEW_CHECKS_INCOMPLETE"
	ReasonInvalidApplicationReviewChecks         = "ERROR_REASON_INVALID_APPLICATION_REVIEW_CHECKS"
	ReasonInvalidApplicationReviewReason         = "ERROR_REASON_INVALID_APPLICATION_REVIEW_REASON"
	ReasonInvalidApplicationReviewQuery          = "ERROR_REASON_INVALID_APPLICATION_REVIEW_QUERY"
	ReasonInvalidApplicationReviewPageToken      = "ERROR_REASON_INVALID_APPLICATION_REVIEW_PAGE_TOKEN"
	ReasonDeveloperStatusUnavailable             = "ERROR_REASON_DEVELOPER_STATUS_UNAVAILABLE"
	ReasonSystemPrincipalUnavailable             = "ERROR_REASON_SYSTEM_PRINCIPAL_UNAVAILABLE"
	ReasonInternal                               = "ERROR_REASON_INTERNAL"
	ReasonInvalidApplicationManagementRequest    = "ERROR_REASON_INVALID_APPLICATION_MANAGEMENT_REQUEST"
	ReasonInvalidApplicationManagementPageToken  = "ERROR_REASON_INVALID_APPLICATION_MANAGEMENT_PAGE_TOKEN"
	ReasonApplicationManagementNotFound          = "ERROR_REASON_APPLICATION_MANAGEMENT_NOT_FOUND"
	ReasonApplicationManagementStateInconsistent = "ERROR_REASON_APPLICATION_MANAGEMENT_STATE_INCONSISTENT"

	ReasonInvalidOAuthChannel              = "ERROR_REASON_INVALID_OAUTH_CHANNEL"
	ReasonOAuthChannelNotEnabled           = "ERROR_REASON_OAUTH_CHANNEL_NOT_ENABLED"
	ReasonInvalidOAuthClientType           = "ERROR_REASON_INVALID_OAUTH_CLIENT_TYPE"
	ReasonInvalidOAuthClientID             = "ERROR_REASON_INVALID_OAUTH_CLIENT_ID"
	ReasonInvalidOAuthClientStatus         = "ERROR_REASON_INVALID_OAUTH_CLIENT_STATUS"
	ReasonInvalidOAuthRegistrationRevision = "ERROR_REASON_INVALID_OAUTH_REGISTRATION_REVISION"
	ReasonInvalidOAuthCredentialRevision   = "ERROR_REASON_INVALID_OAUTH_CREDENTIAL_REVISION"
	ReasonOAuthRegistrationNotFound        = "ERROR_REASON_OAUTH_REGISTRATION_NOT_FOUND"
	ReasonOAuthClientAlreadyExists         = "ERROR_REASON_OAUTH_CLIENT_ALREADY_EXISTS"
	ReasonOAuthClientNotFound              = "ERROR_REASON_OAUTH_CLIENT_NOT_FOUND"
	ReasonOAuthRegistrationChanged         = "ERROR_REASON_OAUTH_REGISTRATION_CHANGED"
	ReasonOAuthClientCredentialNotFound    = "ERROR_REASON_OAUTH_CLIENT_CREDENTIAL_NOT_FOUND"
	ReasonOAuthClientCredentialChanged     = "ERROR_REASON_OAUTH_CLIENT_CREDENTIAL_CHANGED"
	ReasonOAuthClientStateInconsistent     = "ERROR_REASON_OAUTH_CLIENT_STATE_INCONSISTENT"
	ReasonServiceIdentityRequired          = "ERROR_REASON_SERVICE_IDENTITY_REQUIRED"
	ReasonInvalidServiceIdentity           = "ERROR_REASON_INVALID_SERVICE_IDENTITY"
	ReasonOAuthProviderPermissionDenied    = "ERROR_REASON_OAUTH_PROVIDER_PERMISSION_DENIED"
	ReasonInvalidOAuthProviderRequest      = "ERROR_REASON_INVALID_OAUTH_PROVIDER_REQUEST"
	ReasonOAuthClientRuntimeUnavailable    = "ERROR_REASON_OAUTH_CLIENT_RUNTIME_UNAVAILABLE"
	ReasonOAuthRuntimeVersionChanged       = "ERROR_REASON_OAUTH_RUNTIME_VERSION_CHANGED"
	ReasonOAuthProviderUnavailable         = "ERROR_REASON_OAUTH_PROVIDER_UNAVAILABLE"

	ReasonInvalidApplicationFilter           = "ERROR_REASON_INVALID_APPLICATION_FILTER"
	ReasonApplicationFilterRevisionConflict  = "ERROR_REASON_APPLICATION_FILTER_REVISION_CONFLICT"
	ReasonApplicationFilterStateInconsistent = "ERROR_REASON_APPLICATION_FILTER_STATE_INCONSISTENT"
)

type errorSpec struct {
	code    codes.Code
	reason  string
	message string
}

// domainErrorSpecs is the single ADR-005 mapping table from protocol-independent
// domain codes to canonical gRPC status and stable reason. Kratos derives the
// HTTP status from the gRPC code, so one table serves both transports without
// branching on error text.
var domainErrorSpecs = map[domain.ErrorCode]errorSpec{
	domain.ErrorCodeInvalidApplicationName: {
		code:    codes.InvalidArgument,
		reason:  ReasonInvalidApplicationName,
		message: "application name is invalid",
	},
	domain.ErrorCodeDeveloperIdentityRequired: {
		code:    codes.Unauthenticated,
		reason:  ReasonDeveloperIdentityRequired,
		message: "developer identity is required",
	},
	domain.ErrorCodeDeveloperApprovalRequired: {
		code:    codes.PermissionDenied,
		reason:  ReasonDeveloperApprovalRequired,
		message: "approved developer status is required",
	},
	domain.ErrorCodeApplicationNameAlreadyExists: {
		code:    codes.AlreadyExists,
		reason:  ReasonApplicationNameAlreadyExists,
		message: "application name already exists",
	},
	domain.ErrorCodeApplicationQuotaExceeded: {
		code:    codes.ResourceExhausted,
		reason:  ReasonApplicationQuotaExceeded,
		message: "application creation quota exceeded",
	},
	// InvalidApplicationId is an internal corruption signal on this path; it is
	// never a caller validation error.
	domain.ErrorCodeInvalidApplicationID: {
		code:    codes.Internal,
		reason:  ReasonInternal,
		message: "internal failure",
	},
	domain.ErrorCodeInternal: {
		code:    codes.Internal,
		reason:  ReasonInternal,
		message: "internal failure",
	},
}

var internalSpec = errorSpec{
	code:    codes.Internal,
	reason:  ReasonInternal,
	message: "internal failure",
}

var managementDomainErrorSpecs = map[managementdomain.ErrorCode]errorSpec{
	managementdomain.ErrorCodeAuthenticatedUserRequired:              {code: codes.Unauthenticated, reason: ReasonAuthenticatedUserRequired, message: "authenticated user is required"},
	managementdomain.ErrorCodeInvalidApplicationManagementRequest:    {code: codes.InvalidArgument, reason: ReasonInvalidApplicationManagementRequest, message: "application management request is invalid"},
	managementdomain.ErrorCodeInvalidApplicationManagementPageToken:  {code: codes.InvalidArgument, reason: ReasonInvalidApplicationManagementPageToken, message: "application management page token is invalid"},
	managementdomain.ErrorCodeApplicationManagementNotFound:          {code: codes.NotFound, reason: ReasonApplicationManagementNotFound, message: "application management resource not found"},
	managementdomain.ErrorCodeApplicationManagementStateInconsistent: {code: codes.Internal, reason: ReasonApplicationManagementStateInconsistent, message: "application management state is inconsistent"},
	managementdomain.ErrorCodeInternal:                               internalSpec,
}

var filterDomainErrorSpecs = map[filterdomain.ErrorCode]errorSpec{
	filterdomain.ErrorCodeInvalidApplicationID:               {code: codes.InvalidArgument, reason: ReasonInvalidApplicationID, message: "application ID is invalid"},
	filterdomain.ErrorCodeDeveloperIdentityRequired:          {code: codes.Unauthenticated, reason: ReasonDeveloperIdentityRequired, message: "developer identity is required"},
	filterdomain.ErrorCodeDeveloperApprovalRequired:          {code: codes.PermissionDenied, reason: ReasonDeveloperApprovalRequired, message: "approved developer status is required"},
	filterdomain.ErrorCodeInvalidApplicationFilter:           {code: codes.InvalidArgument, reason: ReasonInvalidApplicationFilter, message: "application filter is invalid"},
	filterdomain.ErrorCodeApplicationNotFound:                {code: codes.NotFound, reason: ReasonApplicationNotFound, message: "application not found"},
	filterdomain.ErrorCodeApplicationAdminRequired:           {code: codes.PermissionDenied, reason: ReasonApplicationAdminRequired, message: "application administrator is required"},
	filterdomain.ErrorCodeApplicationFilterRevisionConflict:  {code: codes.Aborted, reason: ReasonApplicationFilterRevisionConflict, message: "application filter revision conflict"},
	filterdomain.ErrorCodeApplicationFilterStateInconsistent: {code: codes.Internal, reason: ReasonApplicationFilterStateInconsistent, message: "application filter state is inconsistent"},
	filterdomain.ErrorCodeInternal:                           internalSpec,
}

var oauthClientDomainErrorSpecs = map[oauthclientdomain.ErrorCode]errorSpec{
	oauthclientdomain.ErrorCodeDeveloperIdentityRequired:           {code: codes.Unauthenticated, reason: ReasonDeveloperIdentityRequired, message: "developer identity is required"},
	oauthclientdomain.ErrorCodeDeveloperApprovalRequired:           {code: codes.PermissionDenied, reason: ReasonDeveloperApprovalRequired, message: "approved developer status is required"},
	oauthclientdomain.ErrorCodeInvalidApplicationID:                {code: codes.InvalidArgument, reason: ReasonInvalidApplicationID, message: "application ID is invalid"},
	oauthclientdomain.ErrorCodeInvalidOAuthChannel:                 {code: codes.InvalidArgument, reason: ReasonInvalidOAuthChannel, message: "OAuth channel is invalid"},
	oauthclientdomain.ErrorCodeOAuthChannelNotEnabled:              {code: codes.FailedPrecondition, reason: ReasonOAuthChannelNotEnabled, message: "OAuth channel is not enabled"},
	oauthclientdomain.ErrorCodeInvalidOAuthClientType:              {code: codes.InvalidArgument, reason: ReasonInvalidOAuthClientType, message: "OAuth client type is invalid"},
	oauthclientdomain.ErrorCodeInvalidOAuthClientID:                {code: codes.InvalidArgument, reason: ReasonInvalidOAuthClientID, message: "OAuth client ID is invalid"},
	oauthclientdomain.ErrorCodeInvalidOAuthClientStatus:            {code: codes.InvalidArgument, reason: ReasonInvalidOAuthClientStatus, message: "OAuth client status is invalid"},
	oauthclientdomain.ErrorCodeInvalidRegistrationRevision:         {code: codes.InvalidArgument, reason: ReasonInvalidOAuthRegistrationRevision, message: "OAuth registration revision is invalid"},
	oauthclientdomain.ErrorCodeInvalidCredentialRevision:           {code: codes.InvalidArgument, reason: ReasonInvalidOAuthCredentialRevision, message: "OAuth client credential revision is invalid"},
	oauthclientdomain.ErrorCodeApplicationNotFound:                 {code: codes.NotFound, reason: ReasonApplicationNotFound, message: "application not found"},
	oauthclientdomain.ErrorCodeApplicationAdminRequired:            {code: codes.PermissionDenied, reason: ReasonApplicationAdminRequired, message: "application administrator is required"},
	oauthclientdomain.ErrorCodeOAuthRegistrationNotFound:           {code: codes.NotFound, reason: ReasonOAuthRegistrationNotFound, message: "OAuth registration not found"},
	oauthclientdomain.ErrorCodeOAuthClientAlreadyExists:            {code: codes.AlreadyExists, reason: ReasonOAuthClientAlreadyExists, message: "OAuth client already exists"},
	oauthclientdomain.ErrorCodeOAuthClientNotFound:                 {code: codes.NotFound, reason: ReasonOAuthClientNotFound, message: "OAuth client not found"},
	oauthclientdomain.ErrorCodeOAuthRegistrationChanged:            {code: codes.Aborted, reason: ReasonOAuthRegistrationChanged, message: "OAuth registration changed"},
	oauthclientdomain.ErrorCodeOAuthCredentialNotFound:             {code: codes.NotFound, reason: ReasonOAuthClientCredentialNotFound, message: "OAuth client credential not found"},
	oauthclientdomain.ErrorCodeOAuthCredentialChanged:              {code: codes.Aborted, reason: ReasonOAuthClientCredentialChanged, message: "OAuth client credential changed"},
	oauthclientdomain.ErrorCodeOAuthClientStateInconsistent:        {code: codes.Internal, reason: ReasonOAuthClientStateInconsistent, message: "OAuth client state is inconsistent"},
	oauthclientdomain.ErrorCodeOAuthClientRuntimeUnavailable:       {code: codes.FailedPrecondition, reason: ReasonOAuthClientRuntimeUnavailable, message: "OAuth client runtime is unavailable"},
	oauthclientdomain.ErrorCodeOAuthRuntimeVersionChanged:          {code: codes.FailedPrecondition, reason: ReasonOAuthRuntimeVersionChanged, message: "OAuth runtime version changed"},
	oauthclientdomain.ErrorCodeApplicationProfileStateInconsistent: {code: codes.Internal, reason: ReasonApplicationProfileStateInconsistent, message: "application profile state is inconsistent"},
	oauthclientdomain.ErrorCodeOAuthProviderUnavailable:            {code: codes.Unavailable, reason: ReasonOAuthProviderUnavailable, message: "OAuth provider is unavailable"},
	oauthclientdomain.ErrorCodeInvalidOAuthProviderRequest:         {code: codes.InvalidArgument, reason: ReasonInvalidOAuthProviderRequest, message: "OAuth provider request is invalid"},
	oauthclientdomain.ErrorCodeInternal:                            internalSpec,
}

var versionDomainErrorSpecs = map[versiondomain.ErrorCode]errorSpec{
	versiondomain.ErrorCodeInvalidApplicationID:                 {code: codes.InvalidArgument, reason: ReasonInvalidApplicationID, message: "application ID is invalid"},
	versiondomain.ErrorCodeInvalidVersionLabel:                  {code: codes.InvalidArgument, reason: ReasonInvalidVersionLabel, message: "version label is invalid"},
	versiondomain.ErrorCodeInvalidApplicationLaunchURL:          {code: codes.InvalidArgument, reason: ReasonInvalidApplicationLaunchURL, message: "application launch URL is invalid"},
	versiondomain.ErrorCodeInvalidRPCApiRange:                   {code: codes.InvalidArgument, reason: ReasonInvalidRPCApiRange, message: "RPC API range is invalid"},
	versiondomain.ErrorCodeInvalidRequiredCapability:            {code: codes.InvalidArgument, reason: ReasonInvalidRequiredCapability, message: "required capability is invalid"},
	versiondomain.ErrorCodeInvalidApplicationScope:              {code: codes.InvalidArgument, reason: ReasonInvalidApplicationScope, message: "application scope request is invalid"},
	versiondomain.ErrorCodeInvalidOAuthRedirectConfiguration:    {code: codes.InvalidArgument, reason: ReasonInvalidOAuthRedirectConfiguration, message: "OAuth redirect configuration is invalid"},
	versiondomain.ErrorCodeDeveloperIdentityRequired:            {code: codes.Unauthenticated, reason: ReasonDeveloperIdentityRequired, message: "developer identity is required"},
	versiondomain.ErrorCodeDeveloperApprovalRequired:            {code: codes.PermissionDenied, reason: ReasonDeveloperApprovalRequired, message: "approved developer status is required"},
	versiondomain.ErrorCodeApplicationNotFound:                  {code: codes.NotFound, reason: ReasonApplicationNotFound, message: "application not found"},
	versiondomain.ErrorCodeApplicationAdminRequired:             {code: codes.PermissionDenied, reason: ReasonApplicationAdminRequired, message: "application administrator is required"},
	versiondomain.ErrorCodeApplicationVersionLabelAlreadyExists: {code: codes.AlreadyExists, reason: ReasonApplicationVersionLabelExists, message: "application version label already exists"},
	versiondomain.ErrorCodeScopeCatalogUnavailable:              {code: codes.Unavailable, reason: ReasonScopeCatalogUnavailable, message: "scope catalog is unavailable"},
	versiondomain.ErrorCodeApplicationVersionRevisionRequired:   {code: codes.InvalidArgument, reason: ReasonApplicationVersionRevisionRequired, message: "application version revision is required"},
	versiondomain.ErrorCodeApplicationVersionNotFound:           {code: codes.NotFound, reason: ReasonApplicationVersionNotFound, message: "application version not found"},
	versiondomain.ErrorCodeApplicationVersionNotDraft:           {code: codes.Aborted, reason: ReasonApplicationVersionNotDraft, message: "application version is not a draft"},
	versiondomain.ErrorCodeApplicationVersionNotRejected:        {code: codes.Aborted, reason: ReasonApplicationVersionNotRejected, message: "application version is not rejected"},
	versiondomain.ErrorCodeApplicationVersionRevisionConflict:   {code: codes.Aborted, reason: ReasonApplicationVersionRevisionConflict, message: "application version revision conflicts"},
	versiondomain.ErrorCodeInternal:                             internalSpec,
}

var reviewDomainErrorSpecs = map[reviewdomain.ErrorCode]errorSpec{
	reviewdomain.ErrorCodeDeveloperIdentityRequired:               {code: codes.Unauthenticated, reason: ReasonDeveloperIdentityRequired, message: "developer identity is required"},
	reviewdomain.ErrorCodeDeveloperApprovalRequired:               {code: codes.PermissionDenied, reason: ReasonDeveloperApprovalRequired, message: "approved developer status is required"},
	reviewdomain.ErrorCodeApplicationVersionRevisionRequired:      {code: codes.InvalidArgument, reason: ReasonApplicationVersionRevisionRequired, message: "application version revision is required"},
	reviewdomain.ErrorCodeApplicationVersionNotFound:              {code: codes.NotFound, reason: ReasonApplicationVersionNotFound, message: "application version not found"},
	reviewdomain.ErrorCodeApplicationAdminRequired:                {code: codes.PermissionDenied, reason: ReasonApplicationAdminRequired, message: "application administrator is required"},
	reviewdomain.ErrorCodeApplicationVersionNotDraft:              {code: codes.Aborted, reason: ReasonApplicationVersionNotDraft, message: "application version is not a draft"},
	reviewdomain.ErrorCodeApplicationVersionNotRejected:           {code: codes.Aborted, reason: ReasonApplicationVersionNotRejected, message: "application version is not rejected"},
	reviewdomain.ErrorCodeApplicationVersionRevisionConflict:      {code: codes.Aborted, reason: ReasonApplicationVersionRevisionConflict, message: "application version revision conflicts"},
	reviewdomain.ErrorCodeApplicationReviewNotFound:               {code: codes.NotFound, reason: ReasonApplicationReviewNotFound, message: "application review not found"},
	reviewdomain.ErrorCodeApplicationReviewNotLatest:              {code: codes.Aborted, reason: ReasonApplicationReviewNotLatest, message: "application review is not the latest attempt"},
	reviewdomain.ErrorCodeApplicationReviewAlreadyRestored:        {code: codes.Aborted, reason: ReasonApplicationReviewAlreadyRestored, message: "application review is already restored"},
	reviewdomain.ErrorCodeApplicationReviewStateInconsistent:      {code: codes.Aborted, reason: ReasonApplicationReviewStateInconsistent, message: "application review state is inconsistent"},
	reviewdomain.ErrorCodeApplicationLaunchURLNotReviewable:       {code: codes.InvalidArgument, reason: ReasonApplicationLaunchURLNotReviewable, message: "application launch URL is not reviewable"},
	reviewdomain.ErrorCodeInvalidOAuthRedirectConfiguration:       {code: codes.InvalidArgument, reason: ReasonInvalidOAuthRedirectConfiguration, message: "OAuth redirect configuration is invalid"},
	reviewdomain.ErrorCodeLaunchURLInspectionUnavailable:          {code: codes.Unavailable, reason: ReasonLaunchURLInspectionUnavailable, message: "launch URL inspection is unavailable"},
	reviewdomain.ErrorCodeInvalidApplicationScope:                 {code: codes.InvalidArgument, reason: ReasonInvalidApplicationScope, message: "application scope request is invalid"},
	reviewdomain.ErrorCodeScopeCatalogUnavailable:                 {code: codes.Unavailable, reason: ReasonScopeCatalogUnavailable, message: "scope catalog is unavailable"},
	reviewdomain.ErrorCodeReviewerIdentityRequired:                {code: codes.Unauthenticated, reason: ReasonReviewerIdentityRequired, message: "reviewer identity is required"},
	reviewdomain.ErrorCodeApplicationReviewPermissionRequired:     {code: codes.PermissionDenied, reason: ReasonApplicationReviewPermissionRequired, message: "application version review permission is required"},
	reviewdomain.ErrorCodeApplicationReviewAlreadyDecided:         {code: codes.Aborted, reason: ReasonApplicationReviewAlreadyDecided, message: "application review is already decided"},
	reviewdomain.ErrorCodeApplicationReviewConflictOfInterest:     {code: codes.PermissionDenied, reason: ReasonApplicationReviewConflictOfInterest, message: "reviewer has a conflict of interest"},
	reviewdomain.ErrorCodeInvalidApplicationReviewOutcome:         {code: codes.InvalidArgument, reason: ReasonInvalidApplicationReviewOutcome, message: "application review outcome is invalid"},
	reviewdomain.ErrorCodeInvalidApplicationReviewPolicyVersion:   {code: codes.InvalidArgument, reason: ReasonInvalidApplicationReviewPolicyVersion, message: "application review policy version is invalid"},
	reviewdomain.ErrorCodeApplicationReviewPolicyChanged:          {code: codes.Aborted, reason: ReasonApplicationReviewPolicyChanged, message: "application review policy is no longer usable"},
	reviewdomain.ErrorCodeApplicationReviewChecksIncomplete:       {code: codes.InvalidArgument, reason: ReasonApplicationReviewChecksIncomplete, message: "application review confirmation is incomplete"},
	reviewdomain.ErrorCodeInvalidApplicationReviewChecks:          {code: codes.InvalidArgument, reason: ReasonInvalidApplicationReviewChecks, message: "application review confirmation is invalid"},
	reviewdomain.ErrorCodeInvalidApplicationReviewReason:          {code: codes.InvalidArgument, reason: ReasonInvalidApplicationReviewReason, message: "application review reason is invalid"},
	reviewdomain.ErrorCodeDeveloperStatusUnavailable:              {code: codes.Unavailable, reason: ReasonDeveloperStatusUnavailable, message: "developer status is unavailable"},
	reviewdomain.ErrorCodeSystemPrincipalUnavailable:              {code: codes.Unavailable, reason: ReasonSystemPrincipalUnavailable, message: "system principal is unavailable"},
	reviewdomain.ErrorCodeInvalidApplicationReviewQuery:           {code: codes.InvalidArgument, reason: ReasonInvalidApplicationReviewQuery, message: "application review query is invalid"},
	reviewdomain.ErrorCodeInvalidApplicationReviewPageToken:       {code: codes.InvalidArgument, reason: ReasonInvalidApplicationReviewPageToken, message: "application review page token is invalid"},
	reviewdomain.ErrorCodeApplicationReviewQueryStateInconsistent: {code: codes.Internal, reason: ReasonApplicationReviewStateInconsistent, message: "application review query state is inconsistent"},
	reviewdomain.ErrorCodeInternal:                                internalSpec,
}

var publicationDomainErrorSpecs = map[publicationdomain.ErrorCode]errorSpec{
	publicationdomain.ErrorCodeInvalidRpcApiMajor:                      {code: codes.InvalidArgument, reason: ReasonInvalidRpcApiMajor, message: "RPC API major is invalid"},
	publicationdomain.ErrorCodeInvalidApplicationVersionId:             {code: codes.InvalidArgument, reason: ReasonInvalidApplicationVersionId, message: "application version ID is invalid"},
	publicationdomain.ErrorCodeInvalidApplicationPublicationRevision:   {code: codes.InvalidArgument, reason: ReasonInvalidApplicationPublicationRevision, message: "publication revision is invalid"},
	publicationdomain.ErrorCodeInvalidGreyExposureBasisPoints:          {code: codes.InvalidArgument, reason: ReasonInvalidApplicationPublicationRevision, message: "grey exposure basis points are invalid"},
	publicationdomain.ErrorCodeApplicationVersionNotApproved:           {code: codes.FailedPrecondition, reason: ReasonApplicationVersionNotApproved, message: "application version is not approved"},
	publicationdomain.ErrorCodeApplicationVersionRpcApiIncompatible:    {code: codes.InvalidArgument, reason: ReasonApplicationVersionRpcApiIncompatible, message: "application version RPC API range is incompatible"},
	publicationdomain.ErrorCodeApplicationPublicationAlreadyExists:     {code: codes.AlreadyExists, reason: ReasonApplicationPublicationAlreadyExists, message: "application publication already exists"},
	publicationdomain.ErrorCodeApplicationPublicationNotFound:          {code: codes.NotFound, reason: ReasonApplicationPublicationNotFound, message: "application publication not found"},
	publicationdomain.ErrorCodeApplicationPublicationRevisionConflict:  {code: codes.Aborted, reason: ReasonApplicationPublicationRevisionConflict, message: "publication revision conflicts"},
	publicationdomain.ErrorCodeOAuthClientRegistrationRequired:         {code: codes.FailedPrecondition, reason: ReasonOAuthClientRegistrationRequired, message: "OAuth client registration is required"},
	publicationdomain.ErrorCodeApplicationProfileRequired:              {code: codes.FailedPrecondition, reason: ReasonApplicationProfileRequired, message: "application profile is required"},
	publicationdomain.ErrorCodeApplicationProfileStateInconsistent:     {code: codes.Internal, reason: ReasonApplicationProfileStateInconsistent, message: "application profile state is inconsistent"},
	publicationdomain.ErrorCodeStablePublicationRequiredByGrey:         {code: codes.FailedPrecondition, reason: ReasonStablePublicationRequiredByGrey, message: "stable publication is required by grey rollout"},
	publicationdomain.ErrorCodeGreyStableBaselineRequired:              {code: codes.FailedPrecondition, reason: ReasonGreyStableBaselineRequired, message: "grey rollout requires a stable baseline"},
	publicationdomain.ErrorCodeApplicationPublicationStateInconsistent: {code: codes.Internal, reason: ReasonApplicationPublicationStateInconsistent, message: "application publication state is inconsistent"},
	publicationdomain.ErrorCodeDeveloperIdentityRequired:               {code: codes.Unauthenticated, reason: ReasonDeveloperIdentityRequired, message: "developer identity is required"},
	publicationdomain.ErrorCodeDeveloperApprovalRequired:               {code: codes.PermissionDenied, reason: ReasonDeveloperApprovalRequired, message: "approved developer status is required"},
	publicationdomain.ErrorCodeApplicationVersionNotFound:              {code: codes.NotFound, reason: ReasonApplicationVersionNotFound, message: "application version not found"},
	publicationdomain.ErrorCodeApplicationAdminRequired:                {code: codes.PermissionDenied, reason: ReasonApplicationAdminRequired, message: "application administrator is required"},
	publicationdomain.ErrorCodeApplicationReviewStateInconsistent:      {code: codes.Aborted, reason: ReasonApplicationReviewStateInconsistent, message: "application review state is inconsistent"},
	publicationdomain.ErrorCodeInvalidApplicationScope:                 {code: codes.InvalidArgument, reason: ReasonInvalidApplicationScope, message: "application scope request is invalid"},
	publicationdomain.ErrorCodeScopeCatalogUnavailable:                 {code: codes.Unavailable, reason: ReasonScopeCatalogUnavailable, message: "scope catalog is unavailable"},
	publicationdomain.ErrorCodeApplicationLaunchURLNotReviewable:       {code: codes.InvalidArgument, reason: ReasonApplicationLaunchURLNotReviewable, message: "application launch URL is not reviewable"},
	publicationdomain.ErrorCodeLaunchURLInspectionUnavailable:          {code: codes.Unavailable, reason: ReasonLaunchURLInspectionUnavailable, message: "launch URL inspection is unavailable"},
	publicationdomain.ErrorCodeInternal:                                internalSpec,
}

var testerDomainErrorSpecs = map[testerdomain.ErrorCode]errorSpec{
	testerdomain.ErrorCodeApplicationTesterJoinLinkStateInconsistent: {code: codes.Internal, reason: ReasonApplicationTesterJoinLinkStateInconsistent, message: "application tester join link state is inconsistent"},
	testerdomain.ErrorCodeInvalidTesterMembershipId:                  {code: codes.InvalidArgument, reason: ReasonInvalidTesterMembershipId, message: "tester membership ID is invalid"},
	testerdomain.ErrorCodeApplicationTesterMembershipNotFound:        {code: codes.NotFound, reason: ReasonApplicationTesterMembershipNotFound, message: "application tester membership not found"},
	testerdomain.ErrorCodeApplicationTesterStateInconsistent:         {code: codes.Internal, reason: ReasonApplicationTesterStateInconsistent, message: "application tester state is inconsistent"},
	testerdomain.ErrorCodeAuthenticatedUserRequired:                  {code: codes.Unauthenticated, reason: ReasonAuthenticatedUserRequired, message: "authenticated user is required"},
	testerdomain.ErrorCodeInvalidTesterJoinSecret:                    {code: codes.InvalidArgument, reason: ReasonInvalidTesterJoinSecret, message: "tester join credential is invalid"},
	testerdomain.ErrorCodeTesterJoinLinkInvalid:                      {code: codes.NotFound, reason: ReasonTesterJoinLinkInvalid, message: "tester join link is invalid"},
	testerdomain.ErrorCodeApplicationTesterLimitReached:              {code: codes.Aborted, reason: ReasonApplicationTesterLimitReached, message: "application tester limit reached"},
	testerdomain.ErrorCodeInvalidTesterJoinLinkId:                    {code: codes.InvalidArgument, reason: ReasonInvalidTesterJoinLinkId, message: "tester join link ID is invalid"},
	testerdomain.ErrorCodeApplicationTesterJoinLinkAlreadyExists:     {code: codes.AlreadyExists, reason: ReasonApplicationTesterJoinLinkAlreadyExists, message: "active tester join link already exists"},
	testerdomain.ErrorCodeApplicationTesterJoinLinkNotFound:          {code: codes.NotFound, reason: ReasonApplicationTesterJoinLinkNotFound, message: "active tester join link not found"},
	testerdomain.ErrorCodeApplicationTesterJoinLinkChanged:           {code: codes.Aborted, reason: ReasonApplicationTesterJoinLinkChanged, message: "active tester join link has changed"},
	testerdomain.ErrorCodeDeveloperIdentityRequired:                  {code: codes.Unauthenticated, reason: ReasonDeveloperIdentityRequired, message: "developer identity is required"},
	testerdomain.ErrorCodeDeveloperApprovalRequired:                  {code: codes.PermissionDenied, reason: ReasonDeveloperApprovalRequired, message: "approved developer status is required"},
	testerdomain.ErrorCodeInvalidApplicationId:                       {code: codes.InvalidArgument, reason: ReasonInvalidApplicationID, message: "application ID is invalid"},
	testerdomain.ErrorCodeApplicationNotFound:                        {code: codes.NotFound, reason: ReasonApplicationNotFound, message: "application not found"},
	testerdomain.ErrorCodeApplicationAdminRequired:                   {code: codes.PermissionDenied, reason: ReasonApplicationAdminRequired, message: "application administrator is required"},
	testerdomain.ErrorCodeInternal:                                   internalSpec,
}

var catalogDomainErrorSpecs = map[catalogdomain.ErrorCode]errorSpec{
	catalogdomain.ErrorCodeAuthenticatedUserRequired:              {code: codes.Unauthenticated, reason: ReasonAuthenticatedUserRequired, message: "authenticated user is required"},
	catalogdomain.ErrorCodeInvalidApplicationID:                   {code: codes.InvalidArgument, reason: ReasonInvalidApplicationID, message: "application ID is invalid"},
	catalogdomain.ErrorCodeInvalidHostRPCAPIMajor:                 {code: codes.InvalidArgument, reason: ReasonInvalidHostRPCAPIMajor, message: "host RPC API major is invalid"},
	catalogdomain.ErrorCodeInvalidHostCapabilities:                {code: codes.InvalidArgument, reason: ReasonInvalidHostCapabilities, message: "host capabilities are invalid"},
	catalogdomain.ErrorCodeApplicationNotFound:                    {code: codes.NotFound, reason: ReasonApplicationNotFound, message: "application not found"},
	catalogdomain.ErrorCodeApplicationTesterRequired:              {code: codes.PermissionDenied, reason: ReasonApplicationTesterRequired, message: "active application tester membership is required"},
	catalogdomain.ErrorCodeApplicationTestTargetUnavailable:       {code: codes.NotFound, reason: ReasonApplicationTestTargetUnavailable, message: "application test target is unavailable"},
	catalogdomain.ErrorCodeHostCapabilitiesInsufficient:           {code: codes.FailedPrecondition, reason: ReasonHostCapabilitiesInsufficient, message: "host capabilities are insufficient"},
	catalogdomain.ErrorCodeApplicationTestPublicationInconsistent: {code: codes.Unavailable, reason: ReasonApplicationTestPublicationInconsistent, message: "application test publication is unavailable"},
	catalogdomain.ErrorCodeApplicationLaunchTargetUnavailable:     {code: codes.NotFound, reason: ReasonApplicationLaunchTargetUnavailable, message: "application launch target is unavailable"},
	catalogdomain.ErrorCodeApplicationRuntimeStateInconsistent:    {code: codes.Internal, reason: ReasonApplicationRuntimeStateInconsistent, message: "application runtime state is inconsistent"},
	catalogdomain.ErrorCodeInvalidPageSize:                        {code: codes.InvalidArgument, reason: ReasonInvalidPageSize, message: "page size is invalid"},
	catalogdomain.ErrorCodeInvalidPageToken:                       {code: codes.InvalidArgument, reason: ReasonInvalidPageToken, message: "page token is invalid"},
	catalogdomain.ErrorCodePublicApplicationNotFound:              {code: codes.NotFound, reason: ReasonPublicApplicationNotFound, message: "public application not found"},
	catalogdomain.ErrorCodeApplicationCatalogStateInconsistent:    {code: codes.Internal, reason: ReasonApplicationCatalogStateInconsistent, message: "application catalog state is inconsistent"},
	catalogdomain.ErrorCodeInternal:                               internalSpec,
}

var profileDomainErrorSpecs = map[profiledomain.ErrorCode]errorSpec{
	profiledomain.ErrorCodeReviewerIdentityRequired:                    {code: codes.Unauthenticated, reason: ReasonReviewerIdentityRequired, message: "reviewer identity is required"},
	profiledomain.ErrorCodeApplicationProfileReviewPermissionRequired:  {code: codes.PermissionDenied, reason: ReasonApplicationProfileReviewPermissionRequired, message: "profile review permission is required"},
	profiledomain.ErrorCodeApplicationProfileReviewConflictOfInterest:  {code: codes.PermissionDenied, reason: ReasonApplicationProfileReviewConflictOfInterest, message: "profile review has a conflict of interest"},
	profiledomain.ErrorCodeInvalidApplicationProfileReviewDecision:     {code: codes.InvalidArgument, reason: ReasonInvalidApplicationProfileReviewDecision, message: "profile review decision is invalid"},
	profiledomain.ErrorCodeApplicationProfileReviewNotFound:            {code: codes.NotFound, reason: ReasonApplicationProfileReviewNotFound, message: "profile review not found"},
	profiledomain.ErrorCodeApplicationProfileReviewAlreadyDecided:      {code: codes.Aborted, reason: ReasonApplicationProfileReviewAlreadyDecided, message: "profile review is already decided"},
	profiledomain.ErrorCodeApplicationProfileReviewStateConflict:       {code: codes.Aborted, reason: ReasonApplicationProfileReviewStateConflict, message: "profile review state conflicts"},
	profiledomain.ErrorCodeApplicationProfilePublicationConflict:       {code: codes.Aborted, reason: ReasonApplicationProfilePublicationConflict, message: "profile publication conflicts"},
	profiledomain.ErrorCodeApplicationProfileReviewStateInconsistent:   {code: codes.Internal, reason: ReasonApplicationProfileReviewStateInconsistent, message: "profile review state is inconsistent"},
	profiledomain.ErrorCodeProfileReviewPolicyUnavailable:              {code: codes.Aborted, reason: ReasonProfileReviewPolicyUnavailable, message: "profile review policy is unavailable"},
	profiledomain.ErrorCodeProfileReviewChecksIncomplete:               {code: codes.InvalidArgument, reason: ReasonProfileReviewChecksIncomplete, message: "profile review checks are incomplete"},
	profiledomain.ErrorCodeInvalidProfileReviewReason:                  {code: codes.InvalidArgument, reason: ReasonInvalidProfileReviewReason, message: "profile review reason is invalid"},
	profiledomain.ErrorCodeInvalidApplicationProfileReviewQuery:        {code: codes.InvalidArgument, reason: ReasonInvalidApplicationProfileReviewQuery, message: "profile review query is invalid"},
	profiledomain.ErrorCodeInvalidApplicationProfileReviewPageToken:    {code: codes.InvalidArgument, reason: ReasonInvalidApplicationProfileReviewPageToken, message: "profile review page token is invalid"},
	profiledomain.ErrorCodeInvalidApplicationProfileReviewSubmission:   {code: codes.InvalidArgument, reason: ReasonInvalidApplicationProfileReviewSubmission, message: "application profile review submission is invalid"},
	profiledomain.ErrorCodeInvalidApplicationProfileContent:            {code: codes.InvalidArgument, reason: ReasonInvalidApplicationProfileContent, message: "application profile content is invalid"},
	profiledomain.ErrorCodeInvalidApplicationProfileRevisionID:         {code: codes.InvalidArgument, reason: ReasonInvalidApplicationProfileRevisionID, message: "application profile revision ID is invalid"},
	profiledomain.ErrorCodeApplicationProfileExpectedRevisionRequired:  {code: codes.InvalidArgument, reason: ReasonApplicationProfileExpectedRevisionRequired, message: "application profile expected revision is required"},
	profiledomain.ErrorCodeApplicationProfileRevisionNotFound:          {code: codes.NotFound, reason: ReasonApplicationProfileRevisionNotFound, message: "application profile revision not found"},
	profiledomain.ErrorCodeApplicationProfileRevisionNotDraft:          {code: codes.Aborted, reason: ReasonApplicationProfileRevisionNotDraft, message: "application profile revision is not a draft"},
	profiledomain.ErrorCodeApplicationProfileRevisionConflict:          {code: codes.Aborted, reason: ReasonApplicationProfileRevisionConflict, message: "application profile revision conflicts"},
	profiledomain.ErrorCodeInvalidApplicationID:                        {code: codes.InvalidArgument, reason: ReasonInvalidApplicationID, message: "application ID is invalid"},
	profiledomain.ErrorCodeDeveloperIdentityRequired:                   {code: codes.Unauthenticated, reason: ReasonDeveloperIdentityRequired, message: "developer identity is required"},
	profiledomain.ErrorCodeDeveloperApprovalRequired:                   {code: codes.PermissionDenied, reason: ReasonDeveloperApprovalRequired, message: "approved developer status is required"},
	profiledomain.ErrorCodeInvalidApplicationDisplayName:               {code: codes.InvalidArgument, reason: ReasonInvalidApplicationDisplayName, message: "application display name is invalid"},
	profiledomain.ErrorCodeInvalidApplicationDescription:               {code: codes.InvalidArgument, reason: ReasonInvalidApplicationDescription, message: "application description is invalid"},
	profiledomain.ErrorCodeInvalidApplicationIcon:                      {code: codes.InvalidArgument, reason: ReasonInvalidApplicationIcon, message: "application icon is invalid"},
	profiledomain.ErrorCodeApplicationNotFound:                         {code: codes.NotFound, reason: ReasonApplicationNotFound, message: "application not found"},
	profiledomain.ErrorCodeApplicationAdminRequired:                    {code: codes.PermissionDenied, reason: ReasonApplicationAdminRequired, message: "application administrator is required"},
	profiledomain.ErrorCodeApplicationProfileWorkRevisionAlreadyExists: {code: codes.Aborted, reason: ReasonApplicationProfileWorkRevisionAlreadyExists, message: "application profile work revision already exists"},
	profiledomain.ErrorCodeApplicationProfileStateInconsistent:         {code: codes.Internal, reason: ReasonApplicationProfileStateInconsistent, message: "application profile state is inconsistent"},
	profiledomain.ErrorCodeInternal:                                    internalSpec,
}

func reviewErrorCode(err error) reviewdomain.ErrorCode {
	var domainError *reviewdomain.Error
	if errors.As(err, &domainError) {
		return domainError.Code()
	}
	return reviewdomain.ErrorCodeInternal
}

// toTransportError maps any error crossing the transport boundary. Unknown and
// infrastructure errors collapse to Internal without leaking cause or details.
func toTransportError(err error) error {
	if errors.Is(err, shared.ErrAccountExitBlocked) {
		return transportStatus(codes.FailedPrecondition, "ERROR_REASON_ACCOUNT_OWNER_EXIT_BLOCKED", "account lifecycle prevents new state")
	}
	if err == nil {
		return nil
	}
	if errors.Is(err, errIdentityRequired) {
		return transportStatus(codes.Unauthenticated, ReasonDeveloperIdentityRequired, "developer identity is required")
	}
	if errors.Is(err, errIdentityInvalid) {
		return transportStatus(codes.Unauthenticated, ReasonInvalidDeveloperIdentity, "developer identity is invalid")
	}
	var managementError *managementdomain.Error
	if errors.As(err, &managementError) {
		spec, ok := managementDomainErrorSpecs[managementError.Code()]
		if !ok {
			spec = internalSpec
		}
		return transportStatus(spec.code, spec.reason, spec.message)
	}
	var filterError *filterdomain.Error
	if errors.As(err, &filterError) {
		spec, ok := filterDomainErrorSpecs[filterError.Code()]
		if !ok {
			spec = internalSpec
		}
		return transportStatus(spec.code, spec.reason, spec.message)
	}
	var oauthClientError *oauthclientdomain.Error
	if errors.As(err, &oauthClientError) {
		spec, ok := oauthClientDomainErrorSpecs[oauthClientError.Code()]
		if !ok {
			spec = internalSpec
		}
		return transportStatus(spec.code, spec.reason, spec.message)
	}

	var profileError *profiledomain.Error
	if errors.As(err, &profileError) {
		spec, ok := profileDomainErrorSpecs[profileError.Code()]
		if !ok {
			spec = internalSpec
		}
		return transportStatus(spec.code, spec.reason, spec.message)
	}
	var catalogError *catalogdomain.Error
	if errors.As(err, &catalogError) {
		spec, ok := catalogDomainErrorSpecs[catalogError.Code()]
		if !ok {
			spec = internalSpec
		}
		if catalogError.Code() == catalogdomain.ErrorCodeHostCapabilitiesInsufficient {
			names, _ := json.Marshal(catalogError.MissingCapabilities())
			st, detailErr := status.New(spec.code, spec.message).WithDetails(&errdetails.ErrorInfo{Reason: spec.reason, Metadata: map[string]string{"missingCapabilities": string(names)}})
			if detailErr == nil {
				return st.Err()
			}
		}
		return transportStatus(spec.code, spec.reason, spec.message)
	}
	var testerDomainError *testerdomain.Error
	if errors.As(err, &testerDomainError) {
		spec, ok := testerDomainErrorSpecs[testerDomainError.Code()]
		if !ok {
			spec = internalSpec
		}
		return transportStatus(spec.code, spec.reason, spec.message)
	}
	var publicationDomainError *publicationdomain.Error
	if errors.As(err, &publicationDomainError) {
		spec, ok := publicationDomainErrorSpecs[publicationDomainError.Code()]
		if !ok {
			spec = internalSpec
		}
		return transportStatus(spec.code, spec.reason, spec.message)
	}
	var domainError *domain.Error
	if errors.As(err, &domainError) {
		spec, ok := domainErrorSpecs[domainError.Code()]
		if !ok {
			spec = internalSpec
		}
		return transportStatus(spec.code, spec.reason, spec.message)
	}
	var versionDomainError *versiondomain.Error
	if errors.As(err, &versionDomainError) {
		spec, ok := versionDomainErrorSpecs[versionDomainError.Code()]
		if !ok {
			spec = internalSpec
		}
		return transportStatus(spec.code, spec.reason, spec.message)
	}
	var reviewDomainError *reviewdomain.Error
	if errors.As(err, &reviewDomainError) {
		spec, ok := reviewDomainErrorSpecs[reviewDomainError.Code()]
		if !ok {
			spec = internalSpec
		}
		return transportStatus(spec.code, spec.reason, spec.message)
	}
	return transportStatus(internalSpec.code, internalSpec.reason, internalSpec.message)
}

func transportStatus(code codes.Code, reason, message string) error {
	st := status.New(code, message)
	withDetails, err := st.WithDetails(&errdetails.ErrorInfo{Reason: reason})
	if err != nil {
		return st.Err()
	}
	return withDetails.Err()
}
