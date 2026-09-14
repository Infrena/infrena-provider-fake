//go:build e2e

// Package e2e runs a real infrena binary against a real infrena-plugin-fake binary.
//
// Not part of `go test ./...`: it builds infrena from source, so it is slow and needs a
// checkout. Run it with `go test -tags e2e -count=1 ./e2e/`. INFRENA_SRC points at the
// checkout; the default is the sibling ../infrena that go.mod's replace already assumes.
package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infrena/infrena/pkg/pluginmanifest"
	"github.com/infrena/infrena/pkg/semver"
)

var (
	infrenaBin string // absolute path to the built infrena
	pluginDir  string // directory holding the built infrena-plugin-fake
	skipReason string // non-empty when the binaries could not be built
)

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "fake-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := func() int {
		defer os.RemoveAll(tmp)
		src := os.Getenv("INFRENA_SRC")
		if src == "" {
			src = filepath.Join("..", "..", "infrena")
		}
		if _, err := os.Stat(filepath.Join(src, "cmd", "infrena")); err != nil {
			skipReason = fmt.Sprintf("no infrena checkout at %s (set INFRENA_SRC): %v", src, err)
			fmt.Fprintln(os.Stderr, "E2E SKIPPED: "+skipReason)
			return m.Run()
		}
		infrenaBin = filepath.Join(tmp, "infrena")
		pluginDir = filepath.Join(tmp, "plugins")
		for _, b := range []struct{ dir, out, pkg string }{
			{src, infrenaBin, "./cmd/infrena"},
			{"..", filepath.Join(pluginDir, "infrena-plugin-fake"), "./cmd/infrena-plugin-fake"},
		} {
			cmd := exec.Command("go", "build", "-o", b.out, b.pkg)
			cmd.Dir = b.dir
			if out, err := cmd.CombinedOutput(); err != nil {
				fmt.Fprintf(os.Stderr, "building %s: %v\n%s", b.pkg, err, out)
				return 1
			}
		}
		return m.Run()
	}()
	os.Exit(code)
}

// project copies a fixture into a fresh directory and returns it.
func project(t *testing.T, fixture string) string {
	t.Helper()
	if skipReason != "" {
		t.Skip(skipReason)
	}
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join("testdata", fixture, "infra.yml"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "infra.yml"), string(data))
	return dir
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// infrena runs the CLI in dir and returns combined output and the exit code.
func infrena(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(infrenaBin, append(args, "--plugin-dir", pluginDir)...)
	cmd.Dir = dir
	// Only the plugin directory above may supply plugins: nothing from the developer's machine.
	cmd.Env = append(os.Environ(), "INFRENA_PLUGIN_PATH=", "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("running infrena %v: %v", args, err)
	}
	return string(out), code
}

// expect runs a command and fails unless the exit code and every wanted substring match.
func expect(t *testing.T, dir string, wantCode int, want []string, args ...string) string {
	t.Helper()
	out, code := infrena(t, dir, args...)
	if code != wantCode {
		t.Fatalf("infrena %s: exit %d, want %d\n%s", strings.Join(args, " "), code, wantCode, out)
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Fatalf("infrena %s: output lacks %q\n%s", strings.Join(args, " "), w, out)
		}
	}
	return out
}

