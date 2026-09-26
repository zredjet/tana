package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseArgs は、引数から各ペインのフォルダを決めることを確かめる（filer §6）。
func TestParseArgs(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(wd, "x")
	for _, tt := range []struct {
		args []string
		want []string
	}{
		{nil, []string{wd, wd}},
		{[]string{"sub"}, []string{filepath.Join(wd, "sub"), wd}},
		{[]string{abs, ".."}, []string{abs, filepath.Dir(wd)}},
	} {
		got, err := parseArgs(tt.args)
		if err != nil || strings.Join(got, "|") != strings.Join(tt.want, "|") {
			t.Errorf("parseArgs(%q) = %q, %v, want %q", tt.args, got, err, tt.want)
		}
	}
	for _, args := range [][]string{{"a", "b", "c"}, {""}} {
		if _, err := parseArgs(args); err == nil {
			t.Errorf("parseArgs(%q): no error", args)
		}
	}
}

// TestRunUsage は、引数が多すぎれば、端末を開かずに使い方を出して 2 で終わることを確かめる。
func TestRunUsage(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"a", "b", "c"}, &stderr); code != 2 || !strings.Contains(stderr.String(), "使い方") {
		t.Errorf("run = %d, %q", code, stderr.String())
	}
}

// TestLogger は、ログの行に英語の詳細を書くことを確かめる。
func TestLogger(t *testing.T) {
	var b bytes.Buffer
	logger(&b)(errors.New("readdir /x: permission denied"))
	if !strings.HasSuffix(b.String(), " readdir /x: permission denied\n") {
		t.Errorf("log = %q", b.String())
	}
}
