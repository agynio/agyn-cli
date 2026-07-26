package gateway

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"strings"

	"connectrpc.com/connect"
)

type Clients struct {
	HTTPClient *http.Client
	BaseURL    string
}

// Options carries the transport settings a profile contributes.
type Options struct {
	// CAFile is a PEM bundle trusted in addition to the system trust store, so
	// a Gateway served by a private CA is reachable without installing that CA
	// machine-wide.
	CAFile string
}

func NewClients(baseURL, token string, opts Options) (*Clients, error) {
	base := http.DefaultTransport
	if strings.TrimSpace(opts.CAFile) != "" {
		pool, err := CertPool(opts.CAFile)
		if err != nil {
			return nil, err
		}
		transport := cloneDefaultTransport()
		transport.TLSClientConfig = &tls.Config{RootCAs: pool}
		base = transport
	}
	return &Clients{
		HTTPClient: &http.Client{Transport: &authTransport{base: base, token: token}},
		BaseURL:    baseURL,
	}, nil
}

// CertPool returns the system trust store extended with the PEM bundle at path.
// The system roots are kept rather than replaced: a profile's CA is additional
// trust, so the same client still reaches ordinary public endpoints — file
// uploads and downloads go to object storage, not only to the Gateway.
func CertPool(path string) (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("load system trust store: %w", err)
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read CA file %s: %w", path, err)
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no certificates found in CA file %s", path)
	}
	return pool, nil
}

func cloneDefaultTransport() *http.Transport {
	if transport, ok := http.DefaultTransport.(*http.Transport); ok {
		return transport.Clone()
	}
	return &http.Transport{}
}

func (c *Clients) ConnectOpts() []connect.ClientOption {
	return nil // No additional options needed for HTTP/JSON
}

type authTransport struct {
	base  http.RoundTripper
	token string
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if t.token != "" {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	return t.base.RoundTrip(req)
}
