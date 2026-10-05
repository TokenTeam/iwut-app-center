package domain

import (
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"iwut-app-center/internal/shared"
)

// SystemEligibilityRejectionReason is the fixed authoritative text used when a
// suspended current administrator or Review submitter forces a System
// rejection. It is not user supplied.
const SystemEligibilityRejectionReason = "当前应用管理员或审核提交者不满足账号与开发者资格要求，待处理审核已由系统自动拒绝。"

// ReviewAction is the Command-level verb. Persisted status uses ReviewDecision.
type ReviewAction string

const (
	ReviewActionApprove ReviewAction = "APPROVE"
	ReviewActionReject  ReviewAction = "REJECT"
)

func NewReviewAction(value string) (ReviewAction, error) {
	switch ReviewAction(value) {
	case ReviewActionApprove:
		return ReviewActionApprove, nil
	case ReviewActionReject:
		return ReviewActionReject, nil
	default:
		return "", ErrInvalidApplicationReviewOutcome
	}
}

func (action ReviewAction) Decision() ReviewDecision {
	if action == ReviewActionApprove {
		return ReviewDecisionApproved
	}
	return ReviewDecisionRejected
}

// ReviewDecision is the persisted one-time outcome.
type ReviewDecision string

const (
	ReviewDecisionApproved ReviewDecision = "APPROVED"
	ReviewDecisionRejected ReviewDecision = "REJECTED"
)

func (decision ReviewDecision) valid() bool {
	switch decision {
	case ReviewDecisionApproved, ReviewDecisionRejected:
		return true
	default:
		return false
	}
}

func (decision ReviewDecision) String() string { return string(decision) }

// ReviewPolicyVersion identifies one immutable VersionReviewPolicy.
type ReviewPolicyVersion string

func NewReviewPolicyVersion(value string) (ReviewPolicyVersion, error) {
	if !policyVersionPattern.MatchString(value) {
		return "", ErrInvalidApplicationReviewPolicyVersion
	}
	return ReviewPolicyVersion(value), nil
}

func (version ReviewPolicyVersion) String() string { return string(version) }

// ReviewCheckID identifies one policy check that a reviewer explicitly
// confirmed. It is an opaque policy-owned identifier, not a free-form field.
type ReviewCheckID string

func NewReviewCheckID(value string) (ReviewCheckID, error) {
	if value == "" || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return "", NewInternalError(nil)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", NewInternalError(nil)
		}
	}
	return ReviewCheckID(value), nil
}

func (id ReviewCheckID) String() string { return string(id) }

type ReviewCheckDefinition struct {
	id ReviewCheckID
}

func NewReviewCheckDefinition(id ReviewCheckID) (ReviewCheckDefinition, error) {
	if id == "" {
		return ReviewCheckDefinition{}, NewInternalError(nil)
	}
	return ReviewCheckDefinition{id: id}, nil
}

func (definition ReviewCheckDefinition) ID() ReviewCheckID { return definition.id }

type ReviewPolicyStatus string

const (
	ReviewPolicyStatusActive  ReviewPolicyStatus = "ACTIVE"
	ReviewPolicyStatusRetired ReviewPolicyStatus = "RETIRED"
)

func (status ReviewPolicyStatus) valid() bool {
	switch status {
	case ReviewPolicyStatusActive, ReviewPolicyStatusRetired:
		return true
	default:
		return false
	}
}

// VersionReviewPolicy is immutable policy content keyed by ReviewPolicyVersion.
// Formal strategies and historical versions are owned by App Center's local
// immutable policy repository.
type VersionReviewPolicy struct {
	version        ReviewPolicyVersion
	requiredChecks []ReviewCheckDefinition
	status         ReviewPolicyStatus
}

