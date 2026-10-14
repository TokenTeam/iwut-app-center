package mongo

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	pp "iwut-app-center/internal/profile/port"
)

const profileManagementCursorDomain = "iwut-app-center/profile-management-cursor/v1\x00"

type ProfileManagementCursorKey struct {
	value [32]byte
	valid bool
}

func DeriveProfileManagementCursorKey(material []byte) (ProfileManagementCursorKey, error) {
	if len(material) == 0 {
		return ProfileManagementCursorKey{}, fmt.Errorf("profile management cursor key material is empty")
	}
	mac := hmac.New(sha256.New, material)
	_, _ = mac.Write([]byte(profileManagementCursorDomain + "key"))
	key := ProfileManagementCursorKey{valid: true}
	copy(key.value[:], mac.Sum(nil))
	return key, nil
}

type profileManagementPosition struct {
	Version int    `json:"v"`
	Binding string `json:"b"`
	Ordinal int32  `json:"o"`
	ID      string `json:"i"`
}
type profileManagementCursor struct {
	Position profileManagementPosition `json:"p"`
	MAC      string                    `json:"m"`
}

func profileManagementBinding(caller, app, kind, parent, status string, ownership int64) string {
	raw, _ := json.Marshal([]any{caller, app, kind, parent, status, ownership})
	hash := sha256.Sum256(append([]byte(profileManagementCursorDomain+"binding\x00"), raw...))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}
func (r *ProfileManagementRepository) cursorMAC(p profileManagementPosition) string {
	raw, _ := json.Marshal(p)
	mac := hmac.New(sha256.New, r.cursorKey.value[:])
	_, _ = mac.Write([]byte(profileManagementCursorDomain + "mac\x00"))
	_, _ = mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (r *ProfileManagementRepository) encodeCursor(binding string, ordinal int32, id string) (string, error) {
	if !r.cursorKey.valid || ordinal < 1 || !canonicalTesterManagementID(id) {
		return "", pp.ErrProfileManagementStateInconsistent
	}
	p := profileManagementPosition{Version: 1, Binding: binding, Ordinal: ordinal, ID: id}
	raw, _ := json.Marshal(profileManagementCursor{Position: p, MAC: r.cursorMAC(p)})
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func (r *ProfileManagementRepository) decodeCursor(token, binding string) (*profileManagementPosition, error) {
	if token == "" {
		return nil, nil
	}
	bad := pp.ErrInvalidProfileManagementPageToken
	if len(token) > 4096 || !r.cursorKey.valid {
		return nil, bad
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != token {
		return nil, bad
	}
	var c profileManagementCursor
	if json.Unmarshal(raw, &c) != nil {
		return nil, bad
	}
	canonical, _ := json.Marshal(c)
	if !bytes.Equal(raw, canonical) || c.Position.Version != 1 || c.Position.Binding != binding || c.Position.Ordinal < 1 || !canonicalTesterManagementID(c.Position.ID) {
		return nil, bad
	}
	provided, err := base64.RawURLEncoding.Strict().DecodeString(c.MAC)
	if err != nil || len(provided) != 32 || base64.RawURLEncoding.EncodeToString(provided) != c.MAC || !hmac.Equal([]byte(c.MAC), []byte(r.cursorMAC(c.Position))) {
		return nil, bad
	}
	return &c.Position, nil
}
