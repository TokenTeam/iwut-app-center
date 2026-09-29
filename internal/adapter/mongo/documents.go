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

type oauthRedirectConfigurationDocument struct {
	PKCERedirectURIs         []string `bson:"pkceRedirectUris"`
	ConfidentialRedirectURIs []string `bson:"confidentialRedirectUris"`
}

type applicationVersionOAuthConfigDocument struct {
	ApplicationVersionID string                             `bson:"applicationVersionId"`
	ApplicationID        string                             `bson:"applicationId"`
	OAuthRedirects       oauthRedirectConfigurationDocument `bson:"oauthRedirects"`
}

type applicationVersionReviewSnapshotDocument struct {
	VersionLabel              string                             `bson:"versionLabel"`
	LaunchURL                 string                             `bson:"launchUrl"`
	RPCApiMinVersion          int32                              `bson:"rpcApiMinVersion"`
	RPCApiMaxVersionExclusive int32                              `bson:"rpcApiMaxVersionExclusive"`
	RequiredCapabilities      []string                           `bson:"requiredCapabilities"`
	RequiredScopes            []string                           `bson:"requiredScopes"`
	OptionalScopes            []string                           `bson:"optionalScopes"`
	OAuthRedirects            oauthRedirectConfigurationDocument `bson:"oauthRedirects"`
}

type applicationReviewDocument struct {
	ReviewID               string                                     `bson:"reviewId"`
	ApplicationID          string                                     `bson:"applicationId"`
	VersionID              string                                     `bson:"versionId"`
	Attempt                int32                                      `bson:"attempt"`
	SourceVersionRevision  int64                                      `bson:"sourceVersionRevision"`
	Status                 string                                     `bson:"status"`
	Decision               *applicationReviewDecisionDocument         `bson:"decision"`
	DraftRestoration       *applicationReviewDraftRestorationDocument `bson:"draftRestoration"`
	Snapshot               applicationVersionReviewSnapshotDocument   `bson:"snapshot"`
	ScopeCatalogRevision   int64                                      `bson:"scopeCatalogRevision"`
	PreflightPolicyVersion string                                     `bson:"preflightPolicyVersion"`
	SubmittedBy            string                                     `bson:"submittedBy"`
	SubmittedAt            time.Time                                  `bson:"submittedAt"`
}

type applicationReviewDecisionDocument struct {
	Outcome             string                                       `bson:"outcome"`
	ReviewPolicyVersion string                                       `bson:"reviewPolicyVersion"`
	ConfirmedCheckIDs   []string                                     `bson:"confirmedCheckIds"`
	Reason              *string                                      `bson:"reason"`
	DecidedBy           string                                       `bson:"decidedBy"`
	DecidedAt           time.Time                                    `bson:"decidedAt"`
	ApprovalValidation  *applicationReviewApprovalValidationDocument `bson:"approvalValidation"`
}

type applicationReviewApprovalValidationDocument struct {
	ScopeCatalogRevision   int64  `bson:"scopeCatalogRevision"`
	PreflightPolicyVersion string `bson:"preflightPolicyVersion"`
}

