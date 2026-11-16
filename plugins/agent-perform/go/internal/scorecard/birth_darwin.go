package scorecard

import (
	"os"
	"syscall"

	"agentperform/internal/common"
)

// birth is the file's creation time (Rust's Metadata::created), else its mtime.
func birth(path string) (float64, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		return float64(sys.Birthtimespec.Sec) + float64(sys.Birthtimespec.Nsec)/1e9, true
	}
	return common.Epoch(st.ModTime()), true
}
