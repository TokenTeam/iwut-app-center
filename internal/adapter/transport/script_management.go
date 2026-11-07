package transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	publicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_publication"
	resourcev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_resource"
	reviewv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_review"
	versionv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_version"
	kerrors "github.com/go-kratos/kratos/v2/errors"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"iwut-app-center/internal/script/domain"
	scriptuc "iwut-app-center/internal/script/usecase"
	"iwut-app-center/internal/shared"
)

const (
	createScriptResource  = resourcev1.OperationScriptResourceServiceCreateScriptResource
	replaceScriptResource = resourcev1.OperationScriptResourceServiceReplaceScriptResourceMetadata
	createScriptDraft     = versionv1.OperationScriptVersionServiceCreateScriptVersionDraft
	replaceScriptDraft    = versionv1.OperationScriptVersionServiceReplaceScriptVersionDraft
	submitScriptReview    = versionv1.OperationScriptVersionServiceSubmitScriptVersionReview
	// Every script byte can expand to a six-byte JSON escape.
	maxScriptJSONBytes = 6*domain.MaxContentBytes + 4096
)

type ScriptManagementService struct {
	resourcev1.UnimplementedScriptResourceServiceServer
	versionv1.UnimplementedScriptVersionServiceServer
	reviewv1.UnimplementedScriptReviewServiceServer
	publicationv1.UnimplementedScriptPublicationServiceServer
	resources    *scriptuc.ResourceHandlers
	drafts       *scriptuc.DraftHandlers
	submissions  *scriptuc.SubmitDraftHandler
	decisions    *scriptuc.DecideReviewHandler
	publications *scriptuc.PublicationHandler
}

func NewScriptManagementService(resources *scriptuc.ResourceHandlers, drafts *scriptuc.DraftHandlers, submissions *scriptuc.SubmitDraftHandler, decisions *scriptuc.DecideReviewHandler, publications *scriptuc.PublicationHandler) *ScriptManagementService {
	return &ScriptManagementService{resources: resources, drafts: drafts, submissions: submissions, decisions: decisions, publications: publications}
}

func scriptMaintainer(ctx context.Context, operation string) (shared.TrustedIdentity, error) {
	identity, _ := trustedIdentityFromContext(ctx)
	if err := scriptuc.RequireMaintainer(identity); err != nil {
		return shared.TrustedIdentity{}, scriptManagementError(operation, err)
	}
	return identity, nil
}

func (s *ScriptManagementService) CreateScriptResource(ctx context.Context, req *resourcev1.CreateScriptResourceRequest) (*resourcev1.ScriptResource, error) {
	identity, err := scriptMaintainer(ctx, createScriptResource)
	if err != nil {
		return nil, err
	}
	if req == nil || hasUnknownScriptFields(req) {
		return nil, invalidScriptRequest(createScriptResource)
	}
	resource, err := s.resources.Create(ctx, identity, scriptuc.CreateResourceCommand{Name: req.GetName(), Purpose: req.GetPurpose()})
	if err != nil {
		return nil, scriptManagementError(createScriptResource, err)
	}
	return scriptResourceResponse(resource), nil
}

func (s *ScriptManagementService) ReplaceScriptResourceMetadata(ctx context.Context, req *resourcev1.ReplaceScriptResourceMetadataRequest) (*resourcev1.ScriptResource, error) {
	identity, err := scriptMaintainer(ctx, replaceScriptResource)
	if err != nil {
		return nil, err
	}
	command := req.GetCommand()
	if command == nil || hasUnknownScriptFields(req) || hasUnknownScriptFields(command) {
		return nil, invalidScriptRequest(replaceScriptResource)
	}
	resource, err := s.resources.ReplaceMetadata(ctx, identity, scriptuc.ReplaceResourceMetadataCommand{
		ResourceID: req.GetResourceId(), ExpectedRevision: command.GetExpectedRevision(),
		Name: command.GetName(), Purpose: command.GetPurpose(),
	})
	if err != nil {
		return nil, scriptManagementError(replaceScriptResource, err)
	}
	return scriptResourceResponse(resource), nil
}

func (s *ScriptManagementService) CreateScriptVersionDraft(ctx context.Context, req *versionv1.CreateScriptVersionDraftRequest) (*versionv1.ScriptVersion, error) {
	identity, err := scriptMaintainer(ctx, createScriptDraft)
	if err != nil {
		return nil, err
	}
	draft := req.GetDraft()
	if draft == nil || hasUnknownScriptFields(req) || hasUnknownScriptFields(draft) {
		return nil, invalidScriptRequest(createScriptDraft)
	}
	version, err := s.drafts.Create(ctx, identity, scriptuc.CreateDraftCommand{
		ResourceID: req.GetResourceId(), Label: draft.GetVersionLabel(), Format: draft.GetContentFormat(), Text: draft.GetContentText(),
	})
	if err != nil {
		return nil, scriptManagementError(createScriptDraft, err)
	}
	return scriptVersionResponse(version), nil
}