type applicationReviewDraftRestorationDocument struct {
	RestoredBy            string    `bson:"restoredBy"`
	RestoredAt            time.Time `bson:"restoredAt"`
	ResultVersionRevision int64     `bson:"resultVersionRevision"`
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
func applicationVersionFromDocument(document applicationVersionDocument, oauthDocuments ...applicationVersionOAuthConfigDocument) (*versiondomain.ApplicationVersion, error) {
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
	oauthRedirects := versiondomain.EmptyOAuthRedirectConfiguration()
	if len(oauthDocuments) == 1 {
		oauthDocument := oauthDocuments[0]
		if oauthDocument.ApplicationVersionID != document.VersionID || oauthDocument.ApplicationID != document.ApplicationID ||
			oauthDocument.OAuthRedirects.PKCERedirectURIs == nil || oauthDocument.OAuthRedirects.ConfidentialRedirectURIs == nil {
			return nil, corruptApplicationVersion("invalid OAuth redirect ownership")
		}
		oauthRedirects, err = versiondomain.NewOAuthRedirectConfiguration(
			oauthDocument.OAuthRedirects.PKCERedirectURIs,
			oauthDocument.OAuthRedirects.ConfidentialRedirectURIs,
		)
		if err != nil || !slices.Equal(oauthRedirects.PKCERedirectURIs(), oauthDocument.OAuthRedirects.PKCERedirectURIs) ||
			!slices.Equal(oauthRedirects.ConfidentialRedirectURIs(), oauthDocument.OAuthRedirects.ConfidentialRedirectURIs) {
			return nil, corruptApplicationVersion("invalid OAuth redirect configuration")
		}
	} else if len(oauthDocuments) > 1 {
		return nil, corruptApplicationVersion("multiple OAuth redirect configurations")
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
		oauthRedirects,
	)
	if err != nil {
		return nil, corruptApplicationVersion("invalid application version")
	}
	return version, nil
}

func applicationVersionOAuthConfigToDocument(version *versiondomain.ApplicationVersion) (applicationVersionOAuthConfigDocument, error) {
	if version == nil {
		return applicationVersionOAuthConfigDocument{}, fmt.Errorf("map application version OAuth config: version is nil")
	}
	redirects := version.OAuthRedirects()
	return applicationVersionOAuthConfigDocument{
		ApplicationVersionID: version.ID().String(),
		ApplicationID:        version.ApplicationID().String(),
		OAuthRedirects: oauthRedirectConfigurationDocument{
			PKCERedirectURIs:         redirects.PKCERedirectURIs(),
			ConfidentialRedirectURIs: redirects.ConfidentialRedirectURIs(),
		},
	}, nil
}

func corruptApplicationVersion(reason string) error {
	return fmt.Errorf("%w: %s", errCorruptApplicationVersionDocument, reason)
}

func applicationVersionDocumentToSubmissionCandidate(document applicationVersionDocument, oauthDocuments ...applicationVersionOAuthConfigDocument) (*reviewdomain.SubmissionCandidate, error) {
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
	oauthRedirects, err := reviewOAuthRedirectsFromVersionDocument(document, oauthDocuments...)
	if err != nil {
		return nil, err
	}

	snapshot, err := reviewdomain.NewApplicationVersionReviewSnapshot(
		document.VersionLabel,
		reviewdomain.LaunchURL(document.LaunchURL),
		document.RPCApiMinVersion,
		document.RPCApiMaxVersionExclusive,
		append([]string{}, document.RequiredCapabilities...),
		reviewScopeNames(document.RequiredScopes),
		reviewScopeNames(document.OptionalScopes),
		oauthRedirects,
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
	oauthRedirects := snapshot.OAuthRedirects()
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
			OAuthRedirects: oauthRedirectConfigurationDocument{
				PKCERedirectURIs:         oauthRedirects.PKCERedirectURIs(),
				ConfidentialRedirectURIs: oauthRedirects.ConfidentialRedirectURIs(),
			},
		},
		ScopeCatalogRevision:   review.ScopeCatalogRevision().Int64(),
		PreflightPolicyVersion: review.PreflightPolicyVersion().String(),
		SubmittedBy:            review.SubmittedBy().String(),
		SubmittedAt:            review.SubmittedAt().UTC(),
	}, nil
}

