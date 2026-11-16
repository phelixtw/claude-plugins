package py

import (
	"math"
	"strconv"
	"strings"
)

// Comma is f"{n:,}" for an int.
func Comma(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	s = groupDigits(s)
	if neg {
		s = "-" + s
	}
	return s
}

func groupDigits(s string) string {
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// F is f"{x:.Nf}".
func F(x float64, prec int) string {
	if math.IsInf(x, 0) || math.IsNaN(x) {
		return FloatRepr(x)
	}
	return strconv.FormatFloat(x, 'f', prec, 64)
}

// SignedF is f"{x:+.Nf}".
func SignedF(x float64, prec int) string {
	s := F(x, prec)
	if !strings.HasPrefix(s, "-") {
		s = "+" + s
	}
	return s
}

// CommaF is f"{x:,.Nf}".
func CommaF(x float64, prec int) string {
	s := F(x, prec)
	if math.IsInf(x, 0) || math.IsNaN(x) {
		return s
	}
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	intPart, frac, hasFrac := strings.Cut(s, ".")
	s = groupDigits(intPart)
	if hasFrac {
		s += "." + frac
	}
	if neg {
		s = "-" + s
	}
	return s
}

// Pct is f"{x:.N%}".
func Pct(x float64, prec int) string { return F(x*100, prec) + "%" }

// G is f"{x:g}".
func G(x float64) string {
	if math.IsInf(x, 0) || math.IsNaN(x) {
		return FloatRepr(x)
	}
	s := strconv.FormatFloat(x, 'g', 6, 64)
	if mant, exp, ok := strings.Cut(s, "e"); ok && len(exp) == 2 {
		s = mant + "e" + exp[:1] + "0" + exp[1:]
	}
	return s
}

// CommaNum is f"{v:,}" for an int or a float (float: repr with grouped digits).
func CommaNum(v float64, isInt bool) string {
	if isInt {
		return Comma(int64(v))
	}
	s := FloatRepr(v)
	if strings.ContainsAny(s, "ein") {
		return s
	}
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	intPart, frac, _ := strings.Cut(s, ".")
	s = groupDigits(intPart) + "." + frac
	if neg {
		s = "-" + s
	}
	return s
}

// NumStr is str(v) for a number that is an int or a float.
func NumStr(v float64, isInt bool) string {
	if isInt {
		return strconv.FormatInt(int64(v), 10)
	}
	return FloatRepr(v)
}
