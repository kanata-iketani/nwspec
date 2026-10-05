// 宅建どうじょう フロントエンド（4択クイズ型）
const $ = (id) => document.getElementById(id);

let course = { lessons: [] };
let cur = { lesson: 0, chapter: 0 };
let curData = null; // /api/chapter のレスポンス
let curQ = 0;       // 表示中の問題番号
let answering = false;

// ---- 簡易 Markdown レンダラ ----
function esc(s) {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}
function inlineMd(s) {
  s = esc(s);
  s = s.replace(/`([^`]+)`/g, '<code>$1</code>');
  s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
  return s;
}
function mdToHtml(md) {
  const lines = (md || '').replace(/\r\n/g, '\n').split('\n');
  let html = '', i = 0, list = null;
  const closeList = () => { if (list) { html += `</${list}>`; list = null; } };
  while (i < lines.length) {
    const line = lines[i];
    if (/^```/.test(line)) {
      closeList();
      const code = [];
      i++;
      while (i < lines.length && !/^```/.test(lines[i])) { code.push(lines[i]); i++; }
      i++;
      html += `<pre class="code"><code>${esc(code.join('\n'))}</code></pre>`;
      continue;
    }
    const h = line.match(/^(#{1,4})\s+(.*)/);
    if (h) { closeList(); const lv = Math.min(h[1].length + 2, 6); html += `<h${lv}>${inlineMd(h[2])}</h${lv}>`; i++; continue; }
    if (/^>\s?/.test(line)) {
      closeList();
      const quote = [];
      while (i < lines.length && /^>\s?/.test(lines[i])) { quote.push(lines[i].replace(/^>\s?/, '')); i++; }
      html += `<blockquote>${quote.map(inlineMd).join('<br>')}</blockquote>`;
      continue;
    }
    if (/^\|/.test(line)) {
      closeList();
      const rows = [];
      while (i < lines.length && /^\|/.test(lines[i])) { rows.push(lines[i]); i++; }
      const cells = (r) => r.replace(/^\||\|$/g, '').split('|').map((c) => inlineMd(c.trim()));
      let t = '<table>';
      rows.forEach((r, idx) => {
        if (/^\|[\s:-]+\|?[\s|:-]*$/.test(r)) return; // 区切り行
        const tag = idx === 0 ? 'th' : 'td';
        t += '<tr>' + cells(r).map((c) => `<${tag}>${c}</${tag}>`).join('') + '</tr>';
      });
      html += t + '</table>';
      continue;
    }
    const ul = line.match(/^[-*]\s+(.*)/);
    if (ul) { if (list !== 'ul') { closeList(); html += '<ul>'; list = 'ul'; } html += `<li>${inlineMd(ul[1])}</li>`; i++; continue; }
    const ol = line.match(/^\d+\.\s+(.*)/);
    if (ol) { if (list !== 'ol') { closeList(); html += '<ol>'; list = 'ol'; } html += `<li>${inlineMd(ol[1])}</li>`; i++; continue; }
    if (line.trim() === '') { closeList(); i++; continue; }
    const para = [line];
    i++;
    while (i < lines.length && lines[i].trim() !== '' && !/^(#{1,4}\s|```|[-*]\s|\d+\.\s|>|\|)/.test(lines[i])) {
      para.push(lines[i]); i++;
    }
    closeList();
    html += `<p>${para.map(inlineMd).join('<br>')}</p>`;
  }
  closeList();
  return html;
}

// ---- コースナビ ----
async function loadCourse() {
  course = await (await fetch('/api/course')).json();
  const sel = $('lessonSelect');
  sel.innerHTML = '';
  for (const l of course.lessons) {
    const passed = l.chapters.filter((c) => c.status === 'passed').length;
    const opt = document.createElement('option');
    opt.value = l.lesson;
    opt.textContent = `第${l.lesson}章 ${l.title || ''} (${passed}/${l.chapters.length})`;
    sel.appendChild(opt);
  }
  sel.onchange = () => {
    const l = course.lessons.find((x) => x.lesson === Number(sel.value));
    if (l) openChapter(l.lesson, l.chapters[0].chapter);
  };
  updateProgressBadge();
}

function lessonOf(n) { return course.lessons.find((x) => x.lesson === n); }

