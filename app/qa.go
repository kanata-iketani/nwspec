// 質問対話機能(ネスペ版)。
// 回答はローカルの Claude Code CLI (`claude -p`) をヘッドレス実行して得る。
// Q&A は app/questions.json に保存し、誤答ログとあわせて
// 弱点まとめ (app/notes.md) と復習問題 (lesson90/chNN.json) の自動生成に使う。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var claudeModel string

const (
	reviewLesson = 90
	qaCourseDesc = "「ネスペどうじょう」(ネットワークスペシャリスト試験に午前Ⅰ免除なしで合格することを目指す講座) の"
	qaAudience   = "受講者は IT の実務経験は浅く、基本情報技術者も持っていません。専門用語は毎回かみ砕き、具体的なパケットの流れや身近な例えで説明してください。"
)

// ---- Q&A ストア ----

type QA struct {
	ID           string `json:"id"`
	Time         string `json:"time"`
	Lesson       int    `json:"lesson"`
	Chapter      int    `json:"chapter"`
	ChapterTitle string `json:"chapterTitle"`
	Question     string `json:"question"`
	Answer       string `json:"answer"`
}

type qaStore struct {
	mu   sync.Mutex
	path string
	list []QA
}

var questions *qaStore

func newQAStore(path string) *qaStore {
	s := &qaStore{path: path}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &s.list)
	}
	return s
}

func (s *qaStore) save() {
	b, _ := json.MarshalIndent(s.list, "", "  ")
	os.WriteFile(s.path, b, 0o644)
}

func (s *qaStore) add(qa QA) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = append(s.list, qa)
	s.save()
}

func (s *qaStore) remove(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, qa := range s.list {
		if qa.ID == id {
			s.list = append(s.list[:i], s.list[i+1:]...)
			s.save()
			return true
		}
	}
	return false
}

func (s *qaStore) all() []QA {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]QA, len(s.list))
	copy(out, s.list)
	return out
}

func (s *qaStore) recentInChapter(lesson, chapter, n int) []QA {
	s.mu.Lock()
	defer s.mu.Unlock()
	var hits []QA
	for _, qa := range s.list {
		if qa.Lesson == lesson && qa.Chapter == chapter {
			hits = append(hits, qa)
		}
	}
	if len(hits) > n {
		hits = hits[len(hits)-n:]
	}
	return hits
}

// ---- Claude CLI 実行 ----

func runClaude(category, prompt string, timeout time.Duration) (string, error) {
	return runClaudeArgs(category, prompt, timeout)
}

// ---- AI 利用制限 (ai-config.json / GitHub からリモート取得) ----
// 管理者(リポジトリ所有者)が GitHub 上の ai-config.json を編集すると、
// 実行中のアプリが定期取得して反映する。値: -1=無制限 / 0=停止 / N>0=1日N回。
// 「チャット」(自由質問)と「コア機能」(口頭試問・弱点まとめ・復習生成・統計ノート)で別管理。

type aiConfig struct {
	ChatDailyLimit int `json:"chat_daily_limit"`
	CoreDailyLimit int `json:"core_daily_limit"`
}

var (
	aiConfigURL string // -ai-config-url フラグ
	aiMu        sync.Mutex
	aiCached    aiConfig
	aiFetchedAt time.Time
)

// loadAIConfig はリモート(GitHub) → ローカル ai-config.json → 既定値 の順で設定を得る。
// リモートは10分キャッシュ。
func loadAIConfig() aiConfig {
	cfg := aiConfig{ChatDailyLimit: 20, CoreDailyLimit: -1} // 既定値
	if b, err := os.ReadFile(filepath.Join(courseRoot, "ai-config.json")); err == nil {
		json.Unmarshal(b, &cfg)
	}
	if aiConfigURL == "" {
		return cfg
	}
	if time.Since(aiFetchedAt) < 10*time.Minute {
		return aiCached
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(aiConfigURL)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			var remote aiConfig
			if json.NewDecoder(resp.Body).Decode(&remote) == nil {
				cfg = remote
			}
		}
	}
	aiCached = cfg
	aiFetchedAt = time.Now()
	return cfg
}

type aiUsage struct {
	Date string `json:"date"`
	Chat int    `json:"chat"`
	Core int    `json:"core"`
}

