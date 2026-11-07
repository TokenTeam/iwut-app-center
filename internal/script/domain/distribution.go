package domain

import (
	"errors"
	"strings"
	"time"

	"iwut-app-center/internal/shared"
)

var (
	ErrInvalidDistributionRequest    = errors.New("invalid script distribution request")
	ErrInvalidDistributionCursor     = errors.New("invalid script distribution cursor")
	ErrScriptContentUnavailable      = errors.New("script content unavailable")
	ErrDistributionStateInconsistent = errors.New("script distribution state inconsistent")
	ErrDistributionUnavailable       = errors.New("script distribution unavailable")
)

// ScriptDescriptor identifies immutable reviewed bytes without management facts.
type ScriptDescriptor struct {
	ResourceID ResourceID
	VersionID  VersionID
	Format     string
	Digest     string
	ByteLength int64
}

func (d ScriptDescriptor) Validate() error {
	r, er := ParseResourceID(string(d.ResourceID))
	v, ev := ParseVersionID(string(d.VersionID))
	if er != nil || ev != nil || r != d.ResourceID || v != d.VersionID || d.Format != JSONFormat || !shared.ValidScriptDigest(d.Digest) || d.ByteLength < 1 || d.ByteLength > MaxContentBytes {
		return ErrDistributionStateInconsistent
	}
	return nil
}

type ScriptRuntimeMode string

const (
	ScriptRuntimePureTest ScriptRuntimeMode = "PURE_TEST"
	ScriptRuntimeUnified  ScriptRuntimeMode = "UNIFIED"
)

type ScriptRuntimeQuery struct {
	Mode         ScriptRuntimeMode
	RPCAPIMajor  int32
	Capabilities []string
}

func NewScriptRuntimeQuery(mode ScriptRuntimeMode, major int32, capabilities []string) (ScriptRuntimeQuery, error) {
	host, err := shared.NormalizeRuntimeCapabilities(capabilities)
	if (mode != ScriptRuntimePureTest && mode != ScriptRuntimeUnified) || major < 1 || err != nil {
		return ScriptRuntimeQuery{}, ErrInvalidDistributionRequest
	}
	return ScriptRuntimeQuery{Mode: mode, RPCAPIMajor: major, Capabilities: host}, nil
}

type PublishedScriptItem struct {
	ResourceID                  ResourceID
	Name, Purpose, VersionLabel string
	Head                        ScriptDescriptor
}

func (i PublishedScriptItem) Validate() error {
	metadata, err := NewMetadata(i.Name, i.Purpose)
	if err != nil || metadata.Name != i.Name || metadata.Purpose != i.Purpose || i.ResourceID != i.Head.ResourceID || ValidateVersionLabel(i.VersionLabel) != nil || i.Head.Validate() != nil {
		return ErrDistributionStateInconsistent
	}
	return nil
}

type PublishedScriptPage struct {
	Items      []PublishedScriptItem
	NextCursor string
	AsOf       time.Time
}

type ScriptContent struct {
	Descriptor ScriptDescriptor
	Text       string
	AsOf       time.Time
}

func (c ScriptContent) Validate() error {
	content, err := NewContent(c.Descriptor.Format, c.Text)
	if err != nil || c.Descriptor.Validate() != nil || int64(len(c.Text)) != c.Descriptor.ByteLength || content.Digest != c.Descriptor.Digest || !validTime(c.AsOf) {
		return ErrDistributionStateInconsistent
	}
	return nil
}

type ApplicationScriptTarget struct {
	ApplicationID        shared.ApplicationID
	ApplicationVersionID string
	PublicationRevision  int64
	Channel              string
	RPCAPIMajor          int32
}

func (t ApplicationScriptTarget) Validate() error {
	if !t.ApplicationID.IsValid() || t.ApplicationID.String() != strings.ToLower(t.ApplicationID.String()) || !shared.IsUUIDv7(t.ApplicationVersionID) || t.ApplicationVersionID != strings.ToLower(t.ApplicationVersionID) || t.PublicationRevision < 1 || t.RPCAPIMajor < 1 || (t.Channel != "TEST" && t.Channel != "GREY" && t.Channel != "STABLE") {
		return ErrDistributionStateInconsistent
	}
	return nil
}

type ApplicationScriptManifest struct {
	Target  ApplicationScriptTarget
	Scripts []ScriptDescriptor
	AsOf    time.Time
}

func (m ApplicationScriptManifest) Validate() error {
	if m.Target.Validate() != nil || !validTime(m.AsOf) || len(m.Scripts) > shared.MaxScriptReferences {
		return ErrDistributionStateInconsistent
	}
	resources, versions := map[ResourceID]bool{}, map[VersionID]bool{}
	var previous VersionID
	for _, d := range m.Scripts {
		if d.Validate() != nil || resources[d.ResourceID] || versions[d.VersionID] || (previous != "" && previous >= d.VersionID) {
			return ErrDistributionStateInconsistent
		}
		resources[d.ResourceID], versions[d.VersionID] = true, true
		previous = d.VersionID
	}
	return nil
}

type ApplicationScriptContent struct {
	Target  ApplicationScriptTarget
	Content ScriptContent
}
