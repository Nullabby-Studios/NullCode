// Package secure — Nullabby Studios trust layer.
//
// Everything that crosses a trust boundary is either signed with Ed25519
// (node identities, studio manifests, per-job results) or sealed inside an
// authenticated encryption channel (all agent<->hub traffic). Verification
// is fail-closed: anything unsigned, re-signed, replayed or reordered is
// rejected.
package secure

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"os"
	"strings"
)

var (
	ErrBadSignature = errors.New("secure: signature verification failed")
	ErrBadKey       = errors.New("secure: malformed key material")
)

// Domain-separation tags. A signature made for one purpose can never be
// replayed for another.
const (
	DomainNodeHandshake = "nullabby-node-handshake-v1:"
	DomainHubHandshake  = "nullabby-hub-handshake-v1:"
	DomainHello         = "nullabby-hello-v1:"
	DomainJobResult     = "nullabby-job-result-v1:"
	KeysInfo            = "nullabby-channel-keys-v1:"
)

// CanonicalJSON marshals v deterministically: Go's encoder emits struct
// fields in declaration order and sorts map keys, so both ends of the
// protocol derive byte-identical input from identical values — required
// for signatures and transcript hashes to line up.
func CanonicalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ---- Ed25519 identities ----

// Identity is an Ed25519 keypair. Studio root identities sign manifests;
// node identities sign handshakes, hellos and job results.
type Identity struct {
	priv ed25519.PrivateKey
}

func NewIdentity() (*Identity, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Identity{priv: priv}, nil
}

func IdentityFromSeed(seed []byte) (*Identity, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, ErrBadKey
	}
	return &Identity{priv: ed25519.NewKeyFromSeed(seed)}, nil
}

func (id *Identity) PublicHex() string {
	return hex.EncodeToString(id.priv.Public().(ed25519.PublicKey))
}

func (id *Identity) Sign(data []byte) string {
	return hex.EncodeToString(ed25519.Sign(id.priv, data))
}

func (id *Identity) SignString(s string) string {
	return id.Sign([]byte(s))
}

// VerifyHex checks an Ed25519 signature in hex against a public key in hex.
// Fail-closed on any malformed input.
func VerifyHex(pubHex, sigHex string, data []byte) bool {
	pub, err := hex.DecodeString(strings.ToLower(strings.TrimSpace(pubHex)))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, err := hex.DecodeString(strings.ToLower(strings.TrimSpace(sigHex)))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pub), data, sig)
}

// ConstHexEqual compares two hex strings in constant time.
func ConstHexEqual(a, b string) bool {
	a, b = strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// ---- identity key files ----

const (
	identityFileMagic       = "nullabby-ed25519-v1:"
	legacyIdentityFileMagic = "nullaby-ed25519-v1:" // pre-rename format, still accepted on load
)

// SaveIdentity writes the seed to path with 0600 permissions.
func SaveIdentity(path string, id *Identity) error {
	seed := id.priv.Seed()
	line := identityFileMagic + hex.EncodeToString(seed)
	return os.WriteFile(path, []byte(line), 0o600)
}

// LoadIdentity reads an identity key written by SaveIdentity.
func LoadIdentity(path string) (*Identity, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	line := strings.TrimSpace(string(b))
	format := identityFileMagic
	if !strings.HasPrefix(line, format) {
		format = legacyIdentityFileMagic
		if !strings.HasPrefix(line, format) {
			return nil, ErrBadKey
		}
	}
	seed, err := hex.DecodeString(strings.TrimPrefix(line, format))
	if err != nil {
		return nil, ErrBadKey
	}
	return IdentityFromSeed(seed)
}

// LoadOrCreateIdentity loads the key at path or mints and persists a new one.
// The bool reports whether a fresh key was created.
func LoadOrCreateIdentity(path string) (*Identity, bool, error) {
	if id, err := LoadIdentity(path); err == nil {
		return id, false, nil
	}
	id, err := NewIdentity()
	if err != nil {
		return nil, false, err
	}
	if err := SaveIdentity(path, id); err != nil {
		return nil, false, err
	}
	return id, true, nil
}

// ---- transcript hashing (compute validation) ----

// Transcript incrementally hashes a generation job: the per-job nonce, the
// prompt hash, and every streamed delta in arrival order. Both the node and
// the hub compute the same value independently, so the node cannot claim a
// result it did not produce and a middleman cannot alter content in flight
// without breaking the node's signed result.
type Transcript struct {
	h hash.Hash
}

func NewTranscript(jobNonce, promptHash string) *Transcript {
	t := &Transcript{h: sha256.New()}
	t.frame('n', []byte(jobNonce))
	t.frame('p', []byte(promptHash))
	return t
}

func (t *Transcript) frame(kind byte, b []byte) {
	var l [8]byte
	binary.BigEndian.PutUint64(l[:], uint64(len(b)))
	t.h.Write([]byte{kind})
	t.h.Write(l[:])
	t.h.Write(b)
}

// Add folds one streamed delta into the running hash. kind is "c" (content)
// or "r" (reasoning).
func (t *Transcript) Add(kind, text string) {
	t.frame(kind[0], []byte(text))
}

// Hex returns the final transcript hash.
func (t *Transcript) Hex() string {
	return hex.EncodeToString(t.h.Sum(nil))
}

// ResultSignature builds the canonical byte string a node signs for one job.
// Both ends must construct it identically.
func ResultSignatureInput(domain, jobNonce, promptHash, transcriptHex string, totalTokens int64) []byte {
	return []byte(fmt.Sprintf("%s%s|%s|%s|%d", domain, jobNonce, promptHash, transcriptHex, totalTokens))
}
