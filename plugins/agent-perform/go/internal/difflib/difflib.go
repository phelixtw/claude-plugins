// Package difflib is Python's difflib.SequenceMatcher(None, a, b).ratio()
// on code points, autojunk included, so the 相似度 numbers match exactly.
package difflib

import "sort"

type match struct{ i, j, k int }

type matcher struct {
	a, b []rune
	b2j  map[rune][]int
}

func newMatcher(a, b []rune) *matcher {
	m := &matcher{a: a, b: b, b2j: map[rune][]int{}}
	for i, r := range b {
		m.b2j[r] = append(m.b2j[r], i)
	}
	if n := len(b); n >= 200 { // autojunk: drop the popular elements
		ntest := n/100 + 1
		for r, idx := range m.b2j {
			if len(idx) > ntest {
				delete(m.b2j, r)
			}
		}
	}
	return m
}

func (m *matcher) longest(alo, ahi, blo, bhi int) match {
	besti, bestj, bestsize := alo, blo, 0
	j2len := map[int]int{}
	for i := alo; i < ahi; i++ {
		next := map[int]int{}
		for _, j := range m.b2j[m.a[i]] {
			if j < blo {
				continue
			}
			if j >= bhi {
				break
			}
			k := j2len[j-1] + 1
			next[j] = k
			if k > bestsize {
				besti, bestj, bestsize = i-k+1, j-k+1, k
			}
		}
		j2len = next
	}
	// no junk function: extend over equal elements, popular ones included
	for besti > alo && bestj > blo && m.a[besti-1] == m.b[bestj-1] {
		besti, bestj, bestsize = besti-1, bestj-1, bestsize+1
	}
	for besti+bestsize < ahi && bestj+bestsize < bhi && m.a[besti+bestsize] == m.b[bestj+bestsize] {
		bestsize++
	}
	return match{besti, bestj, bestsize}
}

func (m *matcher) matches() int {
	queue := [][4]int{{0, len(m.a), 0, len(m.b)}}
	var blocks []match
	for len(queue) > 0 {
		q := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		x := m.longest(q[0], q[1], q[2], q[3])
		if x.k == 0 {
			continue
		}
		blocks = append(blocks, x)
		if q[0] < x.i && q[2] < x.j {
			queue = append(queue, [4]int{q[0], x.i, q[2], x.j})
		}
		if x.i+x.k < q[1] && x.j+x.k < q[3] {
			queue = append(queue, [4]int{x.i + x.k, q[1], x.j + x.k, q[3]})
		}
	}
	sort.Slice(blocks, func(p, q int) bool {
		if blocks[p].i != blocks[q].i {
			return blocks[p].i < blocks[q].i
		}
		if blocks[p].j != blocks[q].j {
			return blocks[p].j < blocks[q].j
		}
		return blocks[p].k < blocks[q].k
	})
	total := 0
	for _, x := range blocks {
		total += x.k
	}
	return total
}

// Ratio is SequenceMatcher(None, a, b).ratio().
func Ratio(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	n := len(ra) + len(rb)
	if n == 0 {
		return 1.0
	}
	return 2.0 * float64(newMatcher(ra, rb).matches()) / float64(n)
}
