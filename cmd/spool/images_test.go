package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

const (
	testWorkstationRef = "ghcr.io/example/spool-workstation@sha256:1111"
	testEgressRef      = "ghcr.io/example/spool-egress@sha256:2222"
)

func withReleaseImages(t *testing.T, workstation, egress string) {
	t.Helper()
	oldWorkstation, oldEgress := releaseWorkstationImage, releaseEgressImage
	releaseWorkstationImage, releaseEgressImage = workstation, egress
	t.Cleanup(func() { releaseWorkstationImage, releaseEgressImage = oldWorkstation, oldEgress })
}

// A release build prints a pull by digest and a tag to the local name for
// each image, the names the hub runs by default (ADR-0052).
func TestImagesPrintsAPullAndATagForEachImage(t *testing.T) {
	withReleaseImages(t, testWorkstationRef, testEgressRef)
	var stdout, stderr bytes.Buffer
	if code := imagesCommand(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	for _, want := range []string{
		"docker pull " + testWorkstationRef + "\n",
		"docker tag " + testWorkstationRef + " spool-workstation\n",
		"docker pull " + testEgressRef + "\n",
		"docker tag " + testEgressRef + " spool-egress\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout.String())
		}
	}
}

// A build from source names no images, and says where its images come from
// instead of printing commands that would fetch nothing.
func TestImagesFromASourceBuildPointsAtMakeImage(t *testing.T) {
	withReleaseImages(t, "", "")
	var stdout, stderr bytes.Buffer
	if code := imagesCommand(nil, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "make image") {
		t.Fatalf("stdout %q, stderr %q; want nothing, and make image", stdout.String(), stderr.String())
	}
}

func TestLogImageSource(t *testing.T) {
	cases := []struct {
		name    string
		release string
		digests []string
		err     error
		want    string
	}{
		{"the release's image", testWorkstationRef, []string{"other@sha256:9", testWorkstationRef}, nil, "is this release's"},
		{"a local build", testWorkstationRef, nil, nil, "is not this release's"},
		{"no image", testWorkstationRef, nil, errors.New("no such image"), ""},
		{"a source build", "", []string{testWorkstationRef}, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withReleaseImages(t, tc.release, testEgressRef)
			var out bytes.Buffer
			log := slog.New(slog.NewTextHandler(&out, nil))
			logImageSource(context.Background(), log, "spool-workstation", func(context.Context, string) ([]string, error) {
				return tc.digests, tc.err
			})
			if tc.want == "" {
				if out.Len() != 0 {
					t.Fatalf("logged %q, want nothing", out.String())
				}
				return
			}
			if !strings.Contains(out.String(), tc.want) || !strings.Contains(out.String(), "sha256:1111") {
				t.Fatalf("logged %q, want %q and the release's digest", out.String(), tc.want)
			}
		})
	}
}
