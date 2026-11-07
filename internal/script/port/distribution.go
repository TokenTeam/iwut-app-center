package port

import (
	"context"
	"time"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/shared"
)

type ApplicationScriptRequest struct {
	Caller               shared.AuthID
	ApplicationID        shared.ApplicationID
	ApplicationVersionID string
	Runtime              domain.ScriptRuntimeQuery
	ObservedAt           time.Time
}

type ScriptDistributionRepository interface {
	ListPublishedScripts(context.Context, shared.AuthID, ScriptPageRequest, time.Time) (domain.PublishedScriptPage, error)
	GetPublishedScriptContent(context.Context, domain.ResourceID, domain.VersionID, string, time.Time) (domain.ScriptContent, error)
	ResolveApplicationScriptManifest(context.Context, ApplicationScriptRequest) (domain.ApplicationScriptManifest, error)
	GetApplicationScriptContent(context.Context, ApplicationScriptRequest, domain.VersionID, string) (domain.ApplicationScriptContent, error)
}
