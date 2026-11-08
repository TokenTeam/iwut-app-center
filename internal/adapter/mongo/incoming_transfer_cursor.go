package mongo

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
)

const incomingTransferCursorPurpose = "iwut-app-center/incoming-application-admin-transfers/v1"

type IncomingTransferCursorKey struct {
	value [32]byte
	valid bool
}

func DeriveIncomingTransferCursorKey(material []byte) (IncomingTransferCursorKey, error) {
	if len(material) == 0 {
		return IncomingTransferCursorKey{}, errors.New("incoming transfer cursor secret material is empty")
	}
	mac := hmac.New(sha256.New, material)
	_, _ = mac.Write([]byte(incomingTransferCursorPurpose + "/key"))
	var key IncomingTransferCursorKey
	copy(key.value[:], mac.Sum(nil))
	key.valid = true
	return key, nil
}

type incomingTransferPosition struct {
	Version int    `json:"v"`
	Caller  string `json:"c"`
	Purpose string `json:"p"`
	Millis  int64  `json:"t"`
	ID      string `json:"i"`
}
type incomingTransferCursor struct {
	Position incomingTransferPosition `json:"p"`
	MAC      string                   `json:"m"`
}

func (r *IncomingApplicationAdminTransferRepository) encodeCursor(caller shared.AuthID, last domain.IncomingApplicationAdminTransfer) (string, error) {
	p := incomingTransferPosition{1, incomingTransferCaller(caller), incomingTransferCursorPurpose, last.RequestedAt.UnixMilli(), last.TransferID.String()}
	payload, _ := json.Marshal(p)
	mac := hmac.New(sha256.New, r.key.value[:])
	_, _ = mac.Write([]byte(incomingTransferCursorPurpose + "/mac\x00"))
	_, _ = mac.Write(payload)
	raw, _ := json.Marshal(incomingTransferCursor{p, base64.RawURLEncoding.EncodeToString(mac.Sum(nil))})
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func (r *IncomingApplicationAdminTransferRepository) decodeCursor(token string, caller shared.AuthID) (*incomingTransferPosition, error) {
	if token == "" {
		return nil, nil
	}
	bad := domain.ErrInvalidIncomingTransferPageToken
	if !r.key.valid || len(token) > 4096 {
		return nil, bad
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != token {
		return nil, bad
	}
	var c incomingTransferCursor
	if json.Unmarshal(raw, &c) != nil {
		return nil, bad
	}
	canonical, _ := json.Marshal(c)
	if !bytes.Equal(raw, canonical) {
		return nil, bad
	}
	p := c.Position
	id, err := domain.ParseApplicationAdminTransferID(p.ID)
	t := time.UnixMilli(p.Millis)
	if err != nil || id.String() != p.ID || p.Version != 1 || p.Caller != incomingTransferCaller(caller) || p.Purpose != incomingTransferCursorPurpose || t.IsZero() || t.Year() < 1 || t.Year() > 9999 {
		return nil, bad
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(c.MAC)
	if err != nil || len(signature) != sha256.Size || base64.RawURLEncoding.EncodeToString(signature) != c.MAC {
		return nil, bad
	}
	payload, _ := json.Marshal(p)
	mac := hmac.New(sha256.New, r.key.value[:])
	_, _ = mac.Write([]byte(incomingTransferCursorPurpose + "/mac\x00"))
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, bad
	}
	return &p, nil
}

func incomingTransferCaller(caller shared.AuthID) string {
	sum := sha256.Sum256([]byte(incomingTransferCursorPurpose + "/caller\x00" + caller.String()))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
