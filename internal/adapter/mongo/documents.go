package mongo

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"iwut-app-center/internal/application/domain"
	reviewdomain "iwut-app-center/internal/review/domain"
	"iwut-app-center/internal/shared"
	versiondomain "iwut-app-center/internal/version/domain"
)

var errCorruptApplicationDocument = errors.New("corrupt application document")
var errCorruptApplicationVersionDocument = errors.New("corrupt application version document")
var errCorruptApplicationReviewDocument = errors.New("corrupt application review document")

// applicationDocument is the adapter persistence model. CoordinationRevision is
// a technical write fence owned only by this adapter; it never enters the
// Application domain entity.
type applicationDocument struct {
	ID                          string    `bson:"id"`
	Name                        string    `bson:"name"`
	NameKey                     string    `bson:"nameKey"`
	AdminID                     string    `bson:"adminId"`
	CreatedAt                   time.Time `bson:"createdAt"`
	NextVersionSequence         int32     `bson:"nextVersionSequence"`
	NextProfileRevisionSequence int32     `bson:"nextProfileRevisionSequence"`
	CoordinationRevision        int64     `bson:"coordinationRevision"`
}

type applicationCreationQuotaDocument struct {
	AdminID   string    `bson:"adminId"`
	Limit     int32     `bson:"limit"`
	UsedCount int32     `bson:"usedCount"`
	Revision  int64     `bson:"revision"`
	UpdatedAt time.Time `bson:"updatedAt"`
}

type applicationVersionDocument struct {
	VersionID                 string    `bson:"versionId"`
	ApplicationID             string    `bson:"applicationId"`
	Sequence                  int32     `bson:"sequence"`
	VersionLabel              string    `bson:"versionLabel"`
	LaunchURL                 string    `bson:"launchUrl"`
	RPCApiMinVersion          int32     `bson:"rpcApiMinVersion"`
	RPCApiMaxVersionExclusive int32     `bson:"rpcApiMaxVersionExclusive"`
	RequiredCapabilities      []string  `bson:"requiredCapabilities"`
	RequiredScopes            []string  `bson:"requiredScopes"`
	OptionalScopes            []string  `bson:"optionalScopes"`
	ReviewStatus              string    `bson:"reviewStatus"`
	CreatedBy                 string    `bson:"createdBy"`
	CreatedAt                 time.Time `bson:"createdAt"`
	Revision                  int64     `bson:"revision"`
	UpdatedBy                 string    `bson:"updatedBy"`
	UpdatedAt                 time.Time `bson:"updatedAt"`
}

type applicationVersionReviewSnapshotDocument struct {
	VersionLabel              string   `bson:"versionLabel"`
	LaunchURL                 string   `bson:"launchUrl"`
	RPCApiMinVersion          int32    `bson:"rpcApiMinVersion"`
	RPCApiMaxVersionExclusive int32    `bson:"rpcApiMaxVersionExclusive"`
	RequiredCapabilities      []string `bson:"requiredCapabilities"`
	RequiredScopes            []string `bson:"requiredScopes"`
	OptionalScopes            []string `bson:"optionalScopes"`
}

type applicationReviewDocument struct {
	ReviewID               string                                   `bson:"reviewId"`
	ApplicationID          string                                   `bson:"applicationId"`
	VersionID              string                                   `bson:"versionId"`
	Attempt                int32                                    `bson:"attempt"`
	SourceVersionRevision  int64                                    `bson:"sourceVersionRevision"`
	Status                 string                                   `bson:"status"`
	Decision               any                                      `bson:"decision"`
	DraftRestoration       any                                      `bson:"draftRestoration"`
	Snapshot               applicationVersionReviewSnapshotDocument `bson:"snapshot"`
	ScopeCatalogRevision   int64                                    `bson:"scopeCatalogRevision"`
	PreflightPolicyVersion string                                   `bson:"preflightPolicyVersion"`
	SubmittedBy            string                                   `bson:"submittedBy"`
	SubmittedAt            time.Time                                `bson:"submittedAt"`
}

