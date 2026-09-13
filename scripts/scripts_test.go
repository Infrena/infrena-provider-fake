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
// describes a different version is the mistake §31.2 exists to prevent.
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
// the download name rather than reading it (§31.2), so a wrong name is an uninstallable release.
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
