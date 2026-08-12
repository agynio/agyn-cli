package local

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 30 * time.Minute}

// ResolveVersion turns "latest" into a concrete version via the CDN's
// latest.json pointer. Concrete versions pass through unchanged.
func ResolveVersion(version string) (string, error) {
	if version != "latest" {
		return version, nil
	}

	url := BaseURL() + "/latest.json"
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("resolve latest version: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("resolve latest version: %s returned %s (pin a version with --version)", url, resp.Status)
	}

	var pointer struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&pointer); err != nil {
		return "", fmt.Errorf("parse latest.json: %w", err)
	}
	if pointer.Version == "" {
		return "", fmt.Errorf("latest.json has no version field")
	}
	return pointer.Version, nil
}

// HaveImage reports whether the decompressed disk for version/arch is already
// on this machine, and the directory it lives in either way.
func HaveImage(version, arch string) (string, bool, error) {
	dir, err := ImageDir(version, arch)
	if err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, fmt.Errorf("create image dir: %w", err)
	}
	_, err = os.Stat(filepath.Join(dir, DiskFileName(arch)))
	return dir, err == nil, nil
}

// FetchImage downloads and verifies the compressed disk and the metadata
// beside it, reporting progress through report (which may be nil).
//
// Download and decompression are separate calls because they are separate
// waits: one is minutes of network with a byte count to show, the other is
// minutes of CPU with nothing to show but that it is running.
func FetchImage(version, arch string, report func(detail string)) error {
	dir, _, err := HaveImage(version, arch)
	if err != nil {
		return err
	}

	base := fmt.Sprintf("%s/%s/%s", BaseURL(), version, arch)
	for _, name := range []string{"checksums.sha256", "metadata.json", "lima.yaml"} {
		if err := download(base+"/"+name, filepath.Join(dir, name), nil); err != nil {
			return err
		}
	}

	name := DiskFileName(arch) + ".xz"
	if verifyChecksum(dir, name) {
		return nil
	}
	if err := download(base+"/"+name, filepath.Join(dir, name), report); err != nil {
		return err
	}
	if !verifyChecksum(dir, name) {
		return fmt.Errorf("checksum mismatch for %s after download", name)
	}
	return nil
}

// DecompressImage expands the downloaded archive into the qcow2 disk Lima
// boots, and returns the image directory.
func DecompressImage(version, arch string) (string, error) {
	dir, err := ImageDir(version, arch)
	if err != nil {
		return "", err
	}
	diskXZ := filepath.Join(dir, DiskFileName(arch)+".xz")
	cmd := exec.Command("xz", "--decompress", "--keep", "--force", diskXZ)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("decompress image: %w: %s", err, strings.TrimSpace(string(out)))
	}

	// The compressed artifact is no longer needed once the disk exists.
	_ = os.Remove(diskXZ)
	return dir, nil
}

// verifyChecksum returns true when name exists in dir and matches its entry in
// checksums.sha256.
func verifyChecksum(dir, name string) bool {
	sums, err := os.ReadFile(filepath.Join(dir, "checksums.sha256"))
	if err != nil {
		return false
	}

	var want string
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			want = fields[0]
			break
		}
	}
	if want == "" {
		return false
	}

	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return false
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == want
}

// download fetches url into path atomically (tmp file + rename), resuming a
// previous partial download when the server supports ranges.
func download(url, path string, report func(detail string)) error {
	tmp := path + ".partial"

	var offset int64
	if info, err := os.Stat(tmp); err == nil {
		offset = info.Size()
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("request %s: %w", url, err)
	}
	if offset > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()

	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusOK:
		offset = 0
		flags |= os.O_TRUNC
	default:
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}

	out, err := os.OpenFile(tmp, flags, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", tmp, err)
	}

	var writer io.Writer = out
	if report != nil {
		writer = io.MultiWriter(out, &progressWriter{
			report:  report,
			total:   resp.ContentLength + offset,
			written: offset,
			started: time.Now(),
			// Resumed bytes were not transferred now, so they must not count
			// toward the rate -- a resume would otherwise open by claiming a
			// gigabyte per second and take a minute to settle.
			resumed: offset,
		})
	}

	_, copyErr := io.Copy(writer, resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("download %s: %w", url, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("write %s: %w", tmp, closeErr)
	}

	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("finalize %s: %w", path, err)
	}
	return nil
}

// progressWriter turns the byte stream into the detail line of a step: how much
// of the image has arrived, how fast, and how much longer it will take.
type progressWriter struct {
	report   func(detail string)
	total    int64
	written  int64
	resumed  int64
	started  time.Time
	lastTick time.Time
}

func (p *progressWriter) Write(data []byte) (int, error) {
	p.written += int64(len(data))
	if time.Since(p.lastTick) < 250*time.Millisecond {
		return len(data), nil
	}
	p.lastTick = time.Now()

	elapsed := time.Since(p.started).Seconds()
	rate := float64(p.written-p.resumed) / elapsed
	detail := formatBytes(p.written)
	if p.total > 0 {
		detail += " of " + formatBytes(p.total)
	}
	if elapsed > 1 && rate > 0 {
		detail += fmt.Sprintf("  %s/s", formatBytes(int64(rate)))
		if p.total > p.written {
			remaining := time.Duration(float64(p.total-p.written)/rate) * time.Second
			detail += "  " + formatRemaining(remaining)
		}
	}
	p.report(detail)
	return len(data), nil
}

func formatRemaining(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds left", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm left", int(d.Minutes())+1)
}
