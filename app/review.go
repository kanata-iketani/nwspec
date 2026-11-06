// 復習(間隔反復)機能 — 全講座共通。~/learning/shared/review/review.go を各 app/ にコピーして使う。
//   - 解いた問題ごとに「箱(0〜6)」と「次に出す日」を記録する
//   - 正解: 箱を1つ上げ、1→2→4→7→15→30日後に再出題 / 不正解: 箱0に戻し、すぐ再出題
//   - 今日の復習(due) / 苦手ドリル(weak: 不正解のあと2回続けて正解するまで) / これまでの総復習(all)
//
// 組み込み: main.go の handleAnswer で正誤が決まった直後に srsRecord(lesson, chapter, q, correct) を呼び、
// main() で srsInit() を呼ぶ(ルートもそこで登録する)。
package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var srsIntervals = []int{0, 1, 2, 4, 7, 15, 30} // 箱ごとの間隔(日)

type srsItem struct {
	Lesson  int    `json:"lesson"`
	Chapter int    `json:"chapter"`
	Q       int    `json:"q"`
	Box     int    `json:"box"`
	Due     string `json:"due"`     // RFC3339
	Last    string `json:"last"`    // 最後に解いた日時
	Right   int    `json:"right"`   // 正解回数
	Wrong   int    `json:"wrong"`   // 不正解回数
	Streak  int    `json:"streak"`  // 連続正解
	Weak    bool   `json:"weak"`    // 苦手(不正解のあと、2回続けて正解するまで true)
}

type srsStore struct {
	mu   sync.Mutex
	path string
	m    map[string]*srsItem
}

var srs *srsStore

func srsKey(l, c, q int) string { return fmt.Sprintf("%d/%d/%d", l, c, q) }

var srsChRe = regexp.MustCompile(`lesson(\d+)/ch(\d+)`)

// srsInit はストアを読み込み(無ければ正解済みの記録から作り)、ルートを登録する。
func srsInit() {
	s := &srsStore{path: filepath.Join(appDir, "review.json"), m: map[string]*srsItem{}}
	if b, err := os.ReadFile(s.path); err == nil {
		json.Unmarshal(b, &s.m)
	} else {
		// 初回: これまで正解した問題を「箱1」として登録し、出題日を今日から7日に散らす(初日にどっと来ないように)
		now := time.Now()
		if b, err := os.ReadFile(filepath.Join(appDir, "quizstate.json")); err == nil {
			var qs map[string][]bool
			json.Unmarshal(b, &qs)
			for key, solved := range qs {
				m := srsChRe.FindStringSubmatch(key)
				if m == nil {
					continue
				}
				l, _ := strconv.Atoi(m[1])
				c, _ := strconv.Atoi(m[2])
				for i, ok := range solved {
					if ok {
						s.m[srsKey(l, c, i)] = &srsItem{Lesson: l, Chapter: c, Q: i, Box: 1, Right: 1,
							Due: now.AddDate(0, 0, rand.Intn(7)).Format(time.RFC3339)}
					}
				}
			}
		}
		// 誤答ログがあれば苦手として登録
		if b, err := os.ReadFile(filepath.Join(appDir, "mistakes.json")); err == nil {
			var raw any
			json.Unmarshal(b, &raw)
			var list []any
			switch v := raw.(type) {
			case []any:
				list = v
			case map[string]any:
				if it, ok := v["items"].([]any); ok {
					list = it
				}
			}
			for _, x := range list {
				mk, ok := x.(map[string]any)
				if !ok {
					continue
				}
				l, c, q := toInt(mk["lesson"]), toInt(mk["chapter"]), toInt(mk["q"])
				k := srsKey(l, c, q)
				it := s.m[k]
				if it == nil {
					it = &srsItem{Lesson: l, Chapter: c, Q: q}
					s.m[k] = it
				}
				it.Box, it.Wrong, it.Weak, it.Streak = 0, it.Wrong+1, true, 0
				it.Due = now.Format(time.RFC3339)
			}
		}
		s.save()
	}
	srs = s
	http.HandleFunc("GET /api/review/stats", handleReviewStats)
	http.HandleFunc("GET /api/review/set", handleReviewSet)
	http.HandleFunc("GET /api/review/digest", handleReviewDigest)
}

func toInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	}
	return 0
}

func (s *srsStore) save() {
	b, _ := json.MarshalIndent(s.m, "", " ")
	os.WriteFile(s.path, b, 0o644)
}

