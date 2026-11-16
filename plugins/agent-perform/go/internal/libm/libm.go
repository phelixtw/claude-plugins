// Package libm calls the platform C math library, the one Python's math module
// and numpy use, so results match theirs to the last bit (Go's math package
// is its own implementation and can differ by an ulp).
package libm

/*
#cgo LDFLAGS: -lm
#include <math.h>
*/
import "C"

func Log1p(x float64) float64  { return float64(C.log1p(C.double(x))) }
func Log(x float64) float64    { return float64(C.log(C.double(x))) }
func Exp(x float64) float64    { return float64(C.exp(C.double(x))) }
func Pow(x, y float64) float64 { return float64(C.pow(C.double(x), C.double(y))) }
