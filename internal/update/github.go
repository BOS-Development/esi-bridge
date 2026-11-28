package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Release is a published release newer than the running build.
type Release struct {
	Version string
	// URL is the release's GitHub page, for "download it yourself" fallbacks.
	URL   string
	Asset Asset
}

// Asset is the release's installer for this platform.
type Asset struct {
	Name string
	URL  string
	Size int64
	// SHA256 is the hex digest GitHub computed for the uploaded asset. A
	// download that doesn't match it is never installed.
	SHA256 string
}

// maxAssetSize bounds a download, so a bogus size can't fill the disk.
const maxAssetSize = 512 << 20

type ghRelease struct {
	TagName    string    `json:"tag_name"`
	HTMLURL    string    `json:"html_url"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	// Digest is "sha256:<hex>", computed by GitHub when the asset is uploaded.
	Digest string `json:"digest"`
}

// assetSuffix is the name ending of this platform's installer, as built by
// scripts/package-*.  Empty when there's no installer for this platform.
func assetSuffix(goos string) string {
	switch goos {
	case "windows":
		return "-windows-x64-setup.exe"
	case "darwin":
		return "-macos-universal.dmg"
	}
	return ""
}

// fetchLatest asks GitHub for the repo's latest (non-draft, non-prerelease)
// release.
func (u *Updater) fetchLatest(ctx context.Context) (*ghRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", u.apiBaseURL, u.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", u.userAgent())

	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("check for updates: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("check for updates: no published releases found")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("check for updates: GitHub returned %s", resp.Status)
	}
	var rel ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("check for updates: decode release: %w", err)
	}
	return &rel, nil
}

// newerRelease returns rel as a Release if it's newer than current and has an
// installer for goos, or nil if the running build is up to date.
func newerRelease(rel *ghRelease, current version, goos string) (*Release, error) {
	if rel.Draft || rel.Prerelease {
		return nil, nil
	}
	latest, err := parseVersion(rel.TagName)
	if err != nil {
		return nil, fmt.Errorf("latest release tag %q: %w", rel.TagName, err)
	}
	if latest.compare(current) <= 0 {
		return nil, nil
	}
	out := &Release{Version: latest.String(), URL: rel.HTMLURL}
	if suffix := assetSuffix(goos); suffix != "" {
		for _, a := range rel.Assets {
			if strings.HasSuffix(a.Name, suffix) {
				out.Asset = Asset{
					Name:   a.Name,
					URL:    a.BrowserDownloadURL,
					Size:   a.Size,
					SHA256: strings.ToLower(strings.TrimPrefix(a.Digest, "sha256:")),
				}
				break
			}
		}
	}
	return out, nil
}

// download fetches a's file into dir, verifying its size and SHA-256 against
// what the release listing reported, and returns the local path. progress is
// called with the bytes received so far.
func (u *Updater) download(ctx context.Context, a Asset, dir string, progress func(done, total int64)) (string, error) {
	if a.URL == "" {
		return "", errors.New("no installer for this platform in the release")
	}
	if len(a.SHA256) != sha256.Size*2 {
		return "", errors.New("release asset has no SHA-256 digest; refusing to install it")
	}
	if a.Size <= 0 || a.Size > maxAssetSize {
		return "", fmt.Errorf("release asset has an implausible size (%d bytes)", a.Size)
	}
	// The asset name ends up in a file path; keep it to a plain file name.
	name := filepath.Base(a.Name)
	if name != a.Name || name == "." || name == ".." {
		return "", fmt.Errorf("unexpected asset name %q", a.Name)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", u.userAgent())
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := u.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download update: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download update: server returned %s", resp.Status)
	}

	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	pw := &progressWriter{total: a.Size, fn: progress}
	n, err := io.Copy(io.MultiWriter(f, h, pw), io.LimitReader(resp.Body, a.Size+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n != a.Size {
		err = fmt.Errorf("download update: got %d bytes, expected %d", n, a.Size)
	}
	if err == nil {
		if got := hex.EncodeToString(h.Sum(nil)); got != a.SHA256 {
			err = fmt.Errorf("download update: SHA-256 mismatch (got %s, expected %s)", got, a.SHA256)
		}
	}
	if err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

func (u *Updater) userAgent() string {
	return "esi-bridge-updater/" + u.current.String() + " (" + runtime.GOOS + ")"
}

type progressWriter struct {
	done, total int64
	fn          func(done, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if p.fn != nil {
		p.fn(p.done, p.total)
	}
	return len(b), nil
}
