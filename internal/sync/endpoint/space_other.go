//go:build !unix

package endpoint

import "errors"

func availableBytes(string) (int64, error) {
	return 0, errors.New("free space is not reported on this platform")
}