// applicationReviewFromDocument re-enters the Domain through its constructors.
// Stored invalidity is adapter corruption and never caller validation.
func applicationReviewFromDocument(document applicationReviewDocument) (*reviewdomain.ApplicationReview, error) {
	reviewID := reviewdomain.ApplicationReviewID(document.ReviewID)
	if !reviewID.IsValid() {
		return nil, corruptApplicationReview("invalid review ID")
	}
	applicationID, ok := shared.ParseApplicationID(document.ApplicationID)
	if !ok {
		return nil, corruptApplicationReview("invalid application ID")
	}
	versionID := reviewdomain.ApplicationVersionID(document.VersionID)
	if !versionID.IsValid() {
		return nil, corruptApplicationReview("invalid version ID")
	}
	attempt, err := reviewdomain.NewReviewAttempt(document.Attempt)
	if err != nil {
		return nil, corruptApplicationReview("invalid attempt")
	}
	snapshot, err := applicationReviewSnapshotFromDocument(document.Snapshot)
	if err != nil {
		return nil, err
	}
	scopeCatalogRevision, err := reviewdomain.NewScopeCatalogRevision(document.ScopeCatalogRevision)
	if err != nil {
		return nil, corruptApplicationReview("invalid scope catalog revision")
	}
	preflightPolicyVersion, err := reviewdomain.NewPreflightPolicyVersion(document.PreflightPolicyVersion)
	if err != nil {
		return nil, corruptApplicationReview("invalid preflight policy version")
	}
	submittedBy := shared.AuthID(document.SubmittedBy)
	if !submittedBy.IsValid() {
		return nil, corruptApplicationReview("invalid submitter identity")
	}
	status := reviewdomain.ReviewStatus(document.Status)
	var decision *reviewdomain.ApplicationReviewDecision
	if status != reviewdomain.ReviewStatusPending {
		decision, err = applicationReviewDecisionFromDocument(document.Decision)
		if err != nil {
			return nil, err
		}
	} else if document.Decision != nil {
		return nil, corruptApplicationReview("pending review carries a decision")
	}
	var restoration *reviewdomain.ApplicationReviewDraftRestoration
	if document.DraftRestoration != nil {
		restoration, err = reviewdomain.NewApplicationReviewDraftRestoration(
			shared.AuthID(document.DraftRestoration.RestoredBy),
			document.DraftRestoration.RestoredAt,
			document.DraftRestoration.ResultVersionRevision,
		)
		if err != nil {
			return nil, corruptApplicationReview("invalid draft restoration")
		}
	}
	review, err := reviewdomain.RestoreApplicationReview(
		reviewID,
		applicationID,
		versionID,
		attempt,
		document.SourceVersionRevision,
		*snapshot,
		scopeCatalogRevision,
		preflightPolicyVersion,
		submittedBy,
		document.SubmittedAt,
		status,
		decision,
		restoration,
	)
	if err != nil {
		return nil, corruptApplicationReview("invalid application review")
	}
	return review, nil
}

func applicationReviewSnapshotFromDocument(document applicationVersionReviewSnapshotDocument) (*reviewdomain.ApplicationVersionReviewSnapshot, error) {
	if document.RequiredCapabilities == nil || document.RequiredScopes == nil || document.OptionalScopes == nil {
		return nil, corruptApplicationReview("null snapshot set field")
	}
	pkce := document.OAuthRedirects.PKCERedirectURIs
	confidential := document.OAuthRedirects.ConfidentialRedirectURIs
	if pkce == nil && confidential == nil {
		pkce, confidential = []string{}, []string{}
	}
	oauthRedirects, err := reviewdomain.NewOAuthRedirectConfiguration(pkce, confidential)
	if err != nil {
		return nil, corruptApplicationReview("invalid snapshot OAuth redirects")
	}
	snapshot, err := reviewdomain.NewApplicationVersionReviewSnapshot(
		document.VersionLabel,
		reviewdomain.LaunchURL(document.LaunchURL),
		document.RPCApiMinVersion,
		document.RPCApiMaxVersionExclusive,
		append([]string{}, document.RequiredCapabilities...),
		reviewScopeNames(document.RequiredScopes),
		reviewScopeNames(document.OptionalScopes),
		oauthRedirects,
	)
	if err != nil {
		return nil, corruptApplicationReview("invalid review snapshot")
	}
	return snapshot, nil
}

