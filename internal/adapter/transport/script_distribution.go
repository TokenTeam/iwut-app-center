package transport

import (
	"context"
	"errors"
	"strings"

	runtimev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/runtime_resolution"
	v1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_distribution"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"iwut-app-center/internal/script/domain"
	scriptuc "iwut-app-center/internal/script/usecase"
	"iwut-app-center/internal/shared"
)

type ScriptDistributionService struct {
	v1.UnimplementedScriptDistributionServiceServer
	query *scriptuc.ScriptDistribution
}

func NewScriptDistributionService(query *scriptuc.ScriptDistribution) *ScriptDistributionService {
	return &ScriptDistributionService{query: query}
}

func scriptDistributionOperation(operation string) bool {
	return strings.HasPrefix(operation, "/app_center.v1.script_distribution.ScriptDistributionService/")
}

func scriptDistributionIdentity(ctx context.Context, messages ...proto.Message) (shared.AuthenticatedUserIdentity, error) {
	trusted, _ := trustedIdentityFromContext(ctx)
	identity := shared.AuthenticatedUserIdentity{AuthID: trusted.AuthID}
	if err := scriptuc.RequireDistributionUser(identity); err != nil {
		return identity, scriptDistributionError(err)
	}
	for _, message := range messages {
		if message == nil || !message.ProtoReflect().IsValid() || hasUnknownScriptFields(message) {
			return identity, scriptDistributionError(domain.ErrInvalidDistributionRequest)
		}
	}
	return identity, nil
}

func scriptRuntimeInput(runtime *v1.ScriptRuntimeQuery, app, version string) scriptuc.ResolveScriptManifestQuery {
	mode := domain.ScriptRuntimeMode("")
	switch runtime.GetMode() {
	case v1.ScriptRuntimeMode_SCRIPT_RUNTIME_MODE_PURE_TEST:
		mode = domain.ScriptRuntimePureTest
	case v1.ScriptRuntimeMode_SCRIPT_RUNTIME_MODE_UNIFIED:
		mode = domain.ScriptRuntimeUnified
	}
	return scriptuc.ResolveScriptManifestQuery{ApplicationID: app, ApplicationVersionID: version, Mode: mode, RPCAPIMajor: runtime.GetHostRpcApiMajor(), HostCapabilities: runtime.GetHostCapabilities()}
}

func scriptDescriptorResponse(d domain.ScriptDescriptor) *v1.ScriptDescriptor {
	return &v1.ScriptDescriptor{ResourceId: string(d.ResourceID), VersionId: string(d.VersionID), ContentFormat: d.Format, Digest: d.Digest, ByteLength: d.ByteLength}
}

func (s *ScriptDistributionService) ListPublishedScripts(ctx context.Context, req *v1.ListPublishedScriptsRequest) (*v1.PublishedScriptPage, error) {
	identity, err := scriptDistributionIdentity(ctx, req)
	if err != nil {
		return nil, err
	}
	page, err := s.query.ListPublished(ctx, identity, req.Limit, req.GetCursor())
	if err != nil {
		return nil, scriptDistributionError(err)
	}
	out := &v1.PublishedScriptPage{Items: make([]*v1.PublishedScriptItem, 0, len(page.Items)), NextCursor: page.NextCursor, AsOf: timestamppb.New(page.AsOf)}
	for _, item := range page.Items {
		out.Items = append(out.Items, &v1.PublishedScriptItem{ResourceId: string(item.ResourceID), Name: item.Name, Purpose: item.Purpose, Head: &v1.PublishedScriptHead{VersionId: string(item.Head.VersionID), VersionLabel: item.VersionLabel, ContentFormat: item.Head.Format, Digest: item.Head.Digest, ByteLength: item.Head.ByteLength}})
	}
	return out, nil
}

func (s *ScriptDistributionService) GetPublishedScriptContent(ctx context.Context, req *v1.GetPublishedScriptContentRequest) (*v1.ScriptContent, error) {
	identity, err := scriptDistributionIdentity(ctx, req)
	if err != nil {
		return nil, err
	}
	content, err := s.query.GetPublishedContent(ctx, identity, req.GetResourceId(), req.GetVersionId(), req.GetExpectedDigest())
	if err != nil {
		return nil, scriptDistributionError(err)
	}
	d := content.Descriptor
	return &v1.ScriptContent{ResourceId: string(d.ResourceID), VersionId: string(d.VersionID), ContentFormat: d.Format, Digest: d.Digest, ByteLength: d.ByteLength, ContentText: content.Text, AsOf: timestamppb.New(content.AsOf)}, nil
}

