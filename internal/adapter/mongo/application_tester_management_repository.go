package mongo

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
	"iwut-app-center/internal/shared"
	testerdomain "iwut-app-center/internal/tester/domain"
	testerport "iwut-app-center/internal/tester/port"
)

const testerManagementCursorKeyDomain = "iwut-app-center/application-tester-management-cursor-key/v1"
const testerManagementCursorMACDomain = "iwut-app-center/application-tester-management-cursor-mac/v1\x00"

type TesterManagementCursorKey struct {
	value [32]byte
	valid bool
}

func DeriveTesterManagementCursorKey(material []byte) (TesterManagementCursorKey, error) {
	if len(material) == 0 {
		return TesterManagementCursorKey{}, fmt.Errorf("tester management cursor key material is empty")
	}
	mac := hmac.New(sha256.New, material)
	_, _ = mac.Write([]byte(testerManagementCursorKeyDomain))
	var key TesterManagementCursorKey
	copy(key.value[:], mac.Sum(nil))
	key.valid = true
	return key, nil
}

type ApplicationTesterManagementRepository struct {
	database  *drivermongo.Database
	cursorKey TesterManagementCursorKey
}

func NewApplicationTesterManagementRepository(db *drivermongo.Database, key TesterManagementCursorKey) *ApplicationTesterManagementRepository {
	return &ApplicationTesterManagementRepository{db, key}
}

var _ testerport.ManagementQueryRepository = (*ApplicationTesterManagementRepository)(nil)

// The envelope binds authorization context and position, never an authorization decision.
type testerManagementPosition struct {
	Version     int    `json:"v"`
	Caller      string `json:"c"`
	Application string `json:"a"`
	Ownership   int64  `json:"o"`
	Kind        string `json:"k"`
	Status      string `json:"s"`
	Millis      int64  `json:"t"`
	ID          string `json:"i"`
}
type testerManagementCursor struct {
	Position testerManagementPosition `json:"p"`
	MAC      string                   `json:"m"`
}

func (r *ApplicationTesterManagementRepository) encodeCursor(c testerManagementPosition) (string, error) {
	if !r.cursorKey.valid {
		return "", testerport.ErrManagementUnavailable
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, r.cursorKey.value[:])
	_, _ = mac.Write([]byte(testerManagementCursorMACDomain))
	_, _ = mac.Write(payload)
	raw, err := json.Marshal(testerManagementCursor{c, base64.RawURLEncoding.EncodeToString(mac.Sum(nil))})
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	if len(encoded) > 4096 {
		return "", testerport.ErrManagementUnavailable
	}
	return encoded, err
}
func (r *ApplicationTesterManagementRepository) decodeCursor(token string, bind testerManagementPosition) (*testerManagementPosition, error) {
	if token == "" {
		return nil, nil
	}
	if len(token) > 4096 || !r.cursorKey.valid {
		return nil, testerport.ErrInvalidManagementPageToken
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != token {
		return nil, testerport.ErrInvalidManagementPageToken
	}
	var c testerManagementCursor
	if json.Unmarshal(raw, &c) != nil {
		return nil, testerport.ErrInvalidManagementPageToken
	}
	canonical, _ := json.Marshal(c)
	if !bytes.Equal(raw, canonical) {
		return nil, testerport.ErrInvalidManagementPageToken
	}
	p := c.Position
	bind.Millis = p.Millis
	bind.ID = p.ID
	if p != bind || !canonicalTesterManagementID(p.ID) || !testerManagementTime(time.UnixMilli(p.Millis)) {
		return nil, testerport.ErrInvalidManagementPageToken
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(c.MAC)
	if err != nil || len(signature) != 32 || base64.RawURLEncoding.EncodeToString(signature) != c.MAC {
		return nil, testerport.ErrInvalidManagementPageToken
	}
	payload, _ := json.Marshal(p)
	mac := hmac.New(sha256.New, r.cursorKey.value[:])
	_, _ = mac.Write([]byte(testerManagementCursorMACDomain))
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, testerport.ErrInvalidManagementPageToken
	}
	return &p, nil
}
func testerManagementSnapshot[T any](r *ApplicationTesterManagementRepository, ctx context.Context, fn func(context.Context, time.Time) (T, error)) (T, error) {
	var zero T
	if r == nil || r.database == nil || !r.cursorKey.valid {
		return zero, testerport.ErrManagementUnavailable
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return zero, testerport.ErrManagementUnavailable
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) { return fn(tx, time.Now().UTC()) }, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		if errors.Is(err, testerport.ErrManagementNotFound) || errors.Is(err, testerport.ErrManagementStateInconsistent) || errors.Is(err, testerport.ErrInvalidManagementPageToken) {
			return zero, err
		}
		return zero, testerport.ErrManagementUnavailable
	}
	resultValue, ok := result.(T)
	if !ok {
		return zero, testerport.ErrManagementStateInconsistent
	}
	return resultValue, nil
}
func (r *ApplicationTesterManagementRepository) authorize(ctx context.Context, caller shared.AuthID, app shared.ApplicationID) (int64, error) {
	raw, err := r.database.Collection(applicationsCollectionName).FindOne(ctx, bson.M{"id": app.String(), "adminId": caller.String()}).Raw()
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return 0, testerport.ErrManagementNotFound
	}
	if err != nil {
		return 0, testerport.ErrManagementUnavailable
	}
	if !testerManagementUniqueFields(raw) {
		return 0, testerport.ErrManagementStateInconsistent
	}
	var d struct {
		ID        string `bson:"id"`
		Admin     string `bson:"adminId"`
		Ownership int64  `bson:"ownershipRevision"`
	}
	if raw.Lookup("id").Type != bson.TypeString || raw.Lookup("adminId").Type != bson.TypeString || raw.Lookup("ownershipRevision").Type != bson.TypeInt64 || bson.Unmarshal(raw, &d) != nil || d.ID != app.String() || d.Admin != caller.String() || d.Ownership < 1 {
		return 0, testerport.ErrManagementStateInconsistent
	}
	return d.Ownership, nil
}