function renderChapterTabs() {
  const l = lessonOf(cur.lesson);
  const nav = $('chapterTabs');
  nav.innerHTML = '';
  if (!l) return;
  for (const c of l.chapters) {
    const b = document.createElement('button');
    b.className = 'chTab' + (c.chapter === cur.chapter ? ' active' : '') + (c.status === 'passed' ? ' passed' : '');
    b.textContent = `${c.chapter}. ${c.title}`;
    b.onclick = () => openChapter(cur.lesson, c.chapter);
    nav.appendChild(b);
  }
}

function updateProgressBadge() {
  let total = 0, passed = 0;
  for (const l of course.lessons) {
    total += l.chapters.length;
    passed += l.chapters.filter((c) => c.status === 'passed').length;
  }
  $('progressBadge').textContent = `クリア ${passed}/${total}`;
}

function markLocalStatus(status) {
  const l = lessonOf(cur.lesson);
  if (!l) return;
  const c = l.chapters.find((x) => x.chapter === cur.chapter);
  if (c && c.status !== 'passed') c.status = status;
  renderChapterTabs();
  updateProgressBadge();
  const sel = $('lessonSelect');
  const opt = [...sel.options].find((o) => Number(o.value) === cur.lesson);
  if (opt) {
    const passed = l.chapters.filter((x) => x.status === 'passed').length;
    opt.textContent = `第${l.lesson}章 ${l.title || ''} (${passed}/${l.chapters.length})`;
  }
}

async function openChapter(lesson, chapter) {
  const res = await fetch(`/api/chapter/${lesson}/${chapter}`);
  if (!res.ok) return;
  curData = await res.json();
  cur = { lesson, chapter };
  location.hash = `#${lesson}/${chapter}`;

  $('lessonSelect').value = lesson;
  renderChapterTabs();

  $('chTitle').textContent = `${curData.title} ${curData.priority || ''}`;
  $('chText').innerHTML = mdToHtml(curData.text);
  $('lessonPane').scrollTop = 0;

  // 最初の未正解問題から始める
  const firstUnsolved = curData.solved.findIndex((s) => !s);
  showQuestion(firstUnsolved >= 0 ? firstUnsolved : 0);
  qaSetContext();
}

// ---- クイズ ----
function circled(i) { return ['①', '②', '③', '④', '⑤', '⑥'][i] || String(i + 1); }

function renderDots() {
  const dots = $('quizDots');
  dots.innerHTML = '';
  curData.quiz.forEach((_, i) => {
    const d = document.createElement('button');
    d.className = 'dot' + (curData.solved[i] ? ' solved' : '') + (i === curQ ? ' current' : '');
    d.textContent = i + 1;
    d.title = `問 ${i + 1}`;
    d.onclick = () => showQuestion(i);
    dots.appendChild(d);
  });
}

function showQuestion(i) {
  curQ = i;
  const q = curData.quiz[i];
  $('clearBanner').classList.add('hidden');
  $('feedback').classList.add('hidden');
  $('quizCounter').textContent = `問 ${i + 1} / ${curData.quiz.length}`;
  renderDots();
  $('questionText').innerHTML = mdToHtml(q.question);
  const box = $('choices');
  box.innerHTML = '';
  q.choices.forEach((text, ci) => {
    const b = document.createElement('button');
    b.className = 'choice';
    b.innerHTML = `<span class="num">${circled(ci)}</span><span>${inlineMd(text)}</span>`;
    b.onclick = () => answer(ci);
    box.appendChild(b);
  });
}

async function answer(choice) {
  if (answering) return;
  answering = true;
  const buttons = [...$('choices').children];
  buttons.forEach((b) => (b.disabled = true));
  try {
    const res = await fetch('/api/quiz/answer', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ lesson: cur.lesson, chapter: cur.chapter, q: curQ, choice }),
    });
    if (!res.ok) { alert(await res.text()); return; }
    const r = await res.json();
    curData.solved = r.solved;
    renderDots();

    buttons[r.correctIndex]?.classList.add('correct');
    if (!r.correct) buttons[choice]?.classList.add('wrong');

    const banner = $('feedbackBanner');
    banner.className = r.correct ? 'good' : 'bad';
    banner.textContent = r.correct ? '⭕ 正解！' : '❌ 不正解…';
    $('explanation').innerHTML = mdToHtml(r.explanation);
    $('retryBtn').classList.toggle('hidden', r.correct);
    $('feedback').classList.remove('hidden');
    $('feedback').scrollIntoView({ behavior: 'smooth', block: 'nearest' });

    markLocalStatus(r.cleared ? 'passed' : 'ran');
    $('nextQBtn').textContent = r.cleared ? 'クリア画面へ 🎉' : '次の問題 →';
    $('nextQBtn').dataset.cleared = r.cleared ? '1' : '';
  } catch (e) {
    alert('通信エラー: ' + e);
  } finally {
    answering = false;
  }
}

