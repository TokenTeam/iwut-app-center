package transport

import (
	"context"

	managementv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_management"
	"google.golang.org/protobuf/types/known/timestamppb"

	managementdomain "iwut-app-center/internal/management/domain"
	managementusecase "iwut-app-center/internal/management/usecase"
	"iwut-app-center/internal/shared"
)

type ApplicationManagementQueryHandler interface {
	List(context.Context, shared.AuthenticatedUserIdentity, managementusecase.ListQuery) (*managementdomain.Page, error)
	Get(context.Context, shared.AuthenticatedUserIdentity, string) (*managementdomain.Detail, error)
}

type ApplicationManagementQueryService struct {
	managementv1.UnimplementedApplicationManagementQueryServiceServer
	handler ApplicationManagementQueryHandler
}

func NewApplicationManagementQueryService(handler ApplicationManagementQueryHandler) *ApplicationManagementQueryService {
	return &ApplicationManagementQueryService{handler: handler}
}

var _ managementv1.ApplicationManagementQueryServiceServer = (*ApplicationManagementQueryService)(nil)
var _ managementv1.ApplicationManagementQueryServiceHTTPServer = (*ApplicationManagementQueryService)(nil)

func (s *ApplicationManagementQueryService) ListMyApplications(ctx context.Context, request *managementv1.ListMyApplicationsRequest) (*managementv1.OwnedApplicationPage, error) {
	identity, ok := authenticatedUserIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(managementdomain.ErrAuthenticatedUserRequired)
	}
	if s == nil || s.handler == nil || request == nil {
		return nil, toTransportError(managementdomain.ErrInvalidRequest)
	}
	q := request.GetQuery()
	if q == nil {
		q = &managementv1.ListMyApplicationsQuery{}
	}
	page, err := s.handler.List(ctx, identity, managementusecase.ListQuery{LifecycleStatuses: append([]string(nil), q.GetLifecycleStatuses()...), PlatformAvailabilityStatuses: append([]string(nil), q.GetPlatformAvailabilityStatuses()...), PageSize: q.GetPageSize(), PageToken: q.GetPageToken()})
	if err != nil {
		return nil, toTransportError(err)
	}
	items := make([]*managementv1.OwnedApplicationSummary, len(page.Items))
	for i, item := range page.Items {
		items[i] = &managementv1.OwnedApplicationSummary{Application: managementCoreResponse(item.Application), Counts: managementCountsResponse(item.Counts)}
	}
	return &managementv1.OwnedApplicationPage{Items: items, NextPageToken: page.NextPageToken, AsOf: timestamppb.New(page.AsOf)}, nil
}
func (s *ApplicationManagementQueryService) GetMyApplication(ctx context.Context, request *managementv1.GetMyApplicationRequest) (*managementv1.OwnedApplicationManagementDetail, error) {
	identity, ok := authenticatedUserIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(managementdomain.ErrAuthenticatedUserRequired)
	}
	if s == nil || s.handler == nil || request == nil {
		return nil, toTransportError(managementdomain.ErrInvalidRequest)
	}
	d, err := s.handler.Get(ctx, identity, request.GetApplicationId())
	if err != nil {
		return nil, toTransportError(err)
	}
	pubs := make([]*managementv1.ApplicationPublicationSummary, len(d.Publications))
	for i, p := range d.Publications {
		pubs[i] = &managementv1.ApplicationPublicationSummary{RpcApiMajor: p.RPCAPIMajor, Revision: p.Revision, TestVersionId: p.TestVersionID, GreyVersionId: p.GreyVersionID, GreyRolloutPercent: p.GreyRolloutPercent, StableVersionId: p.StableVersionID}
	}
	oauth := make([]*managementv1.OAuthRegistrationSummary, len(d.OAuthRegistrations))
	for i, r := range d.OAuthRegistrations {
		oauth[i] = &managementv1.OAuthRegistrationSummary{Channel: r.Channel, RegistrationRevision: r.RegistrationRevision, PublicClient: managementOAuthResponse(r.PublicClient), ConfidentialClient: managementOAuthResponse(r.ConfidentialClient)}
	}
	result := &managementv1.OwnedApplicationManagementDetail{Application: managementCoreResponse(d.Application), Counts: managementCountsResponse(d.Counts), ProfileState: &managementv1.ApplicationProfileStateSummary{WorkingProfileRevisionId: d.ProfileState.WorkingProfileRevisionID, CurrentPublishedProfileRevisionId: d.ProfileState.CurrentPublishedProfileRevisionID}, Publications: pubs, FilterState: &managementv1.ApplicationFilterStateSummary{Revision: d.FilterState.Revision, FilterRevisionId: d.FilterState.FilterRevisionID, Sequence: d.FilterState.Sequence, SchemaVersion: d.FilterState.SchemaVersion, Mode: d.FilterState.Mode}, OauthRegistrations: oauth, AsOf: timestamppb.New(d.AsOf)}
	if d.PendingTransfer != nil {
		result.PendingTransfer = &managementv1.PendingApplicationTransferSummary{TransferId: d.PendingTransfer.TransferID, ToAdminId: d.PendingTransfer.ToAdminID, RequestedAt: timestamppb.New(d.PendingTransfer.RequestedAt), ExpiresAt: timestamppb.New(d.PendingTransfer.ExpiresAt)}
	}
	if d.Closure != nil {
		result.Closure = &managementv1.ApplicationClosureSummary{ClosureId: d.Closure.ClosureID, Status: d.Closure.Status, AuthRevocationState: d.Closure.AuthRevocationState, ClosingStartedAt: timestamppb.New(d.Closure.ClosingStartedAt)}
		if d.Closure.ClosedAt != nil {
			result.Closure.ClosedAt = timestamppb.New(*d.Closure.ClosedAt)
		}
	}
	return result, nil
}
func managementCoreResponse(v managementdomain.ApplicationCore) *managementv1.OwnedApplicationCore {
	return &managementv1.OwnedApplicationCore{ApplicationId: v.ApplicationID, Name: v.Name, AdminId: v.AdminID, CreatedAt: timestamppb.New(v.CreatedAt), OwnershipRevision: v.OwnershipRevision, LifecycleStatus: v.LifecycleStatus, LifecycleRevision: v.LifecycleRevision, PlatformAvailabilityStatus: v.PlatformAvailabilityStatus, PlatformAvailabilityRevision: v.PlatformAvailabilityRevision}
}
func managementCountsResponse(v managementdomain.Counts) *managementv1.ApplicationManagementCounts {
	return &managementv1.ApplicationManagementCounts{VersionCount: v.VersionCount, DraftVersionCount: v.DraftVersionCount, SubmittedVersionCount: v.SubmittedVersionCount, ApprovedVersionCount: v.ApprovedVersionCount, RejectedVersionCount: v.RejectedVersionCount, PendingVersionReviewCount: v.PendingVersionReviewCount, PendingProfileReviewCount: v.PendingProfileReviewCount, ActiveTesterCount: v.ActiveTesterCount}
}
func managementOAuthResponse(v *managementdomain.OAuthClient) *managementv1.OAuthClientIdentitySummary {
	if v == nil {
		return nil
	}
	out := &managementv1.OAuthClientIdentitySummary{ClientId: v.ClientID, Status: v.Status, AuthorizationEpoch: v.AuthorizationEpoch, CredentialRevision: v.CredentialRevision}
	if v.CredentialRotatedAt != nil {
		out.CredentialRotatedAt = timestamppb.New(*v.CredentialRotatedAt)
	}
	return out
}
