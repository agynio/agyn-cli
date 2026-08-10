package local

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Endpoint is one platform URL exposed by the VM ingress.
type Endpoint struct {
	Name    string `json:"name" yaml:"name"`
	URL     string `json:"url" yaml:"url"`
	Healthy bool   `json:"healthy" yaml:"healthy"`
}

// GatewayURL returns the API endpoint the CLI talks to. It shares the ingress
// port with the browser endpoints; only the hostname differs.
func GatewayURL(port int) string {
	return fmt.Sprintf("https://gateway.%s:%d", BaseDomain, port)
}

// ConsoleURL is where a finished start sends the user.
func ConsoleURL(port int) string {
	return fmt.Sprintf("https://console.%s:%d", BaseDomain, port)
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

// Readiness is the platform's own account of whether it can be used yet.
type Readiness struct {
	// Endpoints answering through the host's forwarded port. The ingress
	// begins serving tens of seconds after the VM reports itself started.
	Endpoints []Endpoint
	// Organization is the id the platform assigned to the organization its
	// release declares. Provisioning completes after the endpoints answer, so
	// an endpoint check alone is not evidence that it has -- and this id is
	// what the profile records and every org-scoped command runs against.
	Organization string
	// Pending names what is not ready yet, for a caller to show.
	Pending []string
}

// Ready reports whether both readiness signals hold.
func (r Readiness) Ready() bool { return len(r.Pending) == 0 }

// Missing describes what is still outstanding, for a timeout message.
func (r Readiness) Missing() string {
	if len(r.Pending) == 0 {
		return ""
	}
	return strings.Join(r.Pending, ", ")
}

// CheckReadiness probes both signals once.
func CheckReadiness(port int) Readiness {
	readiness := Readiness{Endpoints: CheckEndpoints(Endpoints(port))}

	var unhealthy []string
	for _, endpoint := range readiness.Endpoints {
		if !endpoint.Healthy {
			unhealthy = append(unhealthy, endpoint.Name)
		}
	}
	if len(unhealthy) > 0 {
		readiness.Pending = append(readiness.Pending, "endpoints: "+strings.Join(unhealthy, ", "))
		return readiness
	}

	// Only asked once the ingress answers: before that the API server inside
	// the VM is not up either, and the failure would be noise on every poll.
	organization, err := OrganizationID()
	if err != nil || organization == "" {
		readiness.Pending = append(readiness.Pending, "the platform is provisioning its organization")
		return readiness
	}
	readiness.Organization = organization
	return readiness
}

// WaitForReady blocks until the platform can be used, reporting what it is
// waiting on as that changes.
//
// Returning when the endpoints answer is what left credential provisioning
// running against a platform that had not finished starting: the read it needs
// -- the assigned organization id -- lands after the ingress does.
func WaitForReady(port int, timeout time.Duration, progress func(detail string)) (Readiness, error) {
	deadline := time.Now().Add(timeout)
	var readiness Readiness
	for {
		readiness = CheckReadiness(port)
		if readiness.Ready() {
			return readiness, nil
		}
		if time.Now().After(deadline) {
			return readiness, fmt.Errorf("the platform did not become ready within %s (%s)",
				timeout.Round(time.Second), readiness.Missing())
		}
		if progress != nil {
			progress(waitingDetail(readiness))
		}
		time.Sleep(3 * time.Second)
	}
}

// waitingDetail is what to show beside the spinner: the workloads still coming
// up when the VM can be asked, and what is outstanding when it cannot.
func waitingDetail(readiness Readiness) string {
	if ready, total, err := WorkloadProgress(); err == nil && total > 0 && ready < total {
		return fmt.Sprintf("%d of %d workloads ready", ready, total)
	}
	return readiness.Missing()
}

// Workload is one deployment or statefulset and how far its rollout has got.
type Workload struct {
	Name    string
	Ready   int
	Desired int
}

// Rolling reports whether the workload is short of its desired replicas.
func (w Workload) Rolling() bool { return w.Ready < w.Desired }

// Workloads reads the platform's workloads from inside the VM, so a wait can
// say how far along it is rather than only that it is still waiting.
func Workloads() ([]Workload, error) {
	out, err := Shell("sudo", "kubectl", "get", "deploy,statefulset", "-A",
		"-o", `jsonpath={range .items[*]}{.metadata.name}{" "}{.status.readyReplicas}{" "}{.spec.replicas}{"\n"}{end}`)
	if err != nil {
		return nil, err
	}
	return parseWorkloads(out), nil
}

func parseWorkloads(out string) []Workload {
	var workloads []Workload
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// readyReplicas is absent rather than zero on a workload with none, so
		// a two-field line is "name desired" with nothing ready.
		workload := Workload{Name: fields[0]}
		if len(fields) == 2 {
			workload.Desired, _ = strconv.Atoi(fields[1])
		} else {
			workload.Ready, _ = strconv.Atoi(fields[1])
			workload.Desired, _ = strconv.Atoi(fields[2])
		}
		if workload.Desired == 0 {
			continue
		}
		workloads = append(workloads, workload)
	}
	return workloads
}

// WorkloadProgress counts the workloads that have reached their desired
// replica count.
func WorkloadProgress() (ready, total int, err error) {
	workloads, err := Workloads()
	if err != nil {
		return 0, 0, err
	}
	for _, workload := range workloads {
		total++
		if !workload.Rolling() {
			ready++
		}
	}
	return ready, total, nil
}

// RollingWorkloads names the workloads still short of their replicas, for the
// detail line of an upgrade waiting on rollouts.
func RollingWorkloads(limit int) []string {
	workloads, err := Workloads()
	if err != nil {
		return nil
	}
	var rolling []string
	for _, workload := range workloads {
		if workload.Rolling() {
			rolling = append(rolling, workload.Name)
		}
	}
	sort.Strings(rolling)
	if limit > 0 && len(rolling) > limit {
		return append(rolling[:limit:limit], fmt.Sprintf("and %d more", len(rolling)-limit))
	}
	return rolling
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
