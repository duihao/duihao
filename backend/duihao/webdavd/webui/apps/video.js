/* 影视：海报墙 / 详情 / 播放器（S4）。
   数据来自 /-api/video/*；视频与海报直接走 WebDAV 路径（Cookie 会话内、支持 Range）。 */
(function () {
  'use strict';

  var $ = function (id) { return document.getElementById(id); };
  var esc = function (s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  };

  var state = {
    items: [], cats: [], q: '', cat: '', sort: 'recent',
    page: 1, pages: 1, total: 0, loading: false,
    hist: false, histRows: [],   // 观看记录模式（左栏选中时才显示）
    current: null,   // 详情/播放中的影片
    curIdx: -1,      // 播放中的文件下标
    saveTimer: null
  };

  function api(action, body) {
    return window.hubAPI('video', action, body);
  }

  function toast(msg, bad) {
    var t = document.createElement('div');
    t.className = 'vtoast' + (bad ? ' bad' : '');
    t.textContent = msg;
    document.body.appendChild(t);
    setTimeout(function () { t.classList.add('show'); }, 10);
    setTimeout(function () { t.classList.remove('show'); setTimeout(function () { t.remove(); }, 300); }, 2200);
  }

  function fmtSize(n) {
    if (!n) return '';
    if (n >= 1 << 30) return (n / (1 << 30)).toFixed(1) + ' GB';
    if (n >= 1 << 20) return (n / (1 << 20)).toFixed(0) + ' MB';
    return (n / 1024).toFixed(0) + ' KB';
  }
  function fmtTime(s) {
    s = Math.max(0, Math.floor(s || 0));
    var h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = s % 60;
    var p = function (n) { return (n < 10 ? '0' : '') + n; };
    return h > 0 ? h + ':' + p(m) + ':' + p(sec) : m + ':' + p(sec);
  }
  var KIND = { movie: '电影', series: '剧集', variety: '综艺', anime: '动漫', doc: '纪录片', mv: 'MV' };

  /* ── 列表 ── */

  // reset=true 从第 1 页重画；否则在当前列表后追加（无限滚动）
  function load(reset) {
    if (reset) state.page = 1;
    state.loading = true;
    $('v-scan').disabled = true;
    return api('list', {
      q: state.q, cat: state.cat, sort: state.sort, page: state.page, pageSize: 30
    }).then(function (j) {
      state.cats = j.cats || [];
      state.total = j.total || 0;
      state.pages = j.pages || 1;
      if (reset || !(state.items || []).length) state.items = j.items || [];
      else state.items = state.items.concat(j.items || []);
      state.page = j.page || 1;
      if (reset) painted = 0; // 重画模式先清计数，paintWall 才会清墙
      if (reset) paintChips(); // 追加翻页时不动左栏（避免抢焦点/闪烁）
      paintWall();
      paintFoot();
    }).catch(function (e) {
      toast(e.message, true);
    }).finally(function () {
      state.loading = false;
      $('v-scan').disabled = false;
    });
  }

  function paintChips() {
    var box = $('v-cats');
    box.innerHTML = '';
    var grand = 0;
    state.cats.forEach(function (c) { grand += (c.count || 0); });
    var mk = function (name, label, count) {
      var b = document.createElement('button');
      b.className = 'vt-chip' + (!state.hist && state.cat === name ? ' on' : '');
      b.innerHTML = '<span class="vt-chip-n">' + esc(label) + '</span>' + (count != null ? '<i>' + count + '</i>' : '');
      b.onclick = function () {
        state.cat = name;
        if (state.hist) setHist(false); // 从观看记录切回海报墙
        load(true);
      };
      box.appendChild(b);
    };
    mk('', '全部', grand);
    state.cats.forEach(function (c) { mk(c.name, c.label || c.name, c.count); });
  }

  // 海报兜底：无图时用域名色相渐变 + 片名（不用破图也不做字母占位）
  function hueOf(s) {
    var h = 0;
    for (var i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) % 360;
    return h;
  }

  function posterHTML(p, title) {
    if (p) {
      return '<img src="' + esc(p) + '" alt="" loading="lazy" onerror="this.remove()">';
    }
    var h = hueOf(title || '?');
    return '<div class="vt-ph" style="--h:' + h + '"><span>' + esc(title || '未命名') + '</span></div>';
  }

  var painted = 0; // 已渲染到墙上的卡片数（追加模式下只画新增的，避免封面闪烁）

  function paintWall() {
    var box = $('v-wall');
    var empty = $('v-empty');
    if (!state.items.length) {
      painted = 0;
      box.innerHTML = '';
      empty.hidden = !state.q; // 搜索无结果时提示
      if (state.q) empty.textContent = '没有匹配「' + state.q + '」的影片';
      else { empty.hidden = false; }
      return;
    }
    empty.hidden = true;
    if (!painted) box.innerHTML = '';
    for (; painted < state.items.length; painted++) {
      var it = state.items[painted];
      var card = document.createElement('button');
      card.className = 'vt-card';
      card.setAttribute('data-i', painted);
      var badge = '';
      if (it.rating != null) badge = '<b class="vt-rate">' + it.rating.toFixed(1) + '</b>';
      // 副标题：年份 · 类型 · 分类；类型名与分类名重复时只留一个
      var kindTxt = KIND[it.kind] || it.kind || '';
      var meta = [];
      if (it.year) meta.push(String(it.year));
      if (kindTxt) meta.push(kindTxt);
      if (it.cat && it.cat !== kindTxt) meta.push(it.cat);
      card.innerHTML =
        '<div class="vt-poster">' + posterHTML(it.poster, it.title) + badge +
        (it.epCount > 1 ? '<b class="vt-eps">' + it.epCount + ' 集</b>' : '') + '</div>' +
        '<div class="vt-card-t">' + esc(it.title) + '</div>' +
        '<div class="vt-card-s">' + esc(meta.join(' · ')) + '</div>';
      card.onclick = function () { openDetail(state.items[this.getAttribute('data-i')].id); };
      box.appendChild(card);
    }
  }

  // 无限滚动：页脚只放哨兵，滑近底部自动加载下一页
  var io = null;

  function paintFoot() {
    var box = $('v-foot');
    box.innerHTML = '';
    if (io) { io.disconnect(); }
    if (state.page >= state.pages) {
      if (state.items.length > 8) {
        var t = document.createElement('span');
        t.className = 'muted';
        t.textContent = '已经到底了 · 共 ' + state.total + ' 部';
        box.appendChild(t);
      }
      return;
    }
    var s = document.createElement('div');
    s.className = 'vt-sentinel';
    var tip = document.createElement('span');
    tip.className = 'muted';
    tip.textContent = '加载中…';
    box.appendChild(s);
    box.appendChild(tip);
    if (!io) {
      io = new IntersectionObserver(function (ents) {
        for (var i = 0; i < ents.length; i++) {
          if (ents[i].isIntersecting && !state.loading && state.page < state.pages) {
            state.page += 1;
            load(false);
            break;
          }
        }
      }, { rootMargin: '700px 0px' });
    }
    io.observe(s);
  }

  /* ── 继续观看 ── */

  // 观看记录：默认不显示，左栏「观看记录」选中才列出来（按最后观看时间倒序）
  function loadContinue() {
    return api('continue.list', {}).then(function (j) {
      state.histRows = j.rows || [];
      $('v-hist-n').textContent = state.histRows.length || '';
      $('v-hist-n').hidden = !state.histRows.length;
      if (state.hist) paintHist();
    }).catch(function () { /* 静默：没有历史很正常 */ });
  }

  function relTime(s) {
    var t = Date.parse(s);
    if (!t) return '';
    var d = Math.floor((Date.now() - t) / 1000);
    if (d < 60) return '刚刚';
    if (d < 3600) return Math.floor(d / 60) + ' 分钟前';
    if (d < 86400) return Math.floor(d / 3600) + ' 小时前';
    if (d < 86400 * 7) return Math.floor(d / 86400) + ' 天前';
    var dt = new Date(t);
    return (dt.getMonth() + 1) + '月' + dt.getDate() + '日';
  }

  function paintHist() {
    var box = $('v-hist');
    var rows = state.histRows || [];
    if (state.q) {
      var q = state.q.toLowerCase();
      rows = rows.filter(function (r) { return String(r.title || '').toLowerCase().indexOf(q) >= 0; });
    }
    box.innerHTML = '';
    if (!rows.length) {
      var tip = document.createElement('div');
      tip.className = 'vt-empty muted';
      tip.textContent = state.q
        ? '没有匹配「' + state.q + '」的观看记录'
        : '还没有观看记录。播放任意影片后，进度会按最后观看时间出现在这里。';
      box.appendChild(tip);
      return;
    }
    rows.forEach(function (r) {
      var pct = r.duration > 0 ? Math.min(100, Math.round(r.position / r.duration * 100)) : 0;
      var card = document.createElement('button');
      card.className = 'vt-card';
      card.innerHTML =
        '<div class="vt-poster">' + posterHTML(r.poster, r.title) +
        '<b class="vt-prog" style="width:' + pct + '%"></b>' +
        '<b class="vt-hist-when">' + esc(relTime(r.updated)) + '</b></div>' +
        '<div class="vt-card-t">' + esc(r.title) + '</div>' +
        '<div class="vt-card-s">' + (r.cat ? esc(r.cat) + ' · ' : '') +
        '看到 ' + fmtTime(r.position) + '</div>';
      card.onclick = function () { openPlayer(r.itemId, r.path); };
      box.appendChild(card);
    });
  }

  // 模式切换：false = 分类海报墙，true = 观看记录列表
  function setHist(on) {
    state.hist = !!on;
    $('v-hist-btn').classList.toggle('on', state.hist);
    $('v-hist').hidden = !state.hist;
    $('v-wall').hidden = state.hist;
    $('v-foot').hidden = state.hist;
    if (state.hist) {
      $('v-empty').hidden = true;
      paintHist();
    } else {
      $('v-empty').hidden = true; // 交回 paintWall 判定（有/无结果各不同）
    }
    paintChips();
  }

  /* ── 详情 ── */

  var progressOf = {}; // path → {position,duration}

  function openDetail(id) {
    api('get', { id: id }).then(function (j) {
      state.current = j.item;
      progressOf = {};
      (j.progress || []).forEach(function (p) { progressOf[p.path] = p; });
      paintDetail();
      $('v-mask').hidden = false;
    }).catch(function (e) { toast(e.message, true); });
  }

  function paintDetail() {
    var it = state.current;
    if (!it) return;
    $('v-d-title').textContent = it.title;
    // 海报必须包一层 .vt-poster：占位块是绝对定位的，没有定位祖先会铺满整个弹窗
    $('v-d-poster').innerHTML = '<div class="vt-poster">' + posterHTML(it.poster, it.title) + '</div>';
    var bits = [];
    if (it.year) bits.push(esc(it.year));
    bits.push(esc(KIND[it.kind] || it.kind));
    if (it.area) bits.push(esc(it.area));
    if (it.rating != null) bits.push('<b class="vt-rate-txt">★ ' + it.rating.toFixed(1) + '</b>');
    var meta = '<div class="vt-meta-line">' + bits.join('<i> / </i>') + '</div>';
    if (it.tags) meta += '<div class="vt-meta-line"><b>标签</b>' + esc(it.tags) + '</div>';
    if (it.director) meta += '<div class="vt-meta-line"><b>导演</b>' + esc(it.director) + '</div>';
    if (it.actors) meta += '<div class="vt-meta-line"><b>主演</b>' + esc(it.actors) + '</div>';
    if (it.epCount > 1) meta += '<div class="vt-meta-line"><b>选集</b>' + it.epCount + ' 个文件 · ' + fmtSize(it.size) + '</div>';
    else if (it.size) meta += '<div class="vt-meta-line"><b>大小</b>' + fmtSize(it.size) + '</div>';
    $('v-d-meta').innerHTML = meta;
    $('v-d-intro').textContent = it.intro || '';
    $('v-d-intro').hidden = !it.intro;

    var fx = $('v-d-files');
    fx.innerHTML = '';
    it.files.forEach(function (f, i) {
      var b = document.createElement('button');
      b.className = 'vt-file';
      var p = progressOf[f.path];
      var tail = '';
      if (p && p.position > 30 && (!p.duration || p.position < p.duration - 60)) {
        tail = '<i>看到 ' + fmtTime(p.position) + '</i>';
      } else if (p && p.duration && p.position >= p.duration - 60) {
        tail = '<i class="done">已看完</i>';
      }
      b.innerHTML = '<span class="nm">' + esc(f.name) + '</span>' +
        '<span class="ds">' + fmtSize(f.size) + tail + '</span>';
      b.onclick = function () { openPlayer(it.id, f.path, i); };
      fx.appendChild(b);
    });
    // 默认播放按钮 = 第一个没看完的文件（全看完则回第 1 个）
    var def = 0;
    for (var i = 0; i < it.files.length; i++) {
      var p = progressOf[it.files[i].path];
      var done = p && p.duration && p.position >= p.duration - 60;
      if (!done) { def = i; break; }
    }
    $('v-play').onclick = function () { openPlayer(it.id, it.files[def] ? it.files[def].path : (it.files[0] || {}).path, def); };
    $('v-edit').onclick = openEdit;
  }

  /* ── 播放器 ── */

  function openPlayer(itemId, path, idx) {
    var it = state.current;
    if (!it) {
      api('get', { id: itemId }).then(function (j) {
        state.current = j.item;
        progressOf = {};
        (j.progress || []).forEach(function (p) { progressOf[p.path] = p; });
        var i = idx;
        if (i == null) {
          i = state.current.files.findIndex(function (f) { return f.path === path; });
          if (i < 0) i = 0;
        }
        $('v-mask').hidden = true;
        openPlayer(itemId, state.current.files[i] ? state.current.files[i].path : path, i);
      }).catch(function (e) { toast(e.message, true); });
      return;
    }
    var i = idx;
    if (i == null) {
      i = it.files.findIndex(function (f) { return f.path === path; });
      if (i < 0) i = 0;
    }
    state.curIdx = i;
    var f = it.files[i];
    if (!f) { toast('没有可播放的文件', true); return; }

    $('v-pl-title').textContent = it.title + (it.files.length > 1 ? ' · ' + f.name : '');
    var nx = $('v-next');
    nx.hidden = !(i + 1 < it.files.length);
    nx.onclick = function () { openPlayer(it.id, it.files[i + 1].path, i + 1); };

    var v = $('v-video');
    v.src = f.url;
    $('v-player').hidden = false;
    document.body.classList.add('vplaying');

    // 断点续播：该文件有进度且没看完 → 自动跳过去
    var p = progressOf[f.path];
    v.addEventListener('loadedmetadata', function onMeta() {
      v.removeEventListener('loadedmetadata', onMeta);
      if (p && p.position > 30 && (!p.duration || p.position < p.duration - 60)) {
        try { v.currentTime = p.position; } catch (e) { /* 忽略 seek 失败 */ }
        toast('已从上次位置继续');
      }
      v.play().catch(function () { /* 自动播放被拦：用户手动点 */ });
    });

    // 进度保存：每 5 秒 + 关闭时
    clearInterval(state.saveTimer);
    state.saveTimer = setInterval(function () {
      if (!v.paused && v.currentTime > 5) saveProgress(v);
    }, 5000);
  }

  function saveProgress(v) {
    var f = state.current && state.current.files[state.curIdx];
    if (!f || !v.duration) return;
    api('progress.save', {
      itemId: state.current.id, path: f.path,
      position: Math.floor(v.currentTime), duration: Math.floor(v.duration)
    }).catch(function () { /* 静默：进度失败不打断观看 */ });
  }

  function closePlayer() {
    var v = $('v-video');
    clearInterval(state.saveTimer);
    if (v.duration) saveProgress(v);
    v.pause();
    v.removeAttribute('src');
    v.load();
    $('v-player').hidden = true;
    document.body.classList.remove('vplaying');
    loadContinue(); // 回来刷新继续观看
  }

  /* ── 编辑信息 ── */

  function openEdit() {
    var it = state.current;
    if (!it) return;
    $('v-e-title').value = it.title;
    $('v-e-year').value = it.year || '';
    $('v-e-rating').value = it.rating != null ? it.rating : '';
    $('v-e-kind').value = it.kind;
    $('v-e-tags').value = it.tags;
    $('v-e-area').value = it.area;
    $('v-e-director').value = it.director;
    $('v-e-actors').value = it.actors;
    $('v-e-intro').value = it.intro;
    $('v-mask').hidden = true;
    $('v-emask').hidden = false;
  }

  function submitEdit() {
    var it = state.current;
    if (!it) return;
    var body = { id: it.id };
    var title = $('v-e-title').value.trim();
    if (!title) { toast('片名不能为空', true); return; }
    body.title = title;
    var y = parseInt($('v-e-year').value, 10);
    body.year = isNaN(y) ? 0 : y;
    var r = parseFloat($('v-e-rating').value);
    body.rating = isNaN(r) ? null : r;
    body.kind = $('v-e-kind').value;
    body.tags = $('v-e-tags').value.trim();
    body.area = $('v-e-area').value.trim();
    body.director = $('v-e-director').value.trim();
    body.actors = $('v-e-actors').value.trim();
    body.intro = $('v-e-intro').value.trim();
    api('meta.save', body).then(function (j) {
      state.current = j.item;
      $('v-emask').hidden = true;
      $('v-mask').hidden = false;
      paintDetail();
      load(true);
      toast('已保存');
    }).catch(function (e) { toast(e.message, true); });
  }

  /* ── 扫描 ── */

  function doScan() {
    var btn = $('v-scan');
    btn.disabled = true;
    btn.classList.add('busy');
    api('scan', {}).then(function (j) {
      if (j.hint) { toast(j.hint, true); return; }
      toast('扫描完成：' + j.items + ' 部影片（新增 ' + j.added + '，更新 ' + j.updated + '，移除 ' + j.removed + '）');
      load(true);
      loadContinue();
    }).catch(function (e) {
      toast(e.message, true);
    }).finally(function () {
      btn.disabled = false;
      btn.classList.remove('busy');
    });
  }

  /* ── 键盘（电视遥控友好） ── */

  document.addEventListener('keydown', function (e) {
    var playerOpen = !$('v-player').hidden;
    if (e.key === 'Escape') {
      if (playerOpen) { closePlayer(); return; }
      if (!$('v-emask').hidden) { $('v-emask').hidden = true; return; }
      if (!$('v-mask').hidden) { $('v-mask').hidden = true; return; }
      return;
    }
    if (playerOpen) return;

    // 详情/编辑弹窗开着时不劫持方向键
    if (!$('v-mask').hidden || !$('v-emask').hidden) return;

    // 左栏分类（宽屏竖排）：上下换分类、右键进入海报墙
    var ae = document.activeElement;
    if (ae && ae.classList && ae.classList.contains('vt-chip')) {
      var chips = Array.prototype.slice.call(document.querySelectorAll('#v-cats .vt-chip'));
      var ci = chips.indexOf(ae);
      var vertical = getComputedStyle($('v-cats')).flexDirection === 'column';
      if (e.key === 'ArrowDown' && vertical) {
        e.preventDefault();
        if (ci >= 0 && ci < chips.length - 1) chips[ci + 1].focus();
        return;
      }
      if (e.key === 'ArrowUp' && vertical) {
        e.preventDefault();
        if (ci > 0) chips[ci - 1].focus();
        return;
      }
      if (e.key === 'ArrowRight' || (!vertical && e.key === 'ArrowDown')) {
        var first = cardHost().querySelector('.vt-card');
        if (first) {
          e.preventDefault();
          first.focus();
          first.scrollIntoView({ block: 'nearest' });
        }
        return;
      }
      return; // 其余键交给浏览器
    }

    // 海报墙方向键导航：上下换行、左右换卡片
    var cards = Array.prototype.slice.call(cardHost().querySelectorAll('.vt-card'));
    if (!cards.length) return;
    var fi = cards.indexOf(ae);
    if (fi < 0) return; // 焦点不在墙上，交给浏览器
    e.preventDefault();
    var w = wallColumns();
    var ni = fi;
    if (e.key === 'ArrowRight') ni = fi + 1;
    else if (e.key === 'ArrowLeft') ni = fi - 1;
    else if (e.key === 'ArrowDown') ni = fi + w;
    else if (e.key === 'ArrowUp') ni = fi - w;
    else return;
    if (ni >= 0 && ni < cards.length) {
      cards[ni].focus();
      cards[ni].scrollIntoView({ block: 'nearest' });
    }
  });

  // 估算海报墙列数（供上下键跨行）
  // 当前可见的卡片容器：观看记录态取 #v-hist，否则取海报墙
  function cardHost() { return state.hist ? $('v-hist') : $('v-wall'); }

  function wallColumns() {
    var first = cardHost().querySelector('.vt-card');
    if (!first) return 1;
    var w = first.offsetWidth || 1;
    var gap = 18;
    var host = cardHost().clientWidth || window.innerWidth;
    return Math.max(1, Math.floor((host + gap) / (w + gap)));
  }

  /* ── 绑定 ── */

  var searchTimer = null;
  $('v-q').addEventListener('input', function () {
    clearTimeout(searchTimer);
    var v = this.value.trim();
    searchTimer = setTimeout(function () {
      state.q = v;
      if (state.hist) paintHist(); // 观看记录按片名就地过滤
      else load(true);
    }, 280);
  });
  $('v-hist-btn').onclick = function () {
    if (state.hist) { setHist(false); state.cat = ''; load(true); return; }
    setHist(true);
    loadContinue();
  };
  $('v-sort').onchange = function () { state.sort = this.value; load(true); };
  $('v-scan').onclick = doScan;
  $('v-close').onclick = function () { $('v-mask').hidden = true; };
  $('v-mask').addEventListener('click', function (e) { if (e.target === this) this.hidden = true; });
  $('v-emask').addEventListener('click', function (e) { if (e.target === this) this.hidden = true; });
  $('v-ecancel').onclick = function () { $('v-emask').hidden = true; $('v-mask').hidden = false; };
  $('v-esave').onclick = submitEdit;
  $('v-back').onclick = closePlayer;

  loadContinue();
  load(true);
})();
