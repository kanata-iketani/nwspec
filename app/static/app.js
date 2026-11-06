// 宅建どうじょう フロントエンド（4択クイズ型）
const $ = (id) => document.getElementById(id);

let course = { lessons: [] };
let cur = { lesson: 0, chapter: 0 };
let curData = null; // /api/chapter のレスポンス
let curQ = 0;       // 表示中の問題番号
let answering = false;
let drill = null;   // 復習シャッフル/苦手ドリル中は { type }。通常の章は null
const chapterTextCache = {};

// ---- 簡易 Markdown レンダラ ----
function esc(s) {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}
function inlineMd(s) {
  s = esc(s);
  s = s.replace(/`([^`]+)`/g, '<code>$1</code>');
  s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
  s = s.replace(/\\overline\{([^}]*)\}/g, '<span class="ovl">$1</span>');
  s = s.replace(/!\[([^\]]*)\]\(([^)\s]+)\)/g, '<img class="kq" alt="$1" src="$2">');
  s = s.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>');
  return s;
}
// 図(Mermaid)を描画する。描けなかった図は元のテキストのまま残す
async function renderDiagrams() {
  if (!window.mermaid) return;
  try { await window.mermaid.run({ querySelector: 'pre.mermaid:not([data-processed])', suppressErrors: true }); } catch (e) { console.warn(e); }
}

function mdToHtml(md) {
  const lines = (md || '').replace(/\r\n/g, '\n').split('\n');
  let html = '', i = 0, list = null;
  const closeList = () => { if (list) { html += `</${list}>`; list = null; } };
  while (i < lines.length) {
    const line = lines[i];
    if (/^```/.test(line)) {
      closeList();
      const lang = line.slice(3).trim();
      const code = [];
      i++;
      while (i < lines.length && !/^```/.test(lines[i])) { code.push(lines[i]); i++; }
      i++;
      if (lang === 'mermaid') {
        html += `<div class="diagram"><pre class="mermaid">${esc(code.join('\n'))}</pre></div>`;
      } else {
        html += `<pre class="code"><code>${esc(code.join('\n'))}</code></pre>`;
      }
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
  const mode = $('modeSelect').value;
  try { localStorage.setItem('nwMode', mode); } catch (e) {}
  for (const l of course.lessons) {
    if (!inMode(l.lesson, mode)) continue;
    const passed = l.chapters.filter((c) => c.status === 'passed').length;
    const opt = document.createElement('option');
    opt.value = l.lesson;
    opt.textContent = lessonLabel(l, passed);
    sel.appendChild(opt);
  }
  sel.onchange = () => {
    const l = course.lessons.find((x) => x.lesson === Number(sel.value));
    if (l) openChapter(l.lesson, l.chapters[0].chapter);
  };
  updateProgressBadge();
}

// 過去問: 41-49=午前Ⅱ, 51-69=午前Ⅰ, 71-89=午後
function isKakomon(n) { return n >= 41 && n <= 89; }
function inMode(n, mode) {
  if (mode === 'all') return true;
  if (mode === 'am') return n >= 41 && n <= 69;
  if (mode === 'am2') return n >= 41 && n <= 49;
  if (mode === 'am1') return n >= 51 && n <= 69;
  if (mode === 'pm') return n >= 71 && n <= 89;
  if (mode === 'orig') return !isKakomon(n);
  return true;
}
function lessonLabel(l, passed) {
  const head = isKakomon(l.lesson) ? '' : `第${l.lesson}章 `;
  return `${head}${l.title || ''} (${passed}/${l.chapters.length})`;
}
function initMode() {
  const m = $('modeSelect');
  try { const v = localStorage.getItem('nwMode'); if (v) m.value = v; } catch (e) {}
  m.onchange = async () => {
    await loadCourse();
    updateDrillStats();
    const first = course.lessons.find((x) => inMode(x.lesson, m.value));
    if (first) openChapter(first.lesson, first.chapters[0].chapter);
  };
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
    opt.textContent = lessonLabel(l, passed);
  }
}

async function openChapter(lesson, chapter) {
  const res = await fetch(`/api/chapter/${lesson}/${chapter}`);
  if (!res.ok) return;
  curData = await res.json();
  drill = null;
  cur = { lesson, chapter };
  document.body.classList.toggle('amMode', false);
  location.hash = `#${lesson}/${chapter}`;

  $('lessonSelect').value = lesson;
  renderChapterTabs();

  $('chTitle').textContent = `${curData.title} ${curData.priority || ''}`;
  $('chText').innerHTML = mdToHtml(curData.text);
  renderDiagrams();
  $('lessonPane').scrollTop = 0;

  // 最初の未正解問題から始める
  const firstUnsolved = curData.solved.findIndex((s) => !s);
  showQuestion(firstUnsolved >= 0 ? firstUnsolved : 0);
  qaSetContext();
}

// 今表示している問題が、どの章の何問目か(ドリル中は問題ごとに違う)
function qRef() {
  if (drill) { const it = curData.quiz[curQ]; return { lesson: it.lesson, chapter: it.chapter, q: it.q }; }
  return { lesson: cur.lesson, chapter: cur.chapter, q: curQ };
}

// ---- 復習シャッフル / 苦手ドリル ----
function modeRange() {
  const m = $('modeSelect').value;
  return { am: [41, 69], am2: [41, 49], am1: [51, 69], pm: [71, 89], orig: [1, 40], all: [1, 89] }[m] || [1, 89];
}

async function updateDrillStats() {
  const [from, to] = modeRange();
  try {
    const r = await (await fetch(`/api/drill/stats?from=${from}&to=${to}`)).json();
    $('reviewBtn').textContent = `🔀 復習 (${r.review})`;
    $('weakBtn').textContent = `😣 苦手 (${r.weak})`;
  } catch (e) {}
}

async function startDrill(type) {
  const [from, to] = modeRange();
  const r = await (await fetch(`/api/drill?type=${type}&from=${from}&to=${to}`)).json();
  if (!r.items.length) {
    alert(type === 'weak' ? 'この範囲の苦手問題はまだありません。' : 'この範囲でクリア済みの問題がまだありません。');
    return;
  }
  drill = { type };
  curData = { quiz: r.items, solved: r.items.map(() => false) };
  $('chapterTabs').innerHTML = '';
  history.replaceState(null, '', location.pathname);
  showQuestion(0);
}

async function loadLeftText(lesson, chapter) {
  const k = `${lesson}/${chapter}`;
  if (!chapterTextCache[k]) {
    const res = await fetch(`/api/chapter/${lesson}/${chapter}`);
    if (!res.ok) return;
    const d = await res.json();
    chapterTextCache[k] = { title: d.title, text: d.text };
  }
  const d = chapterTextCache[k];
  const l = lessonOf(lesson);
  $('chTitle').textContent = `${l ? l.title : ''} ${d.title}`;
  $('chText').innerHTML = mdToHtml(d.text);
  renderDiagrams();
  $('lessonPane').scrollTop = 0;
}

function showDrillResult() {
  const n = curData.quiz.length;
  const ok = curData.solved.filter((x) => x).length;
  $('feedback').classList.add('hidden');
  $('questionText').innerHTML = '';
  $('choices').innerHTML = '';
  $('quizCounter').textContent = `${n} 問中 ${ok} 問正解`;
  renderDots();
  $('clearText').textContent = drill.type === 'weak' ? '苦手ドリル おつかれさま!' : '復習シャッフル おつかれさま!';
  $('clearSub').textContent = drill.type === 'weak'
    ? `${n} 問中 ${ok} 問正解。苦手ドリルで${2}回続けて正解した問題は苦手から外れます。`
    : `${n} 問中 ${ok} 問正解。外した問題は苦手に入りました。`;
  $('nextChBtn').textContent = 'もう一度(別の問題で)';
  $('clearBanner').classList.remove('hidden');
  updateDrillStats();
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
  if (drill) {
    const it = curData.quiz[i];
    cur = { lesson: it.lesson, chapter: it.chapter };
    const l = lessonOf(it.lesson);
    $('quizCounter').textContent = `${drill.type === 'weak' ? '😣 苦手' : '🔀 復習'} ${i + 1} / ${curData.quiz.length}` +
      ` ・ ${l ? l.title : ''} ${it.title}` + (it.misses ? ` ・ ミス${it.misses}回` : '');
    loadLeftText(it.lesson, it.chapter);
    qaSetContext();
  }
  renderDots();
  const box = $('choices');
  box.innerHTML = '';
  box.classList.toggle('kana', !!q.image);
  if (q.image) {
    // 過去問午前: 原本画像をそのまま出し、ア〜エのボタンだけ並べる(本番と同じ形)
    const head = (q.question.match(/^\*\*問\d+\*\*(（[^）]*）|\([^)]*\))?/) || [''])[0];
    $('questionText').innerHTML = mdToHtml(head) + `<img class="kq big" src="${q.image}" alt="原本">`;
    q.choices.forEach((text, ci) => {
      const b = document.createElement('button');
      b.className = 'choice';
      b.title = text;
      b.innerHTML = `<span class="num">${'アイウエ'[ci]}</span>`;
      b.onclick = () => answer(ci);
      box.appendChild(b);
    });
    addDontKnow(box);
    return;
  }
  $('questionText').innerHTML = mdToHtml(q.question);
  if (q.write) { showWrite(q, box); return; }
  q.choices.forEach((text, ci) => {
    const b = document.createElement('button');
    b.className = 'choice';
    b.innerHTML = `<span class="num">${isKakomon(cur.lesson) ? 'アイウエオカ'[ci] : circled(ci)}</span><span>${inlineMd(text)}</span>`;
    b.onclick = () => answer(ci);
    box.appendChild(b);
  });
  addDontKnow(box);
}