func checkAIBudget(category string) error {
	aiMu.Lock()
	defer aiMu.Unlock()
	cfg := loadAIConfig()
	limit := cfg.CoreDailyLimit
	label := "AI 機能"
	if category == "chat" {
		limit = cfg.ChatDailyLimit
		label = "チャット質問"
	}
	if limit < 0 {
		return nil // 無制限
	}
	if limit == 0 {
		return fmt.Errorf("%sは現在停止中です(管理者設定)。学習機能はそのまま使えます", label)
	}
	path := filepath.Join(appDir, "ai_usage.json")
	var u aiUsage
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &u)
	}
	today := time.Now().Format("2006-01-02")
	if u.Date != today {
		u = aiUsage{Date: today}
	}
	count := &u.Core
	if category == "chat" {
		count = &u.Chat
	}
	if *count >= limit {
		return fmt.Errorf("今日の%s上限(%d回)に達しました。明日また使えます(学習機能はそのまま使えます)", label, limit)
	}
	*count++
	b, _ := json.Marshal(u)
	os.WriteFile(path, b, 0o644)
	return nil
}

func runClaudeArgs(category, prompt string, timeout time.Duration, extra ...string) (string, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return "", errors.New("この機能にはローカルの Claude Code CLI が必要です (claude コマンドが見つかりません)")
	}
	if err := checkAIBudget(category); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	args := append([]string{"-p", "--model", claudeModel, "--output-format", "text"}, extra...)
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = courseRoot
	cmd.Stdin = strings.NewReader(prompt)
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", errors.New("Claude の応答がタイムアウトしました")
	}
	if err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("claude の実行に失敗しました: %s", msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// ---- 質問に回答 ----

type askReq struct {
	Lesson   int    `json:"lesson"`
	Chapter  int    `json:"chapter"`
	Question string `json:"question"`
}

func handleAsk(w http.ResponseWriter, r *http.Request) {
	var req askReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if strings.TrimSpace(req.Question) == "" {
		http.Error(w, "質問が空です", 400)
		return
	}

	var b strings.Builder
	b.WriteString("あなたは" + qaCourseDesc + "講師です。" + qaAudience + "\n\n")

	chapterTitle := ""
	if c, err := loadChapter(req.Lesson, req.Chapter); err == nil {
		chapterTitle = c.Title
		fmt.Fprintf(&b, "現在学習中のチャプター: lesson%02d ch%02d「%s」\n\n教科書:\n%s\n\n", req.Lesson, req.Chapter, c.Title, c.Text)
		b.WriteString("このチャプターの問題と正解(参考):\n")
		for i, q := range c.Quiz {
			fmt.Fprintf(&b, "問%d: %s / 正解: %s / 解説: %s\n", i+1, q.Question, q.Choices[q.Correct], q.Explanation)
		}
		b.WriteString("\n")
	}
	if recent := questions.recentInChapter(req.Lesson, req.Chapter, 3); len(recent) > 0 {
		b.WriteString("このチャプターでの直近の質疑:\n")
		for _, qa := range recent {
			fmt.Fprintf(&b, "Q: %s\nA: %s\n", qa.Question, qa.Answer)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "受講者の質問:\n%s\n\n", req.Question)
	b.WriteString(`回答のルール:
- です・ます調で、超初心者向けにかみ砕いて答える(目安400字以内)
- 法律用語は身近な例え話に置き換えてから正式な言い方を添える
- 受講者がまだ解いていない問題の答えを先回りして明かさない(解き終えた問題の解説は深掘りしてよい)
- 本試験(午前Ⅰ・午前Ⅱ・午後)のどこでどう問われやすいかをひと言添える
- ファイル操作やツールは使わず、上記の文脈だけで答える。Markdown で書く`)

	answer, err := runClaude("chat", b.String(), 180*time.Second)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}

	qa := QA{
		ID:           fmt.Sprintf("qa-%d", time.Now().UnixNano()),
		Time:         time.Now().Format("2006-01-02 15:04"),
		Lesson:       req.Lesson,
		Chapter:      req.Chapter,
		ChapterTitle: chapterTitle,
		Question:     req.Question,
		Answer:       answer,
	}
	questions.add(qa)
	writeJSON(w, qa)
}

func handleQAList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"items": questions.all()})
}

