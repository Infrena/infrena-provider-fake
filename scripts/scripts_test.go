// Package scripts tests the release scripts: they are what stands between a tag and a
// published release whose manifest, archive names or binary disagree.
package scripts

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
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
// describes a different version is the mistake infrena PLAN.md §31.2 exists to prevent.
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
// release's binary speaks (infrena PLAN.md §31.2), which for an SDK-built binary is exactly one
// version. Each case keeps the version agreeing, so only the protocol can be the refusal: the
// previous release's [1], left behind after an infrena require bump, and the host's whole
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
		[]string{"FAKE_VERSION_SYMBOL=github.com/infrena/infrena-provider-fake/internal/fake.NoSuchVariable"},
		"release-check", "v"+v)
	if err == nil {
		t.Fatalf("release-check passed with a binary whose version was never stamped:\n%s", out)
	}
	if !strings.Contains(out, "0.0.0-dev") {
		t.Errorf("the refusal does not say what the binary reported:\n%s", out)
	}
}

// TestBuildReleaseNamesArchivesByTheInstallConvention. `infrena plugins install` constructs
// the download name rather than reading it (infrena PLAN.md §31.2), so a wrong name is an uninstallable release.
func TestBuildReleaseNamesArchivesByTheInstallConvention(t *testing.T) {
	v := manifestVersion(t)
	dist := t.TempDir()
	if out, err := run(t, []string{"PLATFORMS=linux/amd64 windows/amd64"}, "build-release", v, dist); err != nil {
		t.Fatalf("build-release failed: %v\n%s", err, out)
	}
	stem := "infrena-plugin-fake_" + v + "_linux_amd64"
	for _, name := range []string{stem + ".tar.gz", "infrena-plugin-fake_" + v + "_windows_amd64.zip"} {
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
	for _, want := range []string{stem + "/infrena-plugin-fake", stem + "/plugin.yaml", stem + "/README.md"} {
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
	return readFile(t, filepath.Join("..", name))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

const (
	// infrenaModule is the module whose hashes the guard is about.
	infrenaModule = "github.com/infrena/infrena"
	// unpinnedRelease is go.mod's placeholder require while no renamed infrena release exists
	// (step 1 of the Infrata -> Infrena rename; see go.mod). It is not a release, so there is
	// nothing to pin.
	unpinnedRelease = "v0.0.0"
	// syntheticRelease stands in for a real infrena release in the doctored module copies below.
	// Nothing fetches it: `check` is offline and only reads go.mod and go.sum, so the fixtures stay
	// valid whatever the repository's own go.mod requires.
	syntheticRelease = "v0.9.8"
)

// requiredInfrena reads the infrena version go.mod in dir requires, the way the script does:
// `go mod edit -json`, which reads the file alone and does not load the replace target.
func requiredInfrena(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("go", "mod", "edit", "-json")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go mod edit -json in %s: %v", dir, err)
	}
	var mod struct {
		Require []struct{ Path, Version string }
	}
	if err := json.Unmarshal(out, &mod); err != nil {
		t.Fatal(err)
	}
	for _, r := range mod.Require {
		if r.Path == infrenaModule {
			return r.Version
		}
	}
	t.Fatalf("go.mod in %s does not require %s", dir, infrenaModule)
	return ""
}

// goSumGuard is the body of TestGoSumCarriesWhatABuildWithoutTheReplaceNeeds, against the module
// in dir. It returns a non-empty skip reason while go.mod requires the unpinned placeholder, and
// otherwise the result of `ci-use-infrena-tag check`. Split out so the test below can prove both
// outcomes on doctored copies, which a test that merely calls t.Skip or t.Fatal could not.
func goSumGuard(t *testing.T, dir string) (skip, out string, err error) {
	t.Helper()
	if v := requiredInfrena(t, dir); v == unpinnedRelease {
		return fmt.Sprintf("go.mod requires %s %s: no renamed release pinned yet, so there are no "+
			"hashes to guard. Step 3 of the rename (bump the require to the first renamed release, "+
			"then run `scripts/ci-use-infrena-tag sum`) removes this skip.", infrenaModule, v), "", nil
	}
	out, err = run(t, []string{"FAKE_MODULE_DIR=" + dir}, "ci-use-infrena-tag", "check")
	return "", out, err
}

// TestGoSumCarriesWhatABuildWithoutTheReplaceNeeds is the guard for committed checksums, and
// it is not dead weight. CI drops go.mod's `replace => ../infrena` and builds the tagged infrena
// module under -mod=readonly, which needs go.sum to already hold that module's hashes: a hash
// CI wrote for itself would verify nothing. But `go mod tidy` run locally, with the replace
// present, strips infrena's hashes — so without this test a routine tidy passes every local
// check and breaks only the next push. Here it breaks the next `go test ./...` instead, with the
// restore command in the failure.
//
// It SKIPS, loudly, while go.mod requires v0.0.0: see goSumGuard, and
// TestTheGoSumGuardSkipsOnlyTheUnpinnedPlaceholder for the proof that it skips nothing else.
func TestGoSumCarriesWhatABuildWithoutTheReplaceNeeds(t *testing.T) {
	skip, out, err := goSumGuard(t, "..")
	if skip != "" {
		t.Skip(skip)
	}
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// pinnedModuleCopy is the repository's go.mod and go.sum, doctored to require syntheticRelease
// with that release's hashes present, so it is what go.mod and go.sum look like once a real
// release is pinned. Doctored with `go mod edit` rather than string surgery, so a reworded
// comment in go.mod cannot make the doctoring a silent no-op.
func pinnedModuleCopy(t *testing.T) (dir, gosum string) {
	t.Helper()
	gosum = readRepoFile(t, "go.sum") +
		infrenaModule + " " + syntheticRelease + " h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n" +
		infrenaModule + " " + syntheticRelease + "/go.mod h1:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=\n"
	dir = moduleCopy(t, readRepoFile(t, "go.mod"), gosum)
	cmd := exec.Command("go", "mod", "edit", "-require="+infrenaModule+"@"+syntheticRelease)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("doctoring go.mod: %v\n%s", err, out)
	}
	if v := requiredInfrena(t, dir); v != syntheticRelease {
		t.Fatalf("the doctored go.mod requires %s, not %s", v, syntheticRelease)
	}
	return dir, gosum
}

// TestTheGoSumGuardSkipsOnlyTheUnpinnedPlaceholder. A guard that skips proves nothing unless it
// skips for exactly one reason. Each case contradicts the others: the placeholder skips and the
// script refuses it by name; a real version with its hashes passes; the same real version with
// them stripped fails rather than skipping.
func TestTheGoSumGuardSkipsOnlyTheUnpinnedPlaceholder(t *testing.T) {
	t.Run("v0.0.0 skips, and the script refuses it by name", func(t *testing.T) {
		dir, _ := pinnedModuleCopy(t)
		cmd := exec.Command("go", "mod", "edit", "-require="+infrenaModule+"@"+unpinnedRelease)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("doctoring go.mod: %v\n%s", err, out)
		}
		skip, out, err := goSumGuard(t, dir)
		if skip == "" {
			t.Fatalf("the guard did not skip a go.mod requiring v0.0.0 (check said: %v)\n%s", err, out)
		}
		for _, want := range []string{"no renamed release pinned yet", "step 3", "scripts/ci-use-infrena-tag sum"} {
			if !strings.Contains(strings.ToLower(skip), strings.ToLower(want)) {
				t.Errorf("the skip message does not say %q:\n%s", want, skip)
			}
		}
		for _, command := range []string{"version", "check"} {
			out, err := run(t, []string{"FAKE_MODULE_DIR=" + dir}, "ci-use-infrena-tag", command)
			if err == nil {
				t.Fatalf("%s accepted a go.mod requiring v0.0.0:\n%s", command, out)
			}
			if !strings.Contains(out, "requires "+infrenaModule+" v0.0.0: no tagged release to test against yet") {
				t.Errorf("%s's refusal does not say why:\n%s", command, out)
			}
		}
	})
	t.Run("a real version with its hashes passes", func(t *testing.T) {
		dir, _ := pinnedModuleCopy(t)
		skip, out, err := goSumGuard(t, dir)
		if skip != "" {
			t.Fatalf("the guard skipped a go.mod requiring %s: %s", syntheticRelease, skip)
		}
		if err != nil {
			t.Fatalf("check refused a go.sum that carries every hash: %v\n%s", err, out)
		}
	})
	t.Run("a real version with its hashes missing fails", func(t *testing.T) {
		dir, gosum := pinnedModuleCopy(t)
		stripped := strings.ReplaceAll(gosum, infrenaModule+" "+syntheticRelease, "example.com/other v1.0.0")
		if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte(stripped), 0o644); err != nil {
			t.Fatal(err)
		}
		skip, out, err := goSumGuard(t, dir)
		if skip != "" {
			t.Fatalf("the guard skipped a go.mod requiring %s: %s", syntheticRelease, skip)
		}
		if err == nil {
			t.Fatalf("check accepted a go.sum without %s's hashes:\n%s", syntheticRelease, out)
		}
	})
}

