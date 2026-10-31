package appsso

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // the JKS integrity digest is SHA-1 by format
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf16"
)

// JavaStorePassword is the password of the generated trust store. It
// protects nothing: the store holds public certificates only, and the
// JVM wants a password to check the store's integrity digest (S411).
const JavaStorePassword = "changeit"

// JavaTrustStore is a Java KeyStore (JKS) of trusted certificates: the
// host's public CAs followed by the appliance root. The JVM's trustStore
// property replaces the JDK's own cacerts, so the public CAs travel with
// the root and the app's other HTTPS calls keep working (S411). JKS is
// written here because the standard library cannot encode PKCS12.
func JavaTrustStore(root []byte, now time.Time) ([]byte, error) {
	system, err := systemBundle()
	if err != nil {
		return nil, err
	}
	var ders [][]byte
	for _, src := range [][]byte{system, root} {
		n := len(ders)
		for rest := src; ; {
			var b *pem.Block
			b, rest = pem.Decode(rest)
			if b == nil {
				break
			}
			if b.Type != "CERTIFICATE" {
				continue
			}
			if _, err := x509.ParseCertificate(b.Bytes); err == nil {
				ders = append(ders, b.Bytes)
			}
		}
		if len(ders) == n {
			what := "the host's system CA bundle"
			if bytes.Equal(src, root) {
				what = "the appliance root"
			}
			return nil, fmt.Errorf("appsso: %s holds no readable certificate, so the Java trust store cannot be built", what)
		}
	}
	var buf bytes.Buffer
	w32 := func(v uint32) { _ = binary.Write(&buf, binary.BigEndian, v) }
	utf := func(s string) { _ = binary.Write(&buf, binary.BigEndian, uint16(len(s))); buf.WriteString(s) }
	w32(0xFEEDFEED)
	w32(2)
	w32(uint32(len(ders)))
	ms := uint64(now.UnixMilli())
	for i, der := range ders {
		w32(2) // trusted certificate entry
		alias := fmt.Sprintf("system-%04d", i)
		if i == len(ders)-1 {
			alias = "homestead-root"
		}
		utf(alias)
		_ = binary.Write(&buf, binary.BigEndian, ms)
		utf("X.509")
		w32(uint32(len(der)))
		buf.Write(der)
	}
	var pw []byte
	for _, u := range utf16.Encode([]rune(JavaStorePassword)) {
		pw = append(pw, byte(u>>8), byte(u))
	}
	h := sha1.New() //nolint:gosec
	h.Write(pw)
	h.Write([]byte("Mighty Aphrodite"))
	h.Write(buf.Bytes())
	buf.Write(h.Sum(nil))
	return buf.Bytes(), nil
}

// systemBundle is the host's public CA bundle (first readable path).
func systemBundle() ([]byte, error) {
	for _, p := range SystemBundlePaths {
		if b, err := os.ReadFile(p); err == nil && len(bytes.TrimSpace(b)) > 0 {
			return b, nil
		}
	}
	return nil, fmt.Errorf("appsso: no system CA bundle found (looked in %s), so the combined trust bundle cannot be built", strings.Join(SystemBundlePaths, ", "))
}