function nextQuestion() {
  if ($('nextQBtn').dataset.cleared === '1') {
    showClear();
    return;
  }
  // 次の未正解問題へ (なければ次の番号へ循環)
  const n = curData.quiz.length;
  for (let d = 1; d <= n; d++) {
    const i = (curQ + d) % n;
    if (!curData.solved[i]) { showQuestion(i); return; }
  }
  showClear();
}

function showClear() {
  $('feedback').classList.add('hidden');
  $('questionText').innerHTML = '';
  $('choices').innerHTML = '';
  $('quizCounter').textContent = `全 ${curData.quiz.length} 問クリア`;
  renderDots();
  $('clearBanner').classList.remove('hidden');
}

function nextChapter() {
  const l = lessonOf(cur.lesson);
  if (!l) return;
  const idx = l.chapters.findIndex((c) => c.chapter === cur.chapter);
  if (idx >= 0 && idx + 1 < l.chapters.length) {
    openChapter(cur.lesson, l.chapters[idx + 1].chapter);
    return;
  }
  const li = course.lessons.findIndex((x) => x.lesson === cur.lesson);
  if (li >= 0 && li + 1 < course.lessons.length) {
    const nl = course.lessons[li + 1];
    openChapter(nl.lesson, nl.chapters[0].chapter);
  } else {
    alert('最後のチャプターです。おつかれさまでした！');
  }
}

// ---- 質問対話 ----
function qaSetContext() {
  if (!curData) return;
  $('qaContext').textContent = `文脈: 第${cur.lesson}章 ${curData.title}`;
}

function qaAppend(who, text, asMd) {
  const div = document.createElement('div');
  div.className = 'qaMsg ' + who;
  div.innerHTML = `<div class="who">${who === 'user' ? 'あなた' : '先生'}</div><div class="bubble"></div>`;
  const bubble = div.querySelector('.bubble');
  if (asMd) bubble.innerHTML = mdToHtml(text);
  else bubble.textContent = text;
  $('qaLog').appendChild(div);
  $('qaLog').scrollTop = $('qaLog').scrollHeight;
  return div;
}

let oralPending = null; // 口頭試問の出題中の問題文

function qaThinking(text) {
  const d = document.createElement('div');
  d.className = 'qaThinking';
  d.textContent = text;
  $('qaLog').appendChild(d);
  $('qaLog').scrollTop = $('qaLog').scrollHeight;
  return d;
}

async function oralStart() {
  if (!curData) return;
  $('oralBtn').disabled = true;
  const thinking = qaThinking('先生が問題を考えています…');
  try {
    const res = await fetch('/api/oral', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ phase: 'ask', lesson: cur.lesson, chapter: cur.chapter }),
    });
    thinking.remove();
    if (!res.ok) { qaAppend('claude', 'エラー: ' + (await res.text()), false); return; }
    const data = await res.json();
    oralPending = data.question;
    qaAppend('claude', '🎤 **口頭試問です。自分の言葉で答えてみてください。**\n\n' + data.question, true);
    $('qaInput').placeholder = '自分の言葉で答えを書いて送信（Ctrl+Enter）';
    $('qaInput').focus();
  } catch (e) {
    thinking.remove();
    qaAppend('claude', '通信エラー: ' + e, false);
  } finally {
    $('oralBtn').disabled = false;
  }
}

