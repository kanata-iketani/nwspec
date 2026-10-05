// ネスペどうじょう — ネットワークスペシャリスト試験(免除なし)学習アプリ。
// 左に教科書、右に一問一答(4択)を表示し、回答すると即判定+解説。
// 教材データは lessonNN/chMM.json を正とする。
//
// 起動: go -C app run .  →  http://127.0.0.1:8085
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed static
var staticFS embed.FS

var (
	courseRoot string
	appDir     string
)

const (
	courseTitle = "ネスペどうじょう"
	defaultAddr = "127.0.0.1:8085"
)

// ---- 教材データ ----

type Question struct {
	Question    string   `json:"question"`
	Choices     []string `json:"choices"`
	Correct     int      `json:"correct"` // 0 始まり
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

func chapterPath(lesson, ch int) string {
	return filepath.Join(courseRoot, fmt.Sprintf("lesson%02d", lesson), fmt.Sprintf("ch%02d.json", ch))
}

func loadChapter(lesson, ch int) (*Chapter, error) {
	b, err := os.ReadFile(chapterPath(lesson, ch))
	if err != nil {
		return nil, err
	}
	var c Chapter
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", chapterPath(lesson, ch), err)
	}
	return &c, nil
}

// ---- 進捗と解答状況 ----

type progressStore struct {
	mu   sync.Mutex
	path string
	m    map[string]string // "lesson01/ch01" -> "ran" | "passed"
}

var progress *progressStore

func newProgressStore(path string) *progressStore {
	s := &progressStore{path: path, m: map[string]string{}}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &s.m)
	}
	return s
}

func (s *progressStore) get(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[key]
}

func (s *progressStore) set(key, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m[key] == "passed" && status != "passed" {
		return
	}
	if s.m[key] == status {
		return
	}
	s.m[key] = status
	b, _ := json.MarshalIndent(s.m, "", "  ")
	os.WriteFile(s.path, b, 0o644)
}

func progressKey(lesson, ch int) string {
	return fmt.Sprintf("lesson%02d/ch%02d", lesson, ch)
}

// quizStore は「どの問題を正解済みか」を保持する。
type quizStore struct {
	mu   sync.Mutex
	path string
	m    map[string][]bool // progressKey -> 問題ごとの正解済みフラグ
}

var quizState *quizStore

func newQuizStore(path string) *quizStore {
	s := &quizStore{path: path, m: map[string][]bool{}}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &s.m)
	}
	return s
}

// markSolved は正解を記録し、チャプター全問正解なら true を返す。
func (s *quizStore) markSolved(key string, qIndex, total int) (solved []bool, cleared bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.m[key]
	if len(cur) != total {
		fixed := make([]bool, total)
		copy(fixed, cur)
		cur = fixed
	}
	if qIndex >= 0 && qIndex < total {
		cur[qIndex] = true
	}
	s.m[key] = cur
	b, _ := json.MarshalIndent(s.m, "", "  ")
	os.WriteFile(s.path, b, 0o644)
	cleared = true
	for _, ok := range cur {
		cleared = cleared && ok
	}
	out := make([]bool, total)
	copy(out, cur)
	return out, cleared
}

func (s *quizStore) get(key string, total int) []bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]bool, total)
	copy(out, s.m[key])
	return out
}

// ---- 誤答ログ (弱点分析用) ----

type Mistake struct {
	Time     string `json:"time"`
	Lesson   int    `json:"lesson"`
	Chapter  int    `json:"chapter"`
	Title    string `json:"title"`
	Q        int    `json:"q"`
	Question string `json:"question"`
	Chosen   string `json:"chosen"`
	Answer   string `json:"answer"`
}

type mistakeStore struct {
	mu   sync.Mutex
	path string
	list []Mistake
}

var mistakes *mistakeStore

func newMistakeStore(path string) *mistakeStore {
	s := &mistakeStore{path: path}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &s.list)
	}
	return s
}

