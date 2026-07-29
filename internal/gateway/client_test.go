package gateway

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCertPoolAddsToSystemRoots(t *testing.T) {
	path, certificate := writeTestCA(t)

	pool, err := CertPool(path)
	if err != nil {
		t.Fatalf("build cert pool: %v", err)
	}
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
		t.Fatalf("expected the CA to be trusted: %v", err)
	}

	// The system roots have to survive: a profile's CA is additional trust, not
	// a replacement, and the same client still reaches public endpoints.
	system, err := x509.SystemCertPool()
	if err != nil {
		t.Skipf("system trust store unavailable: %v", err)
	}
	if !pool.Equal(appendTo(t, system, path)) {
		t.Fatal("expected the pool to be the system roots plus the profile CA")
	}
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: system, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err == nil {
		t.Fatal("expected the test CA to be untrusted by the system roots alone")
	}
}

func TestCertPoolRejectsUnusableFiles(t *testing.T) {
	if _, err := CertPool(filepath.Join(t.TempDir(), "missing.crt")); err == nil {
		t.Fatal("expected a missing CA file to be an error")
	}

	path := filepath.Join(t.TempDir(), "empty.crt")
	if err := os.WriteFile(path, []byte("not a certificate\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := CertPool(path); err == nil {
		t.Fatal("expected a file with no certificates to be an error")
	}
}

func TestNewClientsWithoutCAKeepsDefaultTransport(t *testing.T) {
	clients, err := NewClients("https://gateway.example", "token", Options{})
	if err != nil {
		t.Fatalf("build clients: %v", err)
	}
	transport, ok := clients.HTTPClient.Transport.(*authTransport)
	if !ok {
		t.Fatalf("unexpected transport %T", clients.HTTPClient.Transport)
	}
	if transport.base != http.DefaultTransport {
		t.Fatal("expected the default transport to be reused when no CA is configured")
	}
}

func TestNewClientsRejectsUnreadableCA(t *testing.T) {
	if _, err := NewClients("https://gateway.example", "token", Options{CAFile: filepath.Join(t.TempDir(), "missing.crt")}); err == nil {
		t.Fatal("expected an unreadable CA file to fail client construction")
	}
}

func appendTo(t *testing.T, pool *x509.CertPool, path string) *x509.CertPool {
	t.Helper()
	extended := pool.Clone()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read CA: %v", err)
	}
	if !extended.AppendCertsFromPEM(data) {
		t.Fatal("append CA")
	}
	return extended
}

// writeTestCA writes a throwaway self-signed CA to a file and returns both the
// path and the parsed certificate.
func writeTestCA(t *testing.T) (string, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "agyn test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	path := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	return path, certificate
}
