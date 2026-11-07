package domain

import (
	"errors"
	"math"
	"slices"
	"strings"
	"time"

	"iwut-app-center/internal/shared"
)

const (
	InitialPolicyVersion = "crawler-script-manual-v1"
	ReviewPending        = "PENDING"
)

var ErrReviewPolicyUnavailable = errors.New("script review policy unavailable")

type ReviewID string

func ParseReviewID(raw string) (ReviewID, bool) {
	if !shared.IsUUIDv7(raw) {
		return "", false
	}
	return ReviewID(strings.ToLower(raw)), true
}

type ReviewCheck struct {
	ID          string
	Title       string
	Description string
	Mandatory   bool
}

type ReviewPolicy struct {
	Version string
	Checks  []ReviewCheck
}

func InitialReviewPolicy() ReviewPolicy {
	return ReviewPolicy{Version: InitialPolicyVersion, Checks: []ReviewCheck{
		{ID: "declared-purpose", Title: "用途符合平台说明", Description: "人工确认文本实现的采集用途与资源用途一致，不夹带无关行为。", Mandatory: true},
		{ID: "client-execution", Title: "客户端可执行性", Description: "人工确认目标客户端能够按声明格式处理该文本，必要入口、规则与失败处理可用；此确认不是后端运行证明。", Mandatory: true},
		{ID: "data-and-credentials", Title: "数据与凭据边界", Description: "人工确认不存在内置账号凭据、令牌或将用户凭据及采集数据发送到未经说明目标的逻辑。", Mandatory: true},
		{ID: "network-and-resource", Title: "网络与资源使用", Description: "人工检查访问目标、请求频率、终止条件和资源使用，确认没有与用途无关的请求或无界采集。", Mandatory: true},
	}}
}

func (p ReviewPolicy) Validate() error {
	if strings.TrimSpace(p.Version) != p.Version || !validText(p.Version, 128) || len(p.Checks) == 0 {
		return ErrReviewPolicyUnavailable
	}
	ids := make(map[string]bool, len(p.Checks))
	for _, check := range p.Checks {
		if ValidateVersionLabel(check.ID) != nil || ids[check.ID] ||
			!validText(check.Title, 256) || strings.TrimSpace(check.Title) == "" ||
			!validText(check.Description, 4096) || strings.TrimSpace(check.Description) == "" {
			return ErrReviewPolicyUnavailable
		}
		ids[check.ID] = true
	}
	if p.Version == InitialPolicyVersion && !slices.Equal(p.Checks, InitialReviewPolicy().Checks) {
		return ErrReviewPolicyUnavailable
	}
	return nil
}

type ReviewSnapshot struct {
	ResourceID       ResourceID
	VersionID        VersionID
	Label            string
	Content          Content
	SourceRevision   int64
	Contributors     []shared.AuthID
	ResourceMetadata Metadata
	ResourceRevision int64
}

type Review struct {
	Decision    *Decision
	ID          ReviewID
	Status      string
	Snapshot    ReviewSnapshot
	Policy      ReviewPolicy
	SubmittedBy shared.AuthID
	SubmittedAt time.Time
}

type Submission struct {
	ReviewID      ReviewID
	State         VersionState
	Revision      int64
	SubmittedAt   time.Time
	PolicyVersion string
}

func (v Version) Submit(expectedRevision int64, resource Resource, reviewID ReviewID, policy ReviewPolicy, actor shared.AuthID, at time.Time) (Version, Review, error) {
	if err := v.CheckDraftRevision(expectedRevision); err != nil {
		return Version{}, Review{}, err
	}
	if err := policy.Validate(); err != nil {
		return Version{}, Review{}, err
	}
	id, valid := ParseReviewID(string(reviewID))
	if !valid || id != reviewID || resource.ID != v.ResourceID || resource.Validate() != nil ||
		v.Revision == math.MaxInt64 || !actor.IsValid() || !validTime(at) || at.Before(v.UpdatedAt) {
		return Version{}, Review{}, ErrInternal
	}

	review := Review{
		ID: reviewID, Status: ReviewPending,
		Snapshot: ReviewSnapshot{
			ResourceID: v.ResourceID, VersionID: v.ID, Label: v.Label, Content: v.Content,
			SourceRevision: v.Revision, Contributors: slices.Clone(v.Contributors),
			ResourceMetadata: resource.Metadata, ResourceRevision: resource.Revision,
		},
		Policy:      ReviewPolicy{Version: policy.Version, Checks: slices.Clone(policy.Checks)},
		SubmittedBy: actor, SubmittedAt: at.UTC(),
	}
	v.State = VersionSubmitted
	v.Revision++
	v.UpdatedBy, v.UpdatedAt = actor, review.SubmittedAt
	return v, review, nil
}