async function qaAsk() {
  const q = $('qaInput').value.trim();
  if (!q || !curData) return;
  $('qaInput').value = '';
  $('qaSend').disabled = true;
  qaAppend('user', q, false);
  const grading = oralPending !== null;
  const thinking = qaThinking(grading ? '先生が採点しています…' : '先生が考えています…');
  try {
    let res;
    if (grading) {
      res = await fetch('/api/oral', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ phase: 'grade', lesson: cur.lesson, chapter: cur.chapter, question: oralPending, answer: q }),
      });
    } else {
      res = await fetch('/api/ask', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ lesson: cur.lesson, chapter: cur.chapter, question: q }),
      });
    }
    thinking.remove();
    if (!res.ok) { qaAppend('claude', 'エラー: ' + (await res.text()), false); return; }
    const data = await res.json();
    qaAppend('claude', grading ? data.feedback : data.answer, true);
    if (grading) {
      oralPending = null;
      $('qaInput').placeholder = 'いま学んでいる内容について先生に質問（Ctrl+Enter で送信）';
      qaAppend('claude', 'もう1問受けるなら「🎤 口頭試問を受ける」を押してください。', false);
    }
  } catch (e) {
    thinking.remove();
    qaAppend('claude', '通信エラー: ' + e, false);
  } finally {
    $('qaSend').disabled = false;
  }
}

async function latestCreate() {
  if (!confirm('AI が Web 検索で最新の統計数値と法改正を調べてノートを作ります（2〜5分かかります）。よろしいですか？')) return;
  $('latestBtn').disabled = true;
  $('qaNotesStatus').textContent = '先生が最新情報を Web で調べています…（2〜5分かかります）';
  try {
    const res = await fetch('/api/qa/latest', { method: 'POST' });
    if (!res.ok) { $('qaNotesStatus').textContent = 'エラー: ' + (await res.text()); return; }
    const data = await res.json();
    $('latestNote').innerHTML = mdToHtml(data.note);
    $('qaNotesStatus').textContent = '直前対策ノートを作成しました（出典 URL 付き。直前にもう一度作り直すと最新になります）';
  } catch (e) {
    $('qaNotesStatus').textContent = '通信エラー: ' + e;
  } finally {
    $('latestBtn').disabled = false;
  }
}

async function loadMistakes() {
  const items = ((await (await fetch('/api/mistakes')).json()).items || []).slice(-30).reverse();
  const box = $('mistakeList');
  box.innerHTML = '';
  if (items.length === 0) {
    box.innerHTML = '<div class="qaThinking">間違えた問題はまだありません。</div>';
    return;
  }
  for (const m of items) {
    const div = document.createElement('div');
    div.className = 'mistakeItem';
    div.innerHTML =
      `<div class="meta"><span>${esc(m.time)} · 第${m.lesson}章 ${esc(m.title)} 問${m.q + 1}</span>` +
      `<button>この問題を解き直す →</button></div>` +
      `<div>${esc(m.question)}</div>` +
      `<div><span class="wrongPick">あなたの答え: ${esc(m.chosen)}</span> ／ 正解: ${esc(m.answer)}</div>`;
    div.querySelector('button').onclick = async () => {
      $('qaDrawer').classList.add('hidden');
      await openChapter(m.lesson, m.chapter);
      if (curData && m.q < curData.quiz.length) showQuestion(m.q);
    };
    box.appendChild(div);
  }
}

async function qaLoadNotes() {
  loadMistakes();
  try {
    const [listRes, sumRes, latestRes] = await Promise.all([
      fetch('/api/qa'), fetch('/api/qa/summary'), fetch('/api/qa/latest'),
    ]);
    const list = (await listRes.json()).items || [];
    const summary = (await sumRes.json()).summary || '';
    const latest = (await latestRes.json()).note || '';
    $('qaSummary').innerHTML = summary ? mdToHtml(summary) : '';
    $('latestNote').innerHTML = latest ? mdToHtml(latest) : '';
    const box = $('qaList');
    box.innerHTML = '';
    if (list.length === 0) {
      box.innerHTML = '<div class="qaThinking">まだ質問の記録がありません。問題を解いてつまずいたら、遠慮なく先生に聞いてみましょう。</div>';
      return;
    }
    for (const item of [...list].reverse()) {
      const div = document.createElement('div');
      div.className = 'qaItem';
      div.innerHTML =
        `<div class="meta"><span>${esc(item.time)} · 第${item.lesson}章 ${esc(item.chapterTitle || '')}</span>` +
        `<button data-id="${esc(item.id)}">削除</button></div>` +
        `<div class="q">Q. ${esc(item.question)}</div>` +
        `<div class="a">${mdToHtml(item.answer)}</div>`;
      div.querySelector('button').onclick = async (ev) => {
        if (!confirm('この質問記録を削除しますか？')) return;
        await fetch('/api/qa/' + ev.target.dataset.id, { method: 'DELETE' });
        qaLoadNotes();
      };
      box.appendChild(div);
    }
  } catch (e) {
    $('qaNotesStatus').textContent = '読み込みに失敗しました: ' + e;
  }
}

