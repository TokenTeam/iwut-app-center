package domain

import (
	"slices"
	"time"
	"unicode/utf8"

	"iwut-app-center/internal/shared"
)

const ProviderSnapshotValidity = 5 * time.Second

type TokenEndpointAuthMethod string

const (
	TokenEndpointAuthMethodNone              TokenEndpointAuthMethod = "none"
	TokenEndpointAuthMethodClientSecretBasic TokenEndpointAuthMethod = "client_secret_basic"
)

type ClientConfiguration struct {
	ClientID             ClientID
	ApplicationID        shared.ApplicationID
	Type                 ClientType
	Channel              Channel
	Status               ClientStatus
	RegistrationRevision int64
	AuthorizationEpoch   int64
	TokenEndpointAuth    TokenEndpointAuthMethod
	CredentialRevision   *int64
}

func NewClientConfiguration(registration *Registration, clientID ClientID, credentialRevision *int64) (*ClientConfiguration, error) {
	if registration == nil || !clientID.IsValid() {
		return nil, ErrOAuthClientStateInconsistent
	}
	identity := registration.ClientByID(clientID)
	if identity == nil {
		return nil, ErrOAuthClientStateInconsistent
	}
	method := TokenEndpointAuthMethodNone
	if identity.Type() == ClientTypeConfidentialSecret {
		method = TokenEndpointAuthMethodClientSecretBasic
		if credentialRevision == nil || *credentialRevision < 1 {
			return nil, ErrOAuthClientStateInconsistent
		}
	} else if credentialRevision != nil {
		return nil, ErrOAuthClientStateInconsistent
	}
	var revision *int64
	if credentialRevision != nil {
		value := *credentialRevision
		revision = &value
	}
	return &ClientConfiguration{clientID, registration.ApplicationID(), identity.Type(), registration.Channel(), identity.Status(), registration.Revision(), identity.AuthorizationEpoch(), method, revision}, nil
}

type ApplicationDisplay struct {
	ProfileRevisionID string
	DisplayName       string
	Description       *string
	Icon              *string
}

func NewApplicationDisplay(profileRevisionID, displayName string, description, icon *string) (ApplicationDisplay, error) {
	if !shared.IsUUIDv7(profileRevisionID) || displayName == "" || !utf8.ValidString(displayName) {
		return ApplicationDisplay{}, ErrApplicationProfileStateInconsistent
	}
	return ApplicationDisplay{profileRevisionID, displayName, cloneString(description), cloneString(icon)}, nil
}

type RuntimeVersion struct {
	VersionID           string
	PublicationRevision int64
	ProfileRevisionID   string
	AdminAuthID         shared.AuthID
}

func (version RuntimeVersion) IsValid() bool {
	return shared.IsUUIDv7(version.VersionID) && version.PublicationRevision >= 1 && shared.IsUUIDv7(version.ProfileRevisionID) && version.AdminAuthID.IsValid()
}

func (version RuntimeVersion) Equal(other RuntimeVersion) bool {
	return version == other
}

type RuntimeConfiguration struct {
	ClientID             ClientID
	ApplicationID        shared.ApplicationID
	Type                 ClientType
	Channel              Channel
	RPCAPIMajor          int32
	RegistrationRevision int64
	AuthorizationEpoch   int64
	AdminAuthID          shared.AuthID
	VersionID            string
	PublicationRevision  int64
	RedirectURIs         []string
	RequiredScopes       []string
	OptionalScopes       []string
	Display              ApplicationDisplay
	ObservedAt           time.Time
	ValidUntil           time.Time
}

func (runtime *RuntimeConfiguration) Version() RuntimeVersion {
	if runtime == nil {
		return RuntimeVersion{}
	}
	return RuntimeVersion{runtime.VersionID, runtime.PublicationRevision, runtime.Display.ProfileRevisionID, runtime.AdminAuthID}
}