// srsRecord は解答結果を記録する(handleAnswer から呼ぶ)。
func srsRecord(lesson, chapter, q int, correct bool) {
	if srs == nil {
		return
	}
	srs.mu.Lock()
	defer srs.mu.Unlock()
	k := srsKey(lesson, chapter, q)
	it := srs.m[k]
	if it == nil {
		it = &srsItem{Lesson: lesson, Chapter: chapter, Q: q}
		srs.m[k] = it
	}
	now := time.Now()
	it.Last = now.Format(time.RFC3339)
	if correct {
		it.Right++
		it.Streak++
		if it.Box < len(srsIntervals)-1 {
			it.Box++
		}
		if it.Weak && it.Streak >= 2 {
			it.Weak = false
		}
		it.Due = now.AddDate(0, 0, srsIntervals[it.Box]).Format(time.RFC3339)
	} else {
		it.Wrong++
		it.Streak = 0
		it.Box = 0
		it.Weak = true
		it.Due = now.Format(time.RFC3339)
	}
	srs.save()
}

func (s *srsStore) pick(mode string, lesson int) []*srsItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var out []*srsItem
	for _, it := range s.m {
		if lesson > 0 && it.Lesson != lesson {
			continue
		}
		switch mode {
		case "weak":
			if it.Weak {
				out = append(out, it)
			}
		case "all":
			if it.Right > 0 || it.Weak {
				out = append(out, it)
			}
		default: // due
			d, err := time.Parse(time.RFC3339, it.Due)
			if err == nil && !d.After(now) {
				out = append(out, it)
			}
		}
	}
	return out
}

func handleReviewStats(w http.ResponseWriter, r *http.Request) {
	due := len(srs.pick("due", 0))
	weakN := len(srs.pick("weak", 0))
	all := len(srs.pick("all", 0))
	// 明日以降の予定(7日分)
	srs.mu.Lock()
	upcoming := make([]int, 7)
	today := time.Now().Truncate(24 * time.Hour)
	for _, it := range srs.m {
		if d, err := time.Parse(time.RFC3339, it.Due); err == nil {
			days := int(d.Truncate(24*time.Hour).Sub(today).Hours() / 24)
			if days >= 1 && days <= 7 {
				upcoming[days-1]++
			}
		}
	}
	srs.mu.Unlock()
	writeJSON(w, map[string]any{"due": due, "weak": weakN, "learned": all, "upcoming": upcoming})
}

// handleReviewSet は復習セット(シャッフル済み)を、問題文・選択肢つきで返す。正解は返さない。
func handleReviewSet(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n <= 0 || n > 100 {
		n = 20
	}
	lesson, _ := strconv.Atoi(r.URL.Query().Get("lesson"))
	items := srs.pick(mode, lesson)
	rand.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
	if mode == "due" { // 期限切れの古い順を優先しつつ混ぜる
		sort.SliceStable(items, func(i, j int) bool { return items[i].Box < items[j].Box })
		rand.Shuffle(len(items), func(i, j int) {
			if items[i].Box == items[j].Box {
				items[i], items[j] = items[j], items[i]
			}
		})
	}
	includeEssay := r.URL.Query().Get("essay") == "1"
	type view struct {
		Lesson   int      `json:"lesson"`
		Chapter  int      `json:"chapter"`
		Q        int      `json:"q"`
		Title    string   `json:"title"`
		Question string   `json:"question"`
		Choices  []string `json:"choices"`
		Image    string   `json:"image,omitempty"`
		Kind     string   `json:"kind,omitempty"`
		Setup    string   `json:"setup,omitempty"`
		Write    bool     `json:"write,omitempty"`
		Box      int      `json:"box"`
		Weak     bool     `json:"weak"`
	}
	var out []view
	for _, it := range items {
		if len(out) >= n {
			break
		}
		c, err := loadChapter(it.Lesson, it.Chapter)
		if err != nil || it.Q < 0 || it.Q >= len(c.Quiz) {
			continue
		}
		// 講座ごとに Question の項目が違うので JSON 経由で取り出す
		b, _ := json.Marshal(c.Quiz[it.Q])
		var q map[string]any
		json.Unmarshal(b, &q)
		kind, _ := q["kind"].(string)
		answer, _ := q["answer"].(string)
		if kind == "essay" && !includeEssay {
			continue // AI 採点は時間がかかるので、既定では復習に入れない
		}
		v := view{Lesson: it.Lesson, Chapter: it.Chapter, Q: it.Q, Title: c.Title, Kind: kind, Box: it.Box, Weak: it.Weak}
		v.Question, _ = q["question"].(string)
		v.Image, _ = q["image"].(string)
		v.Setup, _ = q["setup"].(string)
		v.Write = answer != "" && kind == ""
		if cs, ok := q["choices"].([]any); ok {
			for _, x := range cs {
				s, _ := x.(string)
				v.Choices = append(v.Choices, s)
			}
		}
		out = append(out, v)
	}
	writeJSON(w, map[string]any{"mode": mode, "items": out, "total": len(items)})
}