func NewVersionReviewPolicy(
	version ReviewPolicyVersion,
	requiredChecks []ReviewCheckDefinition,
	status ReviewPolicyStatus,
) (*VersionReviewPolicy, error) {
	if !policyVersionPattern.MatchString(version.String()) || !status.valid() {
		return nil, NewInternalError(nil)
	}
	seen := make(map[ReviewCheckID]struct{}, len(requiredChecks))
	for _, check := range requiredChecks {
		if check.id == "" {
			return nil, NewInternalError(nil)
		}
		if _, exists := seen[check.id]; exists {
			return nil, NewInternalError(nil)
		}
		seen[check.id] = struct{}{}
	}
	return &VersionReviewPolicy{
		version:        version,
		requiredChecks: append([]ReviewCheckDefinition{}, requiredChecks...),
		status:         status,
	}, nil
}

func (policy *VersionReviewPolicy) Version() ReviewPolicyVersion { return policy.version }
func (policy *VersionReviewPolicy) Status() ReviewPolicyStatus   { return policy.status }
func (policy *VersionReviewPolicy) Usable() bool {
	return policy != nil && policy.status == ReviewPolicyStatusActive
}

func (policy *VersionReviewPolicy) RequiredCheckIDs() []ReviewCheckID {
	values := make([]ReviewCheckID, len(policy.requiredChecks))
	for index, check := range policy.requiredChecks {
		values[index] = check.id
	}
	slices.Sort(values)
	return values
}

// ValidateConfirmation enforces set semantics for confirmedCheckIds: no
// duplicates, every identifier defined by the policy and every policy check
// present. It returns the identifiers sorted by code point.
func (policy *VersionReviewPolicy) ValidateConfirmation(confirmed []ReviewCheckID) ([]ReviewCheckID, error) {
	required := make(map[ReviewCheckID]struct{}, len(policy.requiredChecks))
	for _, check := range policy.requiredChecks {
		required[check.id] = struct{}{}
	}
	seen := make(map[ReviewCheckID]struct{}, len(confirmed))
	for _, id := range confirmed {
		if id == "" {
			return nil, ErrApplicationReviewChecksIncomplete
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, ErrApplicationReviewChecksIncomplete
		}
		if _, known := required[id]; !known {
			return nil, ErrApplicationReviewChecksIncomplete
		}
		seen[id] = struct{}{}
	}
	for _, check := range policy.requiredChecks {
		if _, present := seen[check.id]; !present {
			return nil, ErrApplicationReviewChecksIncomplete
		}
	}
	normalized := append([]ReviewCheckID{}, confirmed...)
	slices.Sort(normalized)
	return normalized, nil
}

// ApprovalValidation records the external facts re-confirmed for an APPROVED
// decision.
type ApprovalValidation struct {
	scopeCatalogRevision   ScopeCatalogRevision
	preflightPolicyVersion PreflightPolicyVersion
}

func NewApprovalValidation(
	scopeCatalogRevision ScopeCatalogRevision,
	preflightPolicyVersion PreflightPolicyVersion,
) (*ApprovalValidation, error) {
	if scopeCatalogRevision < 1 || !policyVersionPattern.MatchString(preflightPolicyVersion.String()) {
		return nil, NewInternalError(nil)
	}
	return &ApprovalValidation{
		scopeCatalogRevision:   scopeCatalogRevision,
		preflightPolicyVersion: preflightPolicyVersion,
	}, nil
}

func (validation *ApprovalValidation) ScopeCatalogRevision() ScopeCatalogRevision {
	return validation.scopeCatalogRevision
}
func (validation *ApprovalValidation) PreflightPolicyVersion() PreflightPolicyVersion {
	return validation.preflightPolicyVersion
}

// ApplicationReviewDecision is the immutable, write-once decision. Reason is
// empty for an APPROVED decision without a note. ApprovalValidation is present
// only for APPROVED decisions.
type ApplicationReviewDecision struct {
	outcome             ReviewDecision
	reviewPolicyVersion ReviewPolicyVersion
	confirmedCheckIDs   []ReviewCheckID
	reason              string
	decidedBy           shared.AuthID
	decidedAt           time.Time
	approvalValidation  *ApprovalValidation
}

