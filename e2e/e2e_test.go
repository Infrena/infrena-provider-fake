//go:build e2e

// Package e2e runs a real infrata binary against a real infrata-plugin-fake binary.
//
// Not part of `go test ./...`: it builds infrata from source, so it is slow and needs a
// checkout. Run it with `go test -tags e2e -count=1 ./e2e/`. INFRATA_SRC points at the
// checkout; the default is the sibling ../ilan that go.mod's replace already assumes.
package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var (
	infrataBin string // absolute path to the built infrata
	pluginDir  string // directory holding the built infrata-plugin-fake
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
		src := os.Getenv("INFRATA_SRC")
		if src == "" {
			src = filepath.Join("..", "..", "ilan")
		}
		if _, err := os.Stat(filepath.Join(src, "cmd", "infrata")); err != nil {
			skipReason = fmt.Sprintf("no infrata checkout at %s (set INFRATA_SRC): %v", src, err)
			fmt.Fprintln(os.Stderr, "E2E SKIPPED: "+skipReason)
			return m.Run()
		}
		infrataBin = filepath.Join(tmp, "infrata")
		pluginDir = filepath.Join(tmp, "plugins")
		for _, b := range []struct{ dir, out, pkg string }{
			{src, infrataBin, "./cmd/infrata"},
			{"..", filepath.Join(pluginDir, "infrata-plugin-fake"), "./cmd/infrata-plugin-fake"},
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

// infrata runs the CLI in dir and returns combined output and the exit code.
func infrata(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(infrataBin, append(args, "--plugin-dir", pluginDir)...)
	cmd.Dir = dir
	// Only the plugin directory above may supply plugins: nothing from the developer's machine.
	cmd.Env = append(os.Environ(), "INFRATA_PLUGIN_PATH=", "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("running infrata %v: %v", args, err)
	}
	return string(out), code
}

// expect runs a command and fails unless the exit code and every wanted substring match.
func expect(t *testing.T, dir string, wantCode int, want []string, args ...string) string {
	t.Helper()
	out, code := infrata(t, dir, args...)
	if code != wantCode {
		t.Fatalf("infrata %s: exit %d, want %d\n%s", strings.Join(args, " "), code, wantCode, out)
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Fatalf("infrata %s: output lacks %q\n%s", strings.Join(args, " "), w, out)
		}
	}
	return out
}

// planOps runs `plan dev --output` and returns address → kind for every proposed operation.
func planOps(t *testing.T, dir string) map[string]string {
	t.Helper()
	outPath := filepath.Join(t.TempDir(), "plan.json")
	infrata(t, dir, "plan", "dev", "--output", outPath)
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
		// the whole plan, not just its changes (infrata's plan.go, documented after
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
			t.Fatalf("plan after removing tags still proposes %v — the update did not remove them (D4)", ops)
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
	t.Run("discover and import adopt what infrata did not create", func(t *testing.T) {
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