func (s *ScriptManagementService) ReplaceScriptVersionDraft(ctx context.Context, req *versionv1.ReplaceScriptVersionDraftRequest) (*versionv1.ScriptVersion, error) {
	identity, err := scriptMaintainer(ctx, replaceScriptDraft)
	if err != nil {
		return nil, err
	}
	command := req.GetCommand()
	if command == nil || hasUnknownScriptFields(req) || hasUnknownScriptFields(command) {
		return nil, invalidScriptRequest(replaceScriptDraft)
	}
	version, err := s.drafts.Replace(ctx, identity, scriptuc.ReplaceDraftCommand{
		ResourceID: req.GetResourceId(), VersionID: req.GetVersionId(), ExpectedRevision: command.GetExpectedRevision(),
		Format: command.GetContentFormat(), Text: command.GetContentText(),
	})
	if err != nil {
		return nil, scriptManagementError(replaceScriptDraft, err)
	}
	return scriptVersionResponse(version), nil
}

func (s *ScriptManagementService) SubmitScriptVersionReview(ctx context.Context, req *versionv1.SubmitScriptVersionReviewRequest) (*versionv1.ScriptVersionReviewSubmission, error) {
	identity, err := scriptMaintainer(ctx, submitScriptReview)
	if err != nil {
		return nil, err
	}
	command := req.GetCommand()
	if command == nil || hasUnknownScriptFields(req) || hasUnknownScriptFields(command) {
		return nil, invalidScriptRequest(submitScriptReview)
	}
	result, err := s.submissions.Submit(ctx, identity, scriptuc.SubmitDraftCommand{
		ResourceID: req.GetResourceId(), VersionID: req.GetVersionId(), ExpectedRevision: command.GetExpectedRevision(),
	})
	if err != nil {
		return nil, scriptManagementError(submitScriptReview, err)
	}
	return &versionv1.ScriptVersionReviewSubmission{
		ReviewId: string(result.ReviewID), State: versionv1.ScriptVersionState_SCRIPT_VERSION_STATE_SUBMITTED,
		Revision: result.Revision, SubmittedAt: timestamppb.New(result.SubmittedAt), PolicyVersion: result.PolicyVersion,
	}, nil
}

func hasUnknownScriptFields(message proto.Message) bool {
	return len(message.ProtoReflect().GetUnknown()) != 0
}

func scriptResourceResponse(resource domain.Resource) *resourcev1.ScriptResource {
	return &resourcev1.ScriptResource{
		ResourceId: string(resource.ID), Name: resource.Metadata.Name, Purpose: resource.Metadata.Purpose,
		Revision: resource.Revision, CreatedAt: timestamppb.New(resource.CreatedAt), UpdatedAt: timestamppb.New(resource.UpdatedAt),
	}
}

func scriptVersionResponse(version domain.Version) *versionv1.ScriptVersion {
	return &versionv1.ScriptVersion{
		ResourceId: string(version.ResourceID), VersionId: string(version.ID), VersionLabel: version.Label,
		ContentFormat: version.Content.Format, ContentText: version.Content.Text, Digest: version.Content.Digest,
		State: versionv1.ScriptVersionState_SCRIPT_VERSION_STATE_DRAFT, Revision: version.Revision,
		CreatedAt: timestamppb.New(version.CreatedAt), UpdatedAt: timestamppb.New(version.UpdatedAt),
	}
}

func scriptManagementOperation(operation string) bool {
	if scriptPublicationAction(operation) != "" {
		return true
	}
	switch operation {
	case createScriptResource, replaceScriptResource, createScriptDraft, replaceScriptDraft, submitScriptReview, decideScriptReview:
		return true
	default:
		return false
	}
}

func scriptVersionOperation(operation string) bool {
	return operation == createScriptDraft || operation == replaceScriptDraft || operation == submitScriptReview
}

