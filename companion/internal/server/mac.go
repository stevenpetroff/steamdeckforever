package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// AuthMAC returns hex(HMAC-SHA256(key, "auth|" + nonce)) per docs/protocol.md.
// key is the 16 raw secret bytes.
func AuthMAC(key []byte, nonce string) string {
	return macHex(key, authInput(nonce))
}

// SendMAC returns hex(HMAC-SHA256(key, nonce|id|ts|text)) per docs/protocol.md,
// with ts written as a decimal integer.
func SendMAC(key []byte, nonce, id string, ts int64, text string) string {
	return macHex(key, sendInput(nonce, id, ts, text))
}

func authInput(nonce string) string { return "auth|" + nonce }

func sendInput(nonce, id string, ts int64, text string) string {
	return nonce + "|" + id + "|" + strconv.FormatInt(ts, 10) + "|" + text
}

func macBytes(key []byte, input string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(input))
	return m.Sum(nil)
}

func macHex(key []byte, input string) string { return hex.EncodeToString(macBytes(key, input)) }

// verifyMAC checks a client-supplied lowercase hex MAC in constant time.
func verifyMAC(got string, key []byte, input string) bool {
	if len(got) != 2*sha256.Size {
		return false
	}
	b, err := hex.DecodeString(got)
	if err != nil {
		return false
	}
	return hmac.Equal(b, macBytes(key, input))
}
