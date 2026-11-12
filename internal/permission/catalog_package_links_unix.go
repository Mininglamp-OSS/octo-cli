//go:build unix

package permission

import (
	"os"
	"syscall"
)

func catalogSingleLink(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}
