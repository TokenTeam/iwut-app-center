package mongo

import (
	"fmt"
	"sort"
	"time"

	"iwut-app-center/internal/oauthclient/domain"
	"iwut-app-center/internal/shared"
)

const (
	applicationOAuthRegistrationsCollectionName = "application_oauth_registrations"
	oauthClientCredentialsCollectionName        = "oauth_client_credentials"
)

type oauthClientIdentityDocument struct {
	ClientID           string    `bson:"clientId"`
	Type               string    `bson:"type"`
	Status             string    `bson:"status"`
	AuthorizationEpoch int64     `bson:"authorizationEpoch"`
	CreatedBy          string    `bson:"createdBy"`
	CreatedAt          time.Time `bson:"createdAt"`
	StatusUpdatedBy    string    `bson:"statusUpdatedBy"`
	StatusUpdatedAt    time.Time `bson:"statusUpdatedAt"`
}

type applicationOAuthRegistrationDocument struct {
	ApplicationID        string                       `bson:"applicationId"`
	Channel              string                       `bson:"channel"`
	PublicClient         *oauthClientIdentityDocument `bson:"publicClient"`
	ConfidentialClient   *oauthClientIdentityDocument `bson:"confidentialClient"`
	ClientIDs            []string                     `bson:"clientIds"`
	RegistrationRevision int64                        `bson:"registrationRevision"`
	CreatedAt            time.Time                    `bson:"createdAt"`
	UpdatedAt            time.Time                    `bson:"updatedAt"`
}

type oauthClientCredentialDocument struct {
	ClientID           string    `bson:"clientId"`
	ApplicationID      string    `bson:"applicationId"`
	SecretDigest       []byte    `bson:"secretDigest"`
	CredentialRevision int64     `bson:"credentialRevision"`
	RotatedBy          string    `bson:"rotatedBy"`
	RotatedAt          time.Time `bson:"rotatedAt"`
}

func identityToDocument(identity *domain.ClientIdentity) *oauthClientIdentityDocument {
	if identity == nil {
		return nil
	}
	return &oauthClientIdentityDocument{identity.ClientID().String(), string(identity.Type()), string(identity.Status()), identity.AuthorizationEpoch(), identity.CreatedBy().String(), identity.CreatedAt(), identity.StatusUpdatedBy().String(), identity.StatusUpdatedAt()}
}

func identityFromDocument(document *oauthClientIdentityDocument) (*domain.ClientIdentity, error) {
	if document == nil {
		return nil, nil
	}
	id, err := domain.ParseClientID(document.ClientID)
	if err != nil {
		return nil, err
	}
	typ, err := domain.ParseClientType(document.Type)
	if err != nil {
		return nil, err
	}
	status, err := domain.ParseClientStatus(document.Status)
	if err != nil {
		return nil, err
	}
	createdBy := shared.AuthID(document.CreatedBy)
	if !createdBy.IsValid() {
		return nil, fmt.Errorf("invalid createdBy")
	}
	updatedBy := shared.AuthID(document.StatusUpdatedBy)
	if !updatedBy.IsValid() {
		return nil, fmt.Errorf("invalid statusUpdatedBy")
	}
	return domain.RestoreClientIdentity(id, typ, status, document.AuthorizationEpoch, createdBy, document.CreatedAt, updatedBy, document.StatusUpdatedAt)
}

func registrationFromDocument(document applicationOAuthRegistrationDocument) (*domain.Registration, error) {
	appID, ok := shared.ParseApplicationID(document.ApplicationID)
	if !ok {
		return nil, fmt.Errorf("invalid applicationId")
	}
	channel, err := domain.ParseChannel(document.Channel)
	if err != nil {
		return nil, err
	}
	public, err := identityFromDocument(document.PublicClient)
	if err != nil {
		return nil, err
	}
	confidential, err := identityFromDocument(document.ConfidentialClient)
	if err != nil {
		return nil, err
	}
	wantIDs := make([]string, 0, 2)
	if public != nil {
		wantIDs = append(wantIDs, public.ClientID().String())
	}
	if confidential != nil {
		wantIDs = append(wantIDs, confidential.ClientID().String())
	}
	gotIDs := append([]string(nil), document.ClientIDs...)
	sort.Strings(wantIDs)
	sort.Strings(gotIDs)
	if len(gotIDs) != len(wantIDs) {
		return nil, fmt.Errorf("oauth client ID index is inconsistent")
	}
	for index := range wantIDs {
		if wantIDs[index] != gotIDs[index] {
			return nil, fmt.Errorf("oauth client ID index is inconsistent")
		}
	}
	return domain.RestoreRegistration(appID, channel, public, confidential, document.RegistrationRevision, document.CreatedAt, document.UpdatedAt)
}

func credentialFromDocument(document oauthClientCredentialDocument) (*domain.Credential, error) {
	if len(document.SecretDigest) != 32 {
		return nil, fmt.Errorf("invalid digest length")
	}
	id, err := domain.ParseClientID(document.ClientID)
	if err != nil {
		return nil, err
	}
	appID, ok := shared.ParseApplicationID(document.ApplicationID)
	if !ok {
		return nil, fmt.Errorf("invalid applicationId")
	}
	by := shared.AuthID(document.RotatedBy)
	if !by.IsValid() {
		return nil, fmt.Errorf("invalid rotatedBy")
	}
	var value [32]byte
	copy(value[:], document.SecretDigest)
	return domain.RestoreCredential(id, appID, domain.NewSecretDigest(value), document.CredentialRevision, by, document.RotatedAt)
}
