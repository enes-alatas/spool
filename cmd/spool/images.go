package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"

	"github.com/enes-alatas/spool/internal/version"
)

// The images a release was built with, each a registry reference by digest
// (ADR-0052), set by scripts/release-build.sh. A build from
// source leaves them empty: its images are the ones `make image` builds.
var (
	releaseWorkstationImage string
	releaseEgressImage      string
)

// The names the hub runs its images under, whoever built them: a release's
// image is tagged with one after its pull, and `make image` builds them.
const (
	localWorkstationImage = "spool-workstation"
	localEgressImage      = "spool-egress"
)

// imagesCommand is `spool images`: the commands that put this build's
// release images on the host. It prints them rather than running them,
// because the hub never pulls (ADR-0052): fetching is the
// operator's step, done where they can see it.
func imagesCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "spool images: takes no arguments, got %q\n", args[0])
		return 2
	}
	if releaseWorkstationImage == "" || releaseEgressImage == "" {
		fmt.Fprintln(stderr, "spool images: this build names no release images, so it was built from source.\n"+
			"Build the images in the checkout with `make image`.")
		return 1
	}
	fmt.Fprintf(stdout, "# The images %s was built with (ADR-0052). Each is pulled by digest,\n"+
		"# which is its check, and tagged with the name the hub runs.\n", version.Resolve(buildVersion, buildCommit, buildTime).Version)
	for _, image := range []struct{ release, local string }{
		{releaseWorkstationImage, localWorkstationImage},
		{releaseEgressImage, localEgressImage},
	} {
		fmt.Fprintf(stdout, "docker pull %s\ndocker tag %s %s\n", image.release, image.release, image.local)
	}
	return 0
}

// logImageSource says whether the workstation image the hub will run is
// its release's. A local build is as legitimate, the newest Claude Code
// CLI included (#304), so this is information, not a warning: it answers
// "which image is this fleet on" from the log.
func logImageSource(ctx context.Context, log *slog.Logger, image string, repoDigests func(context.Context, string) ([]string, error)) {
	if releaseWorkstationImage == "" {
		return
	}
	digests, err := repoDigests(ctx, image)
	if err != nil {
		return // no daemon or no image: the runtime's own checks report that
	}
	if slices.Contains(digests, releaseWorkstationImage) {
		log.Info("workstation image is this release's", "image", image, "release", digestOf(releaseWorkstationImage))
		return
	}
	log.Info("workstation image is not this release's: a local build, or another release's",
		"image", image, "release", digestOf(releaseWorkstationImage), "hint", "spool images")
}

// digestOf is the sha256:… part of a reference by digest.
func digestOf(reference string) string {
	if _, digest, ok := strings.Cut(reference, "@"); ok {
		return digest
	}
	return reference
}
