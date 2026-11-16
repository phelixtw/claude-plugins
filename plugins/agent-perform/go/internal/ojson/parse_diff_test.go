package ojson

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// parseDecoder is the encoding/json Decoder version Parse replaced.
func parseDecoder(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeTok(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data")
	}
	return v, nil
}

func decodeTok(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := NewObject()
			for dec.More() {
				k, err := dec.Token()
				if err != nil {
					return nil, err
				}
				v, err := decodeTok(dec)
				if err != nil {
					return nil, err
				}
				o.Set(k.(string), v)
			}
			_, err := dec.Token()
			return o, err
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := decodeTok(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			_, err := dec.Token()
			return arr, err
		}
		return nil, errors.New("unexpected delimiter")
	default:
		return t, nil
	}
}

func same(t *testing.T, in []byte) {
	t.Helper()
	a, errA := Parse(in)
	b, errB := parseDecoder(in)
	if (errA == nil) != (errB == nil) || !reflect.DeepEqual(a, b) {
		t.Errorf("%.80q: fast %v (%v) vs decoder %v (%v)", in, a, errA, b, errB)
	}
}

func TestParseMatchesDecoder(t *testing.T) {
	for _, in := range []string{
		`{"a": 1, "b": [true, false, null, -0, 1.5e3, 2E-2], "a": "x"}`, `[]`, `{}`, ` "s" `, `"é😀\ud800x\udc00\ud800A"`,
		"\"bad \xff\xfe utf8 \xe4\xb8\"", `"\/\b\f\n\r\t\\\""`, `01`, `1.`, `.5`, `-`, `[1,]`, `{"a":1,}`, `{"a" 1}`, `tru`, `nul`,
		`{"a":1} x`, `{"a":1} {"b":2}`, "\"ctl\x01\"", `"\x"`, `"\u12"`, ``, `   `, `[1 2]`, `{"a":{"b":[{"c":"d"}]}}`, `1e`, `-01`, `"unterminated`,
	} {
		same(t, []byte(in))
	}
	// every line of the local transcripts, when there are any
	files, _ := filepath.Glob(filepath.Join(os.Getenv("HOME"), ".claude", "projects", "*", "*.jsonl"))
	n := 0
	for _, f := range files {
		if n > 20_000 {
			break
		}
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 1<<20), 1<<28)
		for sc.Scan() {
			same(t, sc.Bytes())
			n++
		}
		fh.Close()
	}
	t.Logf("%d transcript lines", n)
}
