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

// EnsureImage makes sure the decompressed disk for version/arch exists locally,
// downloading and verifying it if needed. Returns the image directory.
// Progress messages go to progress (may be nil).
func EnsureImage(version, arch string, progress io.Writer) (string, error) {
	dir, err := ImageDir(version, arch)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create image dir: %w", err)
	}

	disk := filepath.Join(dir, DiskFileName(arch))
	if _, err := os.Stat(disk); err == nil {
		return dir, nil
	}

	base := fmt.Sprintf("%s/%s/%s", BaseURL(), version, arch)
	diskXZ := disk + ".xz"

	for _, name := range []string{"checksums.sha256", "metadata.json", "lima.yaml"} {
		if err := download(base+"/"+name, filepath.Join(dir, name), nil); err != nil {
			return "", err
		}
	}

	if !verifyChecksum(dir, filepath.Base(diskXZ)) {
		if err := download(base+"/"+filepath.Base(diskXZ), diskXZ, progress); err != nil {
			return "", err
		}
		if !verifyChecksum(dir, filepath.Base(diskXZ)) {
			return "", fmt.Errorf("checksum mismatch for %s after download", filepath.Base(diskXZ))
		}
	}

	if progress != nil {
		fmt.Fprintln(progress, "Decompressing image (~4.6 GB)...")
	}
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
func download(url, path string, progress io.Writer) error {
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
	if progress != nil {
		writer = io.MultiWriter(out, &progressWriter{
			out:     progress,
			total:   resp.ContentLength + offset,
			written: offset,
			label:   filepath.Base(path),
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

	if progress != nil {
		fmt.Fprintln(progress)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("finalize %s: %w", path, err)
	}
	return nil
}

type progressWriter struct {
	out      io.Writer
	total    int64
	written  int64
	label    string
	lastTick time.Time
}

func (p *progressWriter) Write(data []byte) (int, error) {
	p.written += int64(len(data))
	if time.Since(p.lastTick) >= time.Second {
		p.lastTick = time.Now()
		if p.total > 0 {
			fmt.Fprintf(p.out, "\r%s: %d%% (%d/%d MB)", p.label, p.written*100/p.total, p.written>>20, p.total>>20)
		} else {
			fmt.Fprintf(p.out, "\r%s: %d MB", p.label, p.written>>20)
		}
	}
	return len(data), nil
}
