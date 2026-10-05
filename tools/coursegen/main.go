// coursegen は lessonNN/chMM.json（クイズ型教材の正）を検証し、README.md を生成する。
//
// 使い方:
//
//	go run ./tools/coursegen              # 全レッスンを検証 + 生成
//	go run ./tools/coursegen -only 2-5    # lesson02〜05 のみ
//	go run ./tools/coursegen -no-gen      # 検証だけ行う
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Question struct {
	Question    string   `json:"question"`
	Choices     []string `json:"choices"`
	Correct     int      `json:"correct"`
	Explanation string   `json:"explanation"`
}

type Chapter struct {
	Lesson   int        `json:"lesson"`
	Chapter  int        `json:"chapter"`
	Title    string     `json:"title"`
	Priority string     `json:"priority"`
	Text     string     `json:"text"`
	Quiz     []Question `json:"quiz"`
}

type courseLesson struct {
	Lesson int    `json:"lesson"`
	Title  string `json:"title"`
}

var (
	root   = flag.String("root", ".", "講座ルート")
	only   = flag.String("only", "", "対象レッスン番号（例: 2-5 や 1,3）。空なら全部")
	noGen  = flag.Bool("no-gen", false, "検証のみ行い、README 生成をしない")
	failed bool
)

func main() {
	flag.Parse()
	targets := parseOnly(*only)

	titles := map[int]string{}
	if b, err := os.ReadFile(filepath.Join(*root, "course.json")); err == nil {
		var cl []courseLesson
		json.Unmarshal(b, &cl)
		for _, c := range cl {
			titles[c.Lesson] = c.Title
		}
	}

	re := regexp.MustCompile(`^ch(\d{2})\.json$`)
	type stat struct{ chapters, questions int }
	stats := map[int]stat{}

	for n := 1; n <= 89; n++ {
		if targets != nil && !targets[n] {
			continue
		}
		dir := filepath.Join(*root, fmt.Sprintf("lesson%02d", n))
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		var chs []*Chapter
		for _, e := range entries {
			m := re.FindStringSubmatch(e.Name())
			if m == nil {
				continue
			}
			path := filepath.Join(dir, e.Name())
			c, errs := loadAndCheck(path)
			if c != nil {
				chs = append(chs, c)
				s := stats[n]
				s.chapters++
				s.questions += len(c.Quiz)
				stats[n] = s
			}
			for _, msg := range errs {
				fmt.Printf("NG %s: %s\n", path, msg)
				failed = true
			}
		}
		if !*noGen && !failed && len(chs) > 0 {
			sort.Slice(chs, func(i, j int) bool { return chs[i].Chapter < chs[j].Chapter })
			if err := genReadme(dir, n, titles[n], chs); err != nil {
				fmt.Printf("生成 NG lesson%02d: %v\n", n, err)
				failed = true
			}
		}
	}

	var nums []int
	for n := range stats {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	fmt.Println("| レッスン | チャプター数 | 問題数 |")
	fmt.Println("|---|---|---|")
	totalCh, totalQ := 0, 0
	for _, n := range nums {
		s := stats[n]
		fmt.Printf("| lesson%02d %s | %d | %d |\n", n, titles[n], s.chapters, s.questions)
		totalCh += s.chapters
		totalQ += s.questions
	}
	fmt.Printf("| 合計 | %d | %d |\n", totalCh, totalQ)
	if failed {
		os.Exit(1)
	}
	fmt.Println("ALL OK")
}

func parseOnly(s string) map[int]bool {
	if s == "" {
		return nil
	}
	m := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			a, _ := strconv.Atoi(strings.TrimSpace(lo))
			b, _ := strconv.Atoi(strings.TrimSpace(hi))
			for i := a; i <= b; i++ {
				m[i] = true
			}
		} else {
			a, _ := strconv.Atoi(strings.TrimSpace(part))
			m[a] = true
		}
	}
	return m
}

func loadAndCheck(path string) (*Chapter, []string) {
	var errs []string
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, []string{err.Error()}
	}
	var c Chapter
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, []string{"JSON parse error: " + err.Error()}
	}
	if c.Title == "" || strings.TrimSpace(c.Text) == "" {
		errs = append(errs, "title または text が空です")
	}
	switch c.Priority {
	case "🔴", "🟡", "⚪":
	default:
		errs = append(errs, "priority は 🔴/🟡/⚪ のいずれかにしてください")
	}
	if n := len([]rune(c.Text)); n < 200 {
		errs = append(errs, fmt.Sprintf("text が短すぎます (%d 文字。300字以上を目安に)", n))
	}
	if len(c.Quiz) < 2 {
		errs = append(errs, "quiz は 2 問以上にしてください")
	}
	for i, q := range c.Quiz {
		if strings.TrimSpace(q.Question) == "" {
			errs = append(errs, fmt.Sprintf("問%d の question が空です", i+1))
		}
		if strings.TrimSpace(q.Explanation) == "" {
			errs = append(errs, fmt.Sprintf("問%d の explanation が空です", i+1))
		}
		if len(q.Choices) < 2 || len(q.Choices) > 6 {
			errs = append(errs, fmt.Sprintf("問%d の選択肢は 2〜6 個にしてください", i+1))
		} else if q.Correct < 0 || q.Correct >= len(q.Choices) {
			errs = append(errs, fmt.Sprintf("問%d の correct が範囲外です", i+1))
		}
		seen := map[string]bool{}
		for _, ch := range q.Choices {
			if seen[ch] {
				errs = append(errs, fmt.Sprintf("問%d に重複した選択肢があります", i+1))
				break
			}
			seen[ch] = true
		}
	}
	return &c, errs
}

func genReadme(dir string, lesson int, title string, chs []*Chapter) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# 第%d章 %s\n", lesson, title)
	if intro, err := os.ReadFile(filepath.Join(dir, "intro.md")); err == nil {
		b.WriteString("\n" + strings.TrimSpace(string(intro)) + "\n")
	}
	for _, c := range chs {
		fmt.Fprintf(&b, "\n## %d. %s\n\n%s\n", c.Chapter, c.Title, strings.TrimSpace(c.Text))
		b.WriteString("\n### 確認問題\n")
		for i, q := range c.Quiz {
			fmt.Fprintf(&b, "\n**問%d.** %s\n\n", i+1, q.Question)
			for ci, ch := range q.Choices {
				fmt.Fprintf(&b, "%d. %s\n", ci+1, ch)
			}
			fmt.Fprintf(&b, "\n<details><summary>答えと解説</summary>\n\n正解: **%d**\n\n%s\n\n</details>\n", q.Correct+1, q.Explanation)
		}
	}
	return os.WriteFile(filepath.Join(dir, "README.md"), []byte(b.String()), 0o644)
}
