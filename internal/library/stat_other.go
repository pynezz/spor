//go:build !unix

package library

import "os"

func statKey(fi os.FileInfo) fileKey {
	return fileKey{Size: fi.Size(), MtimeNs: fi.ModTime().UnixNano()}
}
