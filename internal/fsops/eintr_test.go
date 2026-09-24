package fsops

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// unixPurePass は、x/sys/unix の関数のうち、システムコールをせずに値を変換するだけで、ignoringEINTR で包まなくてよいもの（SPEC §4）。
var unixPurePass = []string{"TimeToTimespec", "ByteSliceToString"}

// eintrViolations は、ファイル f の中で、ignoringEINTR・ignoringEINTR2 に渡した関数リテラルの直下にない
// x/sys/unix の関数の呼び出しと、包まれた unix.Close を、「行:関数名」の形で返す（SPEC §4 のシステムコールのルール）。
// calls には、見つけた x/sys/unix の関数の呼び出しの数を返す。
func eintrViolations(fset *token.FileSet, f *ast.File) (bad []string, calls int) {
	unixName := ""
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == "golang.org/x/sys/unix" {
			unixName = "unix"
			if imp.Name != nil {
				unixName = imp.Name.Name
			}
		}
	}
	if unixName == "" {
		return nil, 0
	}
	// wrapped は、stack の最も内側の関数リテラルが ignoringEINTR・ignoringEINTR2 の引数かを返す。
	wrapped := func(stack []ast.Node) bool {
		for i := len(stack) - 1; i > 0; i-- {
			lit, ok := stack[i].(*ast.FuncLit)
			if !ok {
				continue
			}
			call, ok := stack[i-1].(*ast.CallExpr)
			if !ok || !slices.Contains(call.Args, ast.Expr(lit)) {
				return false
			}
			fun := call.Fun
			switch x := fun.(type) {
			case *ast.IndexExpr:
				fun = x.X
			case *ast.IndexListExpr:
				fun = x.X
			}
			id, ok := fun.(*ast.Ident)
			return ok && (id.Name == "ignoringEINTR" || id.Name == "ignoringEINTR2")
		}
		return false
	}
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == unixName {
					calls++
					name := sel.Sel.Name
					w := wrapped(stack)
					switch {
					case name == "Close" && w:
						bad = append(bad, strconv.Itoa(fset.Position(call.Pos()).Line)+":"+name+" (wrapped)")
					case name == "Close", slices.Contains(unixPurePass, name):
					case !w:
						bad = append(bad, strconv.Itoa(fset.Position(call.Pos()).Line)+":"+name)
					}
				}
			}
		}
		stack = append(stack, n)
		return true
	})
	return bad, calls
}

func TestEINTRViolations(t *testing.T) {
	t.Parallel()
	const src = `package p

import "golang.org/x/sys/unix"

func f(fd int) {
	var st unix.Stat_t
	unix.Fstat(fd, &st)
	_ = ignoringEINTR(func() error { return unix.Fstat(fd, &st) })
	_, _ = ignoringEINTR2(func() (int, error) { return unix.Open("/", 0, 0) })
	_, _ = ignoringEINTR2[int](func() (int, error) { return unix.Open("/", 0, 0) })
	_ = ignoringEINTR(func() error { return unix.Close(fd) })
	unix.Close(fd)
	defer unix.Close(fd)
	_ = unix.TimeToTimespec
	_ = unix.ByteSliceToString(nil)
	_ = ignoringEINTR(func() error { go func() { unix.Unlink("/x") }(); return nil })
	_ = other(func() error { return unix.Unlink("/x") })
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	bad, calls := eintrViolations(fset, f)
	want := []string{"7:Fstat", "11:Close (wrapped)", "16:Unlink", "17:Unlink"}
	if !slices.Equal(bad, want) {
		t.Errorf("eintrViolations = %q, want %q", bad, want)
	}
	if calls != 10 {
		t.Errorf("calls = %d, want 10", calls)
	}
}

// TestEINTRWrapped は、internal/fsops のテスト以外の Go ファイル（ビルド条件にかかわらずすべて）で、
// x/sys/unix の関数の呼び出しが ignoringEINTR で包まれ、unix.Close は包まれていないことを確かめる（SPEC §4）。
func TestEINTRWrapped(t *testing.T) {
	t.Parallel()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	total := 0
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		bad, calls := eintrViolations(fset, f)
		total += calls
		for _, b := range bad {
			t.Errorf("%s:%s: x/sys/unix call must go through ignoringEINTR (unix.Close must not)", name, b)
		}
	}
	// 走査が何も見つけられない壊れ方で素通りしないよう、呼び出しがあることも確かめる。
	if total == 0 {
		t.Fatal("no x/sys/unix calls found; the scan is broken")
	}
}
