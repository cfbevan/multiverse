package activitypub

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	digestPrefix            = "SHA-256="
	signatureHeaderSplitN   = 2
	rsaKeySizeBits          = 2048
	maxBackoffRetryAttempts = 6
)

// SignatureParams contains the parsed HTTP Signature parameters.
type SignatureParams struct {
	KeyID     string
	Algorithm string
	Headers   []string
	Signature string
}

// SignaturePolicy defines the validation rules for inbound HTTP signatures.
type SignaturePolicy struct {
	AllowedAlgorithms map[string]struct{}
	RequiredHeaders   map[string]struct{}
	RequireHost       bool
}

// ParseDigestHeader decodes a Digest: SHA-256 header value.
func ParseDigestHeader(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, digestPrefix) {
		return nil, errors.New("digest header must use SHA-256")
	}
	encoded := strings.TrimPrefix(value, digestPrefix)
	digest, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("digest header is not valid base64: %w", err)
	}
	if len(digest) != sha256.Size {
		return nil, errors.New("digest value has invalid length")
	}

	return digest, nil
}

// ValidateDigest verifies that the body matches the supplied Digest header.
func ValidateDigest(body []byte, digestHeader string) error {
	expected := sha256.Sum256(body)
	provided, err := ParseDigestHeader(digestHeader)
	if err != nil {
		return err
	}
	if !equalBytes(expected[:], provided) {
		return errors.New("digest mismatch")
	}

	return nil
}

// ParseDateHeader parses an HTTP Date header into a UTC time.
func ParseDateHeader(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("missing Date header")
	}

	layouts := []string{time.RFC1123, time.RFC1123Z}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC(), nil
		}
	}

	return time.Time{}, errors.New("invalid Date header")
}

// ValidateDateSkew ensures a date header is within the accepted skew window.
func ValidateDateSkew(date time.Time, now time.Time, maxSkew time.Duration) error {
	delta := now.Sub(date)
	if delta < 0 {
		delta = -delta
	}
	if delta > maxSkew {
		return errors.New("date header outside allowed skew")
	}

	return nil
}

// ParseSignatureHeader parses an HTTP Signature header into typed values.
func ParseSignatureHeader(value string) (SignatureParams, error) {
	params := SignatureParams{}
	value = strings.TrimSpace(value)
	if value == "" {
		return params, errors.New("missing Signature header")
	}

	parts := splitCommaSeparated(value)
	for _, part := range parts {
		pair := strings.SplitN(strings.TrimSpace(part), "=", signatureHeaderSplitN)
		if len(pair) != signatureHeaderSplitN {
			continue
		}
		key := strings.TrimSpace(pair[0])
		val := strings.Trim(strings.TrimSpace(pair[1]), "\"")
		switch key {
		case "keyId":
			params.KeyID = val
		case "algorithm":
			params.Algorithm = val
		case "headers":
			params.Headers = strings.Fields(val)
		case "signature":
			params.Signature = val
		}
	}

	if params.KeyID == "" || params.Signature == "" {
		return params, errors.New("signature header is missing keyId or signature")
	}
	if len(params.Headers) == 0 {
		params.Headers = []string{"date"}
	}
	if params.Algorithm == "" {
		params.Algorithm = "rsa-sha256"
	}

	return params, nil
}

// ValidateSignaturePolicy confirms the signature meets the configured policy.
func ValidateSignaturePolicy(params SignatureParams, policy SignaturePolicy) error {
	algorithm := strings.ToLower(strings.TrimSpace(params.Algorithm))
	if _, ok := policy.AllowedAlgorithms[algorithm]; !ok {
		return fmt.Errorf("signature algorithm %q is not allowed", params.Algorithm)
	}

	seen := make(map[string]struct{}, len(params.Headers))
	for _, h := range params.Headers {
		seen[strings.ToLower(strings.TrimSpace(h))] = struct{}{}
	}

	for required := range policy.RequiredHeaders {
		if _, ok := seen[required]; !ok {
			return fmt.Errorf("missing required signed header %q", required)
		}
	}

	if policy.RequireHost {
		if _, ok := seen["host"]; !ok {
			return errors.New("host must be included in signed headers")
		}
	}

	if _, ok := seen["(request-target)"]; !ok {
		return errors.New("(request-target) must be included in signed headers")
	}

	return nil
}

// BuildSigningString constructs the canonical HTTP Signature string for a request.
func BuildSigningString(r *http.Request, headerNames []string) (string, error) {
	lines := make([]string, 0, len(headerNames))
	for _, h := range headerNames {
		line, err := buildSigningStringLine(r, h)
		if err != nil {
			return "", err
		}
		lines = append(lines, line)
	}

	return strings.Join(lines, "\n"), nil
}