// Decode only after a successful driver read. BSON type/shape failures are data
// corruption (500), while cursor/driver failures remain unavailable (503).
func testerManagementDocuments[T any](ctx context.Context, db *drivermongo.Database, collection string, filter bson.M, sort bson.D, limit int64, shape map[string]bson.Type) ([]T, error) {
	opts := options.Find()
	if len(sort) > 0 {
		opts.SetSort(sort)
	}
	if limit > 0 {
		opts.SetLimit(limit)
	}
	cursor, err := db.Collection(collection).Find(ctx, filter, opts)
	if err != nil {
		return nil, testerport.ErrManagementUnavailable
	}
	defer cursor.Close(ctx)
	result := []T{}
	for cursor.Next(ctx) {
		raw := cursor.Current
		if !testerManagementUniqueFields(raw) {
			return nil, testerport.ErrManagementStateInconsistent
		}
		for key, kind := range shape {
			v := raw.Lookup(key)
			if v.Type != kind {
				return nil, testerport.ErrManagementStateInconsistent
			}
		}
		var nullable map[string]bson.Type
		if collection == applicationTesterMembershipsCollectionName {
			nullable = map[string]bson.Type{"removedBy": bson.TypeString, "removedAt": bson.TypeDateTime}
		} else {
			nullable = map[string]bson.Type{"revokedBy": bson.TypeString, "revokedAt": bson.TypeDateTime, "revocationReason": bson.TypeString, "replacedByJoinLinkId": bson.TypeString}
		}
		for key, kind := range nullable {
			value := raw.Lookup(key)
			if value.Type != kind && value.Type != bson.TypeNull {
				return nil, testerport.ErrManagementStateInconsistent
			}
		}
		var d T
		if bson.Unmarshal(raw, &d) != nil {
			return nil, testerport.ErrManagementStateInconsistent
		}
		result = append(result, d)
	}
	if cursor.Err() != nil {
		return nil, testerport.ErrManagementUnavailable
	}
	return result, nil
}

var testerMembershipManagementShape = map[string]bson.Type{"membershipId": bson.TypeString, "applicationId": bson.TypeString, "testerAuthId": bson.TypeString, "status": bson.TypeString, "joinedViaJoinLinkId": bson.TypeString, "joinedAt": bson.TypeDateTime}
var testerLinkManagementShape = map[string]bson.Type{"joinLinkId": bson.TypeString, "applicationId": bson.TypeString, "status": bson.TypeString, "createdBy": bson.TypeString, "createdAt": bson.TypeDateTime, "tokenHash": bson.TypeBinary}

