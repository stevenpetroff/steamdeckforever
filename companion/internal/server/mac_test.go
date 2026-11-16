package server

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// The vectors in testdata/mac_vectors.json were generated with Python's hmac
// module straight from docs/protocol.md, independently of this Go code. The
// phone page's TypeScript MAC code should be checked against the same file.
type macVector struct {
	Name    string `json:"name"`
	Secret  string `json:"secret"`
	Nonce   string `json:"nonce"`
	AuthMAC string `json:"auth_mac"`
	ID      string `json:"id"`
	TS      int64  `json:"ts"`
	Text    string `json:"text"`
	SendMAC string `json:"send_mac"`
}

func TestMACVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/mac_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vs []macVector
	if err := json.Unmarshal(raw, &vs); err != nil {
		t.Fatal(err)
	}
	if len(vs) != 3 {
		t.Fatalf("want 3 vectors, got %d", len(vs))
	}
	for _, v := range vs {
		key, err := hex.DecodeString(v.Secret)
		if err != nil || len(key) != 16 {
			t.Fatalf("%s: bad secret", v.Name)
		}
		if got := AuthMAC(key, v.Nonce); got != v.AuthMAC {
			t.Errorf("%s: AuthMAC = %s, want %s", v.Name, got, v.AuthMAC)
		}
		if got := SendMAC(key, v.Nonce, v.ID, v.TS, v.Text); got != v.SendMAC {
			t.Errorf("%s: SendMAC = %s, want %s", v.Name, got, v.SendMAC)
		}
		if !verifyMAC(v.AuthMAC, key, "auth|"+v.Nonce) {
			t.Errorf("%s: verifyMAC rejected a valid auth MAC", v.Name)
		}
		if verifyMAC(v.AuthMAC[:62]+"00", key, "auth|"+v.Nonce) && v.AuthMAC[62:] != "00" {
			t.Errorf("%s: verifyMAC accepted a modified MAC", v.Name)
		}
		if verifyMAC("", key, "auth|"+v.Nonce) || verifyMAC("zz", key, "auth|"+v.Nonce) {
			t.Errorf("%s: verifyMAC accepted garbage", v.Name)
		}
	}
}

// Same vector as phone/src/hmac.test.ts, computed independently in Python.
func TestPasteMACVector(t *testing.T) {
	key := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	got := PasteMAC(key, "00112233445566778899aabbccddeeff", "abc123def4567890", 1763143331204, "!WA:2!héllo\nline two")
	if want := "23c1c9a65ed4c80e43510ff214091da51d7cbf3ff2f5bbc00d47285c5b87a8d4"; got != want {
		t.Fatalf("PasteMAC = %s, want %s", got, want)
	}
}
