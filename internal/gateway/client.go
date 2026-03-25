package gateway

import (
	"net/http"

	"connectrpc.com/connect"
)

type Clients struct {
	HTTPClient *http.Client
	BaseURL    string
}

func NewClients(baseURL, token string) *Clients {
	transport := &authTransport{
		base:  http.DefaultTransport,
		token: token,
	}
	return &Clients{
		HTTPClient: &http.Client{Transport: transport},
		BaseURL:    baseURL,
	}
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
