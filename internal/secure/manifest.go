package secure

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Manifest — a studio-signed document describing shipped artifacts.
//
// Kind "models": every model the studio ships, with SHA-256 of the exact
// file. Agents verify local model files against it before advertising a
// model as studio-verified; the hub only routes jobs for verified models.
//
// Kind "update": a released binary, its hash and download URL. Agents
// verify the signature with the pinned studio root key and the binary hash
// before applying any update.
//
// The Sig field covers the canonical JSON of the manifest with Sig empty.
// Tampering with any byte breaks verification.
type Manifest struct {
	Schema    string      `json:"schema"`
	Kind      string      `json:"kind"`
	Version   string      `json:"version,omitempty"`
	URL       string      `json:"url,omitempty"`
	Timestamp int64       `json:"ts"`
	Files     []FileEntry `json:"files,omitempty"`
	Sig       string      `json:"sig,omitempty"`
}

type FileEntry struct {
	Path   string `json:"path"`
	Model  string `json:"model,omitempty"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	// Platform tags the target of a released binary ("linux-amd64",
	// "windows-amd64", ...). Agents select the entry matching their own
	// platform for self-integrity and self-update; legacy manifests
	// without platform tags keep the single-entry fallback.
	Platform string `json:"platform,omitempty"`
}

const (
	ManifestSchema = "nullabby-manifest-v1"
	KindModels     = "models"
	KindUpdate     = "update"
)

// SignBytes returns the canonical bytes that are signed.
func (m *Manifest) SignBytes() ([]byte, error) {
	cp := *m
	cp.Sig = ""
	return CanonicalJSON(&cp)
}

func (m *Manifest) Sign(id *Identity) error {
	b, err := m.SignBytes()
	if err != nil {
		return err
	}
	m.Sig = id.Sign(b)
	return nil
}

// Verify checks the manifest signature against a public key in hex.
func (m *Manifest) Verify(pubHex string) error {
	b, err := m.SignBytes()
	if err != nil {
		return err
	}
	if !VerifyHex(pubHex, m.Sig, b) {
		return ErrBadSignature
	}
	return nil
}

// VerifySelfSigned checks the signature against the studio root key pinned
// into the binary (with optional runtime override already applied).
func (m *Manifest) VerifyWithRoot() error {
	return m.Verify(RootPubHex())
}

func LoadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m.Schema != ManifestSchema {
		return nil, fmt.Errorf("secure: unsupported manifest schema %q", m.Schema)
	}
	return &m, nil
}

func SaveManifest(path string, m *Manifest) error {
	b, err := CanonicalJSON(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// VerifyDir walks every file entry and checks existence, size and SHA-256.
// The returned error names the first mismatch — fail closed, never serve a
// file that does not hash-match the studio manifest. Entries that resolve
// through symlinks or non-regular files are rejected outright: a symlink
// swap at verification time must never pass.
func (m *Manifest) VerifyDir(dir string) error {
	for _, f := range m.Files {
		full := filepath.Join(dir, filepath.Base(f.Path))
		st, err := os.Lstat(full)
		if err != nil {
			return fmt.Errorf("secure: manifest file missing: %s", f.Path)
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("secure: manifest file is not a regular file (symlink?): %s", f.Path)
		}
		if st.Size() != f.Size {
			return fmt.Errorf("secure: manifest size mismatch: %s", f.Path)
		}
		sum, err := fileSHA256(full)
		if err != nil {
			return err
		}
		if !strings.EqualFold(sum, f.SHA256) {
			return fmt.Errorf("secure: manifest hash mismatch: %s", f.Path)
		}
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SHA256File hashes a file (used for self-integrity checks).
func SHA256File(path string) (string, error) { return fileSHA256(path) }

// Models returns the set of studio-shipped model ids declared in the manifest.
func (m *Manifest) Models() []string {
	out := make([]string, 0, len(m.Files))
	for _, f := range m.Files {
		id := f.Model
		if id == "" {
			id = strings.TrimSuffix(f.Path, filepath.Ext(f.Path))
		}
		out = append(out, id)
	}
	return out
}

// AllowsModel reports whether a backend model id refers to a model in this
// manifest. Backend ids commonly carry a quant/tag suffix; matching accepts
// the exact model id, the manifest file stem, or the id up to a ":" tag.
func (m *Manifest) AllowsModel(modelID string) bool {
	return m.EntryForModel(modelID) != nil
}

// EntryForModel resolves a backend model id to the manifest file entry that
// backs it, using the same matching rules as AllowsModel (exact id, id up
// to a ":" tag, or basename stem). nil when the id is not manifest-covered.
func (m *Manifest) EntryForModel(modelID string) *FileEntry {
	if modelID == "" {
		return nil
	}
	base := strings.ToLower(modelID)
	if i := strings.IndexByte(base, ':'); i > 0 {
		base = base[:i]
	}
	for i := range m.Files {
		id := m.Files[i].Model
		if id == "" {
			id = strings.TrimSuffix(m.Files[i].Path, filepath.Ext(m.Files[i].Path))
		}
		lid := strings.ToLower(id)
		stem := strings.ToLower(strings.TrimSuffix(filepath.Base(lid), filepath.Ext(lid)))
		if lid == base || stem == base ||
			strings.ToLower(strings.TrimSuffix(filepath.Base(modelID), filepath.Ext(modelID))) == lid {
			return &m.Files[i]
		}
	}
	return nil
}
