package client

// TLS trust modes (tickets/acp-client-trust-modes-20261029, ACP/1 §4.1): the system
// trust store by default, a cert file, or a "sha256:<hex>" leaf pin (the ext-32
// cert_fingerprint form). A pin replaces the root, never the hostname or validity
// check. Every server here is a local httptest server with a cert minted per test.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// selfSigned mints a self-signed cert for the given SANs and validity window.
func selfSigned(t *testing.T, dns []string, ips []net.IP, notBefore, notAfter time.Time) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "acp-test"},
		NotBefore: notBefore, NotAfter: notAfter, DNSNames: dns, IPAddresses: ips,
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true, // what coordd's self-signed cert is: its own root
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func pinOf(c tls.Certificate) string {
	sum := sha256.Sum256(c.Certificate[0])
	return "sha256:" + hex.EncodeToString(sum[:])
}

// tlsDaemon serves /v1/healthz over TLS with cert; it returns the 127.0.0.1 base URL.
func tlsDaemon(t *testing.T, cert tls.Certificate) string {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","protocol":"acp/1","capabilities":[]}`))
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv.URL
}

var loopback = []net.IP{net.ParseIP("127.0.0.1")}

func healthWith(t *testing.T, base string, o TLSOptions) error {
	t.Helper()
	c, err := NewTLS(base, "tok", "agent", o)
	if err != nil {
		return err
	}
	return c.Health()
}

func TestPinTrustsExactSelfSignedCert(t *testing.T) {
	now := time.Now()
	cert := selfSigned(t, nil, loopback, now.Add(-time.Hour), now.Add(time.Hour))
	base := tlsDaemon(t, cert)
	if err := healthWith(t, base, TLSOptions{Pin: pinOf(cert)}); err != nil {
		t.Fatalf("a correct pin must connect: %v", err)
	}
	if err := healthWith(t, base, TLSOptions{Pin: strings.ToUpper(pinOf(cert))}); err != nil {
		t.Fatalf("the pin is case-insensitive: %v", err)
	}
	// By DNS name (SNI carries the host) as well as by IP (no SNI; the dialed host is used).
	named := selfSigned(t, []string{"localhost"}, nil, now.Add(-time.Hour), now.Add(time.Hour))
	nbase := strings.Replace(tlsDaemon(t, named), "127.0.0.1", "localhost", 1)
	if err := healthWith(t, nbase, TLSOptions{Pin: pinOf(named)}); err != nil {
		t.Fatalf("a correct pin by DNS name must connect: %v", err)
	}
}

func TestPinMismatchRefused(t *testing.T) {
	now := time.Now()
	served := selfSigned(t, nil, loopback, now.Add(-time.Hour), now.Add(time.Hour))
	other := selfSigned(t, nil, loopback, now.Add(-time.Hour), now.Add(time.Hour))
	err := healthWith(t, tlsDaemon(t, served), TLSOptions{Pin: pinOf(other)})
	if err == nil || !strings.Contains(err.Error(), "pin mismatch") {
		t.Fatalf("a wrong pin must be refused with a clear error, got %v", err)
	}
	// Insecure does not weaken a pin.
	if err := healthWith(t, tlsDaemon(t, served), TLSOptions{Pin: pinOf(other), Insecure: true}); err == nil {
		t.Fatal("insecure must not bypass a configured pin")
	}
}

// §4.1: the certificate's SANs MUST include the host the client connects to. A pin
// replaces the root only; dialing a name the cert doesn't list still fails.
func TestPinStillChecksHostname(t *testing.T) {
	now := time.Now()
	cert := selfSigned(t, []string{"coordd.internal"}, nil, now.Add(-time.Hour), now.Add(time.Hour)) // no 127.0.0.1 SAN
	err := healthWith(t, tlsDaemon(t, cert), TLSOptions{Pin: pinOf(cert)})
	if err == nil || !strings.Contains(err.Error(), "does not cover") {
		t.Fatalf("a pinned cert that doesn't cover the dialed host must be refused, got %v", err)
	}
}

