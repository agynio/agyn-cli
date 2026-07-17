package local

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"time"
)

// Endpoint is one platform URL exposed by the VM ingress.
type Endpoint struct {
	Name    string `json:"name" yaml:"name"`
	URL     string `json:"url" yaml:"url"`
	Healthy bool   `json:"healthy" yaml:"healthy"`
}

// Endpoints returns the primary platform endpoints for the configured port.
func Endpoints(port int) []Endpoint {
	names := []string{"console", "chat", "tracing"}
	endpoints := make([]Endpoint, 0, len(names))
	for _, name := range names {
		endpoints = append(endpoints, Endpoint{
			Name: name,
			URL:  fmt.Sprintf("https://%s.%s:%d", name, BaseDomain, port),
		})
	}
	return endpoints
}

// WaitForPlatform polls the platform endpoints until they all answer or the
// timeout elapses. The VM reports "started" as soon as the guest boots, but
// the in-cluster ingress takes tens of seconds more to serve traffic.
func WaitForPlatform(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		healthy := 0
		endpoints := CheckEndpoints(Endpoints(port))
		for _, endpoint := range endpoints {
			if endpoint.Healthy {
				healthy++
			}
		}
		if healthy == len(endpoints) {
			return true
		}
		time.Sleep(5 * time.Second)
	}
	return false
}

// CheckEndpoints probes each endpoint. The local CA is typically not in Go's
// trust store, so certificate validation is skipped; reachability and an HTTP
// response are what is being tested.
func CheckEndpoints(endpoints []Endpoint) []Endpoint {
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	checked := make([]Endpoint, len(endpoints))
	for i, endpoint := range endpoints {
		checked[i] = endpoint
		resp, err := client.Get(endpoint.URL)
		if err == nil {
			resp.Body.Close()
			checked[i].Healthy = resp.StatusCode < 500
		}
	}
	return checked
}
