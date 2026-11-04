package mongo

import (
	"context"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const versionReviewDefinitionsMigrationID = "0033_version_review_check_definitions"

func formalVersionReviewPolicy() versionReviewPolicyDocument {
	definitions := []versionReviewCheckDocument{
		{ID: "content-policy-reviewed", Title: "应用内容与声明", Description: "已阅读提交快照中的应用声明，并结合实际入口确认其用途可识别且与展示功能一致；未发现冒充平台或其他主体、诱骗提交账号凭据、隐瞒实际收集用途或诱导执行恶意程序的内容。无法辨明用途或无法完成这些确认时，不得确认通过。"},
		{ID: "launch-url-content-reviewed", Title: "实际入口内容", Description: "已访问该审核快照的 launchUrl，并检查入口及其正常跳转后可达的主要功能；能实际观察到与声明用途一致的应用内容，未发现伪装登录、凭据窃取或恶意下载引导。入口不可达、仅有占位内容或必须使用 Reviewer 无法取得的访问条件而无法完成检查时，不得确认通过。"},
		{ID: "requested-access-reviewed", Title: "请求能力与 Scope", Description: "已逐项检查快照声明的能力和 Scope，并结合其正式目录含义与实际功能确认用途可说明、请求范围与所展示功能相符，未发现无关或明显多余的访问请求。空集合表示已明确确认该版本未声明相应访问请求；目录含义或用途无法查明时，不得确认通过。"},
		{ID: "oauth-redirects-reviewed", Title: "OAuth 回调用途", Description: "已分别检查 pkceRedirectUris 与 confidentialRedirectUris 中每个回调与应用用途、接收方及对应 client type 的关系，确认回调由预期接收方使用，且无明显无关接收方。两个数组均为空时，明确确认该版本未配置 OAuth/OIDC 回调。不能确认接收方或用途时，不得确认通过。"},
	}
	ids := make([]string, len(definitions))
	for i, d := range definitions {
		ids[i] = d.ID
	}
	return versionReviewPolicyDocument{Version: "app-version-review-v3", RequiredChecks: ids, Definitions: definitions, Status: "ACTIVE", CoordinationRevision: 1}
}

// This prelaunch initialization stage verifies the same complete v3 baseline;
// no ID-only policy versions or old-data upgrade path are created.
func (m *Migrator) applyVersionReviewDefinitionsMigration(ctx context.Context) error {
	return m.applyVersionReviewPolicyMigration(ctx)
}
func versionReviewDefinitionsValidator() bson.D { return versionReviewPolicyValidator() }