func applicationReviewDecisionToDocument(
	decision *reviewdomain.ApplicationReviewDecision,
) (*applicationReviewDecisionDocument, error) {
	if decision == nil {
		return nil, fmt.Errorf("map application review decision document: decision is nil")
	}
	checkIDs := decision.ConfirmedCheckIDs()
	document := &applicationReviewDecisionDocument{
		Outcome:             decision.Outcome().String(),
		ReviewPolicyVersion: decision.ReviewPolicyVersion().String(),
		ConfirmedCheckIDs:   make([]string, len(checkIDs)),
		DecidedBy:           decision.DecidedBy().String(),
		DecidedAt:           decision.DecidedAt().UTC(),
	}
	for index, id := range checkIDs {
		document.ConfirmedCheckIDs[index] = id.String()
	}
	if reason := decision.Reason(); reason != "" {
		document.Reason = &reason
	}
	if validation := decision.ApprovalValidation(); validation != nil {
		document.ApprovalValidation = &applicationReviewApprovalValidationDocument{
			ScopeCatalogRevision:   validation.ScopeCatalogRevision().Int64(),
			PreflightPolicyVersion: validation.PreflightPolicyVersion().String(),
		}
	}
	return document, nil
}

func applicationReviewDecisionFromDocument(
	document *applicationReviewDecisionDocument,
) (*reviewdomain.ApplicationReviewDecision, error) {
	if document == nil {
		return nil, corruptApplicationReview("missing decision object")
	}
	outcome := reviewdomain.ReviewDecision(document.Outcome)
	policyVersion, err := reviewdomain.NewReviewPolicyVersion(document.ReviewPolicyVersion)
	if err != nil {
		return nil, corruptApplicationReview("invalid decision policy version")
	}
	if document.ConfirmedCheckIDs == nil {
		return nil, corruptApplicationReview("null confirmed check IDs")
	}
	checkIDs := make([]reviewdomain.ReviewCheckID, len(document.ConfirmedCheckIDs))
	for index, value := range document.ConfirmedCheckIDs {
		id, err := reviewdomain.NewReviewCheckID(value)
		if err != nil {
			return nil, corruptApplicationReview("invalid confirmed check ID")
		}
		checkIDs[index] = id
	}
	reason := ""
	if document.Reason != nil {
		reason = *document.Reason
	}
	decidedBy := shared.AuthID(document.DecidedBy)
	if !decidedBy.IsValid() || document.DecidedAt.IsZero() {
		return nil, corruptApplicationReview("invalid decision audit")
	}
	switch outcome {
	case reviewdomain.ReviewDecisionApproved:
		if document.ApprovalValidation == nil {
			return nil, corruptApplicationReview("approved decision lacks approval validation")
		}
		validation, err := reviewdomain.NewApprovalValidation(
			reviewdomain.ScopeCatalogRevision(document.ApprovalValidation.ScopeCatalogRevision),
			reviewdomain.PreflightPolicyVersion(document.ApprovalValidation.PreflightPolicyVersion),
		)
		if err != nil {
			return nil, corruptApplicationReview("invalid approval validation")
		}
		decision, err := reviewdomain.NewApprovedDecision(
			policyVersion, checkIDs, reason, validation, decidedBy, document.DecidedAt,
		)
		if err != nil {
			return nil, corruptApplicationReview("invalid approved decision")
		}
		return decision, nil
	case reviewdomain.ReviewDecisionRejected:
		if document.ApprovalValidation != nil {
			return nil, corruptApplicationReview("rejected decision carries approval validation")
		}
		decision, err := reviewdomain.NewRejectedDecision(policyVersion, reason, decidedBy, document.DecidedAt)
		if err != nil {
			return nil, corruptApplicationReview("invalid rejected decision")
		}
		return decision, nil
	default:
		return nil, corruptApplicationReview("invalid decision outcome")
	}
}

