package githubapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// state is carried through GitHub's install redirect so the callback knows which org and
// user started it. It is signed, not encrypted: it holds nothing secret.
type state struct {
	OrgID     uuid.UUID `json:"org_id"`
	UserID    string    `json:"user_id"`
	Nonce     string    `json:"nonce"`
	ReturnTo  string    `json:"return_to"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// stateCodec signs and verifies state tokens of the form base64(payload).base64(hmac).
type stateCodec struct {
	secret []byte
}

func (c stateCodec) encode(s state) (string, error) {
	payload, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("marshal state: %w", err)
	}
	return b64(payload) + "." + b64(c.sign(payload)), nil
}

func (c stateCodec) decode(token string, now time.Time) (state, error) {
	encodedPayload, encodedSig, ok := strings.Cut(token, ".")
	if !ok {
		return state{}, errors.New("invalid state format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(encodedPayload)
	if err != nil {
		return state{}, fmt.Errorf("decode state payload: %w", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(encodedSig)
	if err != nil {
		return state{}, fmt.Errorf("decode state signature: %w", err)
	}
	if !hmac.Equal(sig, c.sign(payload)) {
		return state{}, errors.New("invalid state signature")
	}

	var s state
	if err := json.Unmarshal(payload, &s); err != nil {
		return state{}, fmt.Errorf("unmarshal state: %w", err)
	}
	if now.After(s.ExpiresAt) {
		return state{}, errors.New("state expired")
	}
	s.ReturnTo = SafeReturnTo(s.ReturnTo)
	return s, nil
}

func (c stateCodec) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, c.secret)
	mac.Write(payload)
	return mac.Sum(nil)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