func applicationToDocument(application *domain.Application) (applicationDocument, error) {
	if application == nil {
		return applicationDocument{}, fmt.Errorf("map application document: application is nil")
	}

	return applicationDocument{
		ID:                          application.ID().String(),
		Name:                        application.Name().String(),
		NameKey:                     application.Name().Key(),
		AdminID:                     application.AdminID().String(),
		CreatedAt:                   application.CreatedAt().UTC(),
		NextVersionSequence:         1,
		NextProfileRevisionSequence: 1,
	}, nil
}

// applicationFromDocument is deliberately kept inside the persistence
// adapter. A malformed stored document is corruption, not caller validation.
func applicationFromDocument(document applicationDocument) (*domain.Application, error) {
	if document.NextVersionSequence < 1 || document.NextProfileRevisionSequence < 1 {
		return nil, fmt.Errorf("%w: invalid next sequence", errCorruptApplicationDocument)
	}

	id, err := domain.ParseApplicationID(document.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid application ID: %v", errCorruptApplicationDocument, err)
	}
	name, err := domain.NewApplicationName(document.Name)
	if err != nil || name.Key() != document.NameKey {
		return nil, fmt.Errorf("%w: invalid application name", errCorruptApplicationDocument)
	}
	adminID, err := domain.NewAuthID(document.AdminID)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid admin ID", errCorruptApplicationDocument)
	}
	application, err := domain.NewApplication(id, name, adminID, document.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid application fields", errCorruptApplicationDocument)
	}
	return application, nil
}

func applicationVersionToDocument(
	draft *versiondomain.DraftApplicationVersion,
	sequence versiondomain.VersionSequence,
) (applicationVersionDocument, error) {
	version, err := versiondomain.NewApplicationVersion(draft, sequence)
	if err != nil {
		return applicationVersionDocument{}, fmt.Errorf("map application version document: %w", err)
	}
	return applicationVersionEntityToDocument(version)
}

func applicationVersionEntityToDocument(version *versiondomain.ApplicationVersion) (applicationVersionDocument, error) {
	if version == nil {
		return applicationVersionDocument{}, fmt.Errorf("map application version document: version is nil")
	}
	return applicationVersionDocument{
		VersionID:                 version.ID().String(),
		ApplicationID:             version.ApplicationID().String(),
		Sequence:                  version.Sequence().Int32(),
		VersionLabel:              version.VersionLabel().String(),
		LaunchURL:                 version.LaunchURL().String(),
		RPCApiMinVersion:          version.RPCApiRange().Minimum(),
		RPCApiMaxVersionExclusive: version.RPCApiRange().MaximumExclusive(),
		RequiredCapabilities:      capabilityNamesToStrings(version.RequiredCapabilities()),
		RequiredScopes:            scopeNamesToStrings(version.RequiredScopes()),
		OptionalScopes:            scopeNamesToStrings(version.OptionalScopes()),
		ReviewStatus:              string(version.ReviewStatus()),
		CreatedBy:                 version.CreatedBy().String(),
		CreatedAt:                 version.CreatedAt().UTC(),
		Revision:                  version.Revision(),
		UpdatedBy:                 version.UpdatedBy().String(),
		UpdatedAt:                 version.UpdatedAt().UTC(),
	}, nil
}

