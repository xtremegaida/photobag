package tagger

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// hfBase is the Hugging Face site (tests replace it).
var hfBase = "https://huggingface.co"

// remoteFile is what Hugging Face says about a file before downloading.
type remoteFile struct {
	size int64  // -1 when unknown
	etag string // sha256 for LFS files, the git blob sha1 otherwise
}

func fileURL(repo, name string) string {
	return fmt.Sprintf("%s/%s/resolve/main/%s", hfBase, repo, name)
}

// describe asks for a file's size and hash without following the
// redirect to the download server, whose headers lack them.
func describe(ctx context.Context, repo, name string) (remoteFile, error) {
	rf := remoteFile{size: -1}
	c := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, fileURL(repo, name), nil)
	if err != nil {
		return rf, err
	}
	req.Header.Set("User-Agent", "photobag")
	resp, err := c.Do(req)
	if err != nil {
		return rf, err
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized:
		return rf, fmt.Errorf("%s is not in https://huggingface.co/%s (HTTP %d)", name, repo, resp.StatusCode)
	case resp.StatusCode >= 400:
		return rf, fmt.Errorf("Hugging Face refused %s (HTTP %d)", name, resp.StatusCode)
	}
	if n, err := strconv.ParseInt(resp.Header.Get("X-Linked-Size"), 10, 64); err == nil {
		rf.size = n
	} else if resp.StatusCode == http.StatusOK {
		rf.size = resp.ContentLength
	}
	rf.etag = strings.Trim(strings.TrimPrefix(firstNonEmpty(resp.Header.Get("X-Linked-Etag"), resp.Header.Get("ETag")), "W/"), `"`)
	return rf, nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// verifier checks content against a Hugging Face etag: a sha256 for LFS
// files, the git blob sha1 for others. It is nil when the etag is neither.
func verifier(etag string, size int64) hash.Hash {
	if _, err := hex.DecodeString(etag); err != nil {
		return nil
	}
	switch len(etag) {
	case 64:
		return sha256.New()
	case 40:
		if size < 0 {
			return nil
		}
		h := sha1.New()
		fmt.Fprintf(h, "blob %d\x00", size)
		return h
	}
	return nil
}

// checkFile verifies a downloaded file.
func checkFile(path string, rf remoteFile) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if rf.size >= 0 && st.Size() != rf.size {
		return fmt.Errorf("%s has %d bytes, expected %d", filepath.Base(path), st.Size(), rf.size)
	}
	h := verifier(rf.etag, st.Size())
	if h == nil {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != rf.etag {
		return fmt.Errorf("%s is damaged (checksum %s, expected %s)", filepath.Base(path), got[:12], rf.etag[:12])
	}
	return nil
}

// ModelSize is the size of a repository's model file.
func ModelSize(ctx context.Context, repo string) (int64, error) {
	rf, err := describe(ctx, repo, ModelFile)
	return rf.size, err
}

// Download fetches model.onnx and selected_tags.csv of a Hugging Face
// repository into dir, resuming partial downloads and checking checksums.
func Download(ctx context.Context, repo, dir string, out io.Writer, progress func(file string, done, total int64)) error {
	if out == nil {
		out = io.Discard
	}
	if progress == nil {
		progress = func(string, int64, int64) {}
	}
	for _, name := range []string{TagsFile, ModelFile} {
		rf, err := describe(ctx, repo, name)
		if err != nil {
			return fmt.Errorf("downloading %s: %w", name, err)
		}
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			if err := checkFile(path, rf); err == nil {
				fmt.Fprintf(out, "%s is already downloaded.\n", name)
				continue
			}
			os.Remove(path)
		}
		if rf.size > 0 {
			fmt.Fprintf(out, "Downloading %s from https://huggingface.co/%s (%s)…\n", name, repo, sizeText(rf.size))
		} else {
			fmt.Fprintf(out, "Downloading %s from https://huggingface.co/%s…\n", name, repo)
		}
		if err := fetch(ctx, fileURL(repo, name), path+".part", rf.size, func(done int64) { progress(name, done, rf.size) }); err != nil {
			return fmt.Errorf("downloading %s: %w", name, err)
		}
		if err := checkFile(path+".part", rf); err != nil {
			os.Remove(path + ".part")
			return err
		}
		if err := os.Rename(path+".part", path); err != nil {
			return err
		}
	}
	return nil
}

// fetch downloads url into part, continuing a previous partial download.
func fetch(ctx context.Context, url, part string, size int64, progress func(done int64)) error {
	var have int64
	if st, err := os.Stat(part); err == nil {
		have = st.Size()
		if size >= 0 && have > size {
			os.Remove(part)
			have = 0
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "photobag")
	if have > 0 && (size < 0 || have < size) {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	} else if have > 0 && have == size {
		return nil
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusOK:
		flags |= os.O_TRUNC
		have = 0
	default:
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	buf := make([]byte, 1<<20)
	last := time.Time{}
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				f.Close()
				return err
			}
			have += int64(n)
			if time.Since(last) > 250*time.Millisecond {
				last = time.Now()
				progress(have)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("%w (run the command again to continue)", rerr)
		}
	}
	progress(have)
	if err := f.Close(); err != nil {
		return err
	}
	if size >= 0 && have != size {
		return errors.New("the download ended early (run the command again to continue)")
	}
	return nil
}

func sizeText(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.2f GB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.0f MB", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.0f kB", float64(n)/1e3)
	}
	return fmt.Sprintf("%d bytes", n)
}