function addDontKnow(box) {
  const b = document.createElement('button');
  b.className = 'choice dontKnow';
  b.textContent = '🤔 わからない';
  b.onclick = () => answer(-1);
  box.appendChild(b);
}

// 記述式(午後過去問): 自分の解答を書く → 解答例を見る → 自己採点
function showWrite(q, box) {
  const ta = document.createElement('textarea');
  ta.className = 'writeBox';
  ta.placeholder = '自分の解答を書いてください(字数指定があれば守る)';
  const ref = qRef();
  const key = `nwWrite:${ref.lesson}/${ref.chapter}/${ref.q}`;
  try { ta.value = localStorage.getItem(key) || ''; } catch (e) {}
  ta.oninput = () => { try { localStorage.setItem(key, ta.value); } catch (e) {} };
  const cnt = document.createElement('div');
  cnt.className = 'writeCount';
  const upd = () => { cnt.textContent = `${[...ta.value.replace(/\s/g, '')].length} 字`; };
  ta.addEventListener('input', upd); upd();
  const btn = document.createElement('button');
  btn.className = 'btn primary';
  btn.textContent = '解答例を見る';
  const ansBox = document.createElement('div');
  ansBox.className = 'md writeAnswer hidden';
  btn.onclick = async () => {
    const res = await fetch('/api/quiz/reveal', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(qRef()),
    });
    const r = await res.json();
    ansBox.innerHTML = '<div class="writeLabel">IPA 解答例</div>' + mdToHtml(r.answer) +
      '<div class="writeLabel">自己採点: 解答例と同じ趣旨が書けましたか?</div>';
    ansBox.classList.remove('hidden');
    btn.remove();
    q.choices.forEach((text, ci) => {
      const b = document.createElement('button');
      b.className = 'choice';
      b.innerHTML = `<span>${inlineMd(text)}</span>`;
      b.onclick = () => answer(ci);
      box.appendChild(b);
    });
  };
  box.append(ta, cnt, btn, ansBox);
}

