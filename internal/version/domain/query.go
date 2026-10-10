package domain

import "time"

type OwnedReviewDecision struct {
	Outcome   string
	Reason    *string
	DecidedAt time.Time
}

type OwnedDraftRestoration struct{ RestoredAt time.Time }

type OwnedLatestReview struct {
	ReviewID         string
	Attempt          int32
	Status           string
	SubmittedBy      string
	SubmittedAt      time.Time
	Decision         *OwnedReviewDecision
	DraftRestoration *OwnedDraftRestoration
}

type OwnedVersion struct {
	VersionID, ApplicationID, VersionLabel, LaunchURL    string
	Sequence                                             int32
	RPCApiMinVersion, RPCApiMaxVersionExclusive          int32
	RequiredCapabilities, RequiredScopes, OptionalScopes []string
	PKCERedirectURIs, ConfidentialRedirectURIs           []string
	ReviewStatus                                         string
	Revision                                             int64
	CreatedBy                                            string
	CreatedAt, UpdatedAt                                 time.Time
	LatestReview                                         *OwnedLatestReview
}

type OwnedVersionPage struct {
	Items         []OwnedVersion
	NextPageToken string
	AsOf          time.Time
}

type OwnedVersionDetail struct {
	Version OwnedVersion
	AsOf    time.Time
}
