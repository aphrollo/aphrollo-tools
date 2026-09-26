//go:build !windows

package depinstall

import "errors"

// makeJunction exists only on Windows; LinkDir never calls it elsewhere.
func makeJunction(target, link string) error {
	return errors.New("directory junctions exist only on Windows")
}
