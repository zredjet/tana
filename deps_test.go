// リポジトリ全体の依存の検査（filer §4、tui §3）。
// fsops の internal/fsops/deps_test.go と同じく、go list の出力を調べる。
package tana

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// allowedModules は、リポジトリのどのパッケージも import してよい、標準ライブラリ以外のモジュール（filer §4）。
var allowedModules = []string{"golang.org/x/sys", "golang.org/x/text"}

// directionRule は、パッケージの間の依存の向きの規則。
// pkg（モジュールからの相対パス）が依存してよいのは、標準ライブラリ、pkg 自身とその配下、allowed に挙げたものだけ。
// allowed には、モジュールからの相対パス（このモジュールのパッケージ）か、外部のモジュールの import パスを書く。
type directionRule struct {
	pkg     string
	allowed []string
}

// directionRules は、パッケージの間の依存の向き（tui §3、filer §4）。パッケージを作るフェーズで、そのパッケージの行を足す。
var directionRules = []directionRule{
	// term は x/sys だけを使い、ほかの土台のパッケージとファイラーのパッケージを import しない（tui §3）。
	{"internal/term", []string{"golang.org/x/sys"}},
	// textwidth は標準ライブラリだけを使う（tui §3）。表を作る internal/ucdgen・internal/gen も同じ。
	{"internal/textwidth", nil},
	// keys は標準ライブラリだけを使う（tui §3）。
	{"internal/keys", nil},
	// screen・lineedit は textwidth だけを使う（tui §3）。
	{"internal/screen", []string{"internal/textwidth"}},
	{"internal/lineedit", []string{"internal/textwidth"}},
	// platform は x/sys だけを使い、ほかの tana のパッケージを import しない（filer §4）。
	{"internal/platform", []string{"golang.org/x/sys"}},
	// msg・textfmt・listing・app は、term・keys・screen・tui を import しない（filer §4）。
	{"internal/msg", []string{"internal/fsops", "golang.org/x/sys"}},
	{"internal/textfmt", []string{"internal/textwidth", "golang.org/x/text"}},
	{"internal/listing", []string{"internal/fsops", "golang.org/x/sys", "golang.org/x/text"}},
	{"internal/app", []string{"internal/fsops", "internal/listing", "internal/msg", "internal/platform", "internal/lineedit", "internal/textwidth", "internal/textfmt",
		"golang.org/x/sys", "golang.org/x/text"}},
	// tui は土台のパッケージを組み合わせ、app の状態を描く（filer §4）。term を通して x/sys を使う。
	{"internal/tui", []string{"internal/term", "internal/keys", "internal/screen", "internal/lineedit", "internal/textwidth",
		"internal/app", "internal/listing", "internal/msg", "internal/textfmt", "internal/platform", "internal/fsops",
		"golang.org/x/sys", "golang.org/x/text"}},
}

// within は、p が root そのものか、その配下のパッケージかを返す。
func within(p, root string) bool { return p == root || strings.HasPrefix(p, root+"/") }

// basePath は、go list が表示するテスト用の変種の名前から、元のパッケージの import パスを取り出す。
// テスト用の変種は "p [p.test]"、外部テストパッケージは "p_test [p.test]"、テストの main パッケージは "p.test" と表示される。
func basePath(line string) string {
	p, _, _ := strings.Cut(strings.TrimSpace(line), " ")
	if strings.HasSuffix(p, ".test") {
		return strings.TrimSuffix(p, ".test")
	}
	return strings.TrimSuffix(p, "_test")
}

// disallowedDeps は、標準ライブラリ以外の依存 deps のうち、module（このモジュール）の外で、allowedModules にもないものを返す。
func disallowedDeps(deps []string, module string) []string {
	var bad []string
	for _, line := range deps {
		p, _, _ := strings.Cut(strings.TrimSpace(line), " ")
		if p == "" {
			continue
		}
		if within(basePath(line), module) || slices.ContainsFunc(allowedModules, func(m string) bool { return within(p, m) }) {
			continue
		}
		bad = append(bad, p)
	}
	return bad
}

// listedPackage は、go list -deps -test の 1 行（パッケージとその依存）。
type listedPackage struct {
	path     string // "p [p.test]" のような変種の名前のまま
	standard bool
	deps     []string
}

// parseListed は、listFormat で出力した go list の結果を読む。
func parseListed(out string) []listedPackage {
	var pkgs []listedPackage
	for line := range strings.Lines(out) {
		fields := strings.Split(strings.TrimRight(line, "\r\n"), "\t")
		if len(fields) != 3 || fields[0] == "" {
			continue
		}
		var deps []string
		for d := range strings.SplitSeq(fields[2], ",") {
			if d != "" {
				deps = append(deps, d)
			}
		}
		pkgs = append(pkgs, listedPackage{path: fields[0], standard: fields[1] == "true", deps: deps})
	}
	return pkgs
}

// listFormat は、パッケージごとに「import パス、標準ライブラリか、依存」をタブで区切って出す。
// 依存はテスト用の変種の名前（"p [q.test]"）に空白を含むので、コンマで区切る。
const listFormat = `{{.ImportPath}}{{"\t"}}{{.Standard}}{{"\t"}}{{join .Deps ","}}`

