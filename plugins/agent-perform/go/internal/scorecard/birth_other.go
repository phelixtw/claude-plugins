//go:build !darwin

package scorecard

import (
	"os"

	"agentperform/internal/common"
)

func birth(path string) (float64, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return common.Epoch(st.ModTime()), true
}
