//go:build unix

package library

import (
	"os"
	"syscall"
)

func statKey(fi os.FileInfo) fileKey {
	k := fileKey{Size: fi.Size(), MtimeNs: fi.ModTime().UnixNano()}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		k.Dev = uint64(st.Dev)
		k.Ino = uint64(st.Ino)
	}
	return k
}
