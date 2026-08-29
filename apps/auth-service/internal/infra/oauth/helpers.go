package oauth

import "io"

// ioReadAll is io.ReadAll, aliased so defaultProfileFetch doesn't need to
// import io directly (cosmetic; matches the rest of the file's style).
func ioReadAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}
