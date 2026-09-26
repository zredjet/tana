package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime/debug"
)

// fileHeader は、結果のファイルの先頭の項目。空の項目は、ファイルにある値を残す。
type fileHeader struct {
	Terminal        string `json:"terminal"`
	TerminalVersion string `json:"terminal_version,omitempty"`
	Font            string `json:"font,omitempty"`
	Date            string `json:"date"`
}

// sectionHeader は、各節（width・keys など）に共通の項目。
type sectionHeader struct {
	Started  string            `json:"started"`
	Revision string            `json:"probe_revision,omitempty"`
	GOOS     string            `json:"goos"`
	GOARCH   string            `json:"goarch"`
	Cols     int               `json:"cols"`
	Rows     int               `json:"rows"`
	Output   string            `json:"output_method,omitempty"` // Windows のみ意味を持つ（VT1）
	VTInput  bool              `json:"vt_input"`                // Windows のみ意味を持つ（VT2）
	Env      map[string]string `json:"env"`
	Info     map[string]string `json:"info"`
	Note     string            `json:"note,omitempty"`
}

// saveSection は、path の JSON の節 name を v で置き換え、先頭の項目を h で更新して書く。ほかの節は残す。
func saveSection(path string, h fileHeader, name string, v any) error {
	m := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &m); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	set := func(k string, v any) error {
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			return err
		}
		m[k] = bytes.TrimSpace(b.Bytes())
		return nil
	}
	for k, v := range map[string]string{"terminal": h.Terminal, "terminal_version": h.TerminalVersion, "font": h.Font, "date": h.Date} {
		if _, ok := m[k]; v != "" || !ok {
			if err := set(k, v); err != nil {
				return err
			}
		}
	}
	if err := set(name, v); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// loadSection は、path の JSON の節 name を v に読む。ファイルか節がなければ false を返す。
func loadSection(path, name string, v any) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	raw, ok := m[name]
	if !ok {
		return false, nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return false, fmt.Errorf("%s: %s: %w", path, name, err)
	}
	return true, nil
}

// revision は、ビルドしたときのコミット（変更があれば +dirty を付ける）を返す。
func revision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var rev, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "+dirty"
			}
		}
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	return rev + dirty
}