// applicationVersionFromDocument re-enters the Domain through its
// constructors. Stored invalidity is adapter corruption and never caller
// validation.
func applicationVersionFromDocument(document applicationVersionDocument) (*versiondomain.ApplicationVersion, error) {
	versionID := versiondomain.ApplicationVersionID(document.VersionID)
	if !versionID.IsValid() {
		return nil, corruptApplicationVersion("invalid version ID")
	}
	applicationID, ok := shared.ParseApplicationID(document.ApplicationID)
	if !ok {
		return nil, corruptApplicationVersion("invalid application ID")
	}
	sequence, err := versiondomain.NewVersionSequence(document.Sequence)
	if err != nil {
		return nil, corruptApplicationVersion("invalid sequence")
	}
	versionLabel, err := versiondomain.NewVersionLabel(document.VersionLabel)
	if err != nil {
		return nil, corruptApplicationVersion("invalid version label")
	}
	launchURL, err := versiondomain.NewLaunchURL(document.LaunchURL)
	if err != nil {
		return nil, corruptApplicationVersion("invalid launch URL")
	}
	rpcAPIRange, err := versiondomain.NewRPCApiRange(document.RPCApiMinVersion, document.RPCApiMaxVersionExclusive)
	if err != nil {
		return nil, corruptApplicationVersion("invalid RPC API range")
	}
	if document.RequiredCapabilities == nil || document.RequiredScopes == nil || document.OptionalScopes == nil {
		return nil, corruptApplicationVersion("null set field")
	}
	requiredCapabilities, err := versiondomain.NewCapabilitySet(document.RequiredCapabilities)
	if err != nil || !slices.Equal(requiredCapabilities.Strings(), document.RequiredCapabilities) {
		return nil, corruptApplicationVersion("invalid required capabilities")
	}
	scopeRequest, err := versiondomain.NewScopeRequest(document.RequiredScopes, document.OptionalScopes)
	if err != nil ||
		!slices.Equal(scopeNamesToStrings(scopeRequest.Required()), document.RequiredScopes) ||
		!slices.Equal(scopeNamesToStrings(scopeRequest.Optional()), document.OptionalScopes) {
		return nil, corruptApplicationVersion("invalid scope request")
	}
	createdBy := shared.AuthID(document.CreatedBy)
	if !createdBy.IsValid() {
		return nil, corruptApplicationVersion("invalid created-by identity")
	}
	updatedBy := shared.AuthID(document.UpdatedBy)
	if document.Revision < 1 || !updatedBy.IsValid() || document.UpdatedAt.IsZero() {
		return nil, corruptApplicationVersion("invalid lifecycle or audit fields")
	}
	version, err := versiondomain.RestoreApplicationVersion(
		versionID,
		applicationID,
		sequence,
		versionLabel,
		launchURL,
		rpcAPIRange,
		requiredCapabilities,
		scopeRequest,
		versiondomain.ReviewStatus(document.ReviewStatus),
		createdBy,
		document.CreatedAt,
		document.Revision,
		updatedBy,
		document.UpdatedAt,
	)
	if err != nil {
		return nil, corruptApplicationVersion("invalid application version")
	}
	return version, nil
}

func corruptApplicationVersion(reason string) error {
	return fmt.Errorf("%w: %s", errCorruptApplicationVersionDocument, reason)
}