func scriptHTTPWriteOperation(r *http.Request) string {
	p := strings.Split(r.URL.Path, "/")
	if len(p) < 3 || p[1] != "v1" || p[2] != "script-resources" {
		return ""
	}
	switch {
	case r.Method == http.MethodPost && len(p) == 7 && p[3] != "" && p[4] == "versions" && p[5] != "" && p[6] == "publication":
		return publishScriptVersion
	case r.Method == http.MethodPost && len(p) == 7 && p[3] != "" && p[4] == "publication" && p[5] == "head" && p[6] == "clear":
		return clearScriptHead
	case r.Method == http.MethodPost && len(p) == 6 && p[3] != "" && p[4] == "distribution" && p[5] == "suspend":
		return suspendScriptDistribution
	case r.Method == http.MethodPost && len(p) == 6 && p[3] != "" && p[4] == "distribution" && p[5] == "restore":
		return restoreScriptDistribution
	case r.Method == http.MethodPost && len(p) == 9 && p[3] != "" && p[4] == "versions" && p[5] != "" && p[6] == "reviews" && p[7] != "" && p[8] == "decision":
		return decideScriptReview
	case r.Method == http.MethodPost && len(p) == 3:
		return createScriptResource
	case r.Method == http.MethodPut && len(p) == 5 && p[3] != "" && p[4] == "metadata":
		return replaceScriptResource
	case r.Method == http.MethodPost && len(p) == 5 && p[3] != "" && p[4] == "versions":
		return createScriptDraft
	case r.Method == http.MethodPut && len(p) == 7 && p[3] != "" && p[4] == "versions" && p[5] != "" && p[6] == "draft":
		return replaceScriptDraft
	case r.Method == http.MethodPost && len(p) == 7 && p[3] != "" && p[4] == "versions" && p[5] != "" && p[6] == "reviews":
		return submitScriptReview
	default:
		return ""
	}
}