func NewRuntimeConfiguration(configuration *ClientConfiguration, major int32, admin shared.AuthID, versionID string, publicationRevision int64, redirects, required, optional []string, display ApplicationDisplay, observedAt time.Time) (*RuntimeConfiguration, error) {
	if configuration == nil || configuration.Status != ClientStatusActive || configuration.Channel != ChannelTest || major < 1 || !admin.IsValid() || !shared.IsUUIDv7(versionID) || publicationRevision < 1 || len(redirects) == 0 || observedAt.IsZero() || !strictlySortedUniqueStrings(redirects) || !strictlySortedUniqueStrings(required) || !strictlySortedUniqueStrings(optional) || !displayValid(display) {
		return nil, ErrOAuthClientStateInconsistent
	}
	return &RuntimeConfiguration{
		ClientID: configuration.ClientID, ApplicationID: configuration.ApplicationID, Type: configuration.Type, Channel: configuration.Channel,
		RPCAPIMajor: major, RegistrationRevision: configuration.RegistrationRevision, AuthorizationEpoch: configuration.AuthorizationEpoch,
		AdminAuthID: admin, VersionID: versionID, PublicationRevision: publicationRevision,
		RedirectURIs: append([]string(nil), redirects...), RequiredScopes: append([]string(nil), required...), OptionalScopes: append([]string(nil), optional...),
		Display: cloneDisplay(display), ObservedAt: observedAt.UTC(), ValidUntil: observedAt.UTC().Add(ProviderSnapshotValidity),
	}, nil
}

type AuthorizationContext struct {
	Runtime            *RuntimeConfiguration
	AuthID             shared.AuthID
	TesterMembershipID string
}

func NewAuthorizationContext(runtime *RuntimeConfiguration, authID shared.AuthID, membershipID string) (*AuthorizationContext, error) {
	if runtime == nil || !authID.IsValid() || !shared.IsUUIDv7(membershipID) {
		return nil, ErrOAuthClientStateInconsistent
	}
	return &AuthorizationContext{runtime, authID, membershipID}, nil
}

type PublishedRedirectEntry struct {
	Channel             Channel
	RPCAPIMajor         int32
	VersionID           string
	PublicationRevision int64
}

type PublishedRedirectSnapshot struct {
	ApplicationID shared.ApplicationID
	Entries       []PublishedRedirectEntry
	RedirectURIs  []string
	ObservedAt    time.Time
	ValidUntil    time.Time
}

func NewPublishedRedirectSnapshot(applicationID shared.ApplicationID, entries []PublishedRedirectEntry, redirects []string, observedAt time.Time) (*PublishedRedirectSnapshot, error) {
	if !applicationID.IsValid() || observedAt.IsZero() || !strictlySortedUniqueStrings(redirects) {
		return nil, ErrOAuthClientStateInconsistent
	}
	for index, entry := range entries {
		if entry.Channel != ChannelTest || entry.RPCAPIMajor < 1 || !shared.IsUUIDv7(entry.VersionID) || entry.PublicationRevision < 1 {
			return nil, ErrOAuthClientStateInconsistent
		}
		if index > 0 && entries[index-1].RPCAPIMajor >= entry.RPCAPIMajor {
			return nil, ErrOAuthClientStateInconsistent
		}
	}
	return &PublishedRedirectSnapshot{applicationID, append([]PublishedRedirectEntry(nil), entries...), append([]string(nil), redirects...), observedAt.UTC(), observedAt.UTC().Add(ProviderSnapshotValidity)}, nil
}

func strictlySortedUniqueStrings(values []string) bool {
	return slices.IsSorted(values) && len(values) == len(slices.Compact(append([]string(nil), values...)))
}

func displayValid(display ApplicationDisplay) bool {
	return shared.IsUUIDv7(display.ProfileRevisionID) && display.DisplayName != "" && utf8.ValidString(display.DisplayName)
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneDisplay(value ApplicationDisplay) ApplicationDisplay {
	value.Description = cloneString(value.Description)
	value.Icon = cloneString(value.Icon)
	return value
}