func (r *ApplicationTesterManagementRepository) links(ctx context.Context, filter bson.M, sort bson.D, limit int64) ([]applicationTesterJoinLinkDocument, error) {
	return testerManagementDocuments[applicationTesterJoinLinkDocument](ctx, r.database, applicationTesterJoinLinksCollectionName, filter, sort, limit, testerLinkManagementShape)
}
func (r *ApplicationTesterManagementRepository) memberships(ctx context.Context, filter bson.M, sort bson.D, limit int64) ([]applicationTesterMembershipDocument, error) {
	return testerManagementDocuments[applicationTesterMembershipDocument](ctx, r.database, applicationTesterMembershipsCollectionName, filter, sort, limit, testerMembershipManagementShape)
}
func (r *ApplicationTesterManagementRepository) validateLinks(ctx context.Context, app shared.ApplicationID, docs []applicationTesterJoinLinkDocument) ([]testerdomain.ManagedTesterJoinLink, error) {
	items := make([]testerdomain.ManagedTesterJoinLink, len(docs))
	replacements := []string{}
	seenIDs, activeIDs := map[string]bool{}, map[string]bool{}
	for i, d := range docs {
		if seenIDs[d.JoinLinkID] {
			return nil, testerport.ErrManagementStateInconsistent
		}
		seenIDs[d.JoinLinkID] = true
		if d.Status == "ACTIVE" {
			activeIDs[d.JoinLinkID] = true
			if len(activeIDs) > 1 {
				return nil, testerport.ErrManagementStateInconsistent
			}
		}
		link, err := testerJoinLinkFromDocument(d)
		if err != nil || d.ApplicationID != app.String() || !canonicalTesterManagementID(d.JoinLinkID) || !canonicalTesterManagementID(d.ApplicationID) || d.ReplacedByJoinLinkID != nil && !canonicalTesterManagementID(*d.ReplacedByJoinLinkID) || !testerManagementTime(d.CreatedAt) || d.RevokedAt != nil && !testerManagementTime(*d.RevokedAt) {
			return nil, testerport.ErrManagementStateInconsistent
		}
		items[i] = testerdomain.ManagedTesterJoinLink{JoinLinkID: link.JoinLinkID(), ApplicationID: shared.ApplicationID(d.ApplicationID), Status: link.Status(), CreatedAt: link.CreatedAt(), RevokedAt: link.RevokedAt(), RevocationReason: link.RevocationReason(), ReplacedByJoinLinkID: link.ReplacedByJoinLinkID()}
		if d.ReplacedByJoinLinkID != nil {
			replacements = append(replacements, *d.ReplacedByJoinLinkID)
		}
	}
	if len(replacements) > 0 {
		related, err := r.links(ctx, bson.M{"joinLinkId": bson.M{"$in": replacements}}, nil, 0)
		if err != nil {
			return nil, err
		}
		found := map[string]bool{}
		for _, d := range related {
			if _, err := testerJoinLinkFromDocument(d); err != nil || d.ApplicationID != app.String() || found[d.JoinLinkID] || !canonicalTesterManagementID(d.JoinLinkID) || d.ReplacedByJoinLinkID != nil && !canonicalTesterManagementID(*d.ReplacedByJoinLinkID) || !testerManagementTime(d.CreatedAt) || d.RevokedAt != nil && !testerManagementTime(*d.RevokedAt) {
				return nil, testerport.ErrManagementStateInconsistent
			}
			found[d.JoinLinkID] = true
			if d.Status == "ACTIVE" {
				activeIDs[d.JoinLinkID] = true
				if len(activeIDs) > 1 {
					return nil, testerport.ErrManagementStateInconsistent
				}
			}
		}
		for _, id := range replacements {
			if !found[id] {
				return nil, testerport.ErrManagementStateInconsistent
			}
		}
	}
	return items, nil
}
func (r *ApplicationTesterManagementRepository) validateMemberships(ctx context.Context, app shared.ApplicationID, docs []applicationTesterMembershipDocument) ([]testerdomain.ManagedTesterMembership, error) {
	items := make([]testerdomain.ManagedTesterMembership, len(docs))
	sources := []string{}
	seenIDs, activeAuth := map[string]bool{}, map[string]bool{}
	for i, d := range docs {
		if seenIDs[d.MembershipID] {
			return nil, testerport.ErrManagementStateInconsistent
		}
		seenIDs[d.MembershipID] = true
		if d.Status == "ACTIVE" {
			if activeAuth[d.TesterAuthID] {
				return nil, testerport.ErrManagementStateInconsistent
			}
			activeAuth[d.TesterAuthID] = true
			if len(activeAuth) > 100 {
				return nil, testerport.ErrManagementStateInconsistent
			}
		}
		m, err := testerMembershipFromDocument(d)
		if err != nil || d.ApplicationID != app.String() || !canonicalTesterManagementID(d.MembershipID) || !canonicalTesterManagementID(d.ApplicationID) || !canonicalTesterManagementID(d.JoinedViaJoinLinkID) || !testerManagementTime(d.JoinedAt) || d.RemovedAt != nil && !testerManagementTime(*d.RemovedAt) {
			return nil, testerport.ErrManagementStateInconsistent
		}
		items[i] = testerdomain.ManagedTesterMembership{MembershipID: m.MembershipID(), ApplicationID: shared.ApplicationID(d.ApplicationID), TesterAuthID: m.TesterAuthID(), Status: m.Status(), JoinedViaJoinLinkID: m.JoinedViaJoinLinkID(), JoinedAt: m.JoinedAt(), RemovedAt: m.RemovedAt()}
		sources = append(sources, d.JoinedViaJoinLinkID)
	}
	if len(sources) == 0 {
		return items, nil
	}
	docsLinks, err := r.links(ctx, bson.M{"joinLinkId": bson.M{"$in": sources}}, nil, 0)
	if err != nil {
		return nil, err
	}
	if _, err = r.validateLinks(ctx, app, docsLinks); err != nil {
		return nil, err
	}
	found := map[string]bool{}
	for _, d := range docsLinks {
		if found[d.JoinLinkID] {
			return nil, testerport.ErrManagementStateInconsistent
		}
		found[d.JoinLinkID] = true
	}
	for _, id := range sources {
		if !found[id] {
			return nil, testerport.ErrManagementStateInconsistent
		}
	}
	return items, nil
}