func (s *ScriptDistributionService) ResolveApplicationScriptManifest(ctx context.Context, req *v1.ResolveApplicationScriptManifestRequest) (*v1.ApplicationScriptManifest, error) {
	input := req.GetQuery()
	runtime := input.GetRuntime()
	identity, err := scriptDistributionIdentity(ctx, req, input, runtime)
	if err != nil {
		return nil, err
	}
	manifest, err := s.query.ResolveManifest(ctx, identity, scriptRuntimeInput(runtime, req.GetApplicationId(), req.GetApplicationVersionId()))
	if err != nil {
		return nil, scriptDistributionError(err)
	}
	target := manifest.Target
	out := &v1.ApplicationScriptManifest{ApplicationId: target.ApplicationID.String(), ApplicationVersionId: target.ApplicationVersionID, PublicationRevision: target.PublicationRevision,
		Channel: runtimev1.LaunchChannel(runtimev1.LaunchChannel_value["LAUNCH_CHANNEL_"+target.Channel]), RpcApiMajor: target.RPCAPIMajor, Scripts: make([]*v1.ScriptDescriptor, 0, len(manifest.Scripts)), AsOf: timestamppb.New(manifest.AsOf)}
	for _, d := range manifest.Scripts {
		out.Scripts = append(out.Scripts, scriptDescriptorResponse(d))
	}
	return out, nil
}

func (s *ScriptDistributionService) GetApplicationScriptContent(ctx context.Context, req *v1.GetApplicationScriptContentRequest) (*v1.ApplicationScriptContent, error) {
	input := req.GetQuery()
	runtime := input.GetRuntime()
	identity, err := scriptDistributionIdentity(ctx, req, input, runtime)
	if err != nil {
		return nil, err
	}
	result, err := s.query.GetApplicationContent(ctx, identity, scriptuc.GetApplicationScriptContentQuery{ResolveScriptManifestQuery: scriptRuntimeInput(runtime, req.GetApplicationId(), req.GetApplicationVersionId()), ScriptVersionID: req.GetScriptVersionId(), ExpectedDigest: input.GetExpectedDigest()})
	if err != nil {
		return nil, scriptDistributionError(err)
	}
	target, content := result.Target, result.Content
	d := content.Descriptor
	return &v1.ApplicationScriptContent{ApplicationId: target.ApplicationID.String(), ApplicationVersionId: target.ApplicationVersionID, PublicationRevision: target.PublicationRevision,
		Channel: runtimev1.LaunchChannel(runtimev1.LaunchChannel_value["LAUNCH_CHANNEL_"+target.Channel]), RpcApiMajor: target.RPCAPIMajor,
		ResourceId: string(d.ResourceID), VersionId: string(d.VersionID), ContentFormat: d.Format, Digest: d.Digest, ByteLength: d.ByteLength, ContentText: content.Text, AsOf: timestamppb.New(content.AsOf)}, nil
}

func scriptDistributionError(err error) error {
	for _, entry := range []struct {
		err    error
		code   codes.Code
		reason string
	}{
		{domain.ErrIdentityRequired, codes.Unauthenticated, "USER_IDENTITY_REQUIRED"},
		{domain.ErrInvalidDistributionRequest, codes.InvalidArgument, "INVALID_SCRIPT_DISTRIBUTION_REQUEST"},
		{domain.ErrInvalidDistributionCursor, codes.InvalidArgument, "INVALID_SCRIPT_DISTRIBUTION_CURSOR"},
		{domain.ErrScriptContentUnavailable, codes.NotFound, "SCRIPT_CONTENT_UNAVAILABLE"},
		{domain.ErrDistributionStateInconsistent, codes.Internal, "SCRIPT_DISTRIBUTION_STATE_INCONSISTENT"},
		{domain.ErrDistributionUnavailable, codes.Unavailable, "SCRIPT_DISTRIBUTION_UNAVAILABLE"},
	} {
		if errors.Is(err, entry.err) {
			return transportStatus(entry.code, "ERROR_REASON_"+entry.reason, entry.err.Error())
		}
	}
	return transportStatus(codes.Unavailable, "ERROR_REASON_SCRIPT_DISTRIBUTION_UNAVAILABLE", "script distribution unavailable")
}