// planOps runs `plan dev --output` and returns address → kind for every proposed operation.
func planOps(t *testing.T, dir string) map[string]string {
	t.Helper()
	outPath := filepath.Join(t.TempDir(), "plan.json")
	infrena(t, dir, "plan", "dev", "--output", outPath)
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("plan wrote no --output file: %v", err)
	}
	var doc struct {
		Operations []struct{ Address, Kind string } `json:"operations"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("plan --output is not the expected JSON: %v\n%s", err, data)
	}
	ops := map[string]string{}
	for _, op := range doc.Operations {
		// operations[] lists every resource, "noop" included: the artifact describes
		// the whole plan, not just its changes (infrena's plan.go, documented after
		// this suite reported it as a candidate for confusion).
		if op.Kind == "noop" {
			continue
		}
		ops[op.Address] = op.Kind
	}
	return ops
}

// editCloud changes the cloud file the way a person does: as plain JSON, not through this
// module's own types, so a change to the file's shape cannot hide from the suite.
func editCloud(t *testing.T, path string, edit func(doc map[string]any)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	edit(doc)
	out, _ := json.MarshalIndent(doc, "", "  ")
	writeFile(t, path, string(out))
}

func resourcesOf(doc map[string]any) map[string]any {
	r, _ := doc["resources"].(map[string]any)
	return r
}

// TestTheWorkflow is AGENT.md's checklist, run in order against one project.
func TestTheWorkflow(t *testing.T) {
	dir := project(t, "basic")
	cloud := filepath.Join(dir, ".infra", "fake-cloud.json")

	t.Run("explain", func(t *testing.T) {
		expect(t, dir, 0, []string{"fake.database", "(replaces on change)", "(sensitive)", "(default: 10)", "fake.network"},
			"explain", "fake.database")
	})
	t.Run("plan proposes three creates", func(t *testing.T) {
		expect(t, dir, 2, []string{"Plan: 3 to create", "<sensitive>"}, "plan", "dev")
	})
	t.Run("apply creates them", func(t *testing.T) {
		expect(t, dir, 2, []string{"Apply complete: 3 applied, 0 failed"}, "apply", "dev", "--auto-approve")
		if n := len(planOps(t, dir)); n != 0 {
			t.Fatalf("re-plan after apply proposes %d operations, want none", n)
		}
	})
	t.Run("a hand edit is drift", func(t *testing.T) {
		editCloud(t, cloud, func(doc map[string]any) {
			for _, r := range resourcesOf(doc) {
				obj := r.(map[string]any)
				if obj["type"] == "fake.database" {
					obj["attributes"].(map[string]any)["engine"] = "mysql"
				}
			}
		})
		if kind := planOps(t, dir)["db"]; kind != "replace" {
			t.Fatalf("after changing a ForceNew attribute by hand, db plans as %q, want replace", kind)
		}
		expect(t, dir, 2, []string{"0 failed"}, "apply", "dev", "--auto-approve")
		if n := len(planOps(t, dir)); n != 0 {
			t.Fatalf("drift was not repaired: %d operations remain", n)
		}
	})
	t.Run("removing an optional attribute converges", func(t *testing.T) {
		body, _ := os.ReadFile(filepath.Join(dir, "infra.yml"))
		writeFile(t, filepath.Join(dir, "infra.yml"), strings.Replace(string(body), "    tags:\n      team: data\n", "", 1))
		if kind := planOps(t, dir)["db"]; kind != "update" {
			t.Fatalf("dropping tags plans db as %q, want update", kind)
		}
		expect(t, dir, 2, []string{"0 failed"}, "apply", "dev", "--auto-approve")
		if ops := planOps(t, dir); len(ops) != 0 {
			t.Fatalf("plan after removing tags still proposes %v — Update did not remove the attributes the configuration dropped", ops)
		}
	})
	t.Run("an injected failure fails the apply, once", func(t *testing.T) {
		body, _ := os.ReadFile(filepath.Join(dir, "infra.yml"))
		writeFile(t, filepath.Join(dir, "infra.yml"), string(body)+"\n  extra:\n    type: fake.network\n    cidr: 10.2.0.0/16\n")
		editCloud(t, cloud, func(doc map[string]any) {
			doc["failures"] = []any{map[string]any{
				"op": "create", "address": "extra", "nth": 1, "message": "injected: quota exceeded",
			}}
		})
		expect(t, dir, 1, []string{"injected: quota exceeded", "1 failed"}, "apply", "dev", "--auto-approve")
		expect(t, dir, 2, []string{"Apply complete: 1 applied, 0 failed"}, "apply", "dev", "--auto-approve")
	})
	t.Run("removing a resource destroys it", func(t *testing.T) {
		body, _ := os.ReadFile(filepath.Join(dir, "infra.yml"))
		writeFile(t, filepath.Join(dir, "infra.yml"), strings.Replace(string(body), "\n  extra:\n    type: fake.network\n    cidr: 10.2.0.0/16\n", "", 1))
		expect(t, dir, 2, []string{"1 to destroy"}, "plan", "dev")
		expect(t, dir, 2, []string{"0 failed"}, "apply", "dev", "--auto-approve")
	})
	t.Run("discover and import adopt what infrena did not create", func(t *testing.T) {
		editCloud(t, cloud, func(doc map[string]any) {
			resourcesOf(doc)["net-77"] = map[string]any{
				"type": "fake.network", "attributes": map[string]any{"cidr": "172.16.0.0/12", "id": "net-77"},
			}
		})
		expect(t, dir, 0, []string{"fake.network", "net-77"}, "discover")
		expect(t, dir, 0, []string{"1 resource imported"}, "import", "dev", "fake.network.net-77", "--generate")
		if ops := planOps(t, dir); len(ops) != 0 {
			t.Fatalf("plan after import --generate proposes %v, want nothing", ops)
		}
	})
	t.Run("destroy empties the cloud", func(t *testing.T) {
		expect(t, dir, 2, []string{"0 failed"}, "destroy", "dev", "--auto-approve")
		data, _ := os.ReadFile(cloud)
		var doc map[string]any
		_ = json.Unmarshal(data, &doc)
		if n := len(resourcesOf(doc)); n != 0 {
			t.Fatalf("cloud still holds %d resources after destroy", n)
		}
	})
}

// TestTwoInstancesKeepSeparateClouds. `cloud:` stands in for an account: two instances of one
// plugin, served by one process, must never write into each other's file.
func TestTwoInstancesKeepSeparateClouds(t *testing.T) {
	dir := project(t, "instances")
	expect(t, dir, 2, []string{"Apply complete: 2 applied, 0 failed"}, "apply", "dev", "--auto-approve")

	holds := func(name string) []string {
		data, err := os.ReadFile(filepath.Join(dir, ".infra", name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var doc map[string]any
		_ = json.Unmarshal(data, &doc)
		var addrs []string
		for _, r := range resourcesOf(doc) {
			addrs = append(addrs, fmt.Sprint(r.(map[string]any)["address"]))
		}
		return addrs
	}
	if got := holds("fake-cloud-main.json"); len(got) != 1 || got[0] != "shared" {
		t.Errorf("main's cloud holds %v, want [shared]", got)
	}
	if got := holds("fake-cloud-acct2.json"); len(got) != 1 || got[0] != "isolated" {
		t.Errorf("acct2's cloud holds %v, want [isolated]", got)
	}
}

// TestTheInfrenaUnderTestSpeaksTheManifestsProtocol applies infrena PLAN.md §31.2's compatibility rules to the
// infrena this suite built, reading what that build says it speaks from `infrena version --output`.
// The release rule (AllowsInfrena) exempts only a host reporting 0.0.0. Since the engine has tags, a
// plain `go build` of its checkout is not that: Go stamps the module version from git, so a clean
// checkout AT a tag reports that version, and one commit past it reports a pseudo-version such as
// 0.3.1-0.<time>-<hash>, which compares as 0.3.1 (measured 2026-09-13 at v0.2.0, and again
// 2026-09-14 on infrena main e2be8bf, which is past the infrata-era v0.3.0 tag). So this checks
// plugin.yaml's `infrena:` floor against the release CI's tag job builds its host from, and against
// main in the advisory job — except while go.mod pins no renamed release; see below.
func TestTheInfrenaUnderTestSpeaksTheManifestsProtocol(t *testing.T) {
	if skipReason != "" {
		t.Skip(skipReason)
	}
	out := filepath.Join(t.TempDir(), "version.json")
	if b, err := exec.Command(infrenaBin, "version", "--output", out).CombinedOutput(); err != nil {
		t.Fatalf("infrena version --output: %v\n%s", err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var info struct {
		Version string `json:"version"`
		Formats []struct {
			Name     string `json:"name"`
			Versions []int  `json:"versions"`
		} `json:"formats"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("infrena version --output is not the expected JSON: %v\n%s", err, data)
	}
	var protocols []int
	for _, f := range info.Formats {
		if f.Name == "plugin protocol" {
			protocols = f.Versions
		}
	}
	if len(protocols) == 0 {
		t.Fatalf("infrena version --output lists no plugin protocol:\n%s", data)
	}

	raw, err := os.ReadFile(filepath.Join("..", "plugin.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := pluginmanifest.Parse(raw)
	if err != nil {
		t.Fatalf("plugin.yaml: %v", err)
	}
	if !m.SpeaksProtocol(protocols) {
		t.Errorf("plugin.yaml speaks protocol %v; infrena %s speaks %v", m.Protocol, info.Version, protocols)
	}
	// The floor is checked against a host of the release go.mod pins. While go.mod requires the
	// v0.0.0 placeholder (step 1 of the Infrata -> Infrena rename), no renamed release exists and
	// the floor names the first one, 0.4.0, so a host built from infrena main cannot satisfy it:
	// it reports a pseudo-version past v0.3.0, an infrata-era tag. What CAN be checked is that the
	// host really is such an unreleased build, and that the floor refuses the last infrata release.
	// Step 3 pins v0.4.0, and from then on the full check below applies.
	if required := requiredInfrena(t); required == "v0.0.0" {
		host, err := semver.Parse(info.Version)
		if err != nil {
			t.Fatalf("infrena version %q does not parse: %v", info.Version, err)
		}
		if host.Pre == "" {
			t.Errorf("go.mod pins no renamed release (v0.0.0), but the host reports release %s; "+
				"if a renamed release exists, bump go.mod's require to it", info.Version)
		}
		if m.AllowsInfrena("0.3.0") {
			t.Errorf("plugin.yaml's infrena constraint %q allows 0.3.0, the last infrata-named release", m.Infrena)
		}
		t.Logf("go.mod requires v0.0.0: floor %q not checked against the unreleased host %s until a renamed release is pinned",
			m.Infrena, info.Version)
		return
	}
	if !m.AllowsInfrena(info.Version) {
		t.Errorf("plugin.yaml's infrena constraint %q does not allow infrena %s", m.Infrena, info.Version)
	}
}

// requiredInfrena reads the infrena version this module's go.mod requires, from the file alone.
func requiredInfrena(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("go", "mod", "edit", "-json")
	cmd.Dir = ".."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go mod edit -json: %v", err)
	}
	var mod struct {
		Require []struct{ Path, Version string }
	}
	if err := json.Unmarshal(out, &mod); err != nil {
		t.Fatal(err)
	}
	for _, r := range mod.Require {
		if r.Path == "github.com/infrena/infrena" {
			return r.Version
		}
	}
	t.Fatal("go.mod does not require github.com/infrena/infrena")
	return ""
}
