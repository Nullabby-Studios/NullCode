package secure

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

func TestIdentitySignVerify(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	sig := id.SignString("test-data")
	if !VerifyHex(id.PublicHex(), sig, []byte("test-data")) {
		t.Fatal("valid signature rejected")
	}
	if VerifyHex(id.PublicHex(), sig, []byte("test-datum")) {
		t.Fatal("forged data accepted")
	}
	if VerifyHex(id.PublicHex(), strings.Repeat("ab", 64), []byte("test-data")) {
		t.Fatal("garbage signature accepted")
	}
	if VerifyHex("zzzz", sig, []byte("test-data")) {
		t.Fatal("garbage pubkey accepted")
	}
}

func TestIdentitySaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/id.key"
	id, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveIdentity(path, id); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PublicHex() != id.PublicHex() {
		t.Fatal("identity round-trip mismatch")
	}
}

func TestChannelSealOpenAndReplay(t *testing.T) {
	// Simulated handshake: both sides derive the same directional keys.
	ep, epid, err := EphemeralX25519()
	if err != nil {
		t.Fatal(err)
	}
	hp, hpid, err := EphemeralX25519()
	if err != nil {
		t.Fatal(err)
	}
	secretA, err := ECDH(epid, hp)
	if err != nil {
		t.Fatal(err)
	}
	secretB, err := ECDH(hpid, ep)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(secretA, secretB) {
		t.Fatal("ECDH secrets differ")
	}
	a2h, h2a, err := DeriveChannelKeys(secretA, []byte("salt"), ep, hp)
	if err != nil {
		t.Fatal(err)
	}
	a2h2, h2a2, err := DeriveChannelKeys(secretB, []byte("salt"), ep, hp)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a2h, a2h2) || !bytes.Equal(h2a, h2a2) {
		t.Fatal("key derivation not deterministic across peers")
	}

	agentCh, err := NewChannel(a2h, h2a, true)
	if err != nil {
		t.Fatal(err)
	}
	hubCh, err := NewChannel(a2h, h2a, false)
	if err != nil {
		t.Fatal(err)
	}

	// agent -> hub
	msg := []byte(`{"type":"hello","secret":"prompt-and-completion"}`)
	sealed, err := agentCh.Seal(msg)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := hubCh.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, msg) {
		t.Fatal("round-trip mismatch")
	}

	// tampering fails
	tampered := append([]byte{}, sealed...)
	tampered[20] ^= 0xff
	if _, err := hubCh.Open(tampered); err == nil {
		t.Fatal("tampered frame accepted")
	}

	// replay fails
	if _, err := hubCh.Open(sealed); err == nil {
		t.Fatal("replayed frame accepted")
	}

	// reordering fails: seq 3 before seq 2
	s2, _ := agentCh.Seal([]byte("two"))
	s3, _ := agentCh.Seal([]byte("three"))
	if _, err := hubCh.Open(s3); err == nil {
		t.Fatal("out-of-order frame accepted")
	}
	if opened2, err := hubCh.Open(s2); err != nil || string(opened2) != "two" {
		t.Fatal("in-order frame rejected after failed attempt")
	}

	// hub -> agent direction
	m2 := []byte("gen")
	sd, err := hubCh.Seal(m2)
	if err != nil {
		t.Fatal(err)
	}
	if od, err := agentCh.Open(sd); err != nil || !bytes.Equal(od, m2) {
		t.Fatal("reverse direction failed")
	}

	// cross-direction keys must not interoperate
	if _, err := hubCh.Open(sd); err == nil {
		t.Fatal("same-direction double-open accepted")
	}
}

func TestChannelLowOrderPoint(t *testing.T) {
	// All-zero peer public key must be rejected by the stdlib ECDH.
	_, priv, err := EphemeralX25519()
	if err != nil {
		t.Fatal(err)
	}
	zero := make([]byte, 32)
	if _, err := ECDH(priv, zero); err == nil {
		t.Fatal("low-order point accepted")
	}
}

