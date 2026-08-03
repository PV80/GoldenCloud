//go:build integration

package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// bigFileSize is the size of the round-trip test file. One gibibyte by default,
// overridable so a constrained machine can still run the suite.
func bigFileSize(t *testing.T) int64 {
	if v := os.Getenv("GOLDENCLOUD_BIG_FILE_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			t.Fatalf("GOLDENCLOUD_BIG_FILE_BYTES=%q: %v", v, err)
		}
		return n
	}
	return 1 << 30
}

// makeSparseFile creates a file of the given size that occupies almost no disk:
// it is a hole with a recognisable marker at each end, so a truncated or
// misaligned transfer is detected rather than hidden by a sea of zeroes.
func makeSparseFile(t *testing.T, path string, size int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	head := []byte("GOLDENCLOUD-BIG-FILE-HEAD-MARKER")
	tail := []byte("GOLDENCLOUD-BIG-FILE-TAIL-MARKER")
	if _, err := f.WriteAt(head, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(tail, size-int64(len(tail))); err != nil {
		t.Fatal(err)
	}
	// A marker in the middle too, so a transfer that drops an interior chunk
	// and pads with zeroes cannot pass.
	if _, err := f.WriteAt([]byte("GOLDENCLOUD-MIDDLE"), size/2); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// 3. A gigabyte goes up and comes back down unchanged.
func TestLargeFileRoundTrip(t *testing.T) {
	size := bigFileSize(t)
	s := startServer(t, "alice")
	c := s.client("alice")

	src := filepath.Join(t.TempDir(), "big.bin")
	makeSparseFile(t, src, size)
	want := sha256File(t, src)
	t.Logf("uploading %d bytes (%.1f MiB), sha256 %s", size, float64(size)/(1<<20), want[:16])

	f, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	req := c.request(http.MethodPut, "/big.bin", f)
	req.ContentLength = size
	resp := c.do(req)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	f.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("PUT of %d bytes = %d", size, resp.StatusCode)
	}

	stored := filepath.Join(s.userDir("alice"), "big.bin")
	st, err := os.Stat(stored)
	if err != nil {
		t.Fatalf("the uploaded file is not on disk: %v", err)
	}
	if st.Size() != size {
		t.Fatalf("stored size = %d, want %d", st.Size(), size)
	}
	if got := sha256File(t, stored); got != want {
		t.Fatalf("the file on disk differs from the one uploaded:\n got %s\nwant %s", got, want)
	}

	// And back down again.
	getReq := c.request(http.MethodGet, "/big.bin", nil)
	getResp := c.do(getReq)
	if getResp.StatusCode != http.StatusOK {
		getResp.Body.Close()
		t.Fatalf("GET = %d", getResp.StatusCode)
	}
	h := sha256.New()
	n, err := io.Copy(h, getResp.Body)
	getResp.Body.Close()
	if err != nil {
		t.Fatalf("downloading: %v", err)
	}
	if n != size {
		t.Fatalf("downloaded %d bytes, want %d", n, size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		t.Fatalf("download hash mismatch:\n got %s\nwant %s", got, want)
	}

	// A ranged read of the tail marker, which is what a Windows client does
	// when it opens a large file without reading all of it.
	tailStart := size - 32
	code, body := c.call(http.MethodGet, "/big.bin", nil,
		"Range", fmt.Sprintf("bytes=%d-%d", tailStart, size-1))
	if code != http.StatusPartialContent {
		t.Fatalf("ranged GET = %d, want 206", code)
	}
	if body != "GOLDENCLOUD-BIG-FILE-TAIL-MARKER" {
		t.Fatalf("ranged GET returned %q", body)
	}

	if code, _ := c.delete("/big.bin"); code != http.StatusNoContent {
		t.Fatalf("DELETE = %d", code)
	}
	if _, err := os.Stat(stored); !os.IsNotExist(err) {
		t.Fatalf("the file survived DELETE: %v", err)
	}
}
