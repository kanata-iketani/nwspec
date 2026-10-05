// AI 学習機能(口頭試問・弱点分析・最新統計/法改正ノート)。
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// runClaudeWeb は Web 検索を許可して Claude を実行する(最新統計・法改正の調査用)。
func runClaudeWeb(prompt string, timeout time.Duration) (string, error) {
	return runClaudeArgs("core", prompt, timeout, "--allowedTools", "WebSearch,WebFetch")
}

// ---- 弱点のレッスン別集計 ----

func lessonTitles() map[int]string {
	titles := map[int]string{}
	var cl []courseLesson
	if b, err := os.ReadFile(filepath.Join(courseRoot, "course.json")); err == nil {
		json.Unmarshal(b, &cl)
	}
	for _, c := range cl {
		titles[c.Lesson] = c.Title
	}
	return titles
}

// weakLessonSummary は誤答をレッスン別に集計し、弱い順の要約文を返す。
func weakLessonSummary(topN int) string {
	counts := map[int]int{}
	for _, m := range mistakes.all() {
		counts[m.Lesson]++
	}
	if len(counts) == 0 {
		return ""
	}
	type kv struct{ lesson, n int }
	var list []kv
	for l, n := range counts {
		list = append(list, kv{l, n})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].n > list[j].n })
	if len(list) > topN {
		list = list[:topN]
	}
	titles := lessonTitles()
	var b strings.Builder
	b.WriteString("誤答の多い分野(弱点、多い順):\n")
	for _, kv := range list {
		fmt.Fprintf(&b, "- 第%d章 %s: %d回誤答\n", kv.lesson, titles[kv.lesson], kv.n)
	}
	return b.String()
}

// ---- 口頭試問モード ----
// AI が別角度(理由説明・事例判断・比較)から記述式で1問出題し、
// 受講者の自由記述の答えを採点して曖昧な点を指摘する。

type oralReq struct {
	Phase    string `json:"phase"` // "ask" or "grade"
	Lesson   int    `json:"lesson"`
	Chapter  int    `json:"chapter"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

func handleOral(w http.ResponseWriter, r *http.Request) {
	var req oralReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	c, err := loadChapter(req.Lesson, req.Chapter)
	if err != nil {
		http.Error(w, "chapter not found", 404)
		return
	}

	var b strings.Builder
	b.WriteString("あなたは" + qaCourseDesc + "講師です。" + qaAudience + "\n\n")
	fmt.Fprintf(&b, "学習中のチャプター: 第%d章「%s」\n教科書:\n%s\n\n", req.Lesson, c.Title, c.Text)
	b.WriteString("このチャプターの確認問題(4択。受講者は解答済みの可能性が高い):\n")
	for i, q := range c.Quiz {
		fmt.Fprintf(&b, "問%d: %s / 正解: %s\n", i+1, q.Question, q.Choices[q.Correct])
	}
	if ws := weakLessonSummary(3); ws != "" {
		b.WriteString("\n" + ws)
	}

	switch req.Phase {
	case "ask":
		b.WriteString(`
上記の内容について「口頭試問」を1問だけ出題してください。4択の暗記では答えられない、別の角度からの記述式の問いにします。
出題パターン(どれか1つ): ①理由を説明させる(「なぜ〜なのか」) ②具体的な事例を挙げて判断させる(「この場合どうなるか」) ③似た制度と比較させる ④よくある誤解を提示して誤りを指摘させる
ルール: 問題文だけを出力する(前置き・解答・ヒントは書かない)。超初心者が1〜3文で答えられる粒度。です・ます調。`)
		out, err := runClaude("core", b.String(), 180*time.Second)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		writeJSON(w, map[string]string{"question": out})

	case "grade":
		if strings.TrimSpace(req.Answer) == "" || strings.TrimSpace(req.Question) == "" {
			http.Error(w, "question/answer が空です", 400)
			return
		}
		fmt.Fprintf(&b, "\n口頭試問の問題:\n%s\n\n受講者の答え:\n%s\n\n", req.Question, req.Answer)
		b.WriteString(`受講者の答えを採点してください。出力形式(Markdown):
**判定: ◎(完璧) / ○(概ね正しい) / △(半分) / ✗(誤解あり)** のいずれか1つ
**模範解答:** 2〜3文で
**コメント:** 良かった点と、曖昧・誤解だった点を具体的に。試験でどう問われるかをひと言添える
ルール: です・ます調で励ます調子。甘い採点はしない(試験に受かることが目的)。`)
		out, err := runClaude("core", b.String(), 180*time.Second)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		// 口頭試問も学習記録に残し、弱点まとめ・復習問題生成の材料にする
		questions.add(QA{
			ID:           fmt.Sprintf("qa-%d", time.Now().UnixNano()),
			Time:         time.Now().Format("2006-01-02 15:04"),
			Lesson:       req.Lesson,
			Chapter:      req.Chapter,
			ChapterTitle: c.Title,
			Question:     "【口頭試問】" + req.Question + "\n【あなたの答え】" + req.Answer,
			Answer:       out,
		})
		writeJSON(w, map[string]string{"feedback": out})

	default:
		http.Error(w, "phase は ask か grade", 400)
	}
}

// ---- 最新統計・法改正ノート (Web 検索) ----

func latestPath() string { return filepath.Join(appDir, "latest.md") }

func handleLatestGet(w http.ResponseWriter, r *http.Request) {
	b, _ := os.ReadFile(latestPath())
	writeJSON(w, map[string]string{"note": string(b)})
}

func handleLatestCreate(w http.ResponseWriter, r *http.Request) {
	today := time.Now().Format("2006年1月2日")
	prompt := fmt.Sprintf(`あなたは%s講師です。%s
今日は %s です。WebSearch / WebFetch を使って最新情報を調べ、ネットワークスペシャリスト試験の「最新動向 直前対策ノート」を Markdown で作成してください。

調べること:
1. 次回試験の実施日・申込期間・試験構成の変更点(IPA 公式)
2. 直近の試験での出題傾向(午前Ⅱ・午後でよく出たテーマ)
3. 午前Ⅱ・午後で問われやすい比較的新しい技術トピック(例: Wi-Fi の新規格、TLS/HTTP の新バージョン、ゼロトラスト、SASE、IPv6 移行技術、クラウドネットワークなど)の現在の要点
4. シラバス・出題範囲の改訂情報があれば要点

出力形式:
# 最新動向 直前対策ノート (%s 作成)
## 試験情報 (日程・構成を表で)
## 直近の出題傾向 (分野別に「出やすい論点」を箇条書き)
## 新技術トピックまとめ (各トピック3行以内 + 想定される問われ方)
## 新技術の一問一答 (5問。問→答→ひとこと解説)
## 出典 (参照した URL を列挙)

ルール: です・ます調。検索で確認できた内容だけを書き、確認できなかった項目は「要確認」と明記する。出典 URL を必ず付ける。最後に「試験日程・持ち物は IPA 公式サイトで直前にも再確認してください」と添える。`,
		qaCourseDesc, qaAudience, today, today)

	note, err := runClaudeWeb(prompt, 600*time.Second)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	os.WriteFile(latestPath(), []byte(note+"\n"), 0o644)
	writeJSON(w, map[string]string{"note": note})
}
