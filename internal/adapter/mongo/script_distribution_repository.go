package mongo

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	cd "iwut-app-center/internal/catalog/domain"
	cp "iwut-app-center/internal/catalog/port"
	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

type ScriptDistributionRepository struct {
	db      *dm.Database
	cursors *ScriptQueryRepository
}

func NewScriptDistributionRepository(db *dm.Database, key ScriptQueryCursorKey) *ScriptDistributionRepository {
	return &ScriptDistributionRepository{db: db, cursors: NewScriptQueryRepository(db, key)}
}

var _ port.ScriptDistributionRepository = (*ScriptDistributionRepository)(nil)

func (r *ScriptDistributionRepository) GetPublishedScriptContent(ctx context.Context, resource domain.ResourceID, version domain.VersionID, digest string, at time.Time) (domain.ScriptContent, error) {
	return withScriptTransaction(ctx, r.db, func(tx context.Context) (domain.ScriptContent, error) {
		var empty domain.ScriptContent
		// Do not read an unpublished version's private review or text. A
		// historical public event without its permanent proof is damage.
		err := r.db.Collection(scriptPublishedVersionsCollection).FindOne(tx, bson.M{"resourceId": resource, "version.versionId": version}).Err()
		if errors.Is(err, dm.ErrNoDocuments) {
			err = r.db.Collection(scriptPublicationEventsCollection).FindOne(tx, bson.M{"resourceId": resource, "action": domain.PublishScript, "version.versionId": version}).Err()
			if err == nil {
				return empty, domain.ErrDistributionStateInconsistent
			}
			if errors.Is(err, dm.ErrNoDocuments) {
				return empty, domain.ErrScriptContentUnavailable
			}
			return empty, scriptDistributionError(err)
		}
		if err != nil {
			return empty, scriptDistributionError(err)
		}
		loaded, err := loadScriptDependency(tx, r.db, shared.ScriptReference{ResourceID: string(resource), VersionID: string(version)})
		if errors.Is(err, shared.ErrInvalidScriptReferences) {
			return empty, domain.ErrDistributionStateInconsistent
		}
		if err != nil {
			return empty, scriptDistributionError(err)
		}
		if loaded.version.State != domain.VersionApproved {
			return empty, domain.ErrDistributionStateInconsistent
		}
		value, err := qualifyDistributedScript(tx, r.db, loaded)
		if err != nil {
			return empty, err
		}
		if value.descriptor.Digest != digest {
			return empty, domain.ErrScriptContentUnavailable
		}
		return domain.ScriptContent{Descriptor: value.descriptor, Text: value.text, AsOf: at}, nil
	})
}

func applicationScriptRuntimeError(err error) error {
	for _, hidden := range []error{cp.ErrApplicationNotFound, cp.ErrApplicationTesterRequired, cp.ErrApplicationTestTargetUnavailable, cp.ErrApplicationLaunchTargetUnavailable, cd.ErrHostCapabilitiesInsufficient} {
		if errors.Is(err, hidden) {
			return domain.ErrScriptContentUnavailable
		}
	}
	for _, damaged := range []error{cp.ErrApplicationTestPublicationInconsistent, cp.ErrApplicationRuntimeStateInconsistent, cd.ErrMyTestApplicationStateInconsistent, domain.ErrDistributionStateInconsistent} {
		if errors.Is(err, damaged) {
			return domain.ErrDistributionStateInconsistent
		}
	}
	return scriptDistributionError(err)
}

func (r *ScriptDistributionRepository) applicationScriptsSnapshot(ctx context.Context, request port.ApplicationScriptRequest) (domain.ApplicationScriptTarget, []distributedScript, error) {
	var target domain.ApplicationScriptTarget
	host := make([]cd.CapabilityName, len(request.Runtime.Capabilities))
	for i, value := range request.Runtime.Capabilities {
		host[i] = cd.CapabilityName(value)
	}
	if request.Runtime.Mode == domain.ScriptRuntimePureTest {
		resolved, err := NewTestLaunchResolver(r.db).resolveSnapshot(ctx, request.ApplicationID, request.Caller, request.Runtime.RPCAPIMajor, host)
		if err != nil {
			return target, nil, applicationScriptRuntimeError(err)
		}
		target = domain.ApplicationScriptTarget{ApplicationID: request.ApplicationID, ApplicationVersionID: resolved.VersionID(), PublicationRevision: resolved.PublicationRevision(), Channel: "TEST", RPCAPIMajor: resolved.RPCAPIMajor()}
	} else if request.Runtime.Mode == domain.ScriptRuntimeUnified {
		resolved, err := NewUnifiedLaunchResolver(r.db).resolveSnapshot(ctx, request.ApplicationID, request.Caller, request.Runtime.RPCAPIMajor, host)
		if err != nil {
			return target, nil, applicationScriptRuntimeError(err)
		}
		target = domain.ApplicationScriptTarget{ApplicationID: request.ApplicationID, ApplicationVersionID: resolved.VersionID(), PublicationRevision: resolved.PublicationRevision(), Channel: string(resolved.Channel()), RPCAPIMajor: resolved.RPCAPIMajor()}
	} else {
		return target, nil, domain.ErrInvalidDistributionRequest
	}
	if target.ApplicationVersionID != request.ApplicationVersionID {
		return target, nil, domain.ErrScriptContentUnavailable
	}
	approved, err := loadApplicationScriptReview(ctx, r.db, request.ApplicationID.String(), request.ApplicationVersionID)
	if err != nil {
		return target, nil, err
	}
	scripts, err := applicationDistributedScripts(ctx, r.db, approved, false)
	return target, scripts, err
}

func (r *ScriptDistributionRepository) ResolveApplicationScriptManifest(ctx context.Context, request port.ApplicationScriptRequest) (domain.ApplicationScriptManifest, error) {
	return withScriptTransaction(ctx, r.db, func(tx context.Context) (domain.ApplicationScriptManifest, error) {
		var empty domain.ApplicationScriptManifest
		target, scripts, err := r.applicationScriptsSnapshot(tx, request)
		if err != nil {
			return empty, err
		}
		result := domain.ApplicationScriptManifest{Target: target, Scripts: make([]domain.ScriptDescriptor, 0, len(scripts)), AsOf: request.ObservedAt}
		for _, value := range scripts {
			result.Scripts = append(result.Scripts, value.descriptor)
		}
		return result, result.Validate()
	})
}

func (r *ScriptDistributionRepository) GetApplicationScriptContent(ctx context.Context, request port.ApplicationScriptRequest, version domain.VersionID, digest string) (domain.ApplicationScriptContent, error) {
	return withScriptTransaction(ctx, r.db, func(tx context.Context) (domain.ApplicationScriptContent, error) {
		var empty domain.ApplicationScriptContent
		target, scripts, err := r.applicationScriptsSnapshot(tx, request)
		if err != nil {
			return empty, err
		}
		for _, value := range scripts {
			if value.descriptor.VersionID != version {
				continue
			}
			if value.descriptor.Digest != digest {
				return empty, domain.ErrScriptContentUnavailable
			}
			return domain.ApplicationScriptContent{Target: target, Content: domain.ScriptContent{Descriptor: value.descriptor, Text: value.text, AsOf: request.ObservedAt}}, nil
		}
		return empty, domain.ErrScriptContentUnavailable
	})
}