// 質問時に「今開いている問題」と解答状況を先生に伝える
let lastAns = null;
function askContext() {
  const solved = !!(curData && curData.solved && curData.solved[curQ]);
  const ref = qRef();
  const la = lastAns && lastAns.lesson === ref.lesson && lastAns.chapter === ref.chapter && lastAns.q === ref.q ? lastAns : null;
  const ta = document.querySelector('#choices textarea');
  return { lesson: ref.lesson, chapter: ref.chapter, q: ref.q, answered: !!la, chosen: la ? la.choice : 0, correct: la ? la.correct : false, solved, draft: ta ? ta.value : '' };
}

async function answer(choice) {
  if (answering) return;
  answering = true;
  const buttons = [...$('choices').querySelectorAll('button.choice')];
  buttons.forEach((b) => (b.disabled = true));
  try {
    const res = await fetch('/api/quiz/answer', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ...qRef(), choice, drill: drill ? drill.type : '' }),
    });
    if (!res.ok) { alert(await res.text()); return; }
    const r = await res.json();
    if (drill) curData.solved[curQ] = r.correct;
    else curData.solved = r.solved;
    lastAns = { ...qRef(), choice, correct: r.correct };
    renderDots();

    buttons[r.correctIndex]?.classList.add('correct');
    if (!r.correct && choice >= 0) buttons[choice]?.classList.add('wrong');

    const banner = $('feedbackBanner');
    banner.className = r.correct ? 'good' : 'bad';
    banner.textContent = r.correct ? '⭕ 正解！' : (choice < 0 ? '🤔 わからない → 正解を確認しよう' : '❌ 不正解…');
    if (drill && drill.type === 'weak' && r.correct) {
      banner.textContent += r.graduated ? ' 🎓 苦手を卒業しました' : ` (あと${2 - r.streak}回続けて正解で卒業)`;
    }
    if (!r.correct) banner.textContent += '(苦手に追加)';
    $('explanation').innerHTML = mdToHtml(r.explanation);
    renderDiagrams();
    $('retryBtn').classList.toggle('hidden', r.correct);
    $('feedback').classList.remove('hidden');
    $('feedback').scrollIntoView({ behavior: 'smooth', block: 'nearest' });

    updateDrillStats();
    if (drill) {
      $('nextQBtn').textContent = curQ >= curData.quiz.length - 1 ? '結果を見る' : '次の問題 →';
      return;
    }
    if (!r.correct) {
      const c = lessonOf(cur.lesson)?.chapters.find((x) => x.chapter === cur.chapter);
      if (c) c.status = 'ran';
    }
    markLocalStatus(r.cleared ? 'passed' : 'ran');
    const last = curQ >= curData.quiz.length - 1;
    const allDone = curData.solved.every((x) => x);
    $('nextQBtn').textContent = last && allDone ? 'クリア画面へ 🎉' : '次の問題 →';
  } catch (e) {
    alert('通信エラー: ' + e);
  } finally {
    answering = false;
  }
}

