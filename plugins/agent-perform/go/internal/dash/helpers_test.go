package dash

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSmallHelpers(t *testing.T) {
	eq := func(name, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	eq("firstClause", firstClause("<b>省 3 次</b>，因為重複；其他"), "省 3 次")
	eq("firstClause paren", firstClause("a（b）"), "a")
	eq("firstNumber", firstNumber("<b>約 1,234.5 次</b> 之後"), "1,234.5 次")
	eq("firstNumber none", firstNumber("沒有數字"), "")
	eq("hours", hours(5400), "1.5 小時")
	eq("dur", dur(75), "1:15")
	eq("dur hours", dur(3725), "1:02:05")
	eq("itoa", itoa(42), "42")
	if got := sortedIdx(map[int]bool{3: true, 1: true, 2: true}); len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Errorf("sortedIdx = %v", got)
	}
	if !isDigits("123") || !isDigits("٣") || isDigits("") || isDigits("1a") {
		t.Error("isDigits")
	}
	if got := pstdev([]int64{2, 4, 4, 4, 5, 5, 7, 9}); math.Abs(got-2) > 1e-12 {
		t.Errorf("pstdev = %v, want 2", got)
	}
}

func TestOSMsgAndMoveAcross(t *testing.T) {
	dir := t.TempDir()
	_, err := os.ReadFile(filepath.Join(dir, "nope"))
	if got := osMsg(err); !strings.HasPrefix(got, "[Errno 2] No such file or directory: ") {
		t.Errorf("osMsg = %q", got)
	}
	if got := osMsg(errors.New("plain")); got != "plain" {
		t.Errorf("osMsg plain = %q", got)
	}
	err = os.Rename(filepath.Join(dir, "a"), filepath.Join(dir, "b"))
	if got := osMsg(err); !strings.Contains(got, " -> ") {
		t.Errorf("osMsg link error = %q", got)
	}
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	os.WriteFile(src, []byte("data"), 0o600)
	if err := moveAcross(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "data" {
		t.Errorf("dst = %q", b)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("src not removed")
	}
	if moveAcross(src, dst) == nil {
		t.Error("moving a missing file succeeded")
	}
}

func TestSkillNameAndFront(t *testing.T) {
	dir := t.TempDir()
	write := func(sub, text string) string {
		p := filepath.Join(dir, sub, "SKILL.md")
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(text), 0o644)
		return p
	}
	if got := skillName(write("a", "---\nname: \"fancy\"\n---\nbody")); got != "fancy" {
		t.Errorf("name from frontmatter = %q", got)
	}
	if got := skillName(write("b", "no frontmatter")); got != "b" {
		t.Errorf("folder fallback = %q", got)
	}
	if got := skillName(filepath.Join(dir, "gone", "SKILL.md")); got != "gone" {
		t.Errorf("missing file fallback = %q", got)
	}
	if got := front("name: x\ndescription: 'Hello there'\n", "description"); got != "Hello there" {
		t.Errorf("front = %q", got)
	}
	if got := front("name: x\n", "description"); got != "" {
		t.Errorf("front missing = %q", got)
	}
}