func buildSigningStringLine(r *http.Request, headerName string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(headerName)) {
	case "(request-target)":
		return buildRequestTargetLine(r), nil
	case "host":
		return buildHostLine(r)
	default:
		value := strings.TrimSpace(r.Header.Get(headerName))
		if value == "" {
			return "", fmt.Errorf("missing signed header: %s", headerName)
		}

		return strings.ToLower(strings.TrimSpace(headerName)) + ": " + value, nil
	}
}

func buildRequestTargetLine(r *http.Request) string {
	path := r.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}

	return "(request-target): " + strings.ToLower(r.Method) + " " + path
}

func buildHostLine(r *http.Request) (string, error) {
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	if host == "" {
		return "", errors.New("missing host for signature verification")
	}

	return "host: " + strings.ToLower(host), nil
}

// VerifyRSASignature validates an RSA PKCS#1 signature over the signing string.
func VerifyRSASignature(signingString string, signatureB64 string, publicKeyPEM string) error {
	sig, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil {
		return fmt.Errorf("invalid signature encoding: %w", err)
	}

	publicKey, err := parseRSAPublicKeyPEM(publicKeyPEM)
	if err != nil {
		return err
	}

	hashed := sha256.Sum256([]byte(signingString))

	return rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, hashed[:], sig)
}

// BuildDigestHeader returns the canonical SHA-256 digest header for the body.
func BuildDigestHeader(body []byte) string {
	sum := sha256.Sum256(body)

	return digestPrefix + base64.StdEncoding.EncodeToString(sum[:])
}

// SignRequest adds an HTTP Signature header to an outbound request.
func SignRequest(r *http.Request, body []byte, keyID string, privateKeyPEM string) error {
	privateKey, err := parseRSAPrivateKeyPEM(privateKeyPEM)
	if err != nil {
		return err
	}

	headerNames := []string{"(request-target)", "host", "date", "digest"}
	signingString, err := BuildSigningString(r, headerNames)
	if err != nil {
		return err
	}

	hashed := sha256.Sum256([]byte(signingString))
	sig, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, hashed[:])
	if err != nil {
		return err
	}

	sigValue := base64.StdEncoding.EncodeToString(sig)
	signatureHeader := strings.Join([]string{
		`keyId="` + keyID + `"`,
		`algorithm="rsa-sha256"`,
		`headers="` + strings.Join(headerNames, " ") + `"`,
		`signature="` + sigValue + `"`,
	}, ",")
	r.Header.Set("Signature", signatureHeader)

	return nil
}

// GenerateActorKeyPair creates a new RSA key pair for an ActivityPub actor.
func GenerateActorKeyPair() (string, string, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, rsaKeySizeBits)
	if err != nil {
		return "", "", err
	}

	privateBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}
	privateKeyPEM := string(pem.EncodeToMemory(privateBlock))

	publicASN1, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return "", "", err
	}
	publicBlock := &pem.Block{Type: "PUBLIC KEY", Bytes: publicASN1}
	publicKeyPEM := string(pem.EncodeToMemory(publicBlock))

	return publicKeyPEM, privateKeyPEM, nil
}

// RetryBackoff returns the next retry delay for a failed outbound delivery.
func RetryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > maxBackoffRetryAttempts {
		attempt = maxBackoffRetryAttempts
	}
	seconds := 1 << (attempt - 1)

	return time.Duration(seconds) * time.Minute
}

// ParseTargetPath extracts a URL path and query string without host information.
func ParseTargetPath(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path = path + "?" + u.RawQuery
	}

	return path, nil
}

func splitCommaSeparated(s string) []string {
	parts := []string{}
	var b strings.Builder
	inQuotes := false
	for _, r := range s {
		switch r {
		case '"':
			inQuotes = !inQuotes
			b.WriteRune(r)
		case ',':
			if inQuotes {
				b.WriteRune(r)

				continue
			}
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}

	return parts
}

func parseRSAPublicKeyPEM(value string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		return nil, errors.New("invalid public key pem")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err == nil {
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("public key is not RSA")
		}

		return rsaPub, nil
	}

	pkcs1, err2 := x509.ParsePKCS1PublicKey(block.Bytes)
	if err2 != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	return pkcs1, nil
}

func parseRSAPrivateKeyPEM(value string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		return nil, errors.New("invalid private key pem")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}
	rsaKey, ok := keyAny.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not RSA")
	}

	return rsaKey, nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

// ParseMaxSkewSeconds converts a configured skew value into a time.Duration.
func ParseMaxSkewSeconds(value string, fallback int) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds < 1 {
		seconds = fallback
	}

	return time.Duration(seconds) * time.Second
}
