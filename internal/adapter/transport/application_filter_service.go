package transport

import (
	"context"
	"errors"
	"log/slog"

	filterv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_filter"
	"google.golang.org/protobuf/types/known/timestamppb"

	filterdomain "iwut-app-center/internal/filter/domain"
	filterusecase "iwut-app-center/internal/filter/usecase"
	"iwut-app-center/internal/shared"
)

type ApplicationFilterHandlers interface {
	Get(context.Context, shared.DeveloperIdentity, string) (*filterdomain.ApplicationFilter, error)
	Set(context.Context, shared.DeveloperIdentity, string, filterusecase.SetCommand) (*filterdomain.ChangeResult, error)
	Clear(context.Context, shared.DeveloperIdentity, string, filterusecase.ClearCommand) (*filterdomain.ChangeResult, error)
}

type ApplicationFilterService struct {
	filterv1.UnimplementedApplicationFilterServiceServer
	handlers ApplicationFilterHandlers
}

var _ filterv1.ApplicationFilterServiceServer = (*ApplicationFilterService)(nil)
var _ filterv1.ApplicationFilterServiceHTTPServer = (*ApplicationFilterService)(nil)

func NewApplicationFilterService(handlers ApplicationFilterHandlers) *ApplicationFilterService {
	return &ApplicationFilterService{handlers: handlers}
}

func filterInvalidApplicationFilter() error { return filterdomain.ErrInvalidApplicationFilter }
func filterInternalError() error            { return filterdomain.NewInternalError(nil) }

func (s *ApplicationFilterService) GetApplicationFilter(ctx context.Context, request *filterv1.GetApplicationFilterRequest) (*filterv1.GetApplicationFilterResponse, error) {
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(filterdomain.ErrDeveloperIdentityRequired)
	}
	if s == nil || s.handlers == nil || request == nil {
		return nil, toTransportError(filterdomain.NewInternalError(nil))
	}
	if len(request.ProtoReflect().GetUnknown()) != 0 {
		return nil, toTransportError(filterdomain.ErrInvalidApplicationFilter)
	}
	result, err := s.handlers.Get(ctx, identity, request.GetApplicationId())
	if err != nil {
		return nil, applicationFilterTransportError(ctx, err)
	}
	resource, err := applicationFilterResource(result)
	if err != nil {
		return nil, applicationFilterTransportError(ctx, err)
	}
	return &filterv1.GetApplicationFilterResponse{Filter: resource}, nil
}

func (s *ApplicationFilterService) SetApplicationFilter(ctx context.Context, request *filterv1.SetApplicationFilterRequest) (*filterv1.SetApplicationFilterResponse, error) {
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(filterdomain.ErrDeveloperIdentityRequired)
	}
	if s == nil || s.handlers == nil || request == nil || request.GetCommand() == nil || len(request.ProtoReflect().GetUnknown()) != 0 || len(request.GetCommand().ProtoReflect().GetUnknown()) != 0 {
		return nil, toTransportError(filterdomain.ErrInvalidApplicationFilter)
	}
	rule, err := applicationFilterRuleFromProto(request.GetCommand().GetRule())
	if err != nil {
		return nil, toTransportError(err)
	}
	result, err := s.handlers.Set(ctx, identity, request.GetApplicationId(), filterusecase.SetCommand{ExpectedRevision: request.GetCommand().GetExpectedRevision(), Rule: rule})
	if err != nil {
		return nil, applicationFilterTransportError(ctx, err)
	}
	if result == nil || result.Filter() == nil {
		return nil, toTransportError(filterdomain.NewInternalError(nil))
	}
	resource, err := applicationFilterResource(result.Filter())
	if err != nil {
		return nil, applicationFilterTransportError(ctx, err)
	}
	return &filterv1.SetApplicationFilterResponse{Changed: result.Changed(), Filter: resource}, nil
}

func (s *ApplicationFilterService) ClearApplicationFilter(ctx context.Context, request *filterv1.ClearApplicationFilterRequest) (*filterv1.ClearApplicationFilterResponse, error) {
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(filterdomain.ErrDeveloperIdentityRequired)
	}
	if s == nil || s.handlers == nil || request == nil || len(request.ProtoReflect().GetUnknown()) != 0 {
		return nil, toTransportError(filterdomain.ErrInvalidApplicationFilter)
	}
	result, err := s.handlers.Clear(ctx, identity, request.GetApplicationId(), filterusecase.ClearCommand{ExpectedRevision: request.GetExpectedRevision()})
	if err != nil {
		return nil, applicationFilterTransportError(ctx, err)
	}
	if result == nil || result.Filter() == nil {
		return nil, toTransportError(filterdomain.NewInternalError(nil))
	}
	resource, err := applicationFilterResource(result.Filter())
	if err != nil {
		return nil, applicationFilterTransportError(ctx, err)
	}
	return &filterv1.ClearApplicationFilterResponse{Changed: result.Changed(), Filter: resource}, nil
}