async function qaSummarize() {
  $('qaSummaryBtn').disabled = true;
  $('qaNotesStatus').textContent = '先生が弱点をまとめています…（1分ほどかかります）';
  try {
    const res = await fetch('/api/qa/summary', { method: 'POST' });
    if (!res.ok) { $('qaNotesStatus').textContent = 'エラー: ' + (await res.text()); return; }
    const data = await res.json();
    $('qaSummary').innerHTML = mdToHtml(data.summary);
    $('qaNotesStatus').textContent = '弱点ノートを更新しました';
  } catch (e) {
    $('qaNotesStatus').textContent = '通信エラー: ' + e;
  } finally {
    $('qaSummaryBtn').disabled = false;
  }
}

async function qaMakeReview() {
  if (!confirm('あなたの誤答と質問をもとに、専用の復習問題を作ります（数分かかります）。よろしいですか？')) return;
  $('qaReviewBtn').disabled = true;
  $('qaNotesStatus').textContent = '先生が復習問題を作っています…（数分かかります）';
  try {
    const res = await fetch('/api/qa/review', { method: 'POST' });
    if (!res.ok) { $('qaNotesStatus').textContent = 'エラー: ' + (await res.text()); return; }
    const data = await res.json();
    const n = (data.created || []).length;
    let msg = n > 0 ? `復習チャプターを ${n} 個作成しました。` : '作成できませんでした。';
    if ((data.failed || []).length > 0) msg += ` (失敗: ${data.failed.length}件)`;
    $('qaNotesStatus').textContent = msg;
    if (n > 0) {
      await loadCourse();
      if (confirm(msg + '\n「復習問題」を開きますか？')) {
        $('qaDrawer').classList.add('hidden');
        openChapter(data.created[0].lesson, data.created[0].chapter);
      }
    }
  } catch (e) {
    $('qaNotesStatus').textContent = '通信エラー: ' + e;
  } finally {
    $('qaReviewBtn').disabled = false;
  }
}

function initQA() {
  $('qaToggle').onclick = () => { $('qaDrawer').classList.toggle('hidden'); qaSetContext(); };
  $('qaClose').onclick = () => $('qaDrawer').classList.add('hidden');
  document.querySelectorAll('.qaTab').forEach((tab) => {
    tab.onclick = () => {
      document.querySelectorAll('.qaTab').forEach((t) => t.classList.remove('active'));
      tab.classList.add('active');
      const isChat = tab.dataset.tab === 'chat';
      $('qaChatPane').classList.toggle('hidden', !isChat);
      $('qaNotesPane').classList.toggle('hidden', isChat);
      if (!isChat) qaLoadNotes();
    };
  });
  $('qaSend').onclick = qaAsk;
  $('qaInput').addEventListener('keydown', (ev) => {
    if ((ev.ctrlKey || ev.metaKey) && ev.key === 'Enter') { ev.preventDefault(); qaAsk(); }
  });
  $('qaSummaryBtn').onclick = qaSummarize;
  $('qaReviewBtn').onclick = qaMakeReview;
  $('oralBtn').onclick = oralStart;
  $('latestBtn').onclick = latestCreate;
}

// ---- 起動 ----
async function main() {
  initQA();
  await loadCourse();
  $('nextQBtn').onclick = nextQuestion;
  $('retryBtn').onclick = () => showQuestion(curQ);
  $('nextChBtn').onclick = nextChapter;

  const m = location.hash.match(/^#(\d+)\/(\d+)$/);
  if (m && lessonOf(Number(m[1]))) {
    openChapter(Number(m[1]), Number(m[2]));
  } else if (course.lessons.length > 0) {
    const l = course.lessons[0];
    openChapter(l.lesson, l.chapters[0].chapter);
  }
}

main();