func handleQADelete(w http.ResponseWriter, r *http.Request) {
	if !questions.remove(r.PathValue("id")) {
		http.Error(w, "not found", 404)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// ---- 弱点まとめ (質問 + 誤答ログ) ----

func notesPath() string { return filepath.Join(appDir, "notes.md") }

func handleSummaryGet(w http.ResponseWriter, r *http.Request) {
	b, _ := os.ReadFile(notesPath())
	writeJSON(w, map[string]string{"summary": string(b)})
}

func buildStudyLog(b *strings.Builder) int {
	total := 0
	if all := questions.all(); len(all) > 0 {
		b.WriteString("## 受講者の質問記録\n")
		for _, qa := range all {
			fmt.Fprintf(b, "- [lesson%02d %s] Q: %s\n", qa.Lesson, qa.ChapterTitle, qa.Question)
		}
		total += len(all)
	}
	if ms := mistakes.all(); len(ms) > 0 {
		if len(ms) > 120 {
			ms = ms[len(ms)-120:]
		}
		b.WriteString("\n## 間違えた問題の記録(直近)\n")
		for _, m := range ms {
			fmt.Fprintf(b, "- [lesson%02d %s] %s → 「%s」を選んで誤答(正解: %s)\n",
				m.Lesson, m.Title, m.Question, m.Chosen, m.Answer)
		}
		total += len(ms)
	}
	return total
}

func handleSummaryCreate(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	b.WriteString("あなたは" + qaCourseDesc + "講師です。" + qaAudience + "\n")
	b.WriteString("以下は受講者の学習記録(質問と誤答)です。\n\n")
	if buildStudyLog(&b) == 0 {
		http.Error(w, "まだ記録がありません。問題を解いたり質問したりしてから使ってください", 400)
		return
	}
	b.WriteString(`
この記録から「弱点まとめノート」を Markdown で作成してください。構成:
# 弱点まとめ
## 苦手分野トップ3 (分野ごとに、なぜ間違えやすいかを2〜3文で)
## 間違えやすいポイント (誤答から見える具体的な勘違いを箇条書き)
## よく理解できている点
## 次にやるべきこと (具体的な復習アドバイス2〜3個。関連レッスン番号つき)
ルール: です・ます調。励ます調子で。ツールは使わない。Markdown 本文だけを出力する。`)

	summary, err := runClaude("core", b.String(), 300*time.Second)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	os.WriteFile(notesPath(), []byte(summary+"\n"), 0o644)
	writeJSON(w, map[string]string{"summary": summary})
}

// ---- 復習問題の生成 (lesson90/chNN.json) ----

func handleReviewCreate(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	b.WriteString("あなたは" + qaCourseDesc + "教材作成者です。" + qaAudience + "\n")
	b.WriteString("以下は受講者の学習記録(質問と誤答)です。この受講者の弱点を突く復習チャプターを 1〜2 個作ってください。\n\n")
	if ws := weakLessonSummary(5); ws != "" {
		b.WriteString(ws + "\n")
	}
	if buildStudyLog(&b) == 0 {
		http.Error(w, "まだ記録がありません。問題を解いたり質問したりしてから使ってください", 400)
		return
	}
	b.WriteString("\n重要: 誤答の多い分野を優先し、**同じ論点を別の角度から**出題すること(事例を変える・問い方を変える・正誤を反転する・比較で問う)。受講者が間違えた問題の丸写しは禁止。\n")
	b.WriteString("\n出力は次のスキーマの JSON 配列 **のみ** を ```json フェンスで囲んで出力してください。前後に説明文を書かないでください。\n\n")
	b.WriteString("```\n[{\n" + `  "lesson": 90, "chapter": 1,
  "title": "復習: <弱点のテーマ>",
  "priority": "🔴",
  "text": "教科書 Markdown。受講者が間違えたポイントを重点的に、かみ砕いて説明する(300〜600字)",
  "quiz": [
    {
      "question": "本試験の午前問題風の問題文(4択)。誤答した論点の類題にする",
      "choices": ["選択肢1", "選択肢2", "選択肢3", "選択肢4"],
      "correct": 0,
      "explanation": "正解の理由と、他の選択肢がなぜ誤りかを初心者向けに解説"
    }
  ]
}]` + "\n```\n\n")
	b.WriteString(`ルール:
- chapter は 1 から連番 (保存時に振り直すので仮でよい)
- quiz は 1 チャプターあたり 3〜5 問。correct は 0 始まりの添字
- 過去問の丸写しは禁止。オリジナルの問題にする
- 法改正に注意し、確信が持てない論点は出題しない
- priority は 🔴/🟡/⚪。です・ます調。ツールは使わない。JSON だけを出力する`)

	raw, err := runClaude("core", b.String(), 420*time.Second)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	chapters, err := parseChapterArray(raw)
	if err != nil {
		http.Error(w, "生成結果の JSON を解釈できませんでした: "+err.Error(), 502)
		return
	}

	type created struct {
		Lesson  int    `json:"lesson"`
		Chapter int    `json:"chapter"`
		Title   string `json:"title"`
	}
	var ok []created
	var failed []string

	dir := filepath.Join(courseRoot, fmt.Sprintf("lesson%02d", reviewLesson))
	os.MkdirAll(dir, 0o755)
	next := nextChapterNum(dir)

	for _, c := range chapters {
		if err := validateQuizChapter(&c); err != nil {
			failed = append(failed, fmt.Sprintf("%s (%v)", c.Title, err))
			continue
		}
		c.Lesson = reviewLesson
		c.Chapter = next
		buf, _ := json.MarshalIndent(&c, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("ch%02d.json", next)), append(buf, '\n'), 0o644); err != nil {
			failed = append(failed, c.Title+" (書き込み失敗)")
			continue
		}
		ok = append(ok, created{reviewLesson, next, c.Title})
		next++
	}
	if len(ok) > 0 {
		ensureReviewLessonInCourse()
	}
	writeJSON(w, map[string]any{"created": ok, "failed": failed})
}

func parseChapterArray(raw string) ([]Chapter, error) {
	if m := regexp.MustCompile("(?s)```(?:json)?\\s*(\\[.*?\\])\\s*```").FindStringSubmatch(raw); m != nil {
		raw = m[1]
	} else if i := strings.Index(raw, "["); i >= 0 {
		if j := strings.LastIndex(raw, "]"); j > i {
			raw = raw[i : j+1]
		}
	}
	var chapters []Chapter
	if err := json.Unmarshal([]byte(raw), &chapters); err != nil {
		return nil, err
	}
	if len(chapters) == 0 {
		return nil, errors.New("チャプターが空です")
	}
	return chapters, nil
}

// validateQuizChapter はクイズ型チャプターの構造を検証する。
func validateQuizChapter(c *Chapter) error {
	if c.Title == "" || strings.TrimSpace(c.Text) == "" {
		return errors.New("title または text が空です")
	}
	if len(c.Quiz) == 0 {
		return errors.New("quiz が空です")
	}
	for i, q := range c.Quiz {
		if strings.TrimSpace(q.Question) == "" || strings.TrimSpace(q.Explanation) == "" {
			return fmt.Errorf("問%d の question/explanation が空です", i+1)
		}
		if len(q.Choices) < 2 || len(q.Choices) > 6 {
			return fmt.Errorf("問%d の選択肢は 2〜6 個にしてください", i+1)
		}
		if q.Correct < 0 || q.Correct >= len(q.Choices) {
			return fmt.Errorf("問%d の correct が選択肢の範囲外です", i+1)
		}
	}
	return nil
}

func nextChapterNum(dir string) int {
	next := 1
	re := regexp.MustCompile(`^ch(\d{2})\.json$`)
	entries, _ := os.ReadDir(dir)
	var nums []int
	for _, e := range entries {
		if m := re.FindStringSubmatch(e.Name()); m != nil {
			var n int
			fmt.Sscanf(m[1], "%d", &n)
			nums = append(nums, n)
		}
	}
	if len(nums) > 0 {
		sort.Ints(nums)
		next = nums[len(nums)-1] + 1
	}
	return next
}

func ensureReviewLessonInCourse() {
	path := filepath.Join(courseRoot, "course.json")
	var lessons []courseLesson
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &lessons)
	}
	for _, l := range lessons {
		if l.Lesson == reviewLesson {
			return
		}
	}
	lessons = append(lessons, courseLesson{Lesson: reviewLesson, Title: "復習問題（あなた専用）"})
	b, _ := json.MarshalIndent(lessons, "", "  ")
	os.WriteFile(path, append(b, '\n'), 0o644)
}
