/* 音乐模块（M1）：曲库列表 / 歌手·专辑·歌单分组 / 播放条 / 音乐U盘导出向导。 */
(function () {
  'use strict';

  var APP = 'music';
  function api(action, body) {
    return window.hubAPI(APP, action, body || {});
  }

  var $ = function (id) { return document.getElementById(id); };
  function el(t, cls, text) {
    var e = document.createElement(t);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }
  function esc(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }
  function toast(msg, bad) {
    var t = el('div', 'vtoast' + (bad ? ' bad' : ''), msg);
    document.body.appendChild(t);
    setTimeout(function () { t.classList.add('show'); }, 10);
    setTimeout(function () {
      t.classList.remove('show');
      setTimeout(function () { t.remove(); }, 300);
    }, 2600);
  }
  function fmtDur(s) {
    if (!s || s < 0) return '--:--';
    var m = Math.floor(s / 60), ss = Math.floor(s % 60);
    return m + ':' + (ss < 10 ? '0' : '') + ss;
  }
  function fmtSize(n) {
    if (!n) return '';
    if (n >= 1073741824) return (n / 1073741824).toFixed(1) + ' GB';
    if (n >= 1048576) return (n / 1048576).toFixed(1) + ' MB';
    return Math.round(n / 1024) + ' KB';
  }

  // ── 状态 ──
  var state = {
    tab: 'artists',      // artists / albums / pls / recent
    artist: '', album: '', plId: '', plName: '',
    q: '', items: [], sel: {}, // sel: 勾选待导出的 track id
    queue: [], cur: -1,
  };

  // ── 左栏 ──
  function renderTabs() {
    var box = $('m-tabs');
    [].forEach.call(box.children, function (b) {
      b.classList.toggle('on', b.getAttribute('data-tab') === state.tab);
    });
  }

  function loadChips() {
    var box = $('m-chips');
    box.innerHTML = '';
    var makeChip = function (label, count, on, fn) {
      var b = el('button', 'mu-chip' + (on ? ' on' : ''));
      b.appendChild(el('span', null, label));
      if (count != null) b.appendChild(el('i', null, String(count)));
      b.onclick = fn;
      box.appendChild(b);
      return b;
    };

    if (state.tab === 'pls') {
      api('pl.list').then(function (j) {
        makeChip('全部歌曲', null, !state.plId, function () {
          state.plId = ''; state.plName = ''; loadList();
        });
        (j.playlists || []).forEach(function (p) {
          makeChip(p.name, p.count, state.plId === p.id, function () {
            if (state.plId === p.id) { renamePl(p); return; } // 再点选中项 = 管理
            state.plId = p.id; state.plName = p.name; state.tab = 'pls';
            loadList();
          });
        });
        if (!(j.playlists || []).length) {
          box.appendChild(el('div', 'mu-chips-empty', '还没有歌单，点「新建歌单」创建'));
        }
      }).catch(function (e) { toast(e.message, true); });
      return;
    }

    if (state.tab === 'recent') {
      box.appendChild(el('div', 'mu-chips-empty', '按最近播放排序，最多 50 首'));
      return;
    }

    api('facets').then(function (j) {
      var rows = state.tab === 'artists' ? (j.artists || []) : (j.albums || []);
      makeChip('全部', null, !(state.artist || state.album), function () {
        state.artist = ''; state.album = ''; loadList();
      });
      rows.forEach(function (f) {
        var on = (state.tab === 'artists' && state.artist === f.name) ||
                 (state.tab === 'albums' && state.album === f.name);
        makeChip(f.name, f.count, on, function () {
          if (state.tab === 'artists') { state.artist = f.name; state.album = ''; }
          else { state.album = f.name; state.artist = ''; }
          loadList();
        });
      });
    }).catch(function (e) { toast(e.message, true); });
  }

  // ── 曲目列表 ──
  function listParams() {
    return {
      q: state.q, artist: state.artist, album: state.album,
      plId: state.plId, sort: state.tab === 'recent' ? '' : '',
    };
  }

  function loadList() {
    var p = listParams();
    var req;
    if (state.tab === 'recent') {
      req = api('recent.list').then(function (j) {
        return { items: j.items || [], total: (j.items || []).length, pages: 1 };
      });
    } else {
      req = api('list', p);
    }
    req.then(function (j) {
      state.items = j.items || [];
      paintList();
      // 标题与工具按钮
      var title = '曲库';
      if (state.plId) title = '歌单 · ' + state.plName;
      else if (state.artist) title = state.artist;
      else if (state.album) title = state.album;
      else if (state.tab === 'recent') title = '最近播放';
      if (state.q) title += '（搜索：' + state.q + '）';
      $('m-title').textContent = title;
      $('m-count').textContent = state.items.length ? state.items.length + ' 首' : '';
      $('m-pl-rename').hidden = $('m-pl-del').hidden = !state.plId;
      $('m-selall').hidden = !state.items.length;
      updateSelUI();
    }).catch(function (e) { toast(e.message, true); });
  }

  // ── 勾选批量操作 ──
  function selCount() { return Object.keys(state.sel).length; }

  function updateSelUI() {
    var n = selCount();
    var b = $('m-selpl');
    b.hidden = n === 0;
    b.textContent = (state.plId ? '移出歌单' : '加入歌单') + '（' + n + '）';
    var all = state.items.length && state.items.every(function (t) { return state.sel[t.id]; });
    $('m-selall').textContent = all ? '取消全选' : '全选';
  }

  $('m-selall').onclick = function () {
    var all = state.items.length && state.items.every(function (t) { return state.sel[t.id]; });
    if (all) state.sel = {};
    else state.items.forEach(function (t) { state.sel[t.id] = true; });
    paintList(); updateSelUI();
  };
  $('m-selpl').onclick = function () {
    var ids = Object.keys(state.sel);
    if (!ids.length) return;
    if (!state.plId) { addToPl(ids); return; }
    // 歌单视图里 = 批量移出
    api('pl.remove', { id: state.plId, trackIds: ids }).then(function (r) {
      toast('已移出 ' + (r.removed || 0) + ' 首');
      state.sel = {};
      loadList(); loadChips();
    }).catch(function (e) { toast(e.message, true); });
  };

  function paintList() {
    var box = $('m-list');
    box.innerHTML = '';
    var empty = $('m-empty');
    if (!state.items.length) {
      empty.hidden = false;
      empty.textContent = state.q ? '没有匹配的歌曲' : '曲库还是空的 —— 把音乐放进网盘 _appfiles/music/ 后点「扫描曲库」';
      return;
    }
    empty.hidden = true;
    state.items.forEach(function (t, i) {
      var row = el('div', 'mu-row' + (state.queue[state.cur] && state.queue[state.cur].id === t.id ? ' playing' : ''));
      // 勾选（用于 U 盘导出 / 批量加入歌单）
      var ck = el('input', 'mu-ck'); ck.type = 'checkbox'; ck.checked = !!state.sel[t.id];
      ck.title = '勾选后可批量加入歌单或导出U盘';
      // click 必须阻断冒泡：行 onclick=播放 会重建列表，复选框还没切换默认动作就被移除了
      ck.onclick = function (ev) { ev.stopPropagation(); };
      ck.onchange = function () {
        if (this.checked) state.sel[t.id] = true; else delete state.sel[t.id];
        updateSelUI();
      };
      row.appendChild(ck);
      // 序号 / 播放态
      var idx = el('span', 'mu-no', String(i + 1));
      row.appendChild(idx);
      // 封面
      var cv = el('span', 'mu-cov');
      if (t.cover) {
        var img = el('img'); img.src = t.cover; img.alt = ''; img.loading = 'lazy';
        cv.appendChild(img);
      } else {
        cv.innerHTML = '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M9 18V6l10-2v11"/><circle cx="6.5" cy="18" r="2.4"/><circle cx="16.5" cy="15" r="2.4"/></svg>';
      }
      row.appendChild(cv);
      // 标题 + 歌手
      var info = el('div', 'mu-info');
      info.appendChild(el('b', null, t.title || '（无标题）'));
      info.appendChild(el('span', null, (t.artist || '未知歌手') + ' · ' + (t.album || '未知专辑')));
      row.appendChild(info);
      // 格式/大小
      row.appendChild(el('span', 'mu-ext', (t.ext || '').toUpperCase() + (t.size ? ' · ' + fmtSize(t.size) : '')));
      // 时长
      row.appendChild(el('span', 'mu-dur', fmtDur(t.duration)));
      // 操作
      var acts = el('span', 'mu-acts');
      var bPlay = el('button', 'mu-ab', '▶'); bPlay.title = '播放';
      bPlay.onclick = function (ev) { ev.stopPropagation(); playFrom(i); };
      acts.appendChild(bPlay);
      if (state.plId) {
        var bRm = el('button', 'mu-ab', '移出'); bRm.title = '从歌单移除';
        bRm.onclick = function (ev) {
          ev.stopPropagation();
          api('pl.remove', { id: state.plId, trackIds: [t.id] }).then(function () {
            toast('已移出'); loadList(); loadChips();
          }).catch(function (e) { toast(e.message, true); });
        };
        acts.appendChild(bRm);
      } else {
        var bAdd = el('button', 'mu-ab', '+ 歌单'); bAdd.title = '加入歌单';
        bAdd.onclick = function (ev) { ev.stopPropagation(); addToPl([t.id]); };
        acts.appendChild(bAdd);
      }
      row.appendChild(acts);
      row.ondblclick = function () { playFrom(i); };
      row.onclick = function () { playFrom(i); };
      box.appendChild(row);
    });
  }

  // ── 播放器 ──
  var audio = $('m-audio');

  function playFrom(i) {
    state.queue = state.items.slice();
    playAt(i);
  }
  function playAt(i) {
    if (!state.queue.length) return;
    if (i < 0) i = 0;
    if (i >= state.queue.length) i = state.queue.length - 1;
    state.cur = i;
    var t = state.queue[i];
    audio.src = t.url;
    audio.play().catch(function () { /* 自动播放被拦时用户手动点 */ });
    $('m-player').hidden = false;
    $('mp-cover').src = t.cover || '';
    $('mp-cover').hidden = !t.cover;
    $('mp-title').textContent = t.title || '';
    $('mp-artist').textContent = (t.artist || '未知歌手') + ' · ' + (t.album || '');
    document.body.classList.add('vplaying');
    if (karaMode !== 'off') applyKaraMode(); // 惰性建 Web Audio 图（需在播放后接一次）
    api('stats.play', { id: t.id }).catch(function () {});
    loadLyrics(t);
    paintList();
  }
  audio.onplay = function () { $('mp-play').textContent = '⏸'; };
  audio.onpause = function () { $('mp-play').textContent = '▶'; };
  audio.ontimeupdate = function () {
    var d = audio.duration || 0;
    $('mp-cur').textContent = fmtDur(audio.currentTime);
    $('mp-dur').textContent = fmtDur(d);
    $('mp-fill').style.width = d ? (audio.currentTime / d * 100) + '%' : '0';
    karaSync();
  };
  audio.onended = function () { playAt(state.cur + 1); };
  $('mp-play').onclick = function () {
    if (!audio.src) { if (state.items.length) playFrom(0); return; }
    if (audio.paused) audio.play(); else audio.pause();
  };
  $('mp-prev').onclick = function () { playAt(state.cur - 1); };
  $('mp-next').onclick = function () { playAt(state.cur + 1); };
  $('mp-track').onclick = function (ev) {
    if (!audio.duration) return;
    var r = this.getBoundingClientRect();
    audio.currentTime = (ev.clientX - r.left) / r.width * audio.duration;
  };

  // ── 卡拉OK A：消声（Web Audio 即时 DSP，纯前端、断网可用）──
  // 原理：立体声拆分 → (L-R)/2 消掉中置人声；(L+R)/2 过 200Hz 低通保底鼓贝斯；
  //       dry 干路用于「原唱」与混合档。mono 源相减为 0（伴奏档静音），按钮 title 已提示。
  var karaMode = 'off'; // off / karaoke（伴奏·保低音）/ hard（全消）
  try { karaMode = localStorage.getItem('mu.karaoke') || 'off'; } catch (e) {}
  var actx = null, kNodes = null;

  function karaInit() {
    if (kNodes) return kNodes;
    var AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return null;
    try { actx = new AC(); } catch (e) { return null; }
    var src = actx.createMediaElementSource(audio);
    var split = actx.createChannelSplitter(2);
    src.connect(split);
    // 消人声支路：(L-R)/2
    var gL = actx.createGain(); gL.gain.value = 0.5;
    var gR = actx.createGain(); gR.gain.value = -0.5;
    var cancelSum = actx.createGain();
    split.connect(gL, 0); split.connect(gR, 1);
    gL.connect(cancelSum); gR.connect(cancelSum);
    var cancelG = actx.createGain();
    cancelSum.connect(cancelG); cancelG.connect(actx.destination);
    // 低音保底支路：(L+R)/2 → 低通
    var gLb = actx.createGain(); gLb.gain.value = 0.5;
    var gRb = actx.createGain(); gRb.gain.value = 0.5;
    var bassSum = actx.createGain();
    split.connect(gLb, 0); split.connect(gRb, 1);
    gLb.connect(bassSum); gRb.connect(bassSum);
    var lp = actx.createBiquadFilter(); lp.type = 'lowpass'; lp.frequency.value = 200;
    var bassG = actx.createGain();
    bassSum.connect(lp); lp.connect(bassG); bassG.connect(actx.destination);
    // 原声干路
    var dry = actx.createGain();
    src.connect(dry); dry.connect(actx.destination);
    kNodes = { dry: dry, cancel: cancelG, bass: bassG };
    return kNodes;
  }

  function applyKaraMode() {
    var label = karaMode === 'off' ? '原唱' : (karaMode === 'karaoke' ? '伴奏' : '全消');
    $('mp-mode').textContent = label;
    $('kara-mode').textContent = label;
    $('mp-mode').classList.toggle('kon', karaMode !== 'off');
    $('kara-mode').classList.toggle('kon', karaMode !== 'off');
    var k = karaInit();
    if (!k) return;
    if (actx.state === 'suspended') { actx.resume().catch(function () {}); }
    var g = karaMode === 'off' ? { d: 1, c: 0, b: 0 }
      : karaMode === 'karaoke' ? { d: 0, c: 1, b: 0.9 }
      : { d: 0, c: 1, b: 0 };
    k.dry.gain.value = g.d; k.cancel.gain.value = g.c; k.bass.gain.value = g.b;
  }

  function cycleKara() {
    karaMode = karaMode === 'off' ? 'karaoke' : (karaMode === 'karaoke' ? 'hard' : 'off');
    try { localStorage.setItem('mu.karaoke', karaMode); } catch (e) {}
    applyKaraMode();
    toast(karaMode === 'off' ? '原唱' :
      karaMode === 'karaoke' ? '伴奏：已消中置人声（仅立体声有效）' : '全消：低音也一并消掉');
  }
  $('mp-mode').onclick = cycleKara;
  $('kara-mode').onclick = cycleKara;

  // ── 卡拉OK C：LRC 歌词 + K 歌大屏 ──
  var lyrics = null; // [{t,text}] 或 null（无歌词）

  function loadLyrics(t) {
    lyrics = null;
    state.lrcIdx = -1;
    $('kara-lines').innerHTML = '';
    $('kara-empty').hidden = true;
    $('kara-title').textContent = t ? (t.title || '') : '';
    $('kara-sub').textContent = t ? (t.artist || '未知歌手') + ' · ' + (t.album || '') : '';
    if (!t) return;
    api('lyrics', { id: t.id }).then(function (j) {
      // 歌回来时可能已经切歌了
      var cur = state.queue[state.cur];
      if (!cur || cur.id !== t.id) return;
      lyrics = j.has ? j.items : null;
      paintKara();
    }).catch(function () {});
  }

  function paintKara() {
    var box = $('kara-lines');
    box.innerHTML = '';
    var empty = $('kara-empty');
    if (!lyrics || !lyrics.length) {
      empty.hidden = false;
      state.lrcIdx = -1;
      return;
    }
    empty.hidden = true;
    lyrics.forEach(function (ln, i) {
      var d = el('div', 'kara-line', ln.text || '♪');
      d.onclick = function () { audio.currentTime = Math.max(0, ln.t - 0.15); karaSync(); };
      box.appendChild(d);
    });
    state.lrcIdx = -1;
    karaSync();
  }

  function karaSync() {
    if ($('m-kara').hidden || !lyrics || !lyrics.length) return;
    var t = audio.currentTime + 0.2; // 提前 0.2s 起亮，跟唱不滞后
    var idx = -1;
    for (var i = 0; i < lyrics.length; i++) {
      if (lyrics[i].t <= t) idx = i; else break;
    }
    if (idx === state.lrcIdx) return;
    state.lrcIdx = idx;
    var nodes = $('kara-lines').children;
    for (var j = 0; j < nodes.length; j++) nodes[j].classList.toggle('on', j === idx);
    var cur = nodes[idx];
    if (cur) {
      $('kara-lines').scrollTop = Math.max(0, cur.offsetTop - $('kara-lines').clientHeight / 2 + cur.offsetHeight);
    }
  }

  $('mp-kara').onclick = function () {
    $('m-kara').hidden = false;
    paintKara();
  };
  $('kara-close').onclick = function () { $('m-kara').hidden = true; };
  document.addEventListener('keydown', function (ev) {
    if (ev.key === 'Escape' && !$('m-kara').hidden) $('m-kara').hidden = true;
  });

  // ── 通用弹窗 ──
  function openDlg(node) {
    var dlg = $('m-dlg');
    dlg.innerHTML = '';
    dlg.appendChild(node);
    $('m-mask').hidden = false;
  }
  function closeDlg() { $('m-mask').hidden = true; }
  $('m-mask').onclick = function (ev) { if (ev.target === this) closeDlg(); };
  document.addEventListener('keydown', function (ev) {
    if (ev.key === 'Escape' && !$('m-mask').hidden) closeDlg();
  });

  function dlgPrompt(title, def, fn) {
    var box = el('div', 'mu-dlg-body');
    box.appendChild(el('h3', null, title));
    var inp = el('input'); inp.value = def || '';
    box.appendChild(inp);
    var btns = el('div', 'mu-btns');
    var bc = el('button', null, '取消'); bc.onclick = closeDlg;
    var ok = el('button', 'primary', '确定');
    ok.onclick = function () { var v = inp.value.trim(); if (!v) return; closeDlg(); fn(v); };
    btns.appendChild(bc); btns.appendChild(ok);
    box.appendChild(btns);
    openDlg(box);
    inp.focus(); inp.select();
    inp.onkeydown = function (ev) { if (ev.key === 'Enter') ok.click(); };
  }

  function addToPl(trackIds) {
    if (!trackIds || !trackIds.length) return;
    api('pl.list').then(function (j) {
      var box = el('div', 'mu-dlg-body');
      box.appendChild(el('h3', null, '加入歌单（' + trackIds.length + ' 首）'));
      var list = el('div', 'mu-pick-list');
      (j.playlists || []).forEach(function (p) {
        var b = el('button', 'mu-pick', p.name + '（' + p.count + '）');
        b.onclick = function () {
          api('pl.add', { id: p.id, trackIds: trackIds }).then(function (r) {
            closeDlg();
            if (r.added > 0) toast('已把 ' + r.added + ' 首加入「' + p.name + '」');
            else toast('这些歌都已在歌单里了', true);
            state.sel = {}; paintList(); updateSelUI(); loadChips();
          }).catch(function (e) { toast(e.message, true); });
        };
        list.appendChild(b);
      });
      if (!(j.playlists || []).length) {
        list.appendChild(el('div', 'mu-chips-empty', '还没有歌单'));
      }
      box.appendChild(list);
      var bNew = el('button', 'mu-pick new', '＋ 新建歌单并加入');
      bNew.onclick = function () {
        closeDlg();
        dlgPrompt('新建歌单', '', function (name) {
          api('pl.save', { name: name }).then(function (r) {
            return api('pl.add', { id: r.id, trackIds: trackIds }).then(function (r2) {
              toast('已把 ' + (r2.added || 0) + ' 首加入「' + name + '」');
              state.sel = {}; paintList(); updateSelUI(); loadChips();
            });
          }).catch(function (e) { toast(e.message, true); });
        });
      };
      box.appendChild(bNew);
      openDlg(box);
    }).catch(function (e) { toast(e.message, true); });
  }

  // ── 歌单管理 ──
  function renamePl(p) {
    dlgPrompt('重命名歌单', p.name, function (name) {
      api('pl.save', { id: p.id, name: name, note: p.note }).then(function () {
        state.plName = name; loadChips(); loadList(); toast('已重命名');
      }).catch(function (e) { toast(e.message, true); });
    });
  }
  $('m-pl-new').onclick = function () {
    dlgPrompt('新建歌单', '', function (name) {
      api('pl.save', { name: name }).then(function () {
        toast('已创建'); loadChips();
      }).catch(function (e) { toast(e.message, true); });
    });
  };
  $('m-pl-rename').onclick = function () {
    if (!state.plId) return;
    renamePl({ id: state.plId, name: state.plName });
  };
  $('m-pl-del').onclick = function () {
    if (!state.plId) return;
    if (!confirm('删除歌单「' + state.plName + '」？曲库不受影响。')) return;
    api('pl.delete', { id: state.plId }).then(function () {
      state.plId = ''; state.plName = '';
      toast('已删除'); loadChips(); loadList();
    }).catch(function (e) { toast(e.message, true); });
  };

  // ── 扫描 ──
  $('m-scan').onclick = function () {
    var btn = this;
    btn.disabled = true;
    api('scan').then(function (j) {
      toast('扫描完成：新增 ' + j.added + ' · 更新 ' + j.updated + ' · 移除 ' + j.removed);
      loadChips(); loadList();
    }).catch(function (e) { toast(e.message, true); })
      .then(function () { btn.disabled = false; });
  };

  // ── 音乐U盘向导 ──
  $('m-usb').onclick = function () { usbWizard(); };

  function usbWizard() {
    var selCount = Object.keys(state.sel).length;
    api('pl.list').then(function (j) {
      var box = el('div', 'mu-dlg-body mu-wizard');
      box.appendChild(el('h3', null, '制作音乐U盘'));

      // 第一步：内容来源
      box.appendChild(el('div', 'mu-wz-h', '① 选择内容'));
      var src = selCount > 0 ? 'sel' : (state.plId ? 'pl' : 'all');
      var radios = el('div', 'mu-wz-srcs');
      function radio(v, label) {
        var lb = el('label', 'mu-radio');
        var r = el('input'); r.type = 'radio'; r.name = 'mu-src'; r.value = v; r.checked = v === src;
        lb.appendChild(r); lb.appendChild(el('span', null, label));
        radios.appendChild(lb);
      }
      radio('sel', '勾选的 ' + selCount + ' 首');
      radio('pl', '整个歌单：' + (state.plId ? state.plName : '（选下面的歌单）'));
      radio('all', '整个曲库');
      var plSel = el('select'); plSel.id = 'mu-wz-pl';
      (j.playlists || []).forEach(function (p) {
        var o = el('option', null, p.name + '（' + p.count + '）');
        o.value = p.id;
        if (p.id === state.plId) o.selected = true;
        plSel.appendChild(o);
      });
      plSel.hidden = src !== 'pl'; // 只有选「整个歌单」才露出来
      plSel.onchange = function () {
        if (plSel.value) {
          var op = plSel.options[plSel.selectedIndex];
          nameInp.value = op ? op.textContent.split('（')[0] : nameInp.value;
        }
      };
      radios.appendChild(plSel);
      radios.onchange = function (ev) {
        if (ev.target && ev.target.name === 'mu-src') plSel.hidden = ev.target.value !== 'pl';
      };
      box.appendChild(radios);

      // 目录名
      var nameRow = el('div', 'mu-wz-name');
      nameRow.appendChild(el('span', null, 'U盘内目录名'));
      var nameInp = el('input'); nameInp.value = state.plId ? state.plName : '音乐U盘';
      nameRow.appendChild(nameInp);
      box.appendChild(nameRow);
      // 第二步：选盘
      box.appendChild(el('div', 'mu-wz-h', '② 选择U盘'));
      var drvBox = el('div', 'mu-wz-drives', '正在读取可移动盘…');
      box.appendChild(drvBox);

      // 执行
      var btns = el('div', 'mu-btns');
      var bc = el('button', null, '取消'); bc.onclick = closeDlg;
      var ok = el('button', 'primary', '开始导出');
      ok.disabled = true;
      btns.appendChild(bc); btns.appendChild(ok);
      box.appendChild(btns);
      var result = el('div', 'mu-wz-result');
      result.hidden = true;
      box.appendChild(result);

      var chosenDrive = '';
      api('drive.list').then(function (dj) {
        drvBox.innerHTML = '';
        var ds = dj.drives || [];
        if (!ds.length) {
          drvBox.textContent = '没有检测到可移动盘 —— 插上 U 盘后重试。';
          return;
        }
        ds.forEach(function (d) {
          var card = el('button', 'mu-drive');
          card.appendChild(el('b', null, d.letter));
          card.appendChild(el('span', null, (d.label || '无卷标') + ' · ' + (d.fs || '?')));
          card.appendChild(el('i', null, '剩 ' + (d.freeMB / 1024).toFixed(1) + ' GB / 共 ' + (d.totalMB / 1024).toFixed(1) + ' GB'));
          card.onclick = function () {
            chosenDrive = d.letter;
            [].forEach.call(drvBox.children, function (c) { c.classList.remove('on'); });
            card.classList.add('on');
            ok.disabled = false;
          };
          drvBox.appendChild(card);
        });
      }).catch(function (e) { drvBox.textContent = '读取U盘失败：' + e.message; });

      ok.onclick = function () {
        var srcV = (function () {
          var rs = radios.querySelectorAll('input[name="mu-src"]');
          for (var i = 0; i < rs.length; i++) if (rs[i].checked) return rs[i].value;
          return 'all';
        })();
        var body = { name: nameInp.value.trim(), drive: chosenDrive };
        if (srcV === 'sel') {
          body.trackIds = Object.keys(state.sel);
        } else if (srcV === 'pl') {
          if (!plSel.value) { toast('没有可选歌单', true); return; }
          body.plId = plSel.value;
        }
        if (!body.trackIds && !body.plId && srcV === 'pl') return;
        ok.disabled = true;
        ok.textContent = '导出中…';
        api('usb.export', body).then(function (r) {
          ok.textContent = '开始导出';
          var line = '完成：新写入 ' + r.items + ' 首 · 跳过（已存在）' + r.skipped +
            ' · 共 ' + fmtSize(r.bytes) + '\n目标：' + r.dest + '（含 playlist.m3u8）';
          if (r.warn) line += '\n⚠ ' + r.warn;
          result.textContent = line;
          result.hidden = false;
          ok.disabled = false;
          ok.textContent = '完成';
          state.sel = {};
          usbHistoryInto(result);
          loadList();
        }).catch(function (e) {
          ok.disabled = false; ok.textContent = '开始导出';
          result.textContent = '导出失败：' + e.message;
          result.hidden = false;
        });
      };

      openDlg(box);
    }).catch(function (e) { toast(e.message, true); });
  }

  function usbHistoryInto(box) {
    api('usb.history').then(function (j) {
      var items = (j.items || []).slice(0, 3);
      if (!items.length) return;
      var h = el('div', 'mu-wz-hist');
      h.appendChild(el('div', 'mu-wz-h', '最近导出'));
      items.forEach(function (x) {
        h.appendChild(el('div', 'mu-wz-hist-row',
          x.createdAt.slice(0, 16).replace('T', ' ') + ' · ' + x.label + ' → ' + x.drive +
          '（' + x.items + ' 首' + (x.skipped ? '，跳过 ' + x.skipped : '') + '）'));
      });
      box.appendChild(h);
    }).catch(function () {});
  }

  // ── 搜索（防抖 300ms）──
  var qTimer = null;
  $('m-q').oninput = function () {
    var self = this;
    clearTimeout(qTimer);
    qTimer = setTimeout(function () {
      state.q = self.value.trim();
      loadList();
    }, 300);
  };

  // ── tabs 切换 ──
  [].forEach.call($('m-tabs').children, function (b) {
    b.onclick = function () {
      state.tab = b.getAttribute('data-tab');
      state.artist = ''; state.album = ''; state.plId = ''; state.plName = '';
      renderTabs(); loadChips(); loadList();
    };
  });

  // ── 全部播放 ──
  $('m-playall').onclick = function () {
    if (!state.items.length) { toast('没有可播放的歌曲', true); return; }
    playFrom(0);
  };

  // ── 启动 ──
  renderTabs();
  loadChips();
  loadList();
})();