func scriptManagementHTTPFilter(verifier *IdentityVerifier) khttp.FilterFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			operation := scriptHTTPWriteOperation(r)
			if operation == "" {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Cache-Control", "private, no-store")
			identity, err := verifyScriptHTTPIdentity(r, verifier)
			if err != nil {
				khttp.DefaultErrorEncoder(w, r, toIdentityTransportError(err, operation))
				return
			}
			authorize := scriptuc.RequireMaintainer
			if operation == decideScriptReview {
				authorize = scriptuc.RequireScriptReviewer
			}
			if action := scriptPublicationAction(operation); action != "" {
				authorize = func(identity shared.TrustedIdentity) error {
					return scriptuc.RequirePublicationPermission(identity, action)
				}
			}
			if err := authorize(identity); err != nil {
				khttp.DefaultErrorEncoder(w, r, scriptManagementError(operation, err))
				return
			}
			if !validScriptHTTPBody(w, r, operation) {
				khttp.DefaultErrorEncoder(w, r, invalidScriptRequest(operation))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func verifyScriptHTTPIdentity(r *http.Request, verifier *IdentityVerifier) (shared.TrustedIdentity, error) {
	for _, key := range legacyIdentityHeaders {
		for _, value := range r.Header.Values(key) {
			if strings.TrimSpace(value) != "" {
				return shared.TrustedIdentity{}, errIdentityInvalid
			}
		}
	}
	values := r.Header.Values(IdentityHeader)
	if len(values) == 0 || (len(values) == 1 && strings.TrimSpace(values[0]) == "") {
		return shared.TrustedIdentity{}, errIdentityRequired
	}
	if len(values) != 1 {
		return shared.TrustedIdentity{}, errIdentityInvalid
	}
	return verifier.Verify(strings.TrimSpace(values[0]))
}

func validScriptHTTPBody(w http.ResponseWriter, r *http.Request, operation string) bool {
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.Body == nil {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return false
	}
	maxBytes := int64(16 * 1024)
	if operation == createScriptDraft || operation == replaceScriptDraft {
		maxBytes = maxScriptJSONBytes
	}
	body := http.MaxBytesReader(w, r.Body, maxBytes)
	defer body.Close()
	raw, err := io.ReadAll(body)
	if err != nil || !utf8.Valid(raw) {
		return false
	}
	var input proto.Message
	switch operation {
	case publishScriptVersion, clearScriptHead:
		input = &publicationv1.ScriptPublicationRevisionInput{}
	case suspendScriptDistribution, restoreScriptDistribution:
		input = &publicationv1.ScriptAvailabilityInput{}
	case createScriptResource:
		input = &resourcev1.CreateScriptResourceRequest{}
	case replaceScriptResource:
		input = &resourcev1.ReplaceScriptResourceMetadataCommand{}
	case createScriptDraft:
		input = &versionv1.CreateScriptVersionDraftInput{}
	case replaceScriptDraft:
		input = &versionv1.ReplaceScriptVersionDraftInput{}
	case decideScriptReview:
		input = &reviewv1.DecideScriptVersionReviewInput{}
	case submitScriptReview:
		input = &versionv1.SubmitScriptVersionReviewInput{}
	}
	if err := protojson.Unmarshal(raw, input); err != nil {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return true
}

func scriptManagementHTTPError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	w.Header().Set("Cache-Control", "private, no-store")
	converted := kerrors.FromError(err)
	reasons := resourcev1.ErrorReason_value
	if scriptPublicationAction(operation) != "" {
		reasons = publicationv1.ErrorReason_value
	}
	if operation == decideScriptReview {
		reasons = reviewv1.ErrorReason_value
	}
	if scriptVersionOperation(operation) {
		reasons = versionv1.ErrorReason_value
	}
	if _, ok := reasons[converted.Reason]; !ok {
		if converted.Code == http.StatusBadRequest {
			err = invalidScriptRequest(operation)
		} else {
			err = scriptManagementError(operation, domain.ErrInternal)
		}
	}
	khttp.DefaultErrorEncoder(w, r, err)
}

func invalidScriptRequest(operation string) error {
	if scriptPublicationAction(operation) != "" {
		return scriptPublicationError(domain.ErrInvalidPublicationRequest)
	}
	if operation == decideScriptReview {
		return scriptDecisionError(domain.ErrInvalidDecision)
	}
	if scriptVersionOperation(operation) {
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_SCRIPT_VERSION_REQUEST", "invalid script version request")
	}
	return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_SCRIPT_RESOURCE_REQUEST", "invalid script resource request")
}

func scriptManagementError(operation string, err error) error {
	if scriptPublicationAction(operation) != "" {
		return scriptPublicationError(err)
	}
	if operation == decideScriptReview {
		return scriptDecisionError(err)
	}
	draft := scriptVersionOperation(operation)
	switch {
	case errors.Is(err, domain.ErrIdentityRequired):
		return transportStatus(codes.Unauthenticated, ReasonUserIdentityRequired, "user identity is required")
	case errors.Is(err, domain.ErrMaintainForbidden):
		if draft {
			return transportStatus(codes.PermissionDenied, "ERROR_REASON_SCRIPT_MAINTAIN_FORBIDDEN", "script maintenance permission is required")
		}
		return transportStatus(codes.PermissionDenied, "ERROR_REASON_SCRIPT_RESOURCE_FORBIDDEN", "script maintenance permission is required")
	case errors.Is(err, domain.ErrInvalidResourceID):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_SCRIPT_RESOURCE_ID", "invalid script resource id")
	case errors.Is(err, domain.ErrInvalidName):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_SCRIPT_RESOURCE_NAME", "invalid script resource name")
	case errors.Is(err, domain.ErrInvalidPurpose):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_SCRIPT_RESOURCE_PURPOSE", "invalid script resource purpose")
	case errors.Is(err, domain.ErrInvalidVersionID):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_SCRIPT_VERSION_ID", "invalid script version id")
	case errors.Is(err, domain.ErrInvalidVersionLabel):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_SCRIPT_VERSION_LABEL", "invalid script version label")
	case errors.Is(err, domain.ErrUnsupportedScriptFormat):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_UNSUPPORTED_SCRIPT_FORMAT", "unsupported script format")
	case errors.Is(err, domain.ErrInvalidScriptText):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_SCRIPT_TEXT", "invalid script text")
	case errors.Is(err, domain.ErrInvalidRevision):
		if draft {
			return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_SCRIPT_VERSION_REVISION", "invalid script version revision")
		}
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_SCRIPT_RESOURCE_REVISION", "invalid script resource revision")
	case errors.Is(err, domain.ErrNotFound):
		return transportStatus(codes.NotFound, "ERROR_REASON_SCRIPT_RESOURCE_NOT_FOUND", "script resource not found")
	case errors.Is(err, domain.ErrVersionNotFound):
		return transportStatus(codes.NotFound, "ERROR_REASON_SCRIPT_VERSION_NOT_FOUND", "script version not found")
	case errors.Is(err, domain.ErrRevisionConflict):
		if draft {
			return transportStatus(codes.Aborted, "ERROR_REASON_SCRIPT_VERSION_REVISION_CONFLICT", "script version revision conflict")
		}
		return transportStatus(codes.Aborted, "ERROR_REASON_SCRIPT_RESOURCE_REVISION_CONFLICT", "script resource revision conflict")
	case errors.Is(err, domain.ErrVersionLabelConflict):
		return transportStatus(codes.Aborted, "ERROR_REASON_SCRIPT_VERSION_LABEL_CONFLICT", "script version label already used")
	case errors.Is(err, domain.ErrVersionNotDraft):
		return transportStatus(codes.Aborted, "ERROR_REASON_SCRIPT_VERSION_NOT_DRAFT", "script version is not draft")
	case errors.Is(err, domain.ErrReviewPolicyUnavailable):
		return transportStatus(codes.Unavailable, "ERROR_REASON_SCRIPT_REVIEW_POLICY_UNAVAILABLE", "script review policy is unavailable")
	default:
		return transportStatus(codes.Internal, "ERROR_REASON_INTERNAL", "script management operation failed")
	}
}
