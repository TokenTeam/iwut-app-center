package domain

import (
	"regexp"
	"strings"
	"time"

	"iwut-app-center/internal/shared"
)

type Channel string

const (
	ChannelTest   Channel = "TEST"
	ChannelGrey   Channel = "GREY"
	ChannelStable Channel = "STABLE"
)

func ParseChannel(value string) (Channel, error) {
	channel := Channel(strings.ToUpper(strings.TrimSpace(value)))
	switch channel {
	case ChannelTest, ChannelGrey, ChannelStable:
		return channel, nil
	default:
		return "", ErrInvalidOAuthChannel
	}
}

type ClientType string

const (
	ClientTypePublicPKCE         ClientType = "PUBLIC_PKCE"
	ClientTypeConfidentialSecret ClientType = "CONFIDENTIAL_SECRET"
)

func ParseClientType(value string) (ClientType, error) {
	t := ClientType(value)
	if t != ClientTypePublicPKCE && t != ClientTypeConfidentialSecret {
		return "", ErrInvalidOAuthClientType
	}
	return t, nil
}

type ClientStatus string

const (
	ClientStatusActive   ClientStatus = "ACTIVE"
	ClientStatusDisabled ClientStatus = "DISABLED"
)

func ParseClientStatus(value string) (ClientStatus, error) {
	s := ClientStatus(value)
	if s != ClientStatusActive && s != ClientStatusDisabled {
		return "", ErrInvalidOAuthClientStatus
	}
	return s, nil
}

var canonicalUUIDv4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type ClientID string

func ParseClientID(value string) (ClientID, error) {
	if !canonicalUUIDv4.MatchString(value) {
		return "", ErrInvalidOAuthClientID
	}
	return ClientID(value), nil
}
func (id ClientID) String() string { return string(id) }
func (id ClientID) IsValid() bool  { return canonicalUUIDv4.MatchString(string(id)) }

type SecretDigest struct{ value [32]byte }

func NewSecretDigest(value [32]byte) SecretDigest { return SecretDigest{value: value} }
func (d SecretDigest) Bytes() [32]byte            { return d.value }
func (d SecretDigest) String() string             { return "[redacted oauth client secret digest]" }
func (d SecretDigest) GoString() string           { return d.String() }

type ClientIdentity struct {
	clientID           ClientID
	clientType         ClientType
	status             ClientStatus
	authorizationEpoch int64
	createdBy          shared.AuthID
	createdAt          time.Time
	statusUpdatedBy    shared.AuthID
	statusUpdatedAt    time.Time
}

func RestoreClientIdentity(id ClientID, typ ClientType, status ClientStatus, epoch int64, createdBy shared.AuthID, createdAt time.Time, updatedBy shared.AuthID, updatedAt time.Time) (*ClientIdentity, error) {
	if !id.IsValid() || (typ != ClientTypePublicPKCE && typ != ClientTypeConfidentialSecret) || (status != ClientStatusActive && status != ClientStatusDisabled) || epoch < 1 || !createdBy.IsValid() || !updatedBy.IsValid() || createdAt.IsZero() || updatedAt.IsZero() || updatedAt.Before(createdAt) {
		return nil, ErrOAuthClientStateInconsistent
	}
	return &ClientIdentity{id, typ, status, epoch, createdBy, createdAt.UTC(), updatedBy, updatedAt.UTC()}, nil
}
func NewClientIdentity(id ClientID, typ ClientType, by shared.AuthID, at time.Time) (*ClientIdentity, error) {
	return RestoreClientIdentity(id, typ, ClientStatusActive, 1, by, at, by, at)
}
func (c *ClientIdentity) ClientID() ClientID             { return c.clientID }
func (c *ClientIdentity) Type() ClientType               { return c.clientType }
func (c *ClientIdentity) Status() ClientStatus           { return c.status }
func (c *ClientIdentity) AuthorizationEpoch() int64      { return c.authorizationEpoch }
func (c *ClientIdentity) CreatedBy() shared.AuthID       { return c.createdBy }
func (c *ClientIdentity) CreatedAt() time.Time           { return c.createdAt }
func (c *ClientIdentity) StatusUpdatedBy() shared.AuthID { return c.statusUpdatedBy }
func (c *ClientIdentity) StatusUpdatedAt() time.Time     { return c.statusUpdatedAt }

