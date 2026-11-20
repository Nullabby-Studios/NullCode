package secure

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// ---- TLS pinning ----

// SPKISha256Hex returns the SHA-256 of the DER-encoded SubjectPublicKeyInfo
// of the leaf certificate — the value to pin.
func SPKISha256Hex(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:])
}

// PinnedTLSClientConfig returns a client TLS config that first runs normal
// chain verification, then (when pinHex is non-empty) additionally requires
// the leaf certificate's SPKI SHA-256 to match. When pinHex is set, a
// mismatch or an unparseable chain fails the connection outright.
func PinnedTLSClientConfig(pinHex string) *tls.Config {
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: false,
	}
	if strings.TrimSpace(pinHex) == "" {
		return cfg
	}
	want := strings.ToLower(strings.TrimSpace(pinHex))
	cfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("secure: no server certificate presented")
		}
		leaf, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return fmt.Errorf("secure: cannot parse server certificate: %w", err)
		}
		got := SPKISha256Hex(leaf)
		if !ConstHexEqual(got, want) {
			return fmt.Errorf("secure: server certificate pin mismatch (got %s)", got)
		}
		return nil
	}
	return cfg
}

// PinnedHTTPClient builds an HTTP client whose TLS layer enforces the pin —
// used for manifest and update downloads. Redirects are refused: a hub
// answer must never be able to bounce a download to another host.
func PinnedHTTPClient(pinHex string, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return fmt.Errorf("secure: redirect refused")
		},
		Transport: &http.Transport{
			TLSClientConfig:     PinnedTLSClientConfig(pinHex),
			TLSHandshakeTimeout: 10 * time.Second,
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		},
	}
}

// HardServerTLS builds a server-side TLS config with modern settings.
func HardServerTLS(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
		},
		PreferServerCipherSuites: true,
	}, nil
}

// ---- pinned studio root key ----

// RootPubHexDefault is the studio root Ed25519 public key, pinned into the
// binaries at build time. Rotate by setting STUDIO_ROOT_PUB (hex) at runtime
// — and re-signing all manifests with the matching private key.
var RootPubHexDefault = "ef9b4732d23a8c1e697a2fd3e2bd4434b8e53b2063f98b281dea6362edb964e5"

var rootPubOverride string

// SetRootPub applies a runtime root key override (rotation path). Call once
// at boot, before any manifest verification.
func SetRootPub(hexKey string) error {
	hexKey = strings.ToLower(strings.TrimSpace(hexKey))
	if len(hexKey) != 64 {
		return ErrBadKey
	}
	if _, err := hex.DecodeString(hexKey); err != nil {
		return ErrBadKey
	}
	rootPubOverride = hexKey
	return nil
}

// RootPubHex returns the active studio root public key.
func RootPubHex() string {
	if rootPubOverride != "" {
		return rootPubOverride
	}
	return RootPubHexDefault
}
