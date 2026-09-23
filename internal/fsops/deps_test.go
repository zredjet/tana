package fsops

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// disallowedDeps は、go list -deps -test の出力のうち、許可リストにないパッケージを返す。
// self は internal/fsops の import パスで、その配下のパッケージは許可する。
// deps には標準ライブラリを含めない。
func disallowedDeps(deps []string, self string) []string {
	// internal/fsops が import してよい、標準ライブラリ以外のモジュール。
	allowedModules := []string{"golang.org/x/sys", "golang.org/x/text"}
	within := func(p, root string) bool { return p == root || strings.HasPrefix(p, root+"/") }
	var bad []string
	for _, line := range deps {
		// テスト用の変種は "p [p.test]"、外部テストパッケージは "p_test [p.test]"、
		// テストの main パッケージは "p.test" と表示される。
		p, _, _ := strings.Cut(strings.TrimSpace(line), " ")
		if p == "" {
			continue
		}
		if within(p, self) || within(strings.TrimSuffix(p, ".test"), self) || within(strings.TrimSuffix(p, "_test"), self) {
			continue
		}
		if slices.ContainsFunc(allowedModules, func(m string) bool { return within(p, m) }) {
			continue
		}
		bad = append(bad, p)
	}
	return bad
}

func TestDisallowedDeps(t *testing.T) {
	t.Parallel()
	const self = "example.com/m/internal/fsops"
	deps := []string{
		"golang.org/x/sys/unix",
		"golang.org/x/sys/windows",
		"golang.org/x/text/unicode/norm",
		self,
		self + "/internal/testfs",
		self + " [" + self + ".test]",
		self + "_test [" + self + ".test]",
		self + "/internal/testfs_test [" + self + "/internal/testfs.test]",
		self + ".test",
		"",
		"github.com/some/lib",
		"golang.org/x/sysfoo",
		"golang.org/x/net/context",
		"example.com/m/internal/other",
		"example.com/m/internal/fsopsx",
		"example.com/m/internal/other_test",
	}
	want := []string{
		"github.com/some/lib",
		"golang.org/x/sysfoo",
		"golang.org/x/net/context",
		"example.com/m/internal/other",
		"example.com/m/internal/fsopsx",
		"example.com/m/internal/other_test",
	}
	if got := disallowedDeps(deps, self); !slices.Equal(got, want) {
		t.Errorf("disallowedDeps = %q, want %q", got, want)
	}
}

// TestDependencies は、対応するすべての GOOS・GOARCH（amd64、arm64）と CGO_ENABLED の組み合わせで、
// internal/fsops 配下のパッケージ（テストを含む）の依存が許可リストの中にあることを確かめる（SPEC §4）。
func TestDependencies(t *testing.T) {
	t.Parallel()
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go command not found in PATH: %v", err)
	}
	goList := func(t *testing.T, env []string, args ...string) string {
		t.Helper()
		cmd := exec.Command(gobin, append([]string{"list"}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.Output()
		if err != nil {
			stderr := ""
			if ee, ok := errors.AsType[*exec.ExitError](err); ok {
				stderr = string(ee.Stderr)
			}
			t.Fatalf("go list %v with %v: %v\n%s", args, env, err, stderr)
		}
		return string(out)
	}
	self := strings.TrimSpace(goList(t, nil, "-f", "{{.ImportPath}}", "."))

	for _, goos := range []string{"windows", "darwin", "linux"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			for _, cgo := range []string{"0", "1"} {
				env := []string{"GOOS=" + goos, "GOARCH=" + goarch, "CGO_ENABLED=" + cgo}
				t.Run(goos+"/"+goarch+"/cgo="+cgo, func(t *testing.T) {
					t.Parallel()
					out := goList(t, env, "-deps", "-test", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", "./...")
					if bad := disallowedDeps(strings.Split(out, "\n"), self); len(bad) > 0 {
						t.Errorf("disallowed dependencies (SPEC §4):\n%s", strings.Join(bad, "\n"))
					}
				})
			}
		}
	}
}
