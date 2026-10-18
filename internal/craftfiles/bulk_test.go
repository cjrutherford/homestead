package craftfiles

import (
	"io"
	"sync/atomic"
)

// bigSize is how many bytes the fake serves for a path ending in "big";
// bigSeen records how many a PUT to such a path delivered.
var (
	bigSize atomic.Int64
	bigSeen atomic.Int64
)

// zeroReader yields endless zero bytes without allocating.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

var _ io.Reader = zeroReader{}
