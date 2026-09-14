// Package scripts tests the release scripts: they are what stands between a tag and a
// published release whose manifest, archive names or binary disagree.
package scripts

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// manifestVersion reads plugin.yaml's version, so these tests follow the manifest rather
// than hard-coding a release number that the next bump would silently falsify.
func manifestVersion(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../plugin.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "version:"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	t.Fatal("plugin.yaml has no top-level version: line")
	return ""
}

func run(t *testing.T, env []string, script string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestReleaseCheckPassesWhenTagManifestAndBinaryAgree(t *testing.T) {
	v := manifestVersion(t)
	out, err := run(t, nil, "release-check", "v"+v)
	if err != nil {
		t.Fatalf("release-check v%s failed: %v\n%s", v, err, out)
	}
	if !strings.Contains(out, "all say "+v) {
		t.Errorf("release-check did not confirm the agreement:\n%s", out)
	}
}

// TestReleaseCheckRefusesATagTheManifestDoesNotName. Judging a release by a manifest that
// describes a different version is the mistake infrata PLAN.md §31.2 exists to prevent.
func TestReleaseCheckRefusesATagTheManifestDoesNotName(t *testing.T) {
	v := manifestVersion(t)
	out, err := run(t, nil, "release-check", "v99.0.0")
	if err == nil {
		t.Fatalf("release-check accepted v99.0.0 against a manifest saying %s:\n%s", v, out)
	}
	for _, want := range []string{"99.0.0", v} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, out)
		}
	}
}

// TestReleaseCheckAcceptsACRLFManifest. A checkout with CRLF line endings must not block a
// genuine release: the manifest's version still agrees, a trailing \r is not a disagreement.
func TestReleaseCheckAcceptsACRLFManifest(t *testing.T) {
	v := manifestVersion(t)
	data, err := os.ReadFile("../plugin.yaml")
	if err != nil {
		t.Fatal(err)
	}
	crlf := strings.ReplaceAll(string(data), "\n", "\r\n")
	manifest := filepath.Join(t.TempDir(), "plugin.yaml")
	if err := os.WriteFile(manifest, []byte(crlf), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, []string{"FAKE_MANIFEST=" + manifest}, "release-check", "v"+v)
	if err != nil {
		t.Fatalf("release-check refused a CRLF manifest that agrees with the tag: %v\n%s", err, out)
	}
	if !strings.Contains(out, "all say "+v) {
		t.Errorf("release-check did not confirm the agreement:\n%s", out)
	}
}

// TestReleaseCheckRefusesAManifestProtocolTheBinaryDoesNotSpeak. `protocol:` is what this
// release's binary speaks (infrata PLAN.md §31.2), which for an SDK-built binary is exactly one
// version. Each case keeps the version agreeing, so only the protocol can be the refusal: the
// previous release's [1], left behind after an infrata require bump, and the host's whole
// Supported set, [2, 1], which is the wrong answer §31.2's amendment was written against.
func TestReleaseCheckRefusesAManifestProtocolTheBinaryDoesNotSpeak(t *testing.T) {
	v := manifestVersion(t)
	data, err := os.ReadFile("../plugin.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var current string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "protocol:") {
			current = line
		}
	}
	if current == "" {
		t.Fatal("plugin.yaml has no top-level protocol: line to doctor")
	}
	for _, doctored := range []string{"protocol: [1]", "protocol: [2, 1]", "protocol: [99]"} {
		t.Run(doctored, func(t *testing.T) {
			if doctored == current {
				t.Fatalf("plugin.yaml already says %q, so this case changes nothing", doctored)
			}
			manifest := filepath.Join(t.TempDir(), "plugin.yaml")
			if err := os.WriteFile(manifest, []byte(strings.Replace(string(data), current, doctored, 1)), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := run(t, []string{"FAKE_MANIFEST=" + manifest}, "release-check", "v"+v)
			if err == nil {
				t.Fatalf("release-check accepted %q against the binary's handshake:\n%s", doctored, out)
			}
			if !strings.Contains(out, "speaks protocol") {
				t.Errorf("the refusal does not say which protocol the binary speaks:\n%s", out)
			}
		})
	}
}

// TestReleaseCheckRefusesABinaryThatDoesNotKnowItsVersion. `go build -X` on a symbol that
// does not exist is silently ignored, so a renamed Version variable would ship every
// archive reporting 0.0.0-dev. This simulates exactly that drift.
func TestReleaseCheckRefusesABinaryThatDoesNotKnowItsVersion(t *testing.T) {
	v := manifestVersion(t)
	out, err := run(t,
		[]string{"FAKE_VERSION_SYMBOL=github.com/infrata/infrata-provider-fake/internal/fake.NoSuchVariable"},
		"release-check", "v"+v)
	if err == nil {
		t.Fatalf("release-check passed with a binary whose version was never stamped:\n%s", out)
	}
	if !strings.Contains(out, "0.0.0-dev") {
		t.Errorf("the refusal does not say what the binary reported:\n%s", out)
	}
}