func TestTranscriptDeterminism(t *testing.T) {
	t1 := NewTranscript("nonce123", "phash456")
	t1.Add("c", "hello ")
	t1.Add("c", "world")
	t1.Add("r", "thinking")
	h1 := t1.Hex()

	t2 := NewTranscript("nonce123", "phash456")
	t2.Add("c", "hello ")
	t2.Add("c", "world")
	t2.Add("r", "thinking")
	if h1 != t2.Hex() {
		t.Fatal("transcript hash not deterministic")
	}

	// any reordering or different job binding changes the hash
	t3 := NewTranscript("nonce124", "phash456")
	t3.Add("c", "hello ")
	t3.Add("c", "world")
	t3.Add("r", "thinking")
	if h1 == t3.Hex() {
		t.Fatal("transcript hash ignores job nonce")
	}
	t4 := NewTranscript("nonce123", "phash456")
	t4.Add("c", "helloworld")
	t4.Add("r", "thinking")
	if h1 == t4.Hex() {
		t.Fatal("transcript hash not length-prefixed (concatenation ambiguity)")
	}
}

func TestResultSignature(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	input := string(ResultSignatureInput(DomainJobResult, "n1", "ph", "tr", 42))
	sig := id.SignString(input)
	if !VerifyHex(id.PublicHex(), sig, ResultSignatureInput(DomainJobResult, "n1", "ph", "tr", 42)) {
		t.Fatal("result signature rejected")
	}
	if VerifyHex(id.PublicHex(), sig, ResultSignatureInput(DomainJobResult, "n2", "ph", "tr", 42)) {
		t.Fatal("result signature bound to wrong nonce accepted")
	}
	// domain separation: a handshake signature must not verify as a result
	hsig := id.SignString(DomainNodeHandshake + "challenge")
	if VerifyHex(id.PublicHex(), hsig, ResultSignatureInput(DomainJobResult, "n1", "ph", "tr", 42)) {
		t.Fatal("cross-domain signature replay accepted")
	}
}

func TestManifestSignVerifyTamper(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	m := &Manifest{
		Schema: ManifestSchema, Kind: KindModels, Timestamp: 1234,
		Files: []FileEntry{{Path: "m.gguf", Model: "m", SHA256: "ab", Size: 2}},
	}
	if err := m.Sign(id); err != nil {
		t.Fatal(err)
	}
	if err := m.Verify(id.PublicHex()); err != nil {
		t.Fatal("valid manifest rejected")
	}
	// tamper with the content
	m2 := *m
	m2.Files = []FileEntry{{Path: "m.gguf", Model: "m", SHA256: "cd", Size: 2}}
	if err := m2.Verify(id.PublicHex()); err == nil {
		t.Fatal("tampered manifest accepted")
	}
	// wrong key
	other, _ := NewIdentity()
	if err := m.Verify(other.PublicHex()); err == nil {
		t.Fatal("manifest verified under wrong key")
	}
	// malformed hex sig fails closed
	bad := *m
	bad.Sig = "not-hex!"
	if err := bad.Verify(id.PublicHex()); err == nil {
		t.Fatal("malformed signature accepted")
	}
}

func TestRootPubOverride(t *testing.T) {
	id, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := SetRootPub(id.PublicHex()); err != nil {
		t.Fatal(err)
	}
	defer func() { rootPubOverride = "" }()
	if RootPubHex() != strings.ToLower(id.PublicHex()) {
		t.Fatal("override not applied")
	}
	if err := SetRootPub("short"); err == nil {
		t.Fatal("malformed override accepted")
	}
	m := &Manifest{Schema: ManifestSchema, Kind: KindUpdate, Timestamp: 1, Version: "1"}
	if err := m.Sign(id); err != nil {
		t.Fatal(err)
	}
	if err := m.VerifyWithRoot(); err != nil {
		t.Fatal("manifest failed under overridden root key")
	}
}

func TestHexRoundTrip(t *testing.T) {
	raw := []byte{0, 1, 2, 250, 251, 255}
	enc := hex.EncodeToString(raw)
	if enc != "000102fafbff" {
		t.Fatalf("unexpected hex: %s", enc)
	}
}
