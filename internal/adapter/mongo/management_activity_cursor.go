package mongo

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	ad "iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
	"time"
)

const managementActivityPurpose = "iwut-app-center/application-management-activity/v1"

type ManagementActivityCursorKey struct {
	value [32]byte
	valid bool
}

func DeriveManagementActivityCursorKey(material []byte) (ManagementActivityCursorKey, error) {
	if len(material) == 0 {
		return ManagementActivityCursorKey{}, errors.New("management activity cursor secret material is empty")
	}
	mac := hmac.New(sha256.New, material)
	mac.Write([]byte(managementActivityPurpose + "/key"))
	var key ManagementActivityCursorKey
	copy(key.value[:], mac.Sum(nil))
	key.valid = true
	return key, nil
}

type managementActivityPosition struct {
	Version   int    `json:"v"`
	Purpose   string `json:"p"`
	Caller    string `json:"c"`
	App       string `json:"a"`
	Ownership int64  `json:"o"`
	Kinds     string `json:"k"`
	At        int64  `json:"t"`
	Source    int    `json:"s"`
	Phase     int    `json:"h"`
	ReviewID  string `json:"r"`
}
type managementActivityCursor struct {
	Position managementActivityPosition `json:"p"`
	MAC      string                     `json:"m"`
}

func managementActivityDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func managementActivityBinding(caller shared.AuthID, app shared.ApplicationID, ownership int64, q ad.ManagementActivityQuery) managementActivityPosition {
	raw, _ := json.Marshal(q.Kinds)
	return managementActivityPosition{Version: 1, Purpose: managementActivityPurpose, Caller: managementActivityDigest([]byte(managementActivityPurpose + "/caller\x00" + caller.String())), App: app.String(), Ownership: ownership, Kinds: managementActivityDigest(raw)}
}
func (r *ManagementActivityRepository) encodeCursor(binding managementActivityPosition, last ad.ManagementActivity) string {
	l := last.Locator()
	binding.At = last.OccurredAt.UnixMilli()
	binding.Source = l.Source
	binding.Phase = l.Phase
	binding.ReviewID = l.ReviewID
	payload, _ := json.Marshal(binding)
	mac := hmac.New(sha256.New, r.key.value[:])
	mac.Write([]byte(managementActivityPurpose + "/mac\x00"))
	mac.Write(payload)
	raw, _ := json.Marshal(managementActivityCursor{binding, base64.RawURLEncoding.EncodeToString(mac.Sum(nil))})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func (r *ManagementActivityRepository) decodeCursor(token string, binding managementActivityPosition) (*managementActivityPosition, error) {
	if token == "" {
		return nil, nil
	}
	bad := ad.ErrInvalidManagementActivityPageToken
	if len(token) > 4096 || !r.key.valid {
		return nil, bad
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != token {
		return nil, bad
	}
	var cursor managementActivityCursor
	if json.Unmarshal(raw, &cursor) != nil {
		return nil, bad
	}
	canonical, _ := json.Marshal(cursor)
	if !bytes.Equal(raw, canonical) {
		return nil, bad
	}
	p := cursor.Position
	l := ad.ManagementActivityLocator{Source: p.Source, Phase: p.Phase, ReviewID: p.ReviewID}
	if !l.Valid() || p.At < -62135596800000 || p.At > 253402300799999 {
		return nil, bad
	}
	expected := p
	expected.At = 0
	expected.Source = 0
	expected.Phase = 0
	expected.ReviewID = ""
	if expected != binding {
		return nil, bad
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(cursor.MAC)
	if err != nil || len(signature) != 32 || base64.RawURLEncoding.EncodeToString(signature) != cursor.MAC {
		return nil, bad
	}
	payload, _ := json.Marshal(p)
	mac := hmac.New(sha256.New, r.key.value[:])
	mac.Write([]byte(managementActivityPurpose + "/mac\x00"))
	mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, bad
	}
	return &p, nil
}
func managementActivityAfter(p *managementActivityPosition, source, phase int, timeField, idField string) interface{} {
	if p == nil {
		return nil
	}
	// For an equal timestamp an entire stream can precede/follow the cursor's
	// source/phase rank; only the same stream needs the final ID comparison.
	at := time.UnixMilli(p.At).UTC()
	equal := map[string]interface{}{timeField: at}
	if source < p.Source || (source == p.Source && phase < p.Phase) {
		return map[string]interface{}{timeField: map[string]interface{}{"$lt": at}}
	}
	if source == p.Source && phase == p.Phase {
		equal[idField] = map[string]interface{}{"$lt": p.ReviewID}
	}
	return map[string]interface{}{"$or": []interface{}{map[string]interface{}{timeField: map[string]interface{}{"$lt": at}}, equal}}
}
