// 復習シャッフルと苦手ドリル。
// - 復習シャッフル: 正解済みの問題からランダムに最大30問
// - 苦手: 一度でも間違えた/わからないを押した問題をためる。苦手ドリルで2回続けて正解したら卒業
package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
)

const drillSize = 30
const weakGraduate = 2 // 苦手ドリルで連続この回数正解したら苦手から外す

type weakEntry struct {
	Lesson  int    `json:"lesson"`
	Chapter int    `json:"chapter"`
	Q       int    `json:"q"`
	Misses  int    `json:"misses"`
	Streak  int    `json:"streak"`
	Last    string `json:"last"`
}

type weakStore struct {
	mu   sync.Mutex
	path string
	m    map[string]*weakEntry
}

var weak *weakStore

func weakKey(l, c, q int) string { return fmt.Sprintf("%d/%d/%d", l, c, q) }

func newWeakStore(path string, seed []Mistake) *weakStore {
	s := &weakStore{path: path, m: map[string]*weakEntry{}}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &s.m)
		return s
	}
	// 初回は、これまでのにがて帳(誤答ログ)から苦手を作る
	for _, mk := range seed {
		k := weakKey(mk.Lesson, mk.Chapter, mk.Q)
		e := s.m[k]
		if e == nil {
			e = &weakEntry{Lesson: mk.Lesson, Chapter: mk.Chapter, Q: mk.Q}
			s.m[k] = e
		}
		e.Misses++
		e.Last = mk.Time
	}
	s.save()
	return s
}

func (s *weakStore) save() {
	b, _ := json.MarshalIndent(s.m, "", "  ")
	os.WriteFile(s.path, b, 0o644)
}

// miss は間違えた/わからないを記録する(連続正解はリセット)。
func (s *weakStore) miss(l, c, q int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := weakKey(l, c, q)
	e := s.m[k]
	if e == nil {
		e = &weakEntry{Lesson: l, Chapter: c, Q: q}
		s.m[k] = e
	}
	e.Misses++
	e.Streak = 0
	e.Last = time.Now().Format("2006-01-02 15:04")
	s.save()
}

// hit は苦手ドリルでの正解を記録し、卒業したら true を返す。
func (s *weakStore) hit(l, c, q int) (graduated bool, streak int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := weakKey(l, c, q)
	e := s.m[k]
	if e == nil {
		return false, 0
	}
	e.Streak++
	if e.Streak >= weakGraduate {
		delete(s.m, k)
		s.save()
		return true, weakGraduate
	}
	s.save()
	return false, e.Streak
}

func (s *weakStore) list() []weakEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]weakEntry, 0, len(s.m))
	for _, e := range s.m {
		out = append(out, *e)
	}
	return out
}

type drillItem struct {
	Lesson   int      `json:"lesson"`
	Chapter  int      `json:"chapter"`
	Q        int      `json:"q"`
	Title    string   `json:"title"`
	Question string   `json:"question"`
	Choices  []string `json:"choices"`
	Image    string   `json:"image,omitempty"`
	Write    bool     `json:"write,omitempty"`
	Misses   int      `json:"misses,omitempty"`
	Streak   int      `json:"streak,omitempty"`
}

func rangeOf(r *http.Request) (int, int) {
	from, _ := strconv.Atoi(r.URL.Query().Get("from"))
	to, _ := strconv.Atoi(r.URL.Query().Get("to"))
	if from <= 0 {
		from = 1
	}
	if to <= 0 {
		to = 89
	}
	return from, to
}

func toItem(c *Chapter, l, ch, q int) drillItem {
	qq := c.Quiz[q]
	return drillItem{Lesson: l, Chapter: ch, Q: q, Title: c.Title, Question: qq.Question,
		Choices: qq.Choices, Image: qq.Image, Write: qq.Answer != ""}
}

// 範囲内の (lesson, chapter) を列挙する
func chaptersIn(from, to int) [][2]int {
	var out [][2]int
	for n := from; n <= to; n++ {
		entries, err := os.ReadDir(filepath.Join(courseRoot, fmt.Sprintf("lesson%02d", n)))
		if err != nil {
			continue
		}
		for _, e := range entries {
			m := chFileRe.FindStringSubmatch(e.Name())
			if m == nil {
				continue
			}
			ch, _ := strconv.Atoi(m[1])
			out = append(out, [2]int{n, ch})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

func reviewCandidates(from, to int) []drillItem {
	var items []drillItem
	for _, lc := range chaptersIn(from, to) {
		c, err := loadChapter(lc[0], lc[1])
		if err != nil {
			continue
		}
		solved := quizState.get(progressKey(lc[0], lc[1]), len(c.Quiz))
		for q, ok := range solved {
			if ok {
				items = append(items, toItem(c, lc[0], lc[1], q))
			}
		}
	}
	return items
}

func weakCandidates(from, to int) []drillItem {
	var items []drillItem
	cache := map[[2]int]*Chapter{}
	for _, e := range weak.list() {
		if e.Lesson < from || e.Lesson > to {
			continue
		}
		key := [2]int{e.Lesson, e.Chapter}
		c, ok := cache[key]
		if !ok {
			c, _ = loadChapter(e.Lesson, e.Chapter)
			cache[key] = c
		}
		if c == nil || e.Q < 0 || e.Q >= len(c.Quiz) {
			continue
		}
		it := toItem(c, e.Lesson, e.Chapter, e.Q)
		it.Misses, it.Streak = e.Misses, e.Streak
		items = append(items, it)
	}
	return items
}

// GET /api/drill?type=review|weak&from=41&to=69
func handleDrill(w http.ResponseWriter, r *http.Request) {
	from, to := rangeOf(r)
	var items []drillItem
	if r.URL.Query().Get("type") == "weak" {
		items = weakCandidates(from, to)
	} else {
		items = reviewCandidates(from, to)
	}
	rand.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
	if len(items) > drillSize {
		items = items[:drillSize]
	}
	writeJSON(w, map[string]any{"items": items})
}

// GET /api/drill/stats?from=&to= — ボタンに出す件数
func handleDrillStats(w http.ResponseWriter, r *http.Request) {
	from, to := rangeOf(r)
	writeJSON(w, map[string]int{
		"review": len(reviewCandidates(from, to)),
		"weak":   len(weakCandidates(from, to)),
	})
}
