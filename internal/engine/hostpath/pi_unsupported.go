//go:build !darwin && !linux

package hostpath

func ReadPiOAuthSource(string) ([]byte, PiOAuthSourceStatus) {
	return nil, PiOAuthSourceUnsafe
}
