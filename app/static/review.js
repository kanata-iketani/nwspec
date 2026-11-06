// 復習(間隔反復)の画面 — 全講座共通。index.html で app.js の後に読み込む。
(function () {
  const md = (s) => (typeof mdToHtml === 'function' ? mdToHtml(s || '') : String(s || '').replace(/</g, '&lt;'));
  const escH = (s) => String(s ?? '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  const KANA = 'アイウエオカ';

  // 上部バーに「🔁 復習」ボタン
  const btn = document.createElement('button');
  btn.id = 'reviewBtn';
  btn.title = '復習(間隔反復)';
  btn.textContent = '🔁 復習';
  const bar = document.getElementById('topbar');
  if (bar) bar.insertBefore(btn, document.getElementById('qaToggle') || null);

  const ov = document.createElement('div');
  ov.id = 'reviewOverlay';
  ov.className = 'hidden';
  document.body.appendChild(ov);

  async function stats() {
    try {
      const r = await (await fetch('/api/review/stats')).json();
      btn.textContent = r.due > 0 ? `🔁 復習 ${r.due}` : '🔁 復習';
      btn.classList.toggle('hasDue', r.due > 0);
      return r;
    } catch (e) { return null; }
  }
  stats();
  setInterval(stats, 60000);

  let set = [], idx = 0, right = 0, mode = 'due', retry = [];

  async function openMenu() {
    const r = await stats();
    const up = r ? r.upcoming.map((n, i) => `${i + 1}日後 ${n}`).join(' / ') : '';
    ov.innerHTML = `<div class="rvBox">
      <div class="rvHead"><b>🔁 復習</b><button class="rvClose">✕</button></div>
      <p class="rvLead">解いた問題は、正解するたびに「1→2→4→7→15→30日後」と間を空けて出し直します。間違えると翌日ではなく<b>すぐ</b>出し直し、苦手に入ります(2回続けて正解で卒業)。</p>
      <div class="rvModes">
        <button data-m="due" class="rvMode">📅 今日の復習<span>${r ? r.due : '-'} 問</span></button>
        <button data-m="weak" class="rvMode">🔥 苦手ドリル<span>${r ? r.weak : '-'} 問</span></button>
        <button data-m="all" class="rvMode">📚 これまでの総復習<span>${r ? r.learned : '-'} 問から</span></button>
        <button data-m="digest" class="rvMode">📖 要点の復習(概要)<span>学んだ章のまとめを読み返す</span></button>
      </div>
      <label class="rvOpt"><input type="checkbox" id="rvEssay"> 記述式(AI 採点)も含める</label>
      <label class="rvOpt">1回の問題数 <select id="rvN"><option>10</option><option selected>20</option><option>30</option><option>50</option></select></label>
      <div class="rvUp">今後の予定: ${up}</div>
    </div>`;
    ov.classList.remove('hidden');
    ov.querySelector('.rvClose').onclick = close;
    ov.querySelectorAll('.rvMode').forEach((b) => (b.onclick = () => start(b.dataset.m)));
  }
  function close() { ov.classList.add('hidden'); ov.innerHTML = ''; stats(); }

  // ---- 要点の復習: 学んだ章の「まとめ」を1枚ずつ読む(苦手の多い章から) ----
  let cards = [], ci = 0;
  async function startDigest() {
    const n = ov.querySelector('#rvN').value;
    const r = await (await fetch(`/api/review/digest?mode=all&n=${n}`)).json();
    cards = r.cards || []; ci = 0;
    if (cards.length === 0) {
      ov.querySelector('.rvBox').insertAdjacentHTML('beforeend', '<div class="rvEmpty">まだ学んだ章がありません。</div>');
      return;
    }
    showCard();
  }
  function showCard() {
    if (ci >= cards.length) return finishDigest();
    const c = cards[ci];
    ov.innerHTML = `<div class="rvBox"><div class="rvHead"><b>📖 要点の復習</b><span class="rvCount">${ci + 1} / ${cards.length}</span><button class="rvClose">✕</button></div>
      <div class="rvSrc">第${c.lesson}章 ${escH(c.title)}${c.weak ? ` <span class="rvWeakTag">🔥 苦手 ${c.weak} 問</span>` : ''}</div>
      <div class="md rvDigest">${md(c.digest)}</div>
      <div class="rvCardBar"><button class="btn ghost rvOpen">この章を開いて読み直す</button><button class="btn primary rvNextCard">次の章へ →</button></div></div>`;
    ov.querySelector('.rvClose').onclick = close;
    ov.querySelector('.rvNextCard').onclick = () => { ci++; showCard(); };
    ov.querySelector('.rvOpen').onclick = () => { close(); if (typeof openChapter === 'function') openChapter(c.lesson, c.chapter); else location.hash = `#${c.lesson}/${c.chapter}`; };
    if (window.mermaid) window.mermaid.run({ querySelector: '#reviewOverlay pre.mermaid:not([data-processed])', suppressErrors: true }).catch(() => {});
  }
  function finishDigest() {
    ov.innerHTML = `<div class="rvBox"><div class="rvHead"><b>📖 要点の復習 おつかれさまでした</b><button class="rvClose">✕</button></div>
      <p>${cards.length} 章の要点を読み返しました。続けて問題で確かめると定着します。</p>
      <button class="btn primary rvAgain">復習メニューへ</button></div>`;
    ov.querySelector('.rvClose').onclick = close;
    ov.querySelector('.rvAgain').onclick = openMenu;
  }

  async function start(m) {
    if (m === 'digest') return startDigest();
    mode = m;
    const n = ov.querySelector('#rvN').value;
    const essay = ov.querySelector('#rvEssay').checked ? 1 : 0;
    const r = await (await fetch(`/api/review/set?mode=${m}&n=${n}&essay=${essay}`)).json();
    set = r.items || [];
    idx = 0; right = 0; retry = [];
    if (set.length === 0) {
      ov.querySelector('.rvBox').insertAdjacentHTML('beforeend', `<div class="rvEmpty">${m === 'due' ? '今日の復習はありません。🎉' : m === 'weak' ? '苦手はありません。🎉' : 'まだ解いた問題がありません。'}</div>`);
      return;
    }
    show();
  }

  function show() {
    if (idx >= set.length) {
      if (mode === 'weak' && retry.length > 0) { // 苦手ドリルは、間違えた問題をシャッフルしてもう一周
        set = retry.sort(() => Math.random() - 0.5); retry = []; idx = 0;
      } else { return finish(); }
    }
    const q = set[idx];
    const kana = q.image || /^[RH]\d|過去問/.test(q.title) ? KANA : '①②③④⑤⑥';
    let body = `<div class="rvHead"><b>🔁 ${mode === 'due' ? '今日の復習' : mode === 'weak' ? '苦手ドリル' : '総復習'}</b>
      <span class="rvCount">${idx + 1} / ${set.length}${q.weak ? ' 🔥' : ''}</span><button class="rvClose">✕</button></div>
      <div class="rvSrc">第${q.lesson}章 ${escH(q.title)}</div>`;
    if (q.image) body += `<img class="kq big" src="${q.image}" alt="原本">`;
    else body += `<div class="md rvQ">${md(q.question)}</div>`;
    if (q.kind === 'sql' && q.setup) body += `<details class="sqlSetup"><summary>テーブル定義とデータ</summary><pre class="code"><code>${escH(q.setup)}</code></pre></details>`;
    body += '<div class="rvChoices"></div><div class="rvFb hidden"></div>';
    ov.innerHTML = `<div class="rvBox">${body}</div>`;
    ov.querySelector('.rvClose').onclick = close;
    const box = ov.querySelector('.rvChoices');
    if (q.kind === 'sql' || q.kind === 'essay' || q.write) {
      const ta = document.createElement('textarea');
      ta.className = 'writeBox ' + (q.kind === 'sql' ? 'sqlBox' : 'essayBox');
      ta.placeholder = q.kind === 'sql' ? 'SQL を書いて提出' : '解答を書いて提出';
      const sub = document.createElement('button');
      sub.className = 'btn primary';
      sub.textContent = q.kind === 'sql' ? '提出して採点' : q.kind === 'essay' ? 'AI に採点してもらう' : '解答例を見て自己採点';
      sub.onclick = async () => {
        sub.disabled = true;
        if (q.write) { // 午後過去問: 解答例を見て自己採点
          const m = await post('/api/quiz/reveal', { lesson: q.lesson, chapter: q.chapter, q: q.q });
          box.insertAdjacentHTML('beforeend', `<div class="md writeAnswer">${md(m.answer)}</div>`);
          q.choices.forEach((t, ci) => { const b = document.createElement('button'); b.className = 'choice'; b.innerHTML = `<span>${escH(t)}</span>`; b.onclick = () => answer(q, ci); box.appendChild(b); });
          return;
        }
        sub.textContent = '採点中…';
        try {
          const g = await post('/api/quiz/grade', { lesson: q.lesson, chapter: q.chapter, q: q.q, draft: ta.value });
          box.insertAdjacentHTML('beforeend', `<div class="gradeBanner ${g.pass ? 'good' : 'bad'}">${g.pass ? '⭕' : '❌'} ${g.score ?? ''}点 ${escH(g.feedback || g.missing || '')}</div>`);
          answer(q, g.pass ? 0 : 1);
        } catch (e) { sub.disabled = false; sub.textContent = 'もう一度'; alert(e.message); }
      };
      box.append(ta, sub);
    } else {
      q.choices.forEach((t, ci) => {
        const b = document.createElement('button');
        b.className = 'choice';
        b.innerHTML = `<span class="num">${kana[ci] || ci + 1}</span><span>${q.image ? '' : md(t).replace(/^<p>|<\/p>$/g, '')}</span>`;
        b.onclick = () => answer(q, ci);
        box.appendChild(b);
      });
      const dk = document.createElement('button');
      dk.className = 'choice dontKnow';
      dk.textContent = '🤔 わからない';
      dk.onclick = () => answer(q, -1);
      box.appendChild(dk);
    }
  }

  async function post(url, body) {
    const res = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    if (!res.ok) throw new Error(await res.text());
    return res.json();
  }

  async function answer(q, choice) {
    ov.querySelectorAll('.rvChoices button.choice').forEach((b) => (b.disabled = true));
    let r;
    try { r = await post('/api/quiz/answer', { lesson: q.lesson, chapter: q.chapter, q: q.q, choice }); }
    catch (e) { alert(e.message); return; }
    const bs = [...ov.querySelectorAll('.rvChoices button.choice:not(.dontKnow)')];
    bs[r.correctIndex]?.classList.add('correct');
    if (!r.correct && choice >= 0) bs[choice]?.classList.add('wrong');
    if (r.correct) right++; else retry.push(q);
    const fb = ov.querySelector('.rvFb');
    fb.innerHTML = `<div class="gradeBanner ${r.correct ? 'good' : 'bad'}">${r.correct ? '⭕ 正解' : choice < 0 ? '🤔 わからない' : '❌ 不正解'}</div>
      <div class="md">${md(r.explanation)}</div>
      <details class="rvChDigest"><summary>📖 この章の要点を見る</summary><div class="md">読み込み中…</div></details>
      <button class="btn primary rvNext">次へ →</button>`;
    fb.classList.remove('hidden');
    fb.querySelector('.rvNext').onclick = () => { idx++; show(); };
    const det = fb.querySelector('.rvChDigest');
    det.addEventListener('toggle', async () => {
      if (!det.open || det.dataset.loaded) return;
      det.dataset.loaded = '1';
      try { const d = await (await fetch(`/api/review/digest?lesson=${q.lesson}&chapter=${q.chapter}`)).json(); det.querySelector('.md').innerHTML = md(d.digest); }
      catch (e) { det.querySelector('.md').textContent = '読み込めませんでした'; }
    });
    fb.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  }

  function finish() {
    const total = set.length || 1;
    ov.innerHTML = `<div class="rvBox"><div class="rvHead"><b>🔁 おつかれさまでした</b><button class="rvClose">✕</button></div>
      <div class="rvDone">正解 ${right} 問(${Math.round((right / total) * 100)}%)</div>
      <p>間違えた問題は苦手に入り、すぐ・明日以降も出し直されます。正解した問題は間を空けて、忘れかけたころにまた出ます。</p>
      <button class="btn primary rvAgain">復習メニューへ</button></div>`;
    ov.querySelector('.rvClose').onclick = close;
    ov.querySelector('.rvAgain').onclick = openMenu;
    stats();
  }

  btn.onclick = openMenu;
})();