func applicationFilterTransportError(ctx context.Context, err error) error {
	if errors.Is(err, filterdomain.ErrApplicationFilterStateInconsistent) {
		slog.ErrorContext(ctx, "application filter state invariant failed", "reason", ReasonApplicationFilterStateInconsistent)
	}
	return toTransportError(err)
}

func applicationFilterRuleFromProto(value *filterv1.FilterRule) (filterdomain.Rule, error) {
	if value == nil || len(value.ProtoReflect().GetUnknown()) != 0 {
		return filterdomain.Rule{}, filterdomain.ErrInvalidApplicationFilter
	}
	switch node := value.GetNode().(type) {
	case *filterv1.FilterRule_Group:
		if node.Group == nil || len(node.Group.ProtoReflect().GetUnknown()) != 0 {
			return filterdomain.Rule{}, filterdomain.ErrInvalidApplicationFilter
		}
		operator := filterdomain.GroupOperator("")
		switch node.Group.GetOperator() {
		case filterv1.FilterGroupOperator_FILTER_GROUP_OPERATOR_ALL:
			operator = filterdomain.GroupOperatorAll
		case filterv1.FilterGroupOperator_FILTER_GROUP_OPERATOR_ANY:
			operator = filterdomain.GroupOperatorAny
		case filterv1.FilterGroupOperator_FILTER_GROUP_OPERATOR_NOT:
			operator = filterdomain.GroupOperatorNot
		default:
			return filterdomain.Rule{}, filterdomain.ErrInvalidApplicationFilter
		}
		children := make([]filterdomain.Rule, len(node.Group.GetChildren()))
		for i, child := range node.Group.GetChildren() {
			converted, err := applicationFilterRuleFromProto(child)
			if err != nil {
				return filterdomain.Rule{}, err
			}
			children[i] = converted
		}
		return filterdomain.NewGroup(operator, children)
	case *filterv1.FilterRule_Predicate:
		if node.Predicate == nil || len(node.Predicate.ProtoReflect().GetUnknown()) != 0 {
			return filterdomain.Rule{}, filterdomain.ErrInvalidApplicationFilter
		}
		operator, ok := filterPredicateOperatorFromProto(node.Predicate.GetOperator())
		if !ok {
			return filterdomain.Rule{}, filterdomain.ErrInvalidApplicationFilter
		}
		var scalar *filterdomain.Scalar
		if node.Predicate.GetValue() != nil {
			converted, err := filterScalarFromProto(node.Predicate.GetValue())
			if err != nil {
				return filterdomain.Rule{}, err
			}
			scalar = &converted
		}
		return filterdomain.NewPredicate(node.Predicate.GetFieldKey(), operator, scalar)
	default:
		return filterdomain.Rule{}, filterdomain.ErrInvalidApplicationFilter
	}
}

func filterPredicateOperatorFromProto(value filterv1.FilterPredicateOperator) (filterdomain.PredicateOperator, bool) {
	switch value {
	case filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_EQ:
		return filterdomain.PredicateOperatorEQ, true
	case filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_NE:
		return filterdomain.PredicateOperatorNE, true
	case filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_LT:
		return filterdomain.PredicateOperatorLT, true
	case filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_LTE:
		return filterdomain.PredicateOperatorLTE, true
	case filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_GT:
		return filterdomain.PredicateOperatorGT, true
	case filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_GTE:
		return filterdomain.PredicateOperatorGTE, true
	case filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_EXISTS:
		return filterdomain.PredicateOperatorExists, true
	case filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_NOT_EXISTS:
		return filterdomain.PredicateOperatorNotExists, true
	default:
		return "", false
	}
}

func filterScalarFromProto(value *filterv1.ProfileScalar) (filterdomain.Scalar, error) {
	if value == nil || len(value.ProtoReflect().GetUnknown()) != 0 {
		return filterdomain.Scalar{}, filterdomain.ErrInvalidApplicationFilter
	}
	switch scalar := value.GetValue().(type) {
	case *filterv1.ProfileScalar_StringValue:
		return filterdomain.NewStringScalar(scalar.StringValue)
	case *filterv1.ProfileScalar_IntegerValue:
		return filterdomain.NewIntegerScalar(scalar.IntegerValue), nil
	case *filterv1.ProfileScalar_BooleanValue:
		return filterdomain.NewBooleanScalar(scalar.BooleanValue), nil
	case *filterv1.ProfileScalar_DateValue:
		return filterdomain.NewDateScalar(scalar.DateValue)
	default:
		return filterdomain.Scalar{}, filterdomain.ErrInvalidApplicationFilter
	}
}

