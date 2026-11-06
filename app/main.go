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
	Image       string   `json:"image,omitempty"`  // 過去問午前: 原本の切り抜き画像
	Answer      string   `json:"answer,omitempty"` // 記述式(午後過去問)の解答例。あれば自己採点モード
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

// unmark は正解済みを取り消して未正解に戻す(再挑戦で外れたとき)。
func (s *quizStore) unmark(key string, qIndex, total int) []bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := make([]bool, total)
	copy(cur, s.m[key])
	if qIndex >= 0 && qIndex < total {
		cur[qIndex] = false
	}
	s.m[key] = cur
	b, _ := json.MarshalIndent(s.m, "", "  ")
	os.WriteFile(s.path, b, 0o644)
	out := make([]bool, total)
	copy(out, cur)
	return out
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
	return lesson, ch, lesson >= 1 && lesson <= 999 && ch >= 1 && ch <= 99
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
		Write    bool     `json:"write,omitempty"`
		Image    string   `json:"image,omitempty"`
	}
	qs := make([]qView, len(c.Quiz))
	for i, q := range c.Quiz {
		qs[i] = qView{Question: q.Question, Choices: q.Choices, Write: q.Answer != "", Image: q.Image}
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
	Q       int    `json:"q"`
	Choice  int    `json:"choice"`
	Drill   string `json:"drill,omitempty"` // "weak" のとき苦手ドリルとして連続正解を数える
}

// handleReveal は記述式問題の解答例を返す(自己採点の前に表示する)。
func handleReveal(w http.ResponseWriter, r *http.Request) {
	var req answerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	c, err := loadChapter(req.Lesson, req.Chapter)
	if err != nil || req.Q < 0 || req.Q >= len(c.Quiz) {
		http.Error(w, "not found", 404)
		return
	}
	writeJSON(w, map[string]string{"answer": c.Quiz[req.Q].Answer})
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
	// choice == -1 は「わからない」(不正解として扱い、にがて帳に入れる)
	if req.Choice < -1 || req.Choice >= len(q.Choices) {
		http.Error(w, "invalid choice", 400)
		return
	}
	chosen := "わからない"
	if req.Choice >= 0 {
		chosen = q.Choices[req.Choice]
	}

	key := progressKey(req.Lesson, req.Chapter)
	correct := req.Choice == q.Correct
	srsRecord(req.Lesson, req.Chapter, req.Q, correct) // 復習(間隔反復)の記録

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
		// 以前正解していても、今回外れたら未正解に上書きする
		solved = quizState.unmark(key, req.Q, len(c.Quiz))
		progress.set(key, "ran")
		mistakes.add(Mistake{
			Time: time.Now().Format("2006-01-02 15:04"), Lesson: req.Lesson, Chapter: req.Chapter,
			Title: c.Title, Q: req.Q, Question: q.Question,
			Chosen: chosen, Answer: q.Choices[q.Correct],
		})
	}
	// 苦手問題: 外したら苦手に入れる。苦手ドリルで正解したら連続正解を数える
	graduated, streak := false, 0
	if !correct {
		weak.miss(req.Lesson, req.Chapter, req.Q)
	} else if req.Drill == "weak" {
		graduated, streak = weak.hit(req.Lesson, req.Chapter, req.Q)
	}
	writeJSON(w, map[string]any{
		"correct":      correct,
		"correctIndex": q.Correct,
		"explanation":  q.Explanation,
		"solved":       solved,
		"cleared":      cleared,
		"graduated":    graduated,
		"streak":       streak,
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
	flag.StringVar(&claudeModel, "claude-model", "opus", "質問回答に使う Claude モデル（claude CLI の --model に渡す。CLI がない環境では質問機能のみ無効）")
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
	weak = newWeakStore(filepath.Join(appDir, "weak.json"), mistakes.all())

	static, _ := fs.Sub(staticFS, "static")
	http.Handle("/", http.FileServer(http.FS(static)))
	// 過去問の原本切り抜き画像
	http.Handle("/kakomon/img/", http.StripPrefix("/kakomon/img/", http.FileServer(http.Dir(filepath.Join(courseRoot, "kakomon", "img")))))
	http.HandleFunc("GET /api/course", handleCourse)
	http.HandleFunc("GET /api/chapter/{lesson}/{ch}", handleChapter)
	http.HandleFunc("POST /api/quiz/answer", handleAnswer)
	http.HandleFunc("POST /api/quiz/reveal", handleReveal)
	http.HandleFunc("GET /api/drill", handleDrill)
	http.HandleFunc("GET /api/drill/stats", handleDrillStats)
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
	srsInit() // 復習(間隔反復)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

var _ = strings.TrimSpace
