//go:build !unix

package session

import "errors"

func RootInode(string) (uint64, error) {
	return 0, errors.New("root identity is not available on this platform")
}