func canonicalTesterManagementID(value string) bool {
	return shared.IsUUIDv7(value) && value == strings.ToLower(value)
}

func testerManagementUniqueFields(raw bson.Raw) bool {
	elements, err := raw.Elements()
	if err != nil {
		return false
	}
	seen := map[string]bool{}
	for _, element := range elements {
		key := element.Key()
		if seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}
func (r *ApplicationTesterManagementRepository) GetManagement(ctx context.Context, caller shared.AuthID, app shared.ApplicationID) (*testerdomain.TesterManagement, error) {
	return testerManagementSnapshot(r, ctx, func(tx context.Context, asOf time.Time) (*testerdomain.TesterManagement, error) {
		if _, err := r.authorize(tx, caller, app); err != nil {
			return nil, err
		}
		members, err := r.memberships(tx, bson.M{"applicationId": app.String(), "status": "ACTIVE"}, nil, 101)
		if err != nil {
			return nil, err
		}
		if len(members) > 100 {
			return nil, testerport.ErrManagementStateInconsistent
		}
		seen := map[string]bool{}
		for _, m := range members {
			if seen[m.TesterAuthID] {
				return nil, testerport.ErrManagementStateInconsistent
			}
			seen[m.TesterAuthID] = true
		}
		if _, err = r.validateMemberships(tx, app, members); err != nil {
			return nil, err
		}
		links, err := r.links(tx, bson.M{"applicationId": app.String(), "status": "ACTIVE"}, nil, 2)
		if err != nil {
			return nil, err
		}
		if len(links) > 1 {
			return nil, testerport.ErrManagementStateInconsistent
		}
		projected, err := r.validateLinks(tx, app, links)
		if err != nil {
			return nil, err
		}
		result := &testerdomain.TesterManagement{ApplicationID: app, ActiveTesterCount: int32(len(members)), TesterLimit: 100, AsOf: asOf}
		if len(projected) > 0 {
			result.ActiveJoinLink = &projected[0]
		}
		return result, nil
	})
}
func testerManagementFilter(app shared.ApplicationID, status, timeField, idField string, c *testerManagementPosition) bson.M {
	filter := bson.M{"applicationId": app.String()}
	if status != "ALL" {
		filter["status"] = status
	}
	if c != nil {
		at := time.UnixMilli(c.Millis)
		filter["$or"] = bson.A{bson.M{timeField: bson.M{"$lt": at}}, bson.M{timeField: at, idField: bson.M{"$lt": c.ID}}}
	}
	return filter
}
func (r *ApplicationTesterManagementRepository) ListMemberships(ctx context.Context, caller shared.AuthID, app shared.ApplicationID, status testerdomain.MembershipQueryStatus, size int32, token string) (*testerdomain.TesterMembershipPage, error) {
	return testerManagementSnapshot(r, ctx, func(tx context.Context, asOf time.Time) (*testerdomain.TesterMembershipPage, error) {
		ownership, err := r.authorize(tx, caller, app)
		if err != nil {
			return nil, err
		}
		bind := testerManagementPosition{Version: 1, Caller: testerManagementCaller(caller), Application: app.String(), Ownership: ownership, Kind: "membership", Status: string(status)}
		c, err := r.decodeCursor(token, bind)
		if err != nil {
			return nil, err
		}
		docs, err := r.memberships(tx, testerManagementFilter(app, string(status), "joinedAt", "membershipId", c), bson.D{{Key: "joinedAt", Value: -1}, {Key: "membershipId", Value: -1}}, int64(size)+1)
		if err != nil {
			return nil, err
		}
		items, err := r.validateMemberships(tx, app, docs)
		if err != nil {
			return nil, err
		}
		next := ""
		if len(items) > int(size) {
			items = items[:size]
			last := docs[size-1]
			bind.Millis = last.JoinedAt.UnixMilli()
			bind.ID = last.MembershipID
			next, err = r.encodeCursor(bind)
			if err != nil {
				return nil, testerport.ErrManagementUnavailable
			}
		}
		return &testerdomain.TesterMembershipPage{Items: items, NextPageToken: next, AsOf: asOf}, nil
	})
}
func (r *ApplicationTesterManagementRepository) ListJoinLinks(ctx context.Context, caller shared.AuthID, app shared.ApplicationID, status testerdomain.JoinLinkQueryStatus, size int32, token string) (*testerdomain.TesterJoinLinkPage, error) {
	return testerManagementSnapshot(r, ctx, func(tx context.Context, asOf time.Time) (*testerdomain.TesterJoinLinkPage, error) {
		ownership, err := r.authorize(tx, caller, app)
		if err != nil {
			return nil, err
		}
		bind := testerManagementPosition{Version: 1, Caller: testerManagementCaller(caller), Application: app.String(), Ownership: ownership, Kind: "link", Status: string(status)}
		c, err := r.decodeCursor(token, bind)
		if err != nil {
			return nil, err
		}
		docs, err := r.links(tx, testerManagementFilter(app, string(status), "createdAt", "joinLinkId", c), bson.D{{Key: "createdAt", Value: -1}, {Key: "joinLinkId", Value: -1}}, int64(size)+1)
		if err != nil {
			return nil, err
		}
		items, err := r.validateLinks(tx, app, docs)
		if err != nil {
			return nil, err
		}
		next := ""
		if len(items) > int(size) {
			items = items[:size]
			last := docs[size-1]
			bind.Millis = last.CreatedAt.UnixMilli()
			bind.ID = last.JoinLinkID
			next, err = r.encodeCursor(bind)
			if err != nil {
				return nil, testerport.ErrManagementUnavailable
			}
		}
		return &testerdomain.TesterJoinLinkPage{Items: items, NextPageToken: next, AsOf: asOf}, nil
	})
}

// Auth IDs are opaque and unbounded. Bind a fixed-width digest so each issued
// cursor stays bounded without narrowing the identity contract.
func testerManagementCaller(caller shared.AuthID) string {
	digest := sha256.Sum256([]byte("iwut-app-center/application-tester-management-cursor-caller/v1\x00" + caller.String()))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
func testerManagementTime(at time.Time) bool {
	return !at.IsZero() && at.Year() >= 1 && at.Year() <= 9999
}