// applicationVersionDocumentToDecisionVersion validates the current persisted
// Version content and returns the facts a decision must compare with the
// Review snapshot.
func applicationVersionDocumentToDecisionVersion(
	document applicationVersionDocument,
	oauthDocuments ...applicationVersionOAuthConfigDocument,
) (int64, shared.AuthID, reviewdomain.ApplicationVersionReviewSnapshot, error) {
	if _, err := versiondomain.NewVersionLabel(document.VersionLabel); err != nil {
		return 0, "", reviewdomain.ApplicationVersionReviewSnapshot{}, corruptApplicationVersion("invalid version label")
	}
	if _, err := versiondomain.NewLaunchURL(document.LaunchURL); err != nil {
		return 0, "", reviewdomain.ApplicationVersionReviewSnapshot{}, corruptApplicationVersion("invalid launch URL")
	}
	if _, err := versiondomain.NewRPCApiRange(document.RPCApiMinVersion, document.RPCApiMaxVersionExclusive); err != nil {
		return 0, "", reviewdomain.ApplicationVersionReviewSnapshot{}, corruptApplicationVersion("invalid RPC API range")
	}
	if document.RequiredCapabilities == nil || document.RequiredScopes == nil || document.OptionalScopes == nil {
		return 0, "", reviewdomain.ApplicationVersionReviewSnapshot{}, corruptApplicationVersion("null set field")
	}
	capabilities, err := versiondomain.NewCapabilitySet(document.RequiredCapabilities)
	if err != nil || !slices.Equal(capabilities.Strings(), document.RequiredCapabilities) {
		return 0, "", reviewdomain.ApplicationVersionReviewSnapshot{}, corruptApplicationVersion("invalid required capabilities")
	}
	scopes, err := versiondomain.NewScopeRequest(document.RequiredScopes, document.OptionalScopes)
	if err != nil ||
		!slices.Equal(scopeNamesToStrings(scopes.Required()), document.RequiredScopes) ||
		!slices.Equal(scopeNamesToStrings(scopes.Optional()), document.OptionalScopes) {
		return 0, "", reviewdomain.ApplicationVersionReviewSnapshot{}, corruptApplicationVersion("invalid scope request")
	}
	createdBy := shared.AuthID(document.CreatedBy)
	if !createdBy.IsValid() || document.Revision < 1 {
		return 0, "", reviewdomain.ApplicationVersionReviewSnapshot{}, corruptApplicationVersion("invalid creation audit")
	}
	oauthRedirects, err := reviewOAuthRedirectsFromVersionDocument(document, oauthDocuments...)
	if err != nil {
		return 0, "", reviewdomain.ApplicationVersionReviewSnapshot{}, err
	}
	snapshot, err := reviewdomain.NewApplicationVersionReviewSnapshot(
		document.VersionLabel,
		reviewdomain.LaunchURL(document.LaunchURL),
		document.RPCApiMinVersion,
		document.RPCApiMaxVersionExclusive,
		append([]string{}, document.RequiredCapabilities...),
		reviewScopeNames(document.RequiredScopes),
		reviewScopeNames(document.OptionalScopes),
		oauthRedirects,
	)
	if err != nil {
		return 0, "", reviewdomain.ApplicationVersionReviewSnapshot{}, corruptApplicationVersion("invalid review snapshot")
	}
	return document.Revision, createdBy, *snapshot, nil
}

func reviewOAuthRedirectsFromVersionDocument(document applicationVersionDocument, oauthDocuments ...applicationVersionOAuthConfigDocument) (reviewdomain.OAuthRedirectConfiguration, error) {
	pkce, confidential := []string{}, []string{}
	if len(oauthDocuments) == 1 {
		oauthDocument := oauthDocuments[0]
		if oauthDocument.ApplicationVersionID != document.VersionID || oauthDocument.ApplicationID != document.ApplicationID ||
			oauthDocument.OAuthRedirects.PKCERedirectURIs == nil || oauthDocument.OAuthRedirects.ConfidentialRedirectURIs == nil {
			return reviewdomain.OAuthRedirectConfiguration{}, corruptApplicationVersion("invalid OAuth redirect ownership")
		}
		pkce = oauthDocument.OAuthRedirects.PKCERedirectURIs
		confidential = oauthDocument.OAuthRedirects.ConfidentialRedirectURIs
	} else if len(oauthDocuments) > 1 {
		return reviewdomain.OAuthRedirectConfiguration{}, corruptApplicationVersion("multiple OAuth redirect configurations")
	}
	configuration, err := reviewdomain.NewOAuthRedirectConfiguration(pkce, confidential)
	if err != nil {
		return reviewdomain.OAuthRedirectConfiguration{}, corruptApplicationVersion("invalid OAuth redirect configuration")
	}
	return configuration, nil
}

func corruptApplicationReview(reason string) error {
	return fmt.Errorf("%w: %s", errCorruptApplicationReviewDocument, reason)
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
