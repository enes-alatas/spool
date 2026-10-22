package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// goPlatform is the release archive's platform part for this build, which
// is also the host's: the upgrade runs the binary the unit runs.
func goPlatform() string { return runtime.GOOS + "_" + runtime.GOARCH }

// download gets a release the way OPERATIONS does by hand (ADR-0057): the
// archive and SHA256SUMS over HTTPS into a fresh 0700 directory,
// the sum checked, the attestation checked with gh where it can be, and
// only spool, spool-hook and spool-github taken out of the archive.
func (up *upgrader) download(ctx context.Context, p plan) (candidate, error) {
	tag := p.tag
	if tag == "" {
		latest, err := up.latestRelease(ctx)
		if err != nil {
			return candidate{}, err
		}
		tag = latest
	}
	dir, err := os.MkdirTemp("", "spool-upgrade-")
	if err != nil {
		return candidate{}, err
	}
	cand := candidate{version: tag, cleanup: func() { _ = os.RemoveAll(dir) }}
	name := "spool_" + tag + "_" + up.platform
	archive := filepath.Join(dir, name+".tar.gz")
	base := up.releaseBase + "/download/" + tag + "/"
	up.printf("spool upgrade: downloading %s", base+name+".tar.gz")
	if err := up.fetch(ctx, base+name+".tar.gz", archive, maxArchive); err != nil {
		if errors.Is(err, errNoAsset) {
			err = fmt.Errorf("release %s ships no %s.tar.gz: until a release carries binaries (#747), upgrade with `spool upgrade main` or `--from-source`", tag, name)
		}
		return cand, err
	}
	sums := filepath.Join(dir, "SHA256SUMS")
	if err := up.fetch(ctx, base+"SHA256SUMS", sums, 64<<10); err != nil {
		return cand, err
	}
	if err := checkSum(archive, sums); err != nil {
		return cand, err
	}
	if err := up.attest(ctx, archive); err != nil {
		return cand, err
	}
	if err := extractRelease(archive, name, dir); err != nil {
		return cand, err
	}
	cand.spool, cand.hook = filepath.Join(dir, "spool"), filepath.Join(dir, "spool-hook")
	if _, err := os.Stat(filepath.Join(dir, "spool-github")); err == nil {
		cand.github = filepath.Join(dir, "spool-github")
	}
	cand.pulls = make([]string, 2)
	return cand, nil
}

var errNoAsset = errors.New("no such release asset")

func (up *upgrader) latestRelease(ctx context.Context) (string, error) {
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := up.getJSON(ctx, up.apiBase+"/repos/"+upgradeRepo+"/releases/latest", &release); err != nil {
		return "", fmt.Errorf("finding the latest release: %w", err)
	}
	if !releaseTag.MatchString(release.TagName) {
		return "", fmt.Errorf("the latest release's tag %q isn't vX.Y.Z", release.TagName)
	}
	return release.TagName, nil
}

// fetch writes url's body to dst, refusing one over limit bytes.
func (up *upgrader) fetch(ctx context.Context, url, dst string, limit int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := up.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%s: %w", url, errNoAsset)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%s answered %s", url, resp.Status)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(resp.Body, limit+1))
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil && n > limit {
		err = fmt.Errorf("%s is over %d bytes", url, limit)
	}
	return err
}

// checkSum refuses an archive whose SHA-256 isn't the one SHA256SUMS
// names for it.
func checkSum(archive, sums string) error {
	raw, err := os.ReadFile(sums)
	if err != nil {
		return err
	}
	want := ""
	for line := range strings.SplitSeq(string(raw), "\n") {
		sum, file, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok && strings.TrimLeft(strings.TrimSpace(file), "*") == filepath.Base(archive) {
			want = sum
		}
	}
	if want == "" {
		return fmt.Errorf("SHA256SUMS names no %s", filepath.Base(archive))
	}
	in, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer in.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, in); err != nil {
		return err
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
		return fmt.Errorf("refusing %s: its SHA-256 is %s, and SHA256SUMS says %s", filepath.Base(archive), got, want)
	}
	return nil
}

