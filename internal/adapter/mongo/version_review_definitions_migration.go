package mongo

import (
	"context"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
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
func (m *Migrator) applyVersionReviewDefinitionsMigration(ctx context.Context) error {
	if err := m.ensureValidatedCollection(ctx, versionReviewPoliciesCollectionName, versionReviewDefinitionsValidator()); err != nil {
		return err
	}
	session, err := m.database.Client().StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		coll := m.database.Collection(versionReviewPoliciesCollectionName)
		expected := formalVersionReviewPolicy()
		for _, version := range []string{"app-version-review-v1", "app-version-review-v2"} {
			var old versionReviewPolicyDocument
			if err := coll.FindOne(tx, bson.M{"version": version}).Decode(&old); err != nil {
				return nil, err
			}
			ids := expected.RequiredChecks
			if version == "app-version-review-v1" {
				ids = ids[:3]
			}
			want := versionReviewPolicyDocument{Version: version, RequiredChecks: ids, Status: old.Status}
			if !equalVersionReviewPolicyDocuments(old, want) || (old.Status != "ACTIVE" && old.Status != "RETIRED") || (version == "app-version-review-v1" && old.Status != "RETIRED") {
				return nil, fmt.Errorf("immutable historical policy differs")
			}
		}
		if _, err := coll.UpdateOne(tx, bson.M{"version": "app-version-review-v2"}, bson.M{"$set": bson.M{"status": "RETIRED"}}); err != nil {
			return nil, err
		}
		if _, err := coll.UpdateOne(tx, bson.M{"version": expected.Version}, bson.M{"$setOnInsert": expected}, options.UpdateOne().SetUpsert(true)); err != nil {
			return nil, err
		}
		var actual versionReviewPolicyDocument
		if err := coll.FindOne(tx, bson.M{"version": expected.Version}).Decode(&actual); err != nil {
			return nil, err
		}
		if !equalVersionReviewPolicyDocuments(actual, expected) {
			return nil, fmt.Errorf("immutable v3 policy differs")
		}
		count, err := coll.CountDocuments(tx, bson.M{"status": "ACTIVE"})
		if err != nil {
			return nil, err
		}
		if count != 1 {
			return nil, fmt.Errorf("expected exactly one ACTIVE review policy")
		}
		return nil, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	return err
}
func versionReviewDefinitionsValidator() bson.D {
	base := versionReviewPolicyValidator()[0].Value.(bson.D)
	for i := range base {
		if base[i].Key == "properties" {
			props := base[i].Value.(bson.D)
			item := bson.D{{Key: "bsonType", Value: "object"}, {Key: "required", Value: bson.A{"id", "title", "description"}}, {Key: "additionalProperties", Value: false}, {Key: "properties", Value: bson.D{
				{Key: "id", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "minLength", Value: 1}}},
				{Key: "title", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "minLength", Value: 1}, {Key: "maxLength", Value: 80}}},
				{Key: "description", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "minLength", Value: 1}, {Key: "maxLength", Value: 2000}}},
			}}}
			props = append(props, bson.E{Key: "coordinationRevision", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(1)}}})
			base[i].Value = append(props, bson.E{Key: "definitions", Value: bson.D{{Key: "bsonType", Value: "array"}, {Key: "minItems", Value: 4}, {Key: "maxItems", Value: 4}, {Key: "items", Value: item}}})
		}
	}
	return bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$jsonSchema", Value: base}},
		bson.D{{Key: "$or", Value: bson.A{
			bson.M{"version": bson.M{"$ne": "app-version-review-v3"}},
			bson.D{{Key: "definitions", Value: bson.D{{Key: "$exists", Value: true}}}, {Key: "coordinationRevision", Value: bson.D{{Key: "$exists", Value: true}}}},
		}}},
	}}}
}
