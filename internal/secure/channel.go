package secure

import (
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"crypto/sha256"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

var (
	ErrReplay    = errors.New("secure: frame replay or reordering detected")
	ErrSealed    = errors.New("secure: frame failed authentication")
	ErrHandshake = errors.New("secure: key exchange failed")
)

// EphemeralX25519 generates a fresh X25519 keypair.
func EphemeralX25519() (pub []byte, priv []byte, err error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return k.PublicKey().Bytes(), k.Bytes(), nil
}

// ECDH computes the X25519 shared secret. The stdlib rejects low-order and
// all-zero peer points, so invalid Curve25519 inputs fail closed.
func ECDH(priv, peerPub []byte) ([]byte, error) {
	p, err := ecdh.X25519().NewPrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("%w: bad private key", ErrHandshake)
	}
	pp, err := ecdh.X25519().NewPublicKey(peerPub)
	if err != nil {
		return nil, fmt.Errorf("%w: bad peer public key", ErrHandshake)
	}
	secret, err := p.ECDH(pp)
	if err != nil {
		return nil, fmt.Errorf("%w: ecdh rejected peer point", ErrHandshake)
	}
	return secret, nil
}

// DeriveChannelKeys splits the ECDH secret into directional ChaCha20-Poly1305
// keys with HKDF-SHA256, bound to the handshake salt and both ephemeral
// public keys (KCI-resistant). First 32 bytes: agent->hub. Second: hub->agent.
func DeriveChannelKeys(secret, salt, ephAgentPub, ephHubPub []byte) (agentToHub, hubToAgent []byte, err error) {
	bind := make([]byte, 0, len(salt)+len(ephAgentPub)+len(ephHubPub))
	bind = append(bind, salt...)
	bind = append(bind, ephAgentPub...)
	bind = append(bind, ephHubPub...)
	h := hkdf.New(sha256.New, secret, bind, []byte(KeysInfo))
	out := make([]byte, 64)
	if _, err := io.ReadFull(h, out); err != nil {
		return nil, nil, err
	}
	return out[:32], out[32:], nil
}

// Channel is the authenticated encryption channel for one WebSocket
// connection. Frames are sealed with ChaCha20-Poly1305 under monotonically
// increasing counters used as nonces: replay, reordering, truncation and
// tampering all fail closed. Keys are per-connection (forward secrecy: a
// compromised long-term key does not decrypt past sessions).
type Channel struct {
	send    cipher.AEAD
	recv    cipher.AEAD
	sendSeq uint64
	recvSeq uint64
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	return chacha20poly1305.New(key)
}

// NewChannel builds the channel from derived directional keys.
// iAmAgent selects which derived key this side sends with.
func NewChannel(agentToHub, hubToAgent []byte, iAmAgent bool) (*Channel, error) {
	sendK, recvK := hubToAgent, agentToHub
	if iAmAgent {
		sendK, recvK = agentToHub, hubToAgent
	}
	send, err := newAEAD(sendK)
	if err != nil {
		return nil, err
	}
	recv, err := newAEAD(recvK)
	if err != nil {
		return nil, err
	}
	return &Channel{send: send, recv: recv}, nil
}

func nonceFor(seq uint64) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n[4:], seq)
	return n
}

// framePadBucket pads every sealed frame to a fixed multiple so packet
// sizes leak nothing about prompt or completion length: inter-arrival
// timing still shows generation cadence, but traffic-analysis
// fingerprinting of query/response sizes is dead. Both ends MUST run the
// same protocol constant — hub and agent ship the same code.
const framePadBucket = 4096

// Seal encrypts one frame. Wire format: 8-byte big-endian sequence ||
// AEAD(length-prefixed plaintext zero-padded to framePadBucket) — the
// sealed payload is 4-byte big-endian plaintext length || plaintext ||
// zero padding.
func (c *Channel) Seal(plaintext []byte) ([]byte, error) {
	c.sendSeq++
	seq := c.sendSeq
	size := len(plaintext) + 4
	if pad := (size + framePadBucket - 1) / framePadBucket * framePadBucket; pad > size {
		size = pad
	}
	buf := make([]byte, size)
	binary.BigEndian.PutUint32(buf[:4], uint32(len(plaintext)))
	copy(buf[4:], plaintext) // rest stays zero
	out := make([]byte, 8, 8+size+16)
	binary.BigEndian.PutUint64(out[:8], seq)
	out = c.send.Seal(out[:8], nonceFor(seq), buf, nil)
	return out, nil
}

// Open decrypts and authenticates one frame, then strips the length
// prefix and zero padding. The sequence counter must be exactly one more
// than the last received frame — the WebSocket/TCP order is preserved, so
// anything else is an attack.
func (c *Channel) Open(buf []byte) ([]byte, error) {
	if len(buf) < 8+16 {
		return nil, ErrSealed
	}
	seq := binary.BigEndian.Uint64(buf[:8])
	if seq != c.recvSeq+1 {
		return nil, ErrReplay
	}
	padded, err := c.recv.Open(nil, nonceFor(seq), buf[8:], nil)
	if err != nil {
		return nil, ErrSealed
	}
	if len(padded) < 4 {
		return nil, ErrSealed
	}
	n := binary.BigEndian.Uint32(padded[:4])
	if int(n) > len(padded)-4 {
		return nil, ErrSealed
	}
	c.recvSeq = seq
	return padded[4 : 4+n], nil
}