// attest runs `gh attestation verify` on the archive as the operator who
// ran sudo, with their GitHub login, never root's. Without gh, a sudo user
// or a login the attestation is unchecked: a warning, or a refusal with
// --require-attestation (ADR-0057, operator decision of 2026-10-16).
func (up *upgrader) attest(ctx context.Context, archive string) error {
	unchecked := func(why, fix string) error {
		if up.requireAttestation {
			return fmt.Errorf("the release's attestation can't be checked (%s), and --require-attestation asks for it. Fix: %s", why, fix)
		}
		up.printf("spool upgrade: warning: the release's attestation is unchecked (%s). The checksum catches a broken download, not a swapped release. Fix: %s", why, fix)
		return nil
	}
	gh, err := up.lookGh()
	if err != nil {
		return unchecked("gh isn't installed", "install gh (cli.github.com)")
	}
	operator := up.getenv("SUDO_USER")
	if operator == "" || operator == "root" {
		return unchecked("no SUDO_USER to run gh as", "run it as `sudo spool upgrade` from your own account")
	}
	account, err := user.Lookup(operator)
	if err != nil {
		return unchecked("SUDO_USER "+operator+" isn't a user here", "run it through sudo from your own account")
	}
	env := []string{"HOME=" + account.HomeDir, "PATH=/usr/local/bin:/usr/bin:/bin"}
	if _, err := up.host.Run(ctx, hostCmd{User: operator, Env: env, Name: gh, Args: []string{"auth", "status"}}); err != nil {
		return unchecked("gh has no GitHub login for "+operator, "`gh auth login`, as "+operator)
	}
	dir, err := sharedCopy(archive)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	shared := filepath.Join(dir, filepath.Base(archive))
	if _, err := up.host.Run(ctx, hostCmd{User: operator, Env: env, Name: gh, Args: []string{"attestation", "verify", shared, "--repo", upgradeRepo}}); err != nil {
		return fmt.Errorf("refusing the release: its attestation doesn't verify: %w", err)
	}
	up.printf("spool upgrade: the release's attestation verifies")
	return nil
}

// extractRelease takes spool, spool-hook and spool-github, regular files at
// the top of the archive's one directory, into dir. Any entry that climbs
// out, is absolute, or is a link, any of the three doubled, and spool or
// spool-hook missing, is a refusal. A release from before #1054 has no
// spool-github.
func extractRelease(archive, name, dir string) error {
	in, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer in.Close()
	unzipped, err := gzip.NewReader(in)
	if err != nil {
		return fmt.Errorf("reading %s: %w", filepath.Base(archive), err)
	}
	want := map[string]string{name + "/spool": "spool", name + "/spool-hook": "spool-hook", name + "/spool-github": "spool-github"}
	got := map[string]bool{}
	entries := tar.NewReader(unzipped)
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", filepath.Base(archive), err)
		}
		clean := path.Clean(header.Name)
		if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("refusing %s: entry %q is outside the archive", filepath.Base(archive), header.Name)
		}
		switch header.Typeflag {
		case tar.TypeReg, tar.TypeDir:
		default:
			return fmt.Errorf("refusing %s: entry %q is not a file or a directory", filepath.Base(archive), header.Name)
		}
		file, wanted := want[clean]
		if !wanted || header.Typeflag != tar.TypeReg {
			continue
		}
		if got[file] {
			return fmt.Errorf("refusing %s: it holds %s twice", filepath.Base(archive), file)
		}
		got[file] = true
		out, err := os.OpenFile(filepath.Join(dir, file), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		if err != nil {
			return err
		}
		n, err := io.Copy(out, io.LimitReader(entries, maxArchive+1))
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
		if err == nil && n > maxArchive {
			err = fmt.Errorf("%s is over %d bytes", file, maxArchive)
		}
		if err != nil {
			return err
		}
	}
	for _, file := range want {
		if !got[file] && file != "spool-github" {
			return fmt.Errorf("refusing %s: it holds no %s/%s", filepath.Base(archive), name, file)
		}
	}
	return nil
}