func applicationVersionDocumentToSubmissionCandidate(document applicationVersionDocument) (*reviewdomain.SubmissionCandidate, error) {
	versionID := versiondomain.ApplicationVersionID(document.VersionID)
	if !versionID.IsValid() {
		return nil, corruptApplicationVersion("invalid version ID")
	}
	applicationID, ok := shared.ParseApplicationID(document.ApplicationID)
	if !ok {
		return nil, corruptApplicationVersion("invalid application ID")
	}
	if _, err := versiondomain.NewVersionLabel(document.VersionLabel); err != nil {
		return nil, corruptApplicationVersion("invalid version label")
	}
	if _, err := versiondomain.NewLaunchURL(document.LaunchURL); err != nil {
		return nil, corruptApplicationVersion("invalid launch URL")
	}
	if _, err := versiondomain.NewRPCApiRange(document.RPCApiMinVersion, document.RPCApiMaxVersionExclusive); err != nil {
		return nil, corruptApplicationVersion("invalid RPC API range")
	}
	capabilities, err := versiondomain.NewCapabilitySet(document.RequiredCapabilities)
	if err != nil || !slices.Equal(capabilities.Strings(), document.RequiredCapabilities) {
		return nil, corruptApplicationVersion("invalid required capabilities")
	}
	scopes, err := versiondomain.NewScopeRequest(document.RequiredScopes, document.OptionalScopes)
	if err != nil || !slices.Equal(scopeNamesToStrings(scopes.Required()), document.RequiredScopes) ||
		!slices.Equal(scopeNamesToStrings(scopes.Optional()), document.OptionalScopes) {
		return nil, corruptApplicationVersion("invalid scope request")
	}
	if document.ReviewStatus != string(versiondomain.ReviewStatusDraft) || document.Revision < 1 {
		return nil, corruptApplicationVersion("invalid submission lifecycle")
	}

	snapshot, err := reviewdomain.NewApplicationVersionReviewSnapshot(
		document.VersionLabel,
		reviewdomain.LaunchURL(document.LaunchURL),
		document.RPCApiMinVersion,
		document.RPCApiMaxVersionExclusive,
		append([]string{}, document.RequiredCapabilities...),
		reviewScopeNames(document.RequiredScopes),
		reviewScopeNames(document.OptionalScopes),
	)
	if err != nil {
		return nil, corruptApplicationVersion("invalid review snapshot")
	}
	candidate, err := reviewdomain.NewSubmissionCandidate(
		applicationID,
		reviewdomain.ApplicationVersionID(versionID),
		document.Revision,
		snapshot,
	)
	if err != nil {
		return nil, corruptApplicationVersion("invalid submission candidate")
	}
	return candidate, nil
}

func applicationReviewToDocument(review *reviewdomain.ApplicationReview) (applicationReviewDocument, error) {
	if review == nil || review.Status() != reviewdomain.ReviewStatusPending || review.HasDecision() || review.HasDraftRestoration() {
		return applicationReviewDocument{}, fmt.Errorf("map application review document: invalid review")
	}
	snapshot := review.Snapshot()
	return applicationReviewDocument{
		ReviewID:              review.ReviewID().String(),
		ApplicationID:         review.ApplicationID().String(),
		VersionID:             review.VersionID().String(),
		Attempt:               review.Attempt().Int32(),
		SourceVersionRevision: review.SourceVersionRevision(),
		Status:                string(review.Status()),
		Decision:              nil,
		DraftRestoration:      nil,
		Snapshot: applicationVersionReviewSnapshotDocument{
			VersionLabel:              snapshot.VersionLabel(),
			LaunchURL:                 string(snapshot.LaunchURL()),
			RPCApiMinVersion:          snapshot.RPCAPIMinVersion(),
			RPCApiMaxVersionExclusive: snapshot.RPCAPIMaxVersionExclusive(),
			RequiredCapabilities:      snapshot.RequiredCapabilities(),
			RequiredScopes:            reviewScopeNamesToStrings(snapshot.RequiredScopes()),
			OptionalScopes:            reviewScopeNamesToStrings(snapshot.OptionalScopes()),
		},
		ScopeCatalogRevision:   review.ScopeCatalogRevision().Int64(),
		PreflightPolicyVersion: review.PreflightPolicyVersion().String(),
		SubmittedBy:            review.SubmittedBy().String(),
		SubmittedAt:            review.SubmittedAt().UTC(),
	}, nil
}

func reviewScopeNames(values []string) []reviewdomain.ScopeName {
	result := make([]reviewdomain.ScopeName, len(values))
	for index, value := range values {
		result[index] = reviewdomain.ScopeName(value)
	}
	return result
}

func reviewScopeNamesToStrings(values []reviewdomain.ScopeName) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func capabilityNamesToStrings(values []versiondomain.CapabilityName) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func scopeNamesToStrings(values []versiondomain.ScopeName) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}