func applicationFilterResource(value *filterdomain.ApplicationFilter) (*filterv1.ApplicationFilterResource, error) {
	if value == nil || !value.ApplicationID().IsValid() || value.Revision() < 0 {
		return nil, filterdomain.NewStateInconsistentError(nil)
	}
	result := &filterv1.ApplicationFilterResource{ApplicationId: value.ApplicationID().String(), Revision: value.Revision(), EffectiveMode: filterModeToProto(value.EffectiveMode())}
	if current := value.CurrentRevision(); current != nil {
		resource := &filterv1.ApplicationFilterRevisionResource{FilterRevisionId: current.ID().String(), Sequence: current.Sequence(), SchemaVersion: filterdomain.SchemaVersion, Mode: filterModeToProto(current.Mode()), PublishedBy: current.PublishedBy().String(), PublishedAt: timestamppb.New(current.PublishedAt())}
		if rule := current.Rule(); rule != nil {
			resource.Rule = applicationFilterRuleToProto(*rule)
		}
		if resource.Mode == filterv1.FilterMode_FILTER_MODE_UNSPECIFIED || resource.PublishedAt.CheckValid() != nil {
			return nil, filterdomain.NewStateInconsistentError(nil)
		}
		result.CurrentRevision = resource
	}
	return result, nil
}

func filterModeToProto(value filterdomain.RevisionMode) filterv1.FilterMode {
	switch value {
	case filterdomain.RevisionModeAllowAll:
		return filterv1.FilterMode_FILTER_MODE_ALLOW_ALL
	case filterdomain.RevisionModeRule:
		return filterv1.FilterMode_FILTER_MODE_RULE
	default:
		return filterv1.FilterMode_FILTER_MODE_UNSPECIFIED
	}
}

func applicationFilterRuleToProto(value filterdomain.Rule) *filterv1.FilterRule {
	if operator, children, ok := value.Group(); ok {
		converted := make([]*filterv1.FilterRule, len(children))
		for i, child := range children {
			converted[i] = applicationFilterRuleToProto(child)
		}
		protoOperator := filterv1.FilterGroupOperator_FILTER_GROUP_OPERATOR_UNSPECIFIED
		switch operator {
		case filterdomain.GroupOperatorAll:
			protoOperator = filterv1.FilterGroupOperator_FILTER_GROUP_OPERATOR_ALL
		case filterdomain.GroupOperatorAny:
			protoOperator = filterv1.FilterGroupOperator_FILTER_GROUP_OPERATOR_ANY
		case filterdomain.GroupOperatorNot:
			protoOperator = filterv1.FilterGroupOperator_FILTER_GROUP_OPERATOR_NOT
		}
		return &filterv1.FilterRule{Node: &filterv1.FilterRule_Group{Group: &filterv1.FilterGroup{Operator: protoOperator, Children: converted}}}
	}
	field, operator, scalar, _ := value.Predicate()
	protoOperator := map[filterdomain.PredicateOperator]filterv1.FilterPredicateOperator{
		filterdomain.PredicateOperatorEQ:        filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_EQ,
		filterdomain.PredicateOperatorNE:        filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_NE,
		filterdomain.PredicateOperatorLT:        filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_LT,
		filterdomain.PredicateOperatorLTE:       filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_LTE,
		filterdomain.PredicateOperatorGT:        filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_GT,
		filterdomain.PredicateOperatorGTE:       filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_GTE,
		filterdomain.PredicateOperatorExists:    filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_EXISTS,
		filterdomain.PredicateOperatorNotExists: filterv1.FilterPredicateOperator_FILTER_PREDICATE_OPERATOR_NOT_EXISTS,
	}[operator]
	predicate := &filterv1.FilterPredicate{FieldKey: field, Operator: protoOperator}
	if scalar != nil {
		switch scalar.Kind() {
		case filterdomain.ScalarKindString:
			predicate.Value = &filterv1.ProfileScalar{Value: &filterv1.ProfileScalar_StringValue{StringValue: scalar.StringValue()}}
		case filterdomain.ScalarKindInteger:
			predicate.Value = &filterv1.ProfileScalar{Value: &filterv1.ProfileScalar_IntegerValue{IntegerValue: scalar.IntegerValue()}}
		case filterdomain.ScalarKindBoolean:
			predicate.Value = &filterv1.ProfileScalar{Value: &filterv1.ProfileScalar_BooleanValue{BooleanValue: scalar.BooleanValue()}}
		case filterdomain.ScalarKindDate:
			predicate.Value = &filterv1.ProfileScalar{Value: &filterv1.ProfileScalar_DateValue{DateValue: scalar.StringValue()}}
		}
	}
	return &filterv1.FilterRule{Node: &filterv1.FilterRule_Predicate{Predicate: predicate}}
}
