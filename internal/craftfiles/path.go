package craftfiles

import (
	"errors"
	"strings"
)

// Path errors. Traversal is refused, never silently cleaned.
var (
	ErrTraversal = errors.New("path escapes the root folder")
	ErrBadPath   = errors.New("invalid path")
)

const (
	maxPathBytes   = 1024
	maxSegmentLen  = 255
	maxPathDepth   = 32
	forbiddenChars = "\\\x00"
)

// CleanRel normalises a path relative to the root folder: empty segments
// (slashes) collapse; "." and ".." segments, NUL, backslashes and control
// characters are refused. The result has no leading or trailing slash and
// may be empty (the root itself).
func CleanRel(p string) (string, error) {
	if len(p) > maxPathBytes {
		return "", ErrBadPath
	}
	var out []string
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "":
			continue
		case ".", "..":
			return "", ErrTraversal
		}
		if len(seg) > maxSegmentLen || strings.ContainsAny(seg, forbiddenChars) {
			return "", ErrBadPath
		}
		for _, r := range seg {
			if r < 0x20 || r == 0x7f {
				return "", ErrBadPath
			}
		}
		out = append(out, seg)
	}
	if len(out) > maxPathDepth {
		return "", ErrBadPath
	}
	return strings.Join(out, "/"), nil
}

// hasDotSegment reports a "." or ".." segment, a backslash, a NUL or an
// empty (doubled-slash) interior segment in a decoded request path, so the
// guard can refuse before routing.
func hasDotSegment(p string) bool {
	if strings.ContainsAny(p, forbiddenChars) {
		return true
	}
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		if seg == "." || seg == ".." {
			return true
		}
		if seg == "" && i != 0 && i != len(segs)-1 {
			return true
		}
	}
	return false
}
