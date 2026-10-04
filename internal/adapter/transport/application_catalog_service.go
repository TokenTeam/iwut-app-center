package transport

import (
	"context"
	"errors"
	"log/slog"

	applicationcatalogv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_catalog"
	filterv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_filter"
	kratostransport "github.com/go-kratos/kratos/v2/transport"

	catalogdomain "iwut-app-center/internal/catalog/domain"
	catalogusecase "iwut-app-center/internal/catalog/usecase"
	"iwut-app-center/internal/shared"
)

type PublicCatalogHandler interface {
	List(context.Context, shared.AuthenticatedUserIdentity, catalogusecase.ListPublicApplicationsQuery) (*catalogdomain.PublicApplicationCatalogPage, error)
	Get(context.Context, shared.AuthenticatedUserIdentity, catalogusecase.GetPublicApplicationQuery) (*catalogdomain.PublicApplicationCatalogItem, error)
}

type ApplicationCatalogService struct {
	applicationcatalogv1.UnimplementedApplicationCatalogServiceServer
	handler PublicCatalogHandler
}

func NewApplicationCatalogService(handler PublicCatalogHandler) *ApplicationCatalogService {
	return &ApplicationCatalogService{handler: handler}
}

func (s *ApplicationCatalogService) ListPublicApplications(ctx context.Context, request *applicationcatalogv1.ListPublicApplicationsRequest) (*applicationcatalogv1.PublicApplicationCatalogPage, error) {
	setPrivateNoStore(ctx)
	if s == nil || s.handler == nil || request == nil {
		return nil, toTransportError(catalogdomain.NewInternalError(nil))
	}
	query := request.GetQuery()
	runtime := query.GetRuntime()
	if len(request.ProtoReflect().GetUnknown()) != 0 || query == nil || len(query.ProtoReflect().GetUnknown()) != 0 || runtime == nil || len(runtime.ProtoReflect().GetUnknown()) != 0 {
		return nil, invalidApplicationCatalogRequest()
	}
	identity, _ := authenticatedUserIdentityFromContext(ctx)
	result, err := s.handler.List(ctx, identity, catalogusecase.ListPublicApplicationsQuery{Runtime: catalogusecase.CatalogRuntimeQuery{HostRPCAPIMajor: runtime.GetHostRpcApiMajor(), HostCapabilities: append([]string(nil), runtime.GetHostCapabilities()...)}, PageSize: query.GetPageSize(), PageToken: query.GetPageToken()})
	if err != nil {
		return nil, catalogTransportError(ctx, err)
	}
	items := make([]*applicationcatalogv1.PublicApplicationCatalogItem, 0, len(result.Items()))
	for _, item := range result.Items() {
		items = append(items, catalogItemResource(item))
	}
	return &applicationcatalogv1.PublicApplicationCatalogPage{Applications: items, NextPageToken: result.NextPageToken()}, nil
}

func (s *ApplicationCatalogService) GetPublicApplication(ctx context.Context, request *applicationcatalogv1.GetPublicApplicationRequest) (*applicationcatalogv1.PublicApplicationCatalogItem, error) {
	setPrivateNoStore(ctx)
	if s == nil || s.handler == nil || request == nil {
		return nil, toTransportError(catalogdomain.NewInternalError(nil))
	}
	query := request.GetQuery()
	if len(request.ProtoReflect().GetUnknown()) != 0 || query == nil || len(query.ProtoReflect().GetUnknown()) != 0 {
		return nil, invalidApplicationCatalogRequest()
	}
	identity, _ := authenticatedUserIdentityFromContext(ctx)
	result, err := s.handler.Get(ctx, identity, catalogusecase.GetPublicApplicationQuery{ApplicationID: request.GetApplicationId(), Runtime: catalogusecase.CatalogRuntimeQuery{HostRPCAPIMajor: query.GetHostRpcApiMajor(), HostCapabilities: append([]string(nil), query.GetHostCapabilities()...)}})
	if err != nil {
		return nil, catalogTransportError(ctx, err)
	}
	return catalogItemResource(result), nil
}

func setPrivateNoStore(ctx context.Context) {
	if transporter, ok := kratostransport.FromServerContext(ctx); ok {
		transporter.ReplyHeader().Set("Cache-Control", "private, no-store")
	}
}
func catalogTransportError(ctx context.Context, err error) error {
	if errors.Is(err, catalogdomain.ErrApplicationCatalogStateInconsistent) {
		slog.ErrorContext(ctx, "application catalog state invariant failed", "reason", ReasonApplicationCatalogStateInconsistent)
	}
	return toTransportError(err)
}

