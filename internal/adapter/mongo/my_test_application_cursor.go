package mongo

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	cd "iwut-app-center/internal/catalog/domain"
	"iwut-app-center/internal/shared"
)

const myTestCursorKeyDomain = "iwut-app-center/my-test-application-cursor-key/v1"
const myTestCursorMACDomain = "iwut-app-center/my-test-application-cursor-mac/v1\x00"

type MyTestApplicationCursorKey struct {
	value [32]byte
	valid bool
}

func DeriveMyTestApplicationCursorKey(material []byte) (MyTestApplicationCursorKey, error) {
	if len(material) == 0 {
		return MyTestApplicationCursorKey{}, fmt.Errorf("test participation cursor key material is empty")
	}
	mac := hmac.New(sha256.New, material)
	_, _ = mac.Write([]byte(myTestCursorKeyDomain))
	var key MyTestApplicationCursorKey
	copy(key.value[:], mac.Sum(nil))
	key.valid = true
	return key, nil
}

type myTestPosition struct {
	Version int    `json:"v"`
	Caller  string `json:"c"`
	Runtime string `json:"r"`
	Kind    string `json:"k"`
	Last    string `json:"a"`
}
type myTestCursor struct {
	Position myTestPosition `json:"p"`
	MAC      string         `json:"m"`
}

func myTestBinding(caller shared.AuthID, major int32, host []cd.CapabilityName) myTestPosition {
	digest := sha256.Sum256([]byte("iwut-app-center/my-test-application-cursor-caller/v1\x00" + caller.String()))
	return myTestPosition{Version: 1, Caller: base64.RawURLEncoding.EncodeToString(digest[:]), Runtime: catalogFingerprint(major, host), Kind: "MY_TEST_APPLICATIONS"}
}
func (r *MyTestApplicationRepository) encodeCursor(p myTestPosition) (string, error) {
	if !r.cursorKey.valid || !canonicalTesterManagementID(p.Last) {
		return "", cd.ErrMyTestApplicationUnavailable
	}
	payload, _ := json.Marshal(p)
	mac := hmac.New(sha256.New, r.cursorKey.value[:])
	_, _ = mac.Write([]byte(myTestCursorMACDomain))
	_, _ = mac.Write(payload)
	raw, _ := json.Marshal(myTestCursor{p, base64.RawURLEncoding.EncodeToString(mac.Sum(nil))})
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func (r *MyTestApplicationRepository) decodeCursor(token string, bind myTestPosition) (string, error) {
	if token == "" {
		return "", nil
	}
	bad := cd.ErrInvalidMyTestApplicationPageToken
	if len(token) > 4096 || !r.cursorKey.valid {
		return "", bad
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != token {
		return "", bad
	}
	var c myTestCursor
	if json.Unmarshal(raw, &c) != nil {
		return "", bad
	}
	canonical, _ := json.Marshal(c)
	if !bytes.Equal(raw, canonical) {
		return "", bad
	}
	bind.Last = c.Position.Last
	if bind != c.Position || !canonicalTesterManagementID(bind.Last) {
		return "", bad
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(c.MAC)
	if err != nil || len(signature) != 32 || base64.RawURLEncoding.EncodeToString(signature) != c.MAC {
		return "", bad
	}
	payload, _ := json.Marshal(c.Position)
	mac := hmac.New(sha256.New, r.cursorKey.value[:])
	_, _ = mac.Write([]byte(myTestCursorMACDomain))
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return "", bad
	}
	return bind.Last, nil
}
