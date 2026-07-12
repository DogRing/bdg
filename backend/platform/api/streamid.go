package api

import (
	"regexp"
	"strconv"
	"strings"
)

// Redis STREAM entry IDs are "<ms>-<seq>" (both unsigned decimal). The SSE
// replay cursor (SPEC §Routes GET /sse; data-contracts §4) is such an ID.
var streamIDPattern = regexp.MustCompile(`^\d+-\d+$`)

// validStreamID reports whether s is a well-formed Redis entry ID a client may
// supply as a replay cursor. "$" / "" / garbage are NOT valid cursors.
func validStreamID(s string) bool {
	return streamIDPattern.MatchString(s)
}

// streamIDLess compares two well-formed entry IDs numerically (ms first, then
// seq). Callers must validate with validStreamID first; malformed parts
// compare as 0.
func streamIDLess(a, b string) bool {
	ams, aseq := splitStreamID(a)
	bms, bseq := splitStreamID(b)
	if ams != bms {
		return ams < bms
	}
	return aseq < bseq
}

func splitStreamID(id string) (uint64, uint64) {
	ms, seq, _ := strings.Cut(id, "-")
	m, _ := strconv.ParseUint(ms, 10, 64)
	s, _ := strconv.ParseUint(seq, 10, 64)
	return m, s
}