function nextQuestion() {
  const n = curData.quiz.length;
  if (drill) {
    if (curQ < n - 1) showQuestion(curQ + 1); else showDrillResult();
    return;
  }
  // 最後の問題以外は、正解済みかどうかに関係なく次の番号へ進む
  if (curQ < n - 1) { showQuestion(curQ + 1); return; }
  // 最後の問題: 未正解があればそこへ、全部正解ならクリア画面
  const i = curData.solved.findIndex((x) => !x);
  if (i >= 0) { showQuestion(i); return; }
  showClear();
}

function showClear() {
  $('feedback').classList.add('hidden');
  $('questionText').innerHTML = '';
  $('choices').innerHTML = '';
  $('quizCounter').textContent = `全 ${curData.quiz.length} 問クリア`;
  $('clearText').textContent = 'チャプタークリア！';
  $('clearSub').textContent = '全問正解です。この調子でいきましょう。';
  $('nextChBtn').textContent = '次のチャプターへ ≫';
  renderDots();
  $('clearBanner').classList.remove('hidden');
}

function nextChapter() {
  if (drill) { startDrill(drill.type); return; }
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
        body: JSON.stringify(Object.assign({ lesson: cur.lesson, chapter: cur.chapter, question: q }, askContext())),
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
  initMode();
  await loadCourse();
  $('nextQBtn').onclick = nextQuestion;
  $('retryBtn').onclick = () => showQuestion(curQ);
  $('nextChBtn').onclick = nextChapter;
  $('reviewBtn').onclick = () => startDrill('review');
  $('weakBtn').onclick = () => startDrill('weak');
  updateDrillStats();

  const m = location.hash.match(/^#(\d+)\/(\d+)$/);
  if (m && lessonOf(Number(m[1]))) {
    openChapter(Number(m[1]), Number(m[2]));
  } else if (course.lessons.length > 0) {
    const l = course.lessons.find((x) => inMode(x.lesson, $('modeSelect').value)) || course.lessons[0];
    openChapter(l.lesson, l.chapters[0].chapter);
  }
}

main();
