package py

import "errors"

// ShlexSplit is shlex.split(s) (POSIX mode, no comments): quotes, backslash
// escapes, ” as an empty word. Errors are ValueError's ("No closing
// quotation", "No escaped character").
func ShlexSplit(s string) ([]string, error) {
	const ws = " \t\r\n"
	var out []string
	var tok []rune
	inTok := false // a token has started (even an empty quoted one)
	state := ' '   // ' ' between/in a word, '\'' or '"' in quotes
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch state {
		case '\'':
			if c == '\'' {
				state = ' '
			} else {
				tok = append(tok, c)
			}
		case '"':
			switch {
			case c == '"':
				state = ' '
			case c == '\\':
				if i+1 >= len(rs) {
					return nil, errors.New("No escaped character")
				}
				next := rs[i+1]
				if next != '"' && next != '\\' {
					tok = append(tok, '\\')
				}
				tok = append(tok, next)
				i++
			default:
				tok = append(tok, c)
			}
		default:
			switch {
			case c == ' ' || c == '\t' || c == '\r' || c == '\n':
				if inTok {
					out = append(out, string(tok))
					tok, inTok = nil, false
				}
			case c == '\\':
				if i+1 >= len(rs) {
					return nil, errors.New("No escaped character")
				}
				tok = append(tok, rs[i+1])
				inTok = true
				i++
			case c == '\'' || c == '"':
				state, inTok = c, true
			default:
				tok = append(tok, c)
				inTok = true
			}
		}
	}
	if state != ' ' {
		return nil, errors.New("No closing quotation")
	}
	if inTok {
		out = append(out, string(tok))
	}
	_ = ws
	return out, nil
}