func TestPinStillChecksValidity(t *testing.T) {
	now := time.Now()
	expired := selfSigned(t, nil, loopback, now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	err := healthWith(t, tlsDaemon(t, expired), TLSOptions{Pin: pinOf(expired)})
	if err == nil || !strings.Contains(err.Error(), "validity period") {
		t.Fatalf("an expired pinned cert must be refused, got %v", err)
	}
}

func TestPinAddsToCertFile(t *testing.T) {
	now := time.Now()
	cert := selfSigned(t, nil, loopback, now.Add(-time.Hour), now.Add(time.Hour))
	other := selfSigned(t, nil, loopback, now.Add(-time.Hour), now.Add(time.Hour))
	path := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0o600); err != nil {
		t.Fatal(err)
	}
	base := tlsDaemon(t, cert)
	if err := healthWith(t, base, TLSOptions{CertPath: path}); err != nil {
		t.Fatalf("cert-file pinning is unchanged: %v", err)
	}
	if err := healthWith(t, base, TLSOptions{CertPath: path, Pin: pinOf(cert)}); err != nil {
		t.Fatalf("a matching pin on top of the cert file connects: %v", err)
	}
	if err := healthWith(t, base, TLSOptions{CertPath: path, Pin: pinOf(other)}); err == nil {
		t.Fatal("with a cert file, a non-matching pin must still be enforced")
	}
}

func TestMalformedPinRejectedUpFront(t *testing.T) {
	for _, p := range []string{"abc", "sha1:00", "sha256:zz", "sha256:" + strings.Repeat("a", 62)} {
		if _, err := NewTLS("https://127.0.0.1:1", "t", "a", TLSOptions{Pin: p}); err == nil || !strings.Contains(err.Error(), "sha256:<64 hex>") {
			t.Fatalf("pin %q must be rejected with the expected format, got %v", p, err)
		}
	}
}

// The default (system trust store) rejects a self-signed daemon, and the error says
// what to configure instead of a bare x509 message.
func TestSystemRootsRejectSelfSignedWithHint(t *testing.T) {
	now := time.Now()
	cert := selfSigned(t, nil, loopback, now.Add(-time.Hour), now.Add(time.Hour))
	err := healthWith(t, tlsDaemon(t, cert), TLSOptions{})
	if err == nil {
		t.Fatal("a self-signed daemon must not be trusted by the system store")
	}
	if !strings.Contains(err.Error(), "set cert=") || !strings.Contains(err.Error(), "pin=sha256:") {
		t.Fatalf("the trust error must name the fix, got %v", err)
	}
}

// Verifier P3 (2026-10-29): a pin on an http:// URL does nothing, but a malformed one is
// still a configuration error, not silently accepted.
func TestMalformedPinRejectedOnHTTPToo(t *testing.T) {
	if _, err := NewTLS("http://127.0.0.1:1", "t", "a", TLSOptions{Pin: "nonsense"}); err == nil {
		t.Fatal("a malformed pin must be refused even on http://")
	}
}

// The default (no cert, no pin) must ACCEPT a daemon whose cert chains to a trusted root
// — the public-edge case (verifier finding 2026-10-29: only the rejecting half was tested).
// Go loads the system pool once per process, so this re-runs itself as a subprocess with
// SSL_CERT_FILE pointing at a test CA (honoured by Go's system roots on Linux).
func TestSystemRootsAcceptATrustedDaemon(t *testing.T) {
	if base := os.Getenv("ACP_TEST_SYSROOTS_BASE"); base != "" {
		if err := healthWith(t, base, TLSOptions{}); err != nil {
			t.Fatalf("child: a CA-trusted daemon must be accepted with no cert/pin: %v", err)
		}
		return
	}
	if runtime.GOOS != "linux" {
		t.Skip("SSL_CERT_FILE overrides the system roots on Linux only")
	}
	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "acp-test-ca"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leafTmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "acp-test-daemon"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IPAddresses: loopback,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	base := tlsDaemon(t, tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: leafKey})
	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	child := func(certFile string) ([]byte, error) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSystemRootsAcceptATrustedDaemon$", "-test.count=1", "-test.v")
		cmd.Env = append(os.Environ(), "SSL_CERT_FILE="+certFile, "SSL_CERT_DIR="+t.TempDir(), "ACP_TEST_SYSROOTS_BASE="+base)
		return cmd.CombinedOutput()
	}
	out, err := child(caFile)
	if err != nil || !strings.Contains(string(out), "--- PASS: TestSystemRootsAcceptATrustedDaemon") {
		t.Fatalf("with the CA trusted, the child must connect: %v\n%s", err, out)
	}
	// Control: the same child with an EMPTY trust store must refuse (proves it really checks).
	empty := filepath.Join(dir, "none.pem")
	os.WriteFile(empty, nil, 0o600)
	if out, err := child(empty); err == nil {
		t.Fatalf("with no trusted root the child must fail, or this test proves nothing:\n%s", out)
	}
}
