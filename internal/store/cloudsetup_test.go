package store

import (
	"os/exec"
	"strings"
	"testing"
)

// The rule on a cloud loop's repos, branch and environment (ADR-0061).
func TestCloudSetupCheck(t *testing.T) {
	good := CloudSetup{Repo: "enes-alatas/spool", Branch: "main", EnvironmentID: "env_01abc"}
	if err := good.Check(); err != nil {
		t.Fatalf("a good setup: %v", err)
	}
	if err := (CloudSetup{}).Check(); err != nil {
		t.Errorf("an empty setup is a loop with none yet: %v", err)
	}
	for _, testCase := range []struct {
		name  string
		setup CloudSetup
		want  string
	}{
		{"a URL, not owner/name", CloudSetup{Repo: "https://github.com/a/b", Branch: "main", EnvironmentID: "env_1"}, "owner/name"},
		{"no owner", CloudSetup{Repo: "/b", Branch: "main", EnvironmentID: "env_1"}, "owner/name"},
		{"a dot name", CloudSetup{Repo: "a/..", Branch: "main", EnvironmentID: "env_1"}, "owner/name"},
		{"a flag branch", CloudSetup{Repo: "a/b", Branch: "-x", EnvironmentID: "env_1"}, "branch"},
		{"no branch", CloudSetup{Repo: "a/b", EnvironmentID: "env_1"}, "branch"},
		{"a bridge ID", CloudSetup{Repo: "a/b", Branch: "main", EnvironmentID: "ccpool_1"}, "environment"},
		{"no environment", CloudSetup{Repo: "a/b", Branch: "main"}, "environment"},
		{"a bad further repo", CloudSetup{Repo: "a/b", Branch: "main", EnvironmentID: "env_1", Repos: []string{"a b/c"}}, "further repo"},
		{"the first repo again", CloudSetup{Repo: "a/b", Branch: "main", EnvironmentID: "env_1", Repos: []string{"A/B"}}, "twice"},
		{"too many", CloudSetup{Repo: "a/b", Branch: "main", EnvironmentID: "env_1", Repos: strings.Split("a/1 a/2 a/3 a/4 a/5 a/6 a/7 a/8 a/9 a/10 a/11", " ")}, "at most"},
	} {
		err := testCase.setup.Check()
		if err == nil || !strings.Contains(err.Error(), testCase.want) {
			t.Errorf("%s: Check() = %v, want an error naming %q", testCase.name, err, testCase.want)
		}
	}
}

func TestValidGitHubRepo(t *testing.T) {
	for repo, want := range map[string]bool{
		"enes-alatas/spool": true, "a/b.c_d-e": true, "A1/.github": true,
		"": false, "a": false, "a/": false, "/a": false, "a/b/c": false, "-a/b": false, "a-/b": false,
		"a--b/c": false, "a/.": false, "a/..": false, "a/b c": false, "a/b;c": false, "a_b/c": false,
		strings.Repeat("a", 40) + "/b": false, "a/" + strings.Repeat("b", 101): false,
	} {
		if got := ValidGitHubRepo(repo); got != want {
			t.Errorf("ValidGitHubRepo(%q) = %v, want %v", repo, got, want)
		}
	}
}

// ValidCloudBranch agrees with git's own check-ref-format --branch, and
// refuses a name starting with "-" besides. "@" alone, which --branch reads as
// the current branch, is refused too.
func TestValidCloudBranchAgreesWithGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	for _, branch := range []string{
		"main", "feat/x", "v1.2", "a.b/c_d", "release-2026", "a@b", "ü",
		"", "-x", "a..b", "a b", "a~b", "a^b", "a:b", "a?b", "a*b", "a[b", `a\b`,
		"/a", "a/", "a//b", "a.", ".a", "a/.b", "a.lock", "a/b.lock", "a@{b", "a\tb", "a\x7fb",
	} {
		got := ValidCloudBranch(branch)
		gitSays := exec.Command(git, "check-ref-format", "--branch", branch).Run() == nil && !strings.HasPrefix(branch, "-")
		if got != gitSays {
			t.Errorf("ValidCloudBranch(%q) = %v; git check-ref-format --branch says %v", branch, got, gitSays)
		}
	}
	if ValidCloudBranch("@") {
		t.Error(`ValidCloudBranch("@") = true, want false`)
	}
}