// ---- 要点の復習(概要) ----

var (
	digestHeadRe = regexp.MustCompile(`(?m)^#{2,4}\s+(.+)$`)
	digestBoldRe = regexp.MustCompile(`\*\*([^*\n]{2,30})\*\*`)
)

// chapterDigest は章の本文から要点を作る。「まとめ」「面接でこう語る」があればそれを使い、
// 無ければ見出しごとの最初の一文と重要語(太字)を抜き出す。
func chapterDigest(text string) string {
	lines := strings.Split(text, "\n")
	section := func(name string) string {
		var b []string
		in := false
		for _, l := range lines {
			if m := digestHeadRe.FindStringSubmatch(l); m != nil {
				if in {
					break
				}
				in = strings.Contains(m[1], name)
				continue
			}
			if in {
				b = append(b, l)
			}
		}
		return strings.TrimSpace(strings.Join(b, "\n"))
	}
	var out []string
	if m := section("まとめ"); m != "" {
		out = append(out, "**まとめ**\n\n"+m)
	}
	if m := section("面接でこう語る"); m != "" {
		out = append(out, "**面接でこう語る**\n\n"+m)
	}
	if len(out) > 0 {
		return strings.Join(out, "\n\n")
	}
	// まとめが無い章: 見出し+最初の一文
	var pts []string
	inCode := false
	head := ""
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}
		if m := digestHeadRe.FindStringSubmatch(l); m != nil {
			head = m[1]
			continue
		}
		if head != "" && t != "" && !strings.HasPrefix(t, "|") && !strings.HasPrefix(t, "![") {
			first := t
			if i := strings.Index(first, "。"); i >= 0 {
				first = first[:i+len("。")]
			}
			first = strings.TrimLeft(first, "-*0123456789. ")
			pts = append(pts, "- **"+head+"**: "+first)
			head = ""
		}
	}
	if len(pts) == 0 { // 見出しも無い章は冒頭を短く
		r := []rune(strings.TrimSpace(text))
		if len(r) > 400 {
			r = append(r[:400], []rune("…")...)
		}
		pts = append(pts, string(r))
	}
	seen := map[string]bool{}
	var kws []string
	for _, m := range digestBoldRe.FindAllStringSubmatch(text, -1) {
		if !seen[m[1]] && len(kws) < 12 {
			seen[m[1]] = true
			kws = append(kws, m[1])
		}
	}
	res := "**要点**\n\n" + strings.Join(pts, "\n")
	if len(kws) > 0 {
		res += "\n\n**重要語**: " + strings.Join(kws, "・")
	}
	return res
}

// handleReviewDigest は、学んだ章(または指定の1章)の要点カードを返す。
func handleReviewDigest(w http.ResponseWriter, r *http.Request) {
	type card struct {
		Lesson  int    `json:"lesson"`
		Chapter int    `json:"chapter"`
		Title   string `json:"title"`
		Digest  string `json:"digest"`
		Weak    int    `json:"weak"`
	}
	l, _ := strconv.Atoi(r.URL.Query().Get("lesson"))
	c, _ := strconv.Atoi(r.URL.Query().Get("chapter"))
	if l > 0 && c > 0 { // 1章だけ(問題の復習中に「この章の要点」を開くとき)
		ch, err := loadChapter(l, c)
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		writeJSON(w, card{Lesson: l, Chapter: c, Title: ch.Title, Digest: chapterDigest(ch.Text)})
		return
	}
	mode := r.URL.Query().Get("mode")
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n <= 0 || n > 50 {
		n = 10
	}
	items := srs.pick(mode, 0)
	type key struct{ l, c int }
	weakCount := map[key]int{}
	var order []key
	for _, it := range items {
		k := key{it.Lesson, it.Chapter}
		if _, ok := weakCount[k]; !ok {
			order = append(order, k)
			weakCount[k] = 0
		}
		if it.Weak {
			weakCount[k]++
		}
	}
	rand.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	sort.SliceStable(order, func(i, j int) bool { return weakCount[order[i]] > weakCount[order[j]] }) // 苦手の多い章を先に
	var out []card
	for _, k := range order {
		if len(out) >= n {
			break
		}
		ch, err := loadChapter(k.l, k.c)
		if err != nil {
			continue
		}
		out = append(out, card{Lesson: k.l, Chapter: k.c, Title: ch.Title, Digest: chapterDigest(ch.Text), Weak: weakCount[k]})
	}
	writeJSON(w, map[string]any{"cards": out, "total": len(order)})
}