// directionViolations は、pkgs（go list -deps -test の結果）のうち、rules の pkg とそのテスト用の変種が、
// 許されていないパッケージに依存しているものを "依存元 -> 依存先" の形で返す。
func directionViolations(pkgs []listedPackage, module string, rules []directionRule) []string {
	standard := map[string]bool{}
	for _, p := range pkgs {
		if p.standard {
			standard[basePath(p.path)] = true
		}
	}
	var bad []string
	for _, r := range rules {
		self := module + "/" + r.pkg
		for _, p := range pkgs {
			if !within(basePath(p.path), self) {
				continue
			}
			for _, d := range p.deps {
				d = basePath(d)
				if standard[d] || within(d, self) {
					continue
				}
				ok := slices.ContainsFunc(r.allowed, func(a string) bool {
					return within(d, a) || within(d, module+"/"+a)
				})
				if !ok {
					bad = append(bad, p.path+" -> "+d)
				}
			}
		}
	}
	slices.Sort(bad)
	return slices.Compact(bad)
}

func TestDisallowedDeps(t *testing.T) {
	t.Parallel()
	const module = "example.com/m"
	deps := []string{
		"golang.org/x/sys/unix",
		"golang.org/x/sys/windows",
		"golang.org/x/text/unicode/norm",
		module,
		module + "/internal/fsops",
		module + "/internal/term [" + module + "/internal/term.test]",
		module + "/internal/term_test [" + module + "/internal/term.test]",
		module + "/cmd/tuiprobe.test",
		"",
		"github.com/some/lib",
		"golang.org/x/sysfoo",
		"golang.org/x/net/context",
		"example.com/mx/internal/term",
		"github.com/some/lib [" + module + "/internal/term.test]",
	}
	want := []string{
		"github.com/some/lib",
		"golang.org/x/sysfoo",
		"golang.org/x/net/context",
		"example.com/mx/internal/term",
		"github.com/some/lib",
	}
	if got := disallowedDeps(deps, module); !slices.Equal(got, want) {
		t.Errorf("disallowedDeps = %q, want %q", got, want)
	}
}

func TestDirectionViolations(t *testing.T) {
	t.Parallel()
	const m = "example.com/m"
	rules := []directionRule{
		{"internal/term", []string{"golang.org/x/sys"}},
		{"internal/screen", []string{"internal/textwidth"}},
	}
	pkgs := []listedPackage{
		{path: "os", standard: true},
		{path: "testing", standard: true},
		{path: "golang.org/x/sys/unix", deps: []string{"os"}},
		{path: m + "/internal/textwidth", deps: []string{"os"}},
		{path: m + "/internal/keys", deps: []string{"os"}},
		// 許されるもの: 標準ライブラリ、x/sys、自身の配下、許可したモジュール内のパッケージ
		{path: m + "/internal/term", deps: []string{"os", "golang.org/x/sys/unix", m + "/internal/term/internal/pty"}},
		{path: m + "/internal/screen", deps: []string{"os", m + "/internal/textwidth"}},
		// 許されないもの: テスト用の変種と外部テストパッケージの依存も調べる
		{path: m + "/internal/term [" + m + "/internal/term.test]", deps: []string{"testing", m + "/internal/keys [" + m + "/internal/term.test]"}},
		{path: m + "/internal/term_test [" + m + "/internal/term.test]", deps: []string{"golang.org/x/text/width"}},
		{path: m + "/internal/screen.test", deps: []string{m + "/internal/term"}},
		// 規則のないパッケージは調べない
		{path: m + "/internal/app", deps: []string{m + "/internal/term", m + "/internal/fsops"}},
		// 名前が前方一致するだけの別のパッケージは、自身の配下として扱わない
		{path: m + "/internal/termx", deps: []string{m + "/internal/app"}},
	}
	want := []string{
		m + "/internal/screen.test -> " + m + "/internal/term",
		m + "/internal/term [" + m + "/internal/term.test] -> " + m + "/internal/keys",
		m + "/internal/term_test [" + m + "/internal/term.test] -> golang.org/x/text/width",
	}
	if got := directionViolations(pkgs, m, rules); !slices.Equal(got, want) {
		t.Errorf("directionViolations =\n%q\nwant\n%q", got, want)
	}
}

// TestDependencies は、対応するすべての GOOS・GOARCH（amd64、arm64）と CGO_ENABLED の組み合わせで、
// リポジトリのすべてのパッケージ（テストを含む）の依存が許可リストの中にあること（filer §4）と、
// directionRules の依存の向きを守っていること（tui §3）を確かめる。
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
	module := strings.TrimSpace(goList(t, nil, "-m"))

	for _, goos := range []string{"windows", "darwin", "linux"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			for _, cgo := range []string{"0", "1"} {
				env := []string{"GOOS=" + goos, "GOARCH=" + goarch, "CGO_ENABLED=" + cgo}
				t.Run(goos+"/"+goarch+"/cgo="+cgo, func(t *testing.T) {
					t.Parallel()
					pkgs := parseListed(goList(t, env, "-deps", "-test", "-f", listFormat, "./..."))
					var nonStd []string
					seen := map[string]bool{}
					for _, p := range pkgs {
						if !p.standard {
							nonStd = append(nonStd, p.path)
							seen[basePath(p.path)] = true
						}
					}
					if bad := disallowedDeps(nonStd, module); len(bad) > 0 {
						t.Errorf("disallowed dependencies (filer §4):\n%s", strings.Join(bad, "\n"))
					}
					for _, r := range directionRules {
						if !seen[module+"/"+r.pkg] {
							t.Errorf("directionRules: package %s not found in go list", r.pkg)
						}
					}
					if bad := directionViolations(pkgs, module, directionRules); len(bad) > 0 {
						t.Errorf("dependency direction violations (tui §3):\n%s", strings.Join(bad, "\n"))
					}
				})
			}
		}
	}
}