func NewApprovedDecision(
	policyVersion ReviewPolicyVersion,
	confirmedCheckIDs []ReviewCheckID,
	optionalReason string,
	approvalValidation *ApprovalValidation,
	decidedBy shared.AuthID,
	decidedAt time.Time,
) (*ApplicationReviewDecision, error) {
	if approvalValidation == nil {
		return nil, NewInternalError(nil)
	}
	return newApplicationReviewDecision(
		ReviewDecisionApproved, policyVersion, confirmedCheckIDs, optionalReason,
		approvalValidation, decidedBy, decidedAt,
	)
}

func NewRejectedDecision(
	policyVersion ReviewPolicyVersion,
	reason string,
	decidedBy shared.AuthID,
	decidedAt time.Time,
) (*ApplicationReviewDecision, error) {
	return newApplicationReviewDecision(
		ReviewDecisionRejected, policyVersion, nil, reason, nil, decidedBy, decidedAt,
	)
}

func newApplicationReviewDecision(
	outcome ReviewDecision,
	policyVersion ReviewPolicyVersion,
	confirmedCheckIDs []ReviewCheckID,
	reason string,
	approvalValidation *ApprovalValidation,
	decidedBy shared.AuthID,
	decidedAt time.Time,
) (*ApplicationReviewDecision, error) {
	if !outcome.valid() || !policyVersionPattern.MatchString(policyVersion.String()) ||
		!decidedBy.IsValid() || decidedAt.IsZero() {
		return nil, NewInternalError(nil)
	}

	normalized := append([]ReviewCheckID{}, confirmedCheckIDs...)
	slices.Sort(normalized)
	if !strictlySortedUnique(normalized) {
		return nil, NewInternalError(nil)
	}

	switch outcome {
	case ReviewDecisionApproved:
		if approvalValidation == nil {
			return nil, NewInternalError(nil)
		}
		if reason != "" {
			if err := ValidateReviewReason(reason); err != nil {
				return nil, err
			}
		}
	case ReviewDecisionRejected:
		if approvalValidation != nil || len(normalized) != 0 {
			return nil, NewInternalError(nil)
		}
		if err := ValidateReviewReason(reason); err != nil {
			return nil, err
		}
	}

	return &ApplicationReviewDecision{
		outcome:             outcome,
		reviewPolicyVersion: policyVersion,
		confirmedCheckIDs:   normalized,
		reason:              reason,
		decidedBy:           decidedBy,
		decidedAt:           decidedAt.UTC(),
		approvalValidation:  copyApprovalValidation(approvalValidation),
	}, nil
}

func (decision *ApplicationReviewDecision) Outcome() ReviewDecision {
	return decision.outcome
}
func (decision *ApplicationReviewDecision) ReviewPolicyVersion() ReviewPolicyVersion {
	return decision.reviewPolicyVersion
}
func (decision *ApplicationReviewDecision) ConfirmedCheckIDs() []ReviewCheckID {
	return append([]ReviewCheckID{}, decision.confirmedCheckIDs...)
}
func (decision *ApplicationReviewDecision) Reason() string { return decision.reason }
func (decision *ApplicationReviewDecision) DecidedBy() shared.AuthID {
	return decision.decidedBy
}
func (decision *ApplicationReviewDecision) DecidedAt() time.Time { return decision.decidedAt }
func (decision *ApplicationReviewDecision) ApprovalValidation() *ApprovalValidation {
	return copyApprovalValidation(decision.approvalValidation)
}

// ValidateReviewReason enforces BR-REV-015 for a supplied reason. An empty
// reason is rejected; callers decide whether a reason is optional.
func ValidateReviewReason(value string) error {
	if value == "" || !utf8.ValidString(value) {
		return ErrInvalidApplicationReviewReason
	}
	codePoints := utf8.RuneCountInString(value)
	if codePoints < 1 || codePoints > 2000 {
		return ErrInvalidApplicationReviewReason
	}
	if strings.TrimSpace(value) != value {
		return ErrInvalidApplicationReviewReason
	}
	for _, character := range value {
		if unicode.Is(unicode.Cc, character) {
			return ErrInvalidApplicationReviewReason
		}
	}
	return nil
}

func copyApprovalValidation(validation *ApprovalValidation) *ApprovalValidation {
	if validation == nil {
		return nil
	}
	copy := *validation
	return &copy
}
