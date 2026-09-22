package testercredential

import (
	"encoding/hex"
	"testing"
)

func TestBRTST011HasherKnownRawByteVector(t *testing.T) {
	var raw [32]byte
	for i := range raw {
		raw[i] = byte(i)
	}
	hash := NewTokenHasher().Hash(raw)
	if hex.EncodeToString(hash[:]) != "630dcd2966c4336691125448bbb25b4ff412a49c732db2c8abc1b8581bd710dd" {
		t.Fatal("SHA-256 did not hash the decoded 32-byte input")
	}
}