// TestCheckRefusesAGoSumThatTidyStripped. A go.sum passing proves nothing unless a stripped one
// fails, so each case removes exactly the lines a tidy-with-replace can remove, from a copy pinned
// to a real version (the repository's own go.mod may still require the unpinned placeholder).
func TestCheckRefusesAGoSumThatTidyStripped(t *testing.T) {
	pinned, gosum := pinnedModuleCopy(t)
	gomod := readFile(t, filepath.Join(pinned, "go.mod"))
	version := syntheticRelease

	for name, drop := range map[string]string{
		"infrena's module hash": infrenaModule + " " + version + " h1:",
		"infrena's go.mod hash": infrenaModule + " " + version + "/go.mod h1:",
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
				t.Fatalf("the pinned go.sum has no line starting %q, so this case strips nothing", drop)
			}
			dir := moduleCopy(t, gomod, stripped)
			out, err := run(t, []string{"FAKE_MODULE_DIR=" + dir}, "ci-use-infrena-tag", "check")
			if err == nil {
				t.Fatalf("check accepted a go.sum without %q:\n%s", drop, out)
			}
			for _, want := range []string{"go mod tidy", "replace", "scripts/ci-use-infrena-tag sum"} {
				if !strings.Contains(out, want) {
					t.Errorf("the refusal does not mention %q:\n%s", want, out)
				}
			}
		})
	}
}

// TestVersionRefusesARequireThatIsNotARelease. A pseudo-version or the old v0.0.0 placeholder
// would make CI's infrena checkout ref meaningless, so it must stop at the first step.
func TestVersionRefusesARequireThatIsNotARelease(t *testing.T) {
	gomod := strings.Replace(readRepoFile(t, "go.mod"),
		"require github.com/infrena/infrena v", "require github.com/infrena/infrena v0.0.0-20260913000000-000000000000 // was v", 1)
	if !strings.Contains(gomod, "v0.0.0-2026") {
		t.Fatal("go.mod has no single-line `require github.com/infrena/infrena v…` to doctor")
	}
	dir := moduleCopy(t, gomod, readRepoFile(t, "go.sum"))
	out, err := run(t, []string{"FAKE_MODULE_DIR=" + dir}, "ci-use-infrena-tag", "version")
	if err == nil {
		t.Fatalf("version accepted a pseudo-version require:\n%s", out)
	}
	if !strings.Contains(out, "not a release tag") {
		t.Errorf("the refusal does not say why:\n%s", out)
	}
}
