package secure

import (
	"net"
	"strings"
	"unicode"
)

// zeroSink is invoked through a package-level variable so the compiler
// cannot prove it is side-effect free and elide the zeroing.
var zeroSink = func(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// ZeroBytes best-effort wipes key material from memory after use. Go does
// not guarantee that a value stays only in RAM (registers, GC copies), so
// this is defense in depth, not a hard guarantee — but it removes the
// obvious lingering copies of ECDH secrets and ephemeral private keys.
func ZeroBytes(bufs ...[]byte) {
	for _, b := range bufs {
		if len(b) > 0 {
			zeroSink(b)
		}
	}
}

// ValidHex reports whether s is exactly wantLen hex characters.
func ValidHex(s string, wantLen int) bool {
	if len(s) != wantLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// SanitizeText strips control characters (C0/C1, DEL, CR/LF/tabs included)
// and caps the length — anything that would smuggle newlines into logs,
// terminal output, or the web UI dies here.
func SanitizeText(s string, max int) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if r == 0x7f || (r < 0x20) || (r >= 0x80 && r <= 0x9f) || !unicode.IsPrint(r) {
			continue
		}
		b.WriteRune(r)
		n++
		if n >= max {
			break
		}
	}
	return b.String()
}

// IsLoopbackHost reports whether a URL host (with optional port stripped)
// refers to the loopback interface — where local inference backends live.
func IsLoopbackHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// loopbackIPLiteral returns the IP when a URL host is a literal loopback
// address (127.0.0.0/8, ::1). Empty for hostnames: "localhost" and friends
// are DNS-resolvable, and DNS answers can be faked — strict mode accepts
// only IP literals so nothing is ever resolved.
func LoopbackIPLiteral(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return ip.String()
	}
	return ""
}
