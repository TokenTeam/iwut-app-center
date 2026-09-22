package transport

import (
	"errors"

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
	ReasonInvalidTesterJoinLinkId                = "ERROR_REASON_INVALID_TESTER_JOIN_LINK_ID"
	ReasonApplicationTesterJoinLinkAlreadyExists = "ERROR_REASON_APPLICATION_TESTER_JOIN_LINK_ALREADY_EXISTS"
	ReasonApplicationTesterJoinLinkNotFound      = "ERROR_REASON_APPLICATION_TESTER_JOIN_LINK_NOT_FOUND"
	ReasonApplicationTesterJoinLinkChanged       = "ERROR_REASON_APPLICATION_TESTER_JOIN_LINK_CHANGED"

	ReasonInvalidRpcApiMajor                     = "ERROR_REASON_INVALID_RPC_API_MAJOR"
	ReasonInvalidApplicationVersionId            = "ERROR_REASON_INVALID_APPLICATION_VERSION_ID"
	ReasonInvalidApplicationPublicationRevision  = "ERROR_REASON_INVALID_APPLICATION_PUBLICATION_REVISION"
	ReasonApplicationVersionNotApproved          = "ERROR_REASON_APPLICATION_VERSION_NOT_APPROVED"
	ReasonApplicationVersionRpcApiIncompatible   = "ERROR_REASON_APPLICATION_VERSION_RPC_API_INCOMPATIBLE"
	ReasonApplicationPublicationAlreadyExists    = "ERROR_REASON_APPLICATION_PUBLICATION_ALREADY_EXISTS"
	ReasonApplicationPublicationNotFound         = "ERROR_REASON_APPLICATION_PUBLICATION_NOT_FOUND"
	ReasonApplicationPublicationRevisionConflict = "ERROR_REASON_APPLICATION_PUBLICATION_REVISION_CONFLICT"

	ReasonInvalidApplicationName                = "ERROR_REASON_INVALID_APPLICATION_NAME"
	ReasonDeveloperIdentityRequired             = "ERROR_REASON_DEVELOPER_IDENTITY_REQUIRED"
	ReasonInvalidDeveloperIdentity              = "ERROR_REASON_INVALID_DEVELOPER_IDENTITY"
	ReasonDeveloperApprovalRequired             = "ERROR_REASON_DEVELOPER_APPROVAL_REQUIRED"
	ReasonApplicationNameAlreadyExists          = "ERROR_REASON_APPLICATION_NAME_ALREADY_EXISTS"
	ReasonApplicationQuotaExceeded              = "ERROR_REASON_APPLICATION_QUOTA_EXCEEDED"
	ReasonInvalidApplicationID                  = "ERROR_REASON_INVALID_APPLICATION_ID"
	ReasonInvalidVersionLabel                   = "ERROR_REASON_INVALID_VERSION_LABEL"
	ReasonInvalidApplicationLaunchURL           = "ERROR_REASON_INVALID_APPLICATION_LAUNCH_URL"
	ReasonInvalidRPCApiRange                    = "ERROR_REASON_INVALID_RPC_API_RANGE"
	ReasonInvalidRequiredCapability             = "ERROR_REASON_INVALID_REQUIRED_CAPABILITY"
	ReasonInvalidApplicationScope               = "ERROR_REASON_INVALID_APPLICATION_SCOPE"
	ReasonApplicationNotFound                   = "ERROR_REASON_APPLICATION_NOT_FOUND"
	ReasonApplicationAdminRequired              = "ERROR_REASON_APPLICATION_ADMIN_REQUIRED"
	ReasonApplicationVersionLabelExists         = "ERROR_REASON_APPLICATION_VERSION_LABEL_ALREADY_EXISTS"
	ReasonScopeCatalogUnavailable               = "ERROR_REASON_SCOPE_CATALOG_UNAVAILABLE"
	ReasonApplicationVersionRevisionRequired    = "ERROR_REASON_APPLICATION_VERSION_REVISION_REQUIRED"
	ReasonApplicationVersionNotFound            = "ERROR_REASON_APPLICATION_VERSION_NOT_FOUND"
	ReasonApplicationVersionNotDraft            = "ERROR_REASON_APPLICATION_VERSION_NOT_DRAFT"
	ReasonApplicationVersionNotRejected         = "ERROR_REASON_APPLICATION_VERSION_NOT_REJECTED"
	ReasonApplicationVersionRevisionConflict    = "ERROR_REASON_APPLICATION_VERSION_REVISION_CONFLICT"
	ReasonApplicationReviewNotFound             = "ERROR_REASON_APPLICATION_REVIEW_NOT_FOUND"
	ReasonApplicationReviewNotLatest            = "ERROR_REASON_APPLICATION_REVIEW_NOT_LATEST"
	ReasonApplicationReviewAlreadyRestored      = "ERROR_REASON_APPLICATION_REVIEW_ALREADY_RESTORED"
	ReasonApplicationReviewStateInconsistent    = "ERROR_REASON_APPLICATION_REVIEW_STATE_INCONSISTENT"
	ReasonApplicationLaunchURLNotReviewable     = "ERROR_REASON_APPLICATION_LAUNCH_URL_NOT_REVIEWABLE"
	ReasonLaunchURLInspectionUnavailable        = "ERROR_REASON_LAUNCH_URL_INSPECTION_UNAVAILABLE"
	ReasonReviewerIdentityRequired              = "ERROR_REASON_REVIEWER_IDENTITY_REQUIRED"
	ReasonInvalidReviewerIdentity               = "ERROR_REASON_INVALID_REVIEWER_IDENTITY"
	ReasonApplicationReviewPermissionRequired   = "ERROR_REASON_APPLICATION_REVIEW_PERMISSION_REQUIRED"
	ReasonApplicationReviewAlreadyDecided       = "ERROR_REASON_APPLICATION_REVIEW_ALREADY_DECIDED"
	ReasonApplicationReviewConflictOfInterest   = "ERROR_REASON_APPLICATION_REVIEW_CONFLICT_OF_INTEREST"
	ReasonInvalidApplicationReviewOutcome       = "ERROR_REASON_INVALID_APPLICATION_REVIEW_OUTCOME"
	ReasonInvalidApplicationReviewPolicyVersion = "ERROR_REASON_INVALID_APPLICATION_REVIEW_POLICY_VERSION"
	ReasonApplicationReviewPolicyChanged        = "ERROR_REASON_APPLICATION_REVIEW_POLICY_CHANGED"
	ReasonApplicationReviewChecksIncomplete     = "ERROR_REASON_APPLICATION_REVIEW_CHECKS_INCOMPLETE"
	ReasonInvalidApplicationReviewChecks        = "ERROR_REASON_INVALID_APPLICATION_REVIEW_CHECKS"
	ReasonInvalidApplicationReviewReason        = "ERROR_REASON_INVALID_APPLICATION_REVIEW_REASON"
	ReasonDeveloperStatusUnavailable            = "ERROR_REASON_DEVELOPER_STATUS_UNAVAILABLE"
	ReasonSystemPrincipalUnavailable            = "ERROR_REASON_SYSTEM_PRINCIPAL_UNAVAILABLE"
	ReasonInternal                              = "ERROR_REASON_INTERNAL"
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

var versionDomainErrorSpecs = map[versiondomain.ErrorCode]errorSpec{
	versiondomain.ErrorCodeInvalidApplicationID:                 {code: codes.InvalidArgument, reason: ReasonInvalidApplicationID, message: "application ID is invalid"},
	versiondomain.ErrorCodeInvalidVersionLabel:                  {code: codes.InvalidArgument, reason: ReasonInvalidVersionLabel, message: "version label is invalid"},
	versiondomain.ErrorCodeInvalidApplicationLaunchURL:          {code: codes.InvalidArgument, reason: ReasonInvalidApplicationLaunchURL, message: "application launch URL is invalid"},
	versiondomain.ErrorCodeInvalidRPCApiRange:                   {code: codes.InvalidArgument, reason: ReasonInvalidRPCApiRange, message: "RPC API range is invalid"},
	versiondomain.ErrorCodeInvalidRequiredCapability:            {code: codes.InvalidArgument, reason: ReasonInvalidRequiredCapability, message: "required capability is invalid"},
	versiondomain.ErrorCodeInvalidApplicationScope:              {code: codes.InvalidArgument, reason: ReasonInvalidApplicationScope, message: "application scope request is invalid"},
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
	reviewdomain.ErrorCodeDeveloperIdentityRequired:             {code: codes.Unauthenticated, reason: ReasonDeveloperIdentityRequired, message: "developer identity is required"},
	reviewdomain.ErrorCodeDeveloperApprovalRequired:             {code: codes.PermissionDenied, reason: ReasonDeveloperApprovalRequired, message: "approved developer status is required"},
	reviewdomain.ErrorCodeApplicationVersionRevisionRequired:    {code: codes.InvalidArgument, reason: ReasonApplicationVersionRevisionRequired, message: "application version revision is required"},
	reviewdomain.ErrorCodeApplicationVersionNotFound:            {code: codes.NotFound, reason: ReasonApplicationVersionNotFound, message: "application version not found"},
	reviewdomain.ErrorCodeApplicationAdminRequired:              {code: codes.PermissionDenied, reason: ReasonApplicationAdminRequired, message: "application administrator is required"},
	reviewdomain.ErrorCodeApplicationVersionNotDraft:            {code: codes.Aborted, reason: ReasonApplicationVersionNotDraft, message: "application version is not a draft"},
	reviewdomain.ErrorCodeApplicationVersionNotRejected:         {code: codes.Aborted, reason: ReasonApplicationVersionNotRejected, message: "application version is not rejected"},
	reviewdomain.ErrorCodeApplicationVersionRevisionConflict:    {code: codes.Aborted, reason: ReasonApplicationVersionRevisionConflict, message: "application version revision conflicts"},
	reviewdomain.ErrorCodeApplicationReviewNotFound:             {code: codes.NotFound, reason: ReasonApplicationReviewNotFound, message: "application review not found"},
	reviewdomain.ErrorCodeApplicationReviewNotLatest:            {code: codes.Aborted, reason: ReasonApplicationReviewNotLatest, message: "application review is not the latest attempt"},
	reviewdomain.ErrorCodeApplicationReviewAlreadyRestored:      {code: codes.Aborted, reason: ReasonApplicationReviewAlreadyRestored, message: "application review is already restored"},
	reviewdomain.ErrorCodeApplicationReviewStateInconsistent:    {code: codes.Aborted, reason: ReasonApplicationReviewStateInconsistent, message: "application review state is inconsistent"},
	reviewdomain.ErrorCodeApplicationLaunchURLNotReviewable:     {code: codes.InvalidArgument, reason: ReasonApplicationLaunchURLNotReviewable, message: "application launch URL is not reviewable"},
	reviewdomain.ErrorCodeLaunchURLInspectionUnavailable:        {code: codes.Unavailable, reason: ReasonLaunchURLInspectionUnavailable, message: "launch URL inspection is unavailable"},
	reviewdomain.ErrorCodeInvalidApplicationScope:               {code: codes.InvalidArgument, reason: ReasonInvalidApplicationScope, message: "application scope request is invalid"},
	reviewdomain.ErrorCodeScopeCatalogUnavailable:               {code: codes.Unavailable, reason: ReasonScopeCatalogUnavailable, message: "scope catalog is unavailable"},
	reviewdomain.ErrorCodeReviewerIdentityRequired:              {code: codes.Unauthenticated, reason: ReasonReviewerIdentityRequired, message: "reviewer identity is required"},
	reviewdomain.ErrorCodeApplicationReviewPermissionRequired:   {code: codes.PermissionDenied, reason: ReasonApplicationReviewPermissionRequired, message: "application version review permission is required"},
	reviewdomain.ErrorCodeApplicationReviewAlreadyDecided:       {code: codes.Aborted, reason: ReasonApplicationReviewAlreadyDecided, message: "application review is already decided"},
	reviewdomain.ErrorCodeApplicationReviewConflictOfInterest:   {code: codes.PermissionDenied, reason: ReasonApplicationReviewConflictOfInterest, message: "reviewer has a conflict of interest"},
	reviewdomain.ErrorCodeInvalidApplicationReviewOutcome:       {code: codes.InvalidArgument, reason: ReasonInvalidApplicationReviewOutcome, message: "application review outcome is invalid"},
	reviewdomain.ErrorCodeInvalidApplicationReviewPolicyVersion: {code: codes.InvalidArgument, reason: ReasonInvalidApplicationReviewPolicyVersion, message: "application review policy version is invalid"},
	reviewdomain.ErrorCodeApplicationReviewPolicyChanged:        {code: codes.Aborted, reason: ReasonApplicationReviewPolicyChanged, message: "application review policy is no longer usable"},
	reviewdomain.ErrorCodeApplicationReviewChecksIncomplete:     {code: codes.InvalidArgument, reason: ReasonApplicationReviewChecksIncomplete, message: "application review confirmation is incomplete"},
	reviewdomain.ErrorCodeInvalidApplicationReviewChecks:        {code: codes.InvalidArgument, reason: ReasonInvalidApplicationReviewChecks, message: "application review confirmation is invalid"},
	reviewdomain.ErrorCodeInvalidApplicationReviewReason:        {code: codes.InvalidArgument, reason: ReasonInvalidApplicationReviewReason, message: "application review reason is invalid"},
	reviewdomain.ErrorCodeDeveloperStatusUnavailable:            {code: codes.Unavailable, reason: ReasonDeveloperStatusUnavailable, message: "developer status is unavailable"},
	reviewdomain.ErrorCodeSystemPrincipalUnavailable:            {code: codes.Unavailable, reason: ReasonSystemPrincipalUnavailable, message: "system principal is unavailable"},
	reviewdomain.ErrorCodeInternal:                              internalSpec,
}

var publicationDomainErrorSpecs = map[publicationdomain.ErrorCode]errorSpec{
	publicationdomain.ErrorCodeInvalidRpcApiMajor:                     {code: codes.InvalidArgument, reason: ReasonInvalidRpcApiMajor, message: "RPC API major is invalid"},
	publicationdomain.ErrorCodeInvalidApplicationVersionId:            {code: codes.InvalidArgument, reason: ReasonInvalidApplicationVersionId, message: "application version ID is invalid"},
	publicationdomain.ErrorCodeInvalidApplicationPublicationRevision:  {code: codes.InvalidArgument, reason: ReasonInvalidApplicationPublicationRevision, message: "publication revision is invalid"},
	publicationdomain.ErrorCodeApplicationVersionNotApproved:          {code: codes.FailedPrecondition, reason: ReasonApplicationVersionNotApproved, message: "application version is not approved"},
	publicationdomain.ErrorCodeApplicationVersionRpcApiIncompatible:   {code: codes.InvalidArgument, reason: ReasonApplicationVersionRpcApiIncompatible, message: "application version RPC API range is incompatible"},
	publicationdomain.ErrorCodeApplicationPublicationAlreadyExists:    {code: codes.AlreadyExists, reason: ReasonApplicationPublicationAlreadyExists, message: "application publication already exists"},
	publicationdomain.ErrorCodeApplicationPublicationNotFound:         {code: codes.NotFound, reason: ReasonApplicationPublicationNotFound, message: "application publication not found"},
	publicationdomain.ErrorCodeApplicationPublicationRevisionConflict: {code: codes.Aborted, reason: ReasonApplicationPublicationRevisionConflict, message: "publication revision conflicts"},
	publicationdomain.ErrorCodeDeveloperIdentityRequired:              {code: codes.Unauthenticated, reason: ReasonDeveloperIdentityRequired, message: "developer identity is required"},
	publicationdomain.ErrorCodeDeveloperApprovalRequired:              {code: codes.PermissionDenied, reason: ReasonDeveloperApprovalRequired, message: "approved developer status is required"},
	publicationdomain.ErrorCodeApplicationVersionNotFound:             {code: codes.NotFound, reason: ReasonApplicationVersionNotFound, message: "application version not found"},
	publicationdomain.ErrorCodeApplicationAdminRequired:               {code: codes.PermissionDenied, reason: ReasonApplicationAdminRequired, message: "application administrator is required"},
	publicationdomain.ErrorCodeApplicationReviewStateInconsistent:     {code: codes.Aborted, reason: ReasonApplicationReviewStateInconsistent, message: "application review state is inconsistent"},
	publicationdomain.ErrorCodeInvalidApplicationScope:                {code: codes.InvalidArgument, reason: ReasonInvalidApplicationScope, message: "application scope request is invalid"},
	publicationdomain.ErrorCodeScopeCatalogUnavailable:                {code: codes.Unavailable, reason: ReasonScopeCatalogUnavailable, message: "scope catalog is unavailable"},
	publicationdomain.ErrorCodeApplicationLaunchURLNotReviewable:      {code: codes.InvalidArgument, reason: ReasonApplicationLaunchURLNotReviewable, message: "application launch URL is not reviewable"},
	publicationdomain.ErrorCodeLaunchURLInspectionUnavailable:         {code: codes.Unavailable, reason: ReasonLaunchURLInspectionUnavailable, message: "launch URL inspection is unavailable"},
	publicationdomain.ErrorCodeInternal:                               internalSpec,
}

var testerDomainErrorSpecs = map[testerdomain.ErrorCode]errorSpec{
	testerdomain.ErrorCodeInvalidTesterJoinLinkId:                {code: codes.InvalidArgument, reason: ReasonInvalidTesterJoinLinkId, message: "tester join link ID is invalid"},
	testerdomain.ErrorCodeApplicationTesterJoinLinkAlreadyExists: {code: codes.AlreadyExists, reason: ReasonApplicationTesterJoinLinkAlreadyExists, message: "active tester join link already exists"},
	testerdomain.ErrorCodeApplicationTesterJoinLinkNotFound:      {code: codes.NotFound, reason: ReasonApplicationTesterJoinLinkNotFound, message: "active tester join link not found"},
	testerdomain.ErrorCodeApplicationTesterJoinLinkChanged:       {code: codes.Aborted, reason: ReasonApplicationTesterJoinLinkChanged, message: "active tester join link has changed"},
	testerdomain.ErrorCodeDeveloperIdentityRequired:              {code: codes.Unauthenticated, reason: ReasonDeveloperIdentityRequired, message: "developer identity is required"},
	testerdomain.ErrorCodeDeveloperApprovalRequired:              {code: codes.PermissionDenied, reason: ReasonDeveloperApprovalRequired, message: "approved developer status is required"},
	testerdomain.ErrorCodeInvalidApplicationId:                   {code: codes.InvalidArgument, reason: ReasonInvalidApplicationID, message: "application ID is invalid"},
	testerdomain.ErrorCodeApplicationNotFound:                    {code: codes.NotFound, reason: ReasonApplicationNotFound, message: "application not found"},
	testerdomain.ErrorCodeApplicationAdminRequired:               {code: codes.PermissionDenied, reason: ReasonApplicationAdminRequired, message: "application administrator is required"},
	testerdomain.ErrorCodeInternal:                               internalSpec,
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
	if err == nil {
		return nil
	}
	if errors.Is(err, errIdentityRequired) {
		return transportStatus(codes.Unauthenticated, ReasonDeveloperIdentityRequired, "developer identity is required")
	}
	if errors.Is(err, errIdentityInvalid) {
		return transportStatus(codes.Unauthenticated, ReasonInvalidDeveloperIdentity, "developer identity is invalid")
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