// TestBuildReleaseNamesArchivesByTheInstallConvention. `infrata plugins install` constructs
// the download name rather than reading it (infrata PLAN.md §31.2), so a wrong name is an uninstallable release.
func TestBuildReleaseNamesArchivesByTheInstallConvention(t *testing.T) {
	v := manifestVersion(t)
	dist := t.TempDir()
	if out, err := run(t, []string{"PLATFORMS=linux/amd64 windows/amd64"}, "build-release", v, dist); err != nil {
		t.Fatalf("build-release failed: %v\n%s", err, out)
	}
	stem := "infrata-plugin-fake_" + v + "_linux_amd64"
	for _, name := range []string{stem + ".tar.gz", "infrata-plugin-fake_" + v + "_windows_amd64.zip"} {
		if _, err := os.Stat(filepath.Join(dist, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}

	f, err := os.Open(filepath.Join(dist, stem+".tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got[h.Name] = true
	}
	for _, want := range []string{stem + "/infrata-plugin-fake", stem + "/plugin.yaml", stem + "/README.md"} {
		if !got[want] {
			t.Errorf("%s.tar.gz does not contain %s; it holds %v", stem, want, got)
		}
	}
}

// moduleCopy writes a go.mod and go.sum into a fresh directory for FAKE_MODULE_DIR, so a test
// can doctor them without touching the repository's own.
func moduleCopy(t *testing.T, gomod, gosum string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte(gosum), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestGoSumCarriesWhatABuildWithoutTheReplaceNeeds is the guard for committed checksums, and
// it is not dead weight. CI drops go.mod's `replace => ../infrata` and builds the tagged infrata
// module under -mod=readonly, which needs go.sum to already hold that module's hashes: a hash
// CI wrote for itself would verify nothing. But `go mod tidy` run locally, with the replace
// present, strips infrata's hashes — so without this test a routine tidy passes every local
// check and breaks only the next push. Here it breaks the next `go test ./...` instead, with the
// restore command in the failure.
func TestGoSumCarriesWhatABuildWithoutTheReplaceNeeds(t *testing.T) {
	if out, err := run(t, nil, "ci-use-infrata-tag", "check"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestCheckRefusesAGoSumThatTidyStripped. The repository's go.sum passing proves nothing unless a
// stripped one fails, so each case removes exactly the lines a tidy-with-replace can remove.
func TestCheckRefusesAGoSumThatTidyStripped(t *testing.T) {
	gomod, gosum := readRepoFile(t, "go.mod"), readRepoFile(t, "go.sum")
	version, err := run(t, nil, "ci-use-infrata-tag", "version")
	if err != nil {
		t.Fatalf("version: %v\n%s", err, version)
	}
	version = strings.TrimSpace(version)

	for name, drop := range map[string]string{
		"infrata's module hash": "github.com/infrata/infrata " + version + " h1:",
		"infrata's go.mod hash": "github.com/infrata/infrata " + version + "/go.mod h1:",
		"an indirect module":    "gopkg.in/yaml.v3 ",
	} {
		t.Run(name, func(t *testing.T) {
			var kept []string
			for _, line := range strings.SplitAfter(gosum, "\n") {
				if !strings.HasPrefix(line, drop) {
					kept = append(kept, line)
				}
			}
			stripped := strings.Join(kept, "")
			if stripped == gosum {
				t.Fatalf("the repository's go.sum has no line starting %q, so this case strips nothing", drop)
			}
			dir := moduleCopy(t, gomod, stripped)
			out, err := run(t, []string{"FAKE_MODULE_DIR=" + dir}, "ci-use-infrata-tag", "check")
			if err == nil {
				t.Fatalf("check accepted a go.sum without %q:\n%s", drop, out)
			}
			for _, want := range []string{"go mod tidy", "replace", "scripts/ci-use-infrata-tag sum"} {
				if !strings.Contains(out, want) {
					t.Errorf("the refusal does not mention %q:\n%s", want, out)
				}
			}
		})
	}
}

// TestVersionRefusesARequireThatIsNotARelease. A pseudo-version or the old v0.0.0 placeholder
// would make CI's infrata checkout ref meaningless, so it must stop at the first step.
func TestVersionRefusesARequireThatIsNotARelease(t *testing.T) {
	gomod := strings.Replace(readRepoFile(t, "go.mod"),
		"require github.com/infrata/infrata v", "require github.com/infrata/infrata v0.0.0-20260913000000-000000000000 // was v", 1)
	if !strings.Contains(gomod, "v0.0.0-2026") {
		t.Fatal("go.mod has no single-line `require github.com/infrata/infrata v…` to doctor")
	}
	dir := moduleCopy(t, gomod, readRepoFile(t, "go.sum"))
	out, err := run(t, []string{"FAKE_MODULE_DIR=" + dir}, "ci-use-infrata-tag", "version")
	if err == nil {
		t.Fatalf("version accepted a pseudo-version require:\n%s", out)
	}
	if !strings.Contains(out, "not a release tag") {
		t.Errorf("the refusal does not say why:\n%s", out)
	}
}