func (s *mistakeStore) add(m Mistake) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = append(s.list, m)
	if len(s.list) > 2000 {
		s.list = s.list[len(s.list)-2000:]
	}
	b, _ := json.MarshalIndent(s.list, "", "  ")
	os.WriteFile(s.path, b, 0o644)
}

func (s *mistakeStore) all() []Mistake {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Mistake, len(s.list))
	copy(out, s.list)
	return out
}

// ---- HTTP ハンドラ ----

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

var chFileRe = regexp.MustCompile(`^ch(\d{2})\.json$`)

func handleCourse(w http.ResponseWriter, r *http.Request) {
	var titles []courseLesson
	if b, err := os.ReadFile(filepath.Join(courseRoot, "course.json")); err == nil {
		json.Unmarshal(b, &titles)
	}
	titleOf := map[int]string{}
	for _, t := range titles {
		titleOf[t.Lesson] = t.Title
	}

	type chapterInfo struct {
		Chapter  int    `json:"chapter"`
		Title    string `json:"title"`
		Priority string `json:"priority"`
		Status   string `json:"status"`
	}
	type lessonInfo struct {
		Lesson   int           `json:"lesson"`
		Title    string        `json:"title"`
		Chapters []chapterInfo `json:"chapters"`
	}

	var lessons []lessonInfo
	for n := 1; n <= 99; n++ {
		dir := filepath.Join(courseRoot, fmt.Sprintf("lesson%02d", n))
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		li := lessonInfo{Lesson: n, Title: titleOf[n]}
		for _, e := range entries {
			m := chFileRe.FindStringSubmatch(e.Name())
			if m == nil {
				continue
			}
			var chNum int
			fmt.Sscanf(m[1], "%d", &chNum)
			c, err := loadChapter(n, chNum)
			if err != nil {
				continue
			}
			li.Chapters = append(li.Chapters, chapterInfo{
				Chapter:  chNum,
				Title:    c.Title,
				Priority: c.Priority,
				Status:   progress.get(progressKey(n, chNum)),
			})
		}
		if len(li.Chapters) > 0 {
			sort.Slice(li.Chapters, func(i, j int) bool { return li.Chapters[i].Chapter < li.Chapters[j].Chapter })
			lessons = append(lessons, li)
		}
	}
	writeJSON(w, map[string]any{"lessons": lessons})
}

func pathInts(r *http.Request) (lesson, ch int, ok bool) {
	if _, err := fmt.Sscanf(r.PathValue("lesson"), "%d", &lesson); err != nil {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(r.PathValue("ch"), "%d", &ch); err != nil {
		return 0, 0, false
	}
	return lesson, ch, lesson >= 1 && lesson <= 99 && ch >= 1 && ch <= 99
}

// handleChapter は正解と解説を**含めずに**チャプターを返す。
func handleChapter(w http.ResponseWriter, r *http.Request) {
	lesson, ch, ok := pathInts(r)
	if !ok {
		http.Error(w, "invalid id", 400)
		return
	}
	c, err := loadChapter(lesson, ch)
	if err != nil {
		http.Error(w, "chapter not found", 404)
		return
	}
	type qView struct {
		Question string   `json:"question"`
		Choices  []string `json:"choices"`
	}
	qs := make([]qView, len(c.Quiz))
	for i, q := range c.Quiz {
		qs[i] = qView{Question: q.Question, Choices: q.Choices}
	}
	writeJSON(w, map[string]any{
		"lesson":   c.Lesson,
		"chapter":  c.Chapter,
		"title":    c.Title,
		"priority": c.Priority,
		"text":     c.Text,
		"quiz":     qs,
		"solved":   quizState.get(progressKey(lesson, ch), len(c.Quiz)),
		"status":   progress.get(progressKey(lesson, ch)),
	})
}

type answerReq struct {
	Lesson  int `json:"lesson"`
	Chapter int `json:"chapter"`
	Q       int `json:"q"`
	Choice  int `json:"choice"`
}

func handleAnswer(w http.ResponseWriter, r *http.Request) {
	var req answerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	c, err := loadChapter(req.Lesson, req.Chapter)
	if err != nil {
		http.Error(w, "chapter not found", 404)
		return
	}
	if req.Q < 0 || req.Q >= len(c.Quiz) {
		http.Error(w, "invalid question", 400)
		return
	}
	q := c.Quiz[req.Q]
	if req.Choice < 0 || req.Choice >= len(q.Choices) {
		http.Error(w, "invalid choice", 400)
		return
	}

	key := progressKey(req.Lesson, req.Chapter)
	correct := req.Choice == q.Correct

	var solved []bool
	var cleared bool
	if correct {
		solved, cleared = quizState.markSolved(key, req.Q, len(c.Quiz))
		if cleared {
			progress.set(key, "passed")
		} else {
			progress.set(key, "ran")
		}
	} else {
		solved = quizState.get(key, len(c.Quiz))
		progress.set(key, "ran")
		mistakes.add(Mistake{
			Time: time.Now().Format("2006-01-02 15:04"), Lesson: req.Lesson, Chapter: req.Chapter,
			Title: c.Title, Q: req.Q, Question: q.Question,
			Chosen: q.Choices[req.Choice], Answer: q.Choices[q.Correct],
		})
	}
	writeJSON(w, map[string]any{
		"correct":      correct,
		"correctIndex": q.Correct,
		"explanation":  q.Explanation,
		"solved":       solved,
		"cleared":      cleared,
	})
}

func handleMistakes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"items": mistakes.all()})
}

