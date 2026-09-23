package fsops

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestEntryTypeFromAttrs(t *testing.T) {
	t.Parallel()
	const (
		dir     = windows.FILE_ATTRIBUTE_DIRECTORY
		reparse = windows.FILE_ATTRIBUTE_REPARSE_POINT
		normal  = windows.FILE_ATTRIBUTE_NORMAL
		archive = windows.FILE_ATTRIBUTE_ARCHIVE
	)
	tests := []struct {
		name  string
		attrs uint32
		tag   uint32
		want  EntryType
	}{
		{"file", normal, 0, TypeFile},
		{"file with other attributes", archive | windows.FILE_ATTRIBUTE_READONLY | windows.FILE_ATTRIBUTE_HIDDEN, 0, TypeFile},
		{"dir", dir, 0, TypeDir},
		{"tag is ignored without the reparse attribute", dir, windows.IO_REPARSE_TAG_SYMLINK, TypeDir},
		{"file symlink", reparse, windows.IO_REPARSE_TAG_SYMLINK, TypeSymlink},
		{"dir symlink", dir | reparse, windows.IO_REPARSE_TAG_SYMLINK, TypeSymlink},
		{"junction", dir | reparse, windows.IO_REPARSE_TAG_MOUNT_POINT, TypeJunction},
		{"cloud file", reparse, 0x9000001A, TypeFile},
		{"cloud dir", dir | reparse, 0x9000001A, TypeDir},
		{"cloud_1 file", reparse, 0x9000101A, TypeFile},
		{"cloud_F dir", dir | reparse, 0x9000F01A, TypeDir},
		{"dedup file", reparse, 0x80000013, TypeFile},
		{"AppExecLink", reparse, 0x8000001B, TypeSpecial},
		{"WSL symlink (LX_SYMLINK)", reparse, 0xA000001D, TypeSpecial},
		{"AF_UNIX socket", reparse, 0x80000023, TypeSpecial},
		{"unknown tag on a dir", dir | reparse, 0x12345678, TypeSpecial},
		{"not a cloud tag (low bits differ)", reparse, 0x9000001B, TypeSpecial},
	}
	for _, tt := range tests {
		if got := entryTypeFromAttrs(tt.attrs, tt.tag); got != tt.want {
			t.Errorf("%s: entryTypeFromAttrs(%#x, %#x) = %v, want %v", tt.name, tt.attrs, tt.tag, got, tt.want)
		}
	}
}
