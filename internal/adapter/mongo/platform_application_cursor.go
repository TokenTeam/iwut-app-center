package mongo

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
)

const platformApplicationCursorPurpose = "iwut-app-center/platform-application-directory/v1"

type PlatformApplicationCursorKey struct {
	value [32]byte
	valid bool
}

func DerivePlatformApplicationCursorKey(material []byte) (PlatformApplicationCursorKey, error) {
	if len(material) == 0 {
		return PlatformApplicationCursorKey{}, errors.New("platform application cursor secret material is empty")
	}
	mac := hmac.New(sha256.New, material)
	_, _ = mac.Write([]byte(platformApplicationCursorPurpose + "/key"))
	var key PlatformApplicationCursorKey
	copy(key.value[:], mac.Sum(nil))
	key.valid = true
	return key, nil
}

type platformApplicationPosition struct {
	Version int    `json:"v"`
	Caller  string `json:"c"`
	Purpose string `json:"p"`
	NameKey string `json:"n"`
	Filter  string `json:"f"`
	ID      string `json:"i"`
}
type platformApplicationCursor struct {
	Position platformApplicationPosition `json:"p"`
	MAC      string                      `json:"m"`
}

func (r *PlatformApplicationQueryRepository) encodeCursor(caller shared.AuthID, q domain.PlatformApplicationQuery, last domain.PlatformApplicationSummary) (string, error) {
	name, _ := domain.NewApplicationName(last.Name)
	p := platformApplicationPosition{Version: 1, Caller: platformApplicationCaller(caller), Purpose: platformApplicationCursorPurpose, NameKey: name.Key(), Filter: platformApplicationFilter(q), ID: last.ApplicationID.String()}
	payload, _ := json.Marshal(p)
	mac := hmac.New(sha256.New, r.key.value[:])
	_, _ = mac.Write([]byte(platformApplicationCursorPurpose + "/mac\x00"))
	_, _ = mac.Write(payload)
	raw, _ := json.Marshal(platformApplicationCursor{p, base64.RawURLEncoding.EncodeToString(mac.Sum(nil))})
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func (r *PlatformApplicationQueryRepository) decodeCursor(token string, caller shared.AuthID, q domain.PlatformApplicationQuery) (*platformApplicationPosition, error) {
	if token == "" {
		return nil, nil
	}
	bad := domain.ErrInvalidPlatformApplicationPageToken
	if !r.key.valid || len(token) > 4096 {
		return nil, bad
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != token {
		return nil, bad
	}
	var c platformApplicationCursor
	if json.Unmarshal(raw, &c) != nil {
		return nil, bad
	}
	canonical, _ := json.Marshal(c)
	if !bytes.Equal(raw, canonical) {
		return nil, bad
	}
	p := c.Position
	id, ok := shared.ParseApplicationID(p.ID)
	name, nameErr := domain.NewApplicationName(p.NameKey)
	if !ok || id.String() != p.ID || nameErr != nil || name.Key() != p.NameKey || p.Filter != platformApplicationFilter(q) || p.Version != 1 || p.Caller != platformApplicationCaller(caller) || p.Purpose != platformApplicationCursorPurpose {
		return nil, bad
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(c.MAC)
	if err != nil || len(signature) != sha256.Size || base64.RawURLEncoding.EncodeToString(signature) != c.MAC {
		return nil, bad
	}
	payload, _ := json.Marshal(p)
	mac := hmac.New(sha256.New, r.key.value[:])
	_, _ = mac.Write([]byte(platformApplicationCursorPurpose + "/mac\x00"))
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, bad
	}
	return &p, nil
}

func platformApplicationCaller(caller shared.AuthID) string {
	sum := sha256.Sum256([]byte(platformApplicationCursorPurpose + "/caller\x00" + caller.String()))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func platformApplicationFilter(q domain.PlatformApplicationQuery) string {
	raw, _ := json.Marshal(struct {
		ApplicationID, AdminAuthID, NamePrefix string
		Lifecycle, Availability, Archive       []string
	}{q.ApplicationID, q.AdminAuthID, q.NamePrefix, q.LifecycleStatuses, q.PlatformAvailabilityStatuses, q.ArchiveStatuses})
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