// ---- 起動 ----

func detectRoot() string {
	for _, dir := range []string{".", ".."} {
		if _, err := os.Stat(filepath.Join(dir, "course.json")); err == nil {
			abs, _ := filepath.Abs(dir)
			return abs
		}
	}
	abs, _ := filepath.Abs(".")
	return abs
}

func main() {
	flag.StringVar(&courseRoot, "root", "", "講座ルート（lessonNN の親ディレクトリ。省略時は自動検出）")
	flag.StringVar(&claudeModel, "claude-model", "sonnet", "質問回答に使う Claude モデル（claude CLI の --model に渡す。CLI がない環境では質問機能のみ無効）")
	flag.StringVar(&aiConfigURL, "ai-config-url", "https://raw.githubusercontent.com/kanata-iketani/nwspec/main/ai-config.json", "AI利用制限設定の取得元URL（空でローカル ai-config.json のみ使用）")
	addr := flag.String("addr", defaultAddr, "待ち受けアドレス（127.0.0.1 のみ推奨）")
	flag.Parse()

	if courseRoot == "" {
		courseRoot = detectRoot()
	}
	appDir = filepath.Join(courseRoot, "app")
	progress = newProgressStore(filepath.Join(appDir, "progress.json"))
	quizState = newQuizStore(filepath.Join(appDir, "quizstate.json"))
	mistakes = newMistakeStore(filepath.Join(appDir, "mistakes.json"))
	questions = newQAStore(filepath.Join(appDir, "questions.json"))

	static, _ := fs.Sub(staticFS, "static")
	http.Handle("/", http.FileServer(http.FS(static)))
	http.HandleFunc("GET /api/course", handleCourse)
	http.HandleFunc("GET /api/chapter/{lesson}/{ch}", handleChapter)
	http.HandleFunc("POST /api/quiz/answer", handleAnswer)
	http.HandleFunc("GET /api/mistakes", handleMistakes)
	http.HandleFunc("POST /api/ask", handleAsk)
	http.HandleFunc("POST /api/oral", handleOral)
	http.HandleFunc("GET /api/qa/latest", handleLatestGet)
	http.HandleFunc("POST /api/qa/latest", handleLatestCreate)
	http.HandleFunc("GET /api/qa", handleQAList)
	http.HandleFunc("DELETE /api/qa/{id}", handleQADelete)
	http.HandleFunc("GET /api/qa/summary", handleSummaryGet)
	http.HandleFunc("POST /api/qa/summary", handleSummaryCreate)
	http.HandleFunc("POST /api/qa/review", handleReviewCreate)

	log.Printf("%sを起動しました: http://%s （講座ルート: %s）", courseTitle, *addr, courseRoot)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

var _ = strings.TrimSpace
