package libm

import (
	"math"
	"testing"
)

func TestMatchesGoMath(t *testing.T) {
	near := func(name string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 1e-12*math.Max(1, math.Abs(want)) {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	for _, x := range []float64{0, 1e-10, 0.5, 3, 1e6} {
		near("Log1p", Log1p(x), math.Log1p(x))
		near("Exp", Exp(x), math.Exp(x))
	}
	near("Log", Log(10), math.Log(10))
	near("Pow", Pow(2, 10), 1024)
	if !math.IsInf(Log(0), -1) || !math.IsNaN(Log(-1)) {
		t.Error("Log edge cases follow C: log(0) = -Inf, log(-1) = NaN")
	}
}