type Registration struct {
	applicationID shared.ApplicationID
	channel       Channel
	public        *ClientIdentity
	confidential  *ClientIdentity
	revision      int64
	createdAt     time.Time
	updatedAt     time.Time
}

func RestoreRegistration(appID shared.ApplicationID, channel Channel, public, confidential *ClientIdentity, revision int64, createdAt, updatedAt time.Time) (*Registration, error) {
	if !appID.IsValid() || !channel.Enabled() || revision < 1 || createdAt.IsZero() || updatedAt.IsZero() || updatedAt.Before(createdAt) || public == nil && confidential == nil || public != nil && public.Type() != ClientTypePublicPKCE || confidential != nil && confidential.Type() != ClientTypeConfidentialSecret {
		return nil, ErrOAuthClientStateInconsistent
	}
	return &Registration{appID, channel, cloneIdentity(public), cloneIdentity(confidential), revision, createdAt.UTC(), updatedAt.UTC()}, nil
}

func (c Channel) Enabled() bool                             { return c == ChannelTest || c == ChannelGrey || c == ChannelStable }
func (r *Registration) ApplicationID() shared.ApplicationID { return r.applicationID }
func (r *Registration) Channel() Channel                    { return r.channel }
func (r *Registration) PublicClient() *ClientIdentity       { return cloneIdentity(r.public) }
func (r *Registration) ConfidentialClient() *ClientIdentity { return cloneIdentity(r.confidential) }
func (r *Registration) Revision() int64                     { return r.revision }
func (r *Registration) CreatedAt() time.Time                { return r.createdAt }
func (r *Registration) UpdatedAt() time.Time                { return r.updatedAt }
func (r *Registration) Client(typ ClientType) *ClientIdentity {
	if typ == ClientTypePublicPKCE {
		return cloneIdentity(r.public)
	}
	if typ == ClientTypeConfidentialSecret {
		return cloneIdentity(r.confidential)
	}
	return nil
}
func (r *Registration) ClientByID(id ClientID) *ClientIdentity {
	if r.public != nil && r.public.ClientID() == id {
		return cloneIdentity(r.public)
	}
	if r.confidential != nil && r.confidential.ClientID() == id {
		return cloneIdentity(r.confidential)
	}
	return nil
}
func cloneIdentity(value *ClientIdentity) *ClientIdentity {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

type Credential struct {
	clientID      ClientID
	applicationID shared.ApplicationID
	digest        SecretDigest
	revision      int64
	rotatedBy     shared.AuthID
	rotatedAt     time.Time
}

func RestoreCredential(clientID ClientID, appID shared.ApplicationID, digest SecretDigest, revision int64, by shared.AuthID, at time.Time) (*Credential, error) {
	if !clientID.IsValid() || !appID.IsValid() || revision < 1 || !by.IsValid() || at.IsZero() {
		return nil, ErrOAuthClientStateInconsistent
	}
	return &Credential{clientID, appID, digest, revision, by, at.UTC()}, nil
}
func (c *Credential) ClientID() ClientID                  { return c.clientID }
func (c *Credential) ApplicationID() shared.ApplicationID { return c.applicationID }
func (c *Credential) Digest() SecretDigest                { return c.digest }
func (c *Credential) Revision() int64                     { return c.revision }
func (c *Credential) RotatedBy() shared.AuthID            { return c.rotatedBy }
func (c *Credential) RotatedAt() time.Time                { return c.rotatedAt }

type RegisterResult struct {
	Registration *Registration
	Credential   *Credential
}
type StatusResult struct {
	Registration *Registration
	Changed      bool
}
