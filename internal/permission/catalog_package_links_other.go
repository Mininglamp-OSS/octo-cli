//go:build !unix

package permission

import "os"

func catalogSingleLink(os.FileInfo) bool { return false }