func catalogItemResource(item *catalogdomain.PublicApplicationCatalogItem) *applicationcatalogv1.PublicApplicationCatalogItem {
	if item == nil {
		return nil
	}
	profile := item.Profile()
	filter := item.Filter()
	return &applicationcatalogv1.PublicApplicationCatalogItem{ApplicationId: item.ApplicationID().String(), Profile: &applicationcatalogv1.PublicApplicationProfile{ProfileRevisionId: profile.RevisionID(), DisplayName: profile.DisplayName(), Description: profile.Description(), Icon: profile.Icon()}, LaunchTarget: launchTargetResource(item.LaunchTarget()), Filter: &applicationcatalogv1.PublicApplicationFilter{Revision: filter.Revision(), FilterRevisionId: filter.RevisionID(), SchemaVersion: filter.SchemaVersion(), Mode: catalogFilterMode(filter.Mode()), Rule: catalogFilterRule(filter.Rule())}}
}
func catalogFilterMode(mode string) filterv1.FilterMode {
	if mode == "ALLOW_ALL" {
		return filterv1.FilterMode_FILTER_MODE_ALLOW_ALL
	}
	if mode == "RULE" {
		return filterv1.FilterMode_FILTER_MODE_RULE
	}
	return filterv1.FilterMode_FILTER_MODE_UNSPECIFIED
}
func catalogFilterRule(rule *catalogdomain.PublicFilterRule) *filterv1.FilterRule {
	if rule == nil {
		return nil
	}
	if rule.Kind() == "GROUP" {
		children := rule.Children()
		mapped := make([]*filterv1.FilterRule, len(children))
		for i := range children {
			mapped[i] = catalogFilterRule(&children[i])
		}
		return &filterv1.FilterRule{Node: &filterv1.FilterRule_Group{Group: &filterv1.FilterGroup{Operator: catalogGroupOperator(rule.Operator()), Children: mapped}}}
	}
	value := rule.Value()
	var scalar *filterv1.ProfileScalar
	if value != nil {
		scalar = catalogScalar(*value)
	}
	return &filterv1.FilterRule{Node: &filterv1.FilterRule_Predicate{Predicate: &filterv1.FilterPredicate{FieldKey: rule.FieldKey(), Operator: catalogPredicateOperator(rule.Operator()), Value: scalar}}}
}
func catalogGroupOperator(value string) filterv1.FilterGroupOperator {
	switch value {
	case "ALL":
		return filterv1.FilterGroupOperator_FILTER_GROUP_OPERATOR_ALL
	case "ANY":
		return filterv1.FilterGroupOperator_FILTER_GROUP_OPERATOR_ANY
	case "NOT":
		return filterv1.FilterGroupOperator_FILTER_GROUP_OPERATOR_NOT
	}
	return 0
}
func catalogPredicateOperator(value string) filterv1.FilterPredicateOperator {
	switch value {
	case "EQ":
		return filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_EQ
	case "NE":
		return filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_NE
	case "LT":
		return filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_LT
	case "LTE":
		return filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_LTE
	case "GT":
		return filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_GT
	case "GTE":
		return filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_GTE
	case "EXISTS":
		return filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_EXISTS
	case "NOT_EXISTS":
		return filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_NOT_EXISTS
	}
	return 0
}
func catalogScalar(value catalogdomain.PublicFilterScalar) *filterv1.ProfileScalar {
	switch value.Kind() {
	case "STRING":
		return &filterv1.ProfileScalar{Value: &filterv1.ProfileScalar_StringValue{StringValue: value.StringValue()}}
	case "INTEGER":
		return &filterv1.ProfileScalar{Value: &filterv1.ProfileScalar_IntegerValue{IntegerValue: value.IntegerValue()}}
	case "BOOLEAN":
		return &filterv1.ProfileScalar{Value: &filterv1.ProfileScalar_BooleanValue{BooleanValue: value.BooleanValue()}}
	case "DATE":
		return &filterv1.ProfileScalar{Value: &filterv1.ProfileScalar_DateValue{DateValue: value.StringValue()}}
	}
	return nil
}
