package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const tokenTTL = 7 * 24 * time.Hour

// tokenHeader is the fixed header for all issued tokens.
var tokenHeader = mustBase64JSON(map[string]string{"alg": "HS256"})

func mustBase64JSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

type payload struct {
	Sub string `json:"sub"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
}

// Issue produces a signed HMAC-SHA256 token for the given owner UUID.
// The token expires after 7 days.
func (s *Service) Issue(ownerID uuid.UUID) (string, error) {
	return s.issueWithExpiry(ownerID, time.Now().Add(tokenTTL))
}

// issueWithExpiry produces a signed token with an explicit expiry time.
// It is intended for internal use and testing only.
func (s *Service) issueWithExpiry(ownerID uuid.UUID, exp time.Time) (string, error) {
	now := time.Now()
	p := payload{
		Sub: ownerID.String(),
		Iat: now.Unix(),
		Exp: exp.Unix(),
	}

	pb, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	payloadPart := base64.RawURLEncoding.EncodeToString(pb)

	unsigned := tokenHeader + "." + payloadPart
	sig := s.sign(unsigned)

	return unsigned + "." + sig, nil
}

// Validate parses and verifies a token string, returning the Claims on success.
// It returns an error if the token is malformed, the signature does not match,
// or the token has expired.
func (s *Service) Validate(token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, errors.New("auth: malformed token")
	}

	unsigned := parts[0] + "." + parts[1]
	expectedSig := s.sign(unsigned)
	if !hmac.Equal([]byte(parts[2]), []byte(expectedSig)) {
		return Claims{}, errors.New("auth: invalid token signature")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, errors.New("auth: malformed token payload")
	}

	var p payload
	if err := json.Unmarshal(payloadBytes, &p); err != nil {
		return Claims{}, errors.New("auth: malformed token payload")
	}

	if time.Now().Unix() > p.Exp {
		return Claims{}, errors.New("auth: token expired")
	}

	id, err := uuid.Parse(p.Sub)
	if err != nil {
		return Claims{}, errors.New("auth: invalid subject in token")
	}

	return Claims{OwnerID: id}, nil
}

// sign returns the base64url-encoded HMAC-SHA256 signature of data.
func (s *Service) sign(data string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
