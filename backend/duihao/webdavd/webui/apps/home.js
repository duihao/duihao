/* 收藏夹（原「导航」）：时钟日期 / 搜索（引擎下拉 + 网址直达） / 分组图标墙 / 管理弹窗 / 添加网站弹窗。
   数据走 /-api/home/*（Cookie 会话），全部存本机单库；图标走 /-api/home/favicon 服务端取一次并缓存在库里。 */
(function () {
  'use strict';

  // 惰性取用：shell.js 在 head 里已就绪，但仍按调用时解析，避免加载顺序变化时拿到 undefined
  var api = function () { return window.hubAPI.apply(null, arguments); };
  var state = { groups: [], marks: [], q: '', editing: null, view: 'icon', loaded: false };
  var $ = function (id) { return document.getElementById(id); };
  var esc = function (s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  };

  /* ── 时钟 / 日期（含农历，Intl 中国历） ── */
  var curDay = '';
  function tickClock() {
    var d = new Date(), p = function (n) { return (n < 10 ? '0' : '') + n; };
    $('clock').textContent = p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds());
    // 跨零点自动换日期（农历跟着走），不用手动刷新页面
    var key = d.getFullYear() + '-' + d.getMonth() + '-' + d.getDate();
    if (key !== curDay) { curDay = key; paintDate(); }
  }
  // 农历写法按万年历/老黄历：农历丙午年九月二十（干支纪年 + 汉字的月与日）
  var LDAY = ['初一', '初二', '初三', '初四', '初五', '初六', '初七', '初八', '初九', '初十',
    '十一', '十二', '十三', '十四', '十五', '十六', '十七', '十八', '十九', '二十',
    '廿一', '廿二', '廿三', '廿四', '廿五', '廿六', '廿七', '廿八', '廿九', '三十'];
  function ganzhi(y) {
    var gan = '甲乙丙丁戊己庚辛壬癸', zhi = '子丑寅卯辰巳午未申酉戌亥';
    return gan[((y - 4) % 10 + 10) % 10] + zhi[((y - 4) % 12 + 12) % 12];
  }
  function lunarOf(d) {
    try {
      var parts = new Intl.DateTimeFormat('zh-CN-u-ca-chinese', { year: 'numeric', month: 'long', day: 'numeric' }).formatToParts(d);
      var g = {};
      parts.forEach(function (p) { g[p.type] = p.value; });
      var year = g.yearName;
      if (!year) { // 老引擎不给 yearName 时自己算干支（按农历所属年）
        year = ganzhi(parseInt(g.relatedYear || String(d.getFullYear()), 10));
      }
      var mon = g.month || '';
      var day = parseInt(g.day, 10);
      var dayTxt = day >= 1 && day <= 30 ? LDAY[day - 1] : (g.day || '');
      return '农历' + year + '年' + mon + dayTxt;
    } catch (e) { return ''; }
  }
  function paintDate() {
    var d = new Date();
    var wk = ['星期日', '星期一', '星期二', '星期三', '星期四', '星期五', '星期六'][d.getDay()];
    var lun = lunarOf(d);
    $('dateline').textContent = d.getFullYear() + '年' + (d.getMonth() + 1) + '月' + d.getDate() + '日 / ' + wk +
      (lun ? ' / ' + lun : '');
  }

  /* ── 图标：服务端取 favicon（本地缓存），失败回落首字母 ── */
  function hostOf(u) { try { return new URL(u).host; } catch (e) { return ''; } }
  function hue(s) {
    var h = 0;
    for (var i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) % 360;
    return h;
  }
  // 取不到图标时的默认图标：纯色圆块（按域名色相）+ 地球轮廓。
  // 不用文字占位，也不留破图：img 加载失败直接移除节点，露出底下的轮廓图标。
  var PH_ICON = '<svg class="bm-ico-ph" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" ' +
    'stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M3 12h18"/>' +
    '<path d="M12 3c2.6 2.7 2.6 15.3 0 18M12 3c-2.6 2.7-2.6 15.3 0 18"/></svg>';

  function iconHTML(b, sz) {
    var host = hostOf(b.url);
    var h = hue(host || b.title);
    return '<span class="bm-ico' + (sz ? ' ' + sz : '') + '" style="--h:' + h + '">' + PH_ICON +
      (host ? '<img src="/-api/home/favicon?host=' + esc(encodeURIComponent(host)) + '" alt="" loading="lazy" ' +
        'onload="this.parentNode.classList.add(\'has-ico\')" onerror="this.remove()">' : '') +
      '</span>';
  }

  /* ── 数据 ── */
  async function load() {
    var j = await api('home', 'list');
    state.groups = j.groups || [];
    state.marks = j.bookmarks || [];
    state.loaded = true;
    paintBoard();
    if (!$('manage-mask').hidden) paintManage();
  }

  function byGroup(gid) {
    return state.marks.filter(function (b) { return (b.groupId || '') === gid; });
  }
  function matchQ(b) {
    if (!state.q) return true;
    var q = state.q.toLowerCase();
    return (b.title + ' ' + b.url + ' ' + (b.descr || '') + ' ' + (b.account || '')).toLowerCase().indexOf(q) >= 0;
  }

  /* ── 首页：两种展示方式（图标式 = 分组图标墙 / 卡片式 = 一类一行） ── */
  function collect() {
    var cols = [];
    state.groups.forEach(function (g) {
      var items = byGroup(g.id).filter(matchQ);
      if (items.length) cols.push({ title: g.title, items: items });
    });
    var loose = byGroup('').filter(matchQ);
    if (loose.length) cols.push({ title: '未分组', items: loose });
    return cols;
  }

  function paintEmpty(cols) {
    $('empty').hidden = cols.length > 0 || !!state.q;
    if (!cols.length && state.q) {
      $('empty').hidden = false;
      $('empty').textContent = '没有匹配的收藏，回车用搜索引擎搜「' + state.q + '」';
    } else {
      $('empty').textContent = '还没有收藏。点下方「管理常用网站」添加第一个。';
    }
  }

  function siteLink(b, cls) {
    var a = document.createElement('a');
    a.className = cls;
    a.href = b.url;
    a.target = '_blank';
    a.rel = 'noopener';
    a.title = b.title + (b.descr ? '\n' + b.descr : '') + (b.account ? '\n账号：' + b.account : '');
    return a;
  }

  function paintBoard() {
    var box = $('board');
    box.removeAttribute('style');
    box.innerHTML = '';
    var cols = collect();
    paintEmpty(cols);
    if (state.view === 'vcard') paintVCards(box, cols);
    else if (state.view === 'card') paintCards(box, cols);
    else paintIcons(box, cols);
  }

  /* 竖版卡片：瀑布流列宽固定 8.75rem。列数取「分类数」与「可用宽度能放下的列数」的较小值，
     再由脚本把分类按估算高度逐个放进当前最矮的列（CSS 多列做不到内容不满时整体居中）。 */
  var VCOLW = 8.75, VGAP = 0.9; // rem，与 shared.css 里 .bm.vcard 保持一致

  // 图标式：每列一个分组，列内竖排圆形图标
  function paintIcons(box, cols) {
    cols.forEach(function (col) {
      var colEl = document.createElement('div');
      colEl.className = 'bm-col';
      colEl.innerHTML = '<div class="bm-col-h">' + esc(col.title) + '</div>';
      col.items.forEach(function (b) {
        var a = siteLink(b, 'bm-site');
        a.innerHTML = iconHTML(b) + '<span class="bm-site-n">' + esc(b.title) + '</span>';
        box.appendChild(colEl);
        colEl.appendChild(a);
      });
    });
  }

  // 卡片式：一个分类一行，行内平铺「图标 + 名称 + 描述」的卡片
  function paintCards(box, cols) {
    cols.forEach(function (col) {
      var row = document.createElement('div');
      row.className = 'bm-crow';
      row.innerHTML = '<div class="bm-crow-h">' + esc(col.title) + '</div>';
      var body = document.createElement('div');
      body.className = 'bm-crow-body';
      col.items.forEach(function (b) {
        var a = siteLink(b, 'bm-ccard');
        a.innerHTML = iconHTML(b, 'sm') +
          '<span class="bm-ccard-t"><span class="nm">' + esc(b.title) + '</span>' +
          (b.descr ? '<span class="ds">' + esc(b.descr) + '</span>' : '') + '</span>';
        body.appendChild(a);
      });
      row.appendChild(body);
      box.appendChild(row);
    });
  }

  // 竖版卡片：瀑布流——列宽固定，分类按估算高度放进当前最矮的列，整块水平居中
  function paintVCards(box, cols) {
    var fs = parseFloat(getComputedStyle(document.documentElement).fontSize) || 16;
    var gap = VGAP * fs;
    var host = box.parentElement || box;
    var avail = host.clientWidth || 0;
    var fit = Math.max(1, Math.floor((avail + gap) / (VCOLW * fs + gap)));
    var n = Math.min(Math.max(cols.length, 1), fit);

    var colEls = [], heights = [];
    for (var i = 0; i < n; i++) {
      var el = document.createElement('div');
      el.className = 'bm-vcol';
      colEls.push(el); heights.push(0);
      box.appendChild(el);
    }

    cols.forEach(function (col) {
      var k = 0;
      for (var j = 1; j < n; j++) if (heights[j] < heights[k]) k = j;
      var head = document.createElement('div');
      head.className = 'bm-vcol-h';
      head.textContent = col.title;
      colEls[k].appendChild(head);
      col.items.forEach(function (b) {
        var a = siteLink(b, 'bm-vcard');
        a.innerHTML = iconHTML(b, 'sm') +
          '<span class="bm-ccard-t"><span class="nm">' + esc(b.title) + '</span>' +
          (b.descr ? '<span class="ds">' + esc(b.descr) + '</span>' : '') + '</span>';
        colEls[k].appendChild(a);
      });
      heights[k] += 1.8 + col.items.length * 2.7;  // 分类标题 + 每张卡片（约值，仅用于配平）
    });
  }

  /* ── 管理弹窗：分组卡片（+ / 改名 / 删 / 拖动排序）与条目（拖拽移动 / 改 / 删） ── */
  var dragEl = null;   // 正在拖的 .bm-item 节点
  var dragBook = null; // 正在拖的书签数据
  var groupDragEl = null; // 正在拖的 .bm-gcard 节点
  var groupDragId = '';

  function groupCards() {
    return [].slice.call($('mgroups').querySelectorAll('.bm-gcard')).filter(function (n) {
      return !n.classList.contains('is-loose');
    });
  }

  async function submitGroupMove(id, index) {
    try {
      await api('home', 'group.move', { id: id, index: index });
      await load();
      toast('分类顺序已更新');
    } catch (e) {
      toast(e.message, true);
      await load();
    }
  }

  function itemIndexOf(body, el) {
    var kids = [].slice.call(body.children).filter(function (n) { return n.classList.contains('bm-item'); });
    return kids.indexOf(el);
  }

  // 插入参照点：同一行比 x（左半 → 插它前面），不同行则比 y。
  function insertRef(body, x, y) {
    var els = [].slice.call(body.children).filter(function (n) {
      return n.classList.contains('bm-item') && n !== dragEl;
    });
    for (var i = 0; i < els.length; i++) {
      var r = els[i].getBoundingClientRect();
      var sameRow = y >= r.top && y <= r.bottom;
      var before = sameRow ? x < r.left + r.width / 2 : y < r.top;
      if (before) return els[i];
    }
    return null;
  }

  function bindDrop(body, gid) {
    body.setAttribute('data-gid', gid);
    body.addEventListener('dragover', function (e) {
      if (!dragEl) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = 'move';
      var slot = body.querySelector('.bm-slot');
      if (slot) slot.parentNode.removeChild(slot);
      var ref = insertRef(body, e.clientX, e.clientY);
      if (ref) body.insertBefore(dragEl, ref); else body.appendChild(dragEl);
      body.classList.add('is-over');
    });
    body.addEventListener('dragleave', function (e) {
      if (!body.contains(e.relatedTarget)) body.classList.remove('is-over');
    });
    body.addEventListener('drop', function (e) {
      if (!dragEl || !dragBook) return;
      e.preventDefault();
      body.classList.remove('is-over');
      var idx = itemIndexOf(body, dragEl);
      submitMove(dragBook.id, gid, idx < 0 ? 0 : idx);
    });
  }

  async function submitMove(id, groupId, index) {
    try {
      await api('home', 'bookmark.move', { id: id, groupId: groupId, index: index });
      await load();
      toast('已移动');
    } catch (e) {
      toast(e.message, true);
      await load();
    }
  }

  function paintManage() {
    var box = $('mgroups');
    box.innerHTML = '';
    var rows = state.groups.map(function (g) { return { g: g, items: byGroup(g.id) }; });
    var loose = byGroup('');
    if (loose.length) rows.push({ g: { id: '', title: '未分组' }, items: loose });

    rows.forEach(function (row) {
      var card = document.createElement('div');
      card.className = 'bm-gcard' + (row.g.id === '' ? ' is-loose' : '');
      var head = document.createElement('div');
      head.className = 'bm-gcard-h';
      head.innerHTML = '<b>' + esc(row.g.title) + '</b><span class="acts">' +
        (row.g.id ? '<button data-act="gadd" class="link-btn">+ 添加</button>' +
          '<button data-act="gedit" title="重命名分类">✏️</button>' +
          '<button data-act="gdel" class="danger" title="删除分类">✕</button>' : '') +
        '</span>';
      head.querySelector('[data-act="gadd"]').onclick = function () { openMark(null, row.g.id); };
      head.querySelector('[data-act="gedit"]').onclick = function () { renameGroup(row.g); };
      head.querySelector('[data-act="gdel"]').onclick = function () { removeGroup(row.g); };
      card.appendChild(head);

      // 分类拖拽重排：只有从标题栏空白处按下才能拖整张卡片，条目区留给网站拖拽
      card.draggable = false;
      head.title = row.g.id ? '按住标题栏可拖动排序' : '';
      head.addEventListener('mousedown', function (e) {
        if (e.target.closest('button')) return;
        if (row.g.id) card.draggable = true;
      });
      if (row.g.id) {
        card.addEventListener('dragstart', function (e) {
          if (e.target !== card) return; // 条目拖动冒泡上来时不当作分类拖动
          groupDragEl = card; groupDragId = row.g.id;
          card.classList.add('is-drag');
          e.dataTransfer.effectAllowed = 'move';
          try { e.dataTransfer.setData('text/plain', row.g.id); } catch (err) { /* 老内核忽略 */ }
        });
        card.addEventListener('dragend', function () {
          card.draggable = false;
          card.classList.remove('is-drag');
          if (groupDragEl !== card) return;
          var idx = groupCards().indexOf(card);
          var id = groupDragId;
          groupDragEl = null; groupDragId = '';
          if (idx >= 0) submitGroupMove(id, idx);
        });
      }

      var body = document.createElement('div');
      body.className = 'bm-gcard-body';
      bindDrop(body, row.g.id);
      row.items.forEach(function (b) {
        var it = document.createElement('div');
        it.className = 'bm-item';
        it.draggable = true;
        it.title = '按住拖动可换分类 / 调顺序';

        var ops = document.createElement('div');
        ops.className = 'bm-item-ops';
        var ed = document.createElement('button'); ed.textContent = '✎'; ed.title = '修改';
        ed.onclick = function () { openMark(b); };
        var dl = document.createElement('button'); dl.textContent = '✕'; dl.title = '删除'; dl.className = 'danger';
        dl.onclick = function () { removeMark(b); };
        ops.appendChild(ed); ops.appendChild(dl);

        var cardEl = document.createElement('div');
        cardEl.className = 'bm-item-card';
        cardEl.innerHTML = iconHTML(b) + '<span class="bm-nm"></span>';
        cardEl.querySelector('.bm-nm').textContent = b.title;
        cardEl.title = b.url + (b.descr ? '\n' + b.descr : '');

        // 拖拽从整张卡片发起（按钮区不参与，避免误拖）
        it.addEventListener('dragstart', function (e) {
          if (e.target.closest && e.target.closest('.bm-item-ops')) { e.preventDefault(); return; }
          dragEl = it; dragBook = b;
          it.classList.add('is-drag');
          e.dataTransfer.effectAllowed = 'move';
          try { e.dataTransfer.setData('text/plain', b.id); } catch (err) { /* 老内核忽略 */ }
        });
        it.addEventListener('dragend', function () {
          it.classList.remove('is-drag');
          dragEl = null; dragBook = null;
          var overs = box.querySelectorAll('.bm-gcard-body.is-over');
          for (var i = 0; i < overs.length; i++) overs[i].classList.remove('is-over');
        });

        it.appendChild(ops);
        it.appendChild(cardEl);
        body.appendChild(it);
      });
      if (!row.items.length) {
        body.innerHTML = '<span class="bm-slot muted" style="font-size:.76rem;padding:.3rem 0">空分类（可把网站拖进来）</span>';
      }
      card.appendChild(body);
      box.appendChild(card);
    });
  }

  function openManage() { $('manage-mask').hidden = false; paintManage(); }
  function closeManage() { $('manage-mask').hidden = true; }

  /* ── 添加 / 编辑网站弹窗 ── */
  function fillGroupOptions(sel) {
    var s = $('f-group');
    s.innerHTML = '';
    var fallback = '';
    state.groups.forEach(function (g) {
      var op = document.createElement('option');
      op.value = g.id;
      op.textContent = g.title;
      s.appendChild(op);
      if (!fallback && (g.title === '其他' || g.title === '其它')) fallback = g.id;
    });
    if (!s.options.length) { // 一个分类都没有时先给个占位，保存后后端会自动建「其他」
      var op = document.createElement('option');
      op.value = '';
      op.textContent = '其他';
      s.appendChild(op);
    }
    s.value = sel || fallback || s.options[0].value;
  }

  function openMark(b, presetGroup) {
    state.editing = b || null;
    $('mark-title').textContent = b ? '编辑网站' : '添加网站';
    $('f-name').value = b ? b.title : '';
    $('f-url').value = b ? b.url : '';
    $('f-descr').value = b ? (b.descr || '') : '';
    $('f-account').value = b ? (b.account || '') : '';
    $('f-pwd').value = b ? (b.pwdHint || '') : '';
    fillGroupOptions(b ? (b.groupId || '') : (presetGroup || ''));
    $('mark-mask').hidden = false;
    $('f-name').focus();
  }

  async function submitMark() {
    var payload = {
      title: $('f-name').value.trim(),
      url: $('f-url').value.trim(),
      descr: $('f-descr').value.trim(),
      account: $('f-account').value.trim(),
      pwdHint: $('f-pwd').value.trim(),
      groupId: $('f-group').value
    };
    if (!payload.url) { toast('请输入网站地址', true); return; }
    if (state.editing) payload.id = state.editing.id;
    try {
      await api('home', 'bookmark.save', payload);
      $('mark-mask').hidden = true;
      toast(state.editing ? '已保存' : '已添加');
      await load();
    } catch (e) {
      toast(e.message, true);
    }
  }

  async function removeMark(b) {
    if (!confirm('删除「' + b.title + '」？')) return;
    try {
      await api('home', 'bookmark.delete', { id: b.id });
      toast('已删除');
      await load();
    } catch (e) { toast(e.message, true); }
  }

  /* ── 通用输入弹窗（替代原生 prompt，样式与站点弹窗一致） ── */
  var askResolve = null;
  function askOpen(opts) {
    var input = $('ask-input'), area = $('ask-text');
    $('ask-title').textContent = opts.title || '';
    input.hidden = !!opts.multiline;
    area.hidden = !opts.multiline;
    $('ask-hint').hidden = !opts.multiline;
    var field = opts.multiline ? area : input;
    field.value = opts.value || '';
    $('ask-ok').textContent = opts.ok || '确定';
    $('ask-mask').hidden = false;
    setTimeout(function () { field.focus(); }, 30);
    return new Promise(function (res) { askResolve = res; });
  }
  function askClose(v) {
    $('ask-mask').hidden = true;
    var r = askResolve;
    askResolve = null;
    if (r) r(v === undefined ? null : v);
  }
  // 单行版：返回去掉首尾空白的字符串，取消返回 null
  function askName(title, value, ok) {
    return askOpen({ title: title, value: value, ok: ok }).then(function (v) {
      return v == null ? null : String(v).trim();
    });
  }

  async function renameGroup(g) {
    var v = await askName('重命名分类', g.title, '保存');
    if (!v) return;
    try {
      await api('home', 'group.save', { id: g.id, title: v });
      await load();
      toast('已保存');
    } catch (e) { toast(e.message, true); }
  }

  async function addGroup() {
    var v = await askName('新建分类', '', '创建');
    if (!v) return;
    try {
      await api('home', 'group.save', { title: v });
      await load();
      toast('已创建');
    } catch (e) { toast(e.message, true); }
  }

  async function removeGroup(g) {
    if (!confirm('删除分类「' + g.title + '」？其中的网站会移到「其他」。')) return;
    try {
      await api('home', 'group.delete', { id: g.id });
      await load();
    } catch (e) { toast(e.message, true); }
  }

  async function doImport() {
    var v = await askOpen({ title: '导入书签', multiline: true, ok: '开始导入' });
    if (v == null || !v.trim()) return;
    try {
      var r = await api('home', 'import', { text: v });
      toast('导入 ' + r.added + ' 条' + (r.skipped ? '，跳过重复 ' + r.skipped + ' 条' : ''));
      await load();
    } catch (e) { toast(e.message, true); }
  }

  /* ── 搜索：输入即过滤图标墙；回车 = 网址直达 → 首个命中的收藏 → 所选引擎搜索 ── */
  function isUrlLike(s) {
    if (!s || /\s/.test(s)) return false;
    if (/^https?:\/\//i.test(s)) return true;
    if (/^localhost(:\d+)?([\/?#].*)?$/i.test(s)) return true;
    return /^[a-z0-9-]+(\.[a-z0-9-]+)+(:\d+)?([\/?#].*)?$/i.test(s);
  }
  function onSearchEnter() {
    var q = state.q;
    if (!q) return;
    if (isUrlLike(q)) {
      window.open(/^https?:\/\//i.test(q) ? q : 'https://' + q, '_blank', 'noopener');
      return;
    }
    var first = document.querySelector('.bm-site');
    if (first) { window.open(first.href, '_blank', 'noopener'); return; }
    window.open($('engine').value + encodeURIComponent(q), '_blank', 'noopener');
  }

  /* ── 展示方式：图标墙 / 竖版卡片 / 横版卡片（记住选择；对称排版只对图标墙有意义） ── */
  var VIEWS = ['icon', 'vcard', 'card'];
  function applyView(v) {
    state.view = VIEWS.indexOf(v) >= 0 ? v : 'icon';
    var home = $('bm-home');
    home.classList.toggle('vcard', state.view === 'vcard');
    home.classList.toggle('cards', state.view === 'card');
    var seg = $('viewseg').querySelectorAll('button');
    for (var i = 0; i < seg.length; i++) seg[i].classList.toggle('on', seg[i].getAttribute('data-view') === state.view);
    localStorage.setItem('hub-view', state.view);
    if (state.loaded) paintBoard();
  }

  /* ── 提示 ── */
  var toastTimer = null;
  function toast(msg, err) {
    var t = document.getElementById('hub-toast');
    if (!t) {
      t = document.createElement('div');
      t.id = 'hub-toast'; t.className = 'hub-toast';
      document.body.appendChild(t);
    }
    t.textContent = msg;
    t.className = 'hub-toast on' + (err ? ' err' : '');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { t.className = 'hub-toast'; }, 2400);
  }

  /* ── 绑定 ── */
  tickClock();
  setInterval(tickClock, 1000);
  applyView(localStorage.getItem('hub-view') || 'icon');
  var segBtns = $('viewseg').querySelectorAll('button');
  for (var di = 0; di < segBtns.length; di++) {
    segBtns[di].onclick = function () { applyView(this.getAttribute('data-view')); };
  }

  // 记住上次选的搜索引擎
  var savedEngine = localStorage.getItem('hub-engine');
  if (savedEngine) {
    var opts = $('engine').options;
    for (var i = 0; i < opts.length; i++) if (opts[i].value === savedEngine) { $('engine').value = savedEngine; break; }
  }
  $('engine').addEventListener('change', function () { localStorage.setItem('hub-engine', this.value); });

  // 窗口宽度变了要重算瀑布流列数（列数受可用宽度限制，否则居中块会偏）
  var rzTimer = null;
  window.addEventListener('resize', function () {
    clearTimeout(rzTimer);
    rzTimer = setTimeout(function () { if (state.loaded) paintBoard(); }, 140);
  });

  $('q').addEventListener('input', function () { state.q = this.value.trim(); paintBoard(); });
  $('q').addEventListener('keydown', function (e) {
    if (e.key === 'Enter') { e.preventDefault(); onSearchEnter(); }
  });

  $('btn-manage').onclick = openManage;
  $('btn-add-site').onclick = function () { openMark(null, ''); };
  $('btn-manage-close').onclick = closeManage;
  $('manage-mask').addEventListener('click', function (e) { if (e.target === this) closeManage(); });
  $('btn-add-group').onclick = addGroup;
  $('btn-import').onclick = doImport;

  // 分类拖拽排序（只在 #mgroups 上挂一次，卡片重绘不影响）
  $('mgroups').addEventListener('dragover', function (e) {
    if (!groupDragEl) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    var cards = groupCards().filter(function (n) { return n !== groupDragEl; });
    var ref = null;
    for (var i = 0; i < cards.length; i++) {
      var r = cards[i].getBoundingClientRect();
      if (e.clientY < r.top + r.height / 2) { ref = cards[i]; break; }
    }
    if (ref) this.insertBefore(groupDragEl, ref);
    else if (cards.length) this.insertBefore(groupDragEl, cards[cards.length - 1].nextSibling);
  });

  // 通用输入弹窗
  $('ask-ok').onclick = function () {
    var v = $('ask-text').hidden ? $('ask-input').value : $('ask-text').value;
    askClose(v);
  };
  $('ask-cancel').onclick = function () { askClose(null); };
  $('ask-mask').addEventListener('click', function (e) { if (e.target === this) askClose(null); });
  $('ask-input').addEventListener('keydown', function (e) {
    if (e.key === 'Enter') { e.preventDefault(); askClose(this.value); }
  });

  $('mark-cancel').onclick = function () { $('mark-mask').hidden = true; };
  $('mark-ok').onclick = submitMark;
  $('mark-mask').addEventListener('click', function (e) { if (e.target === this) this.hidden = true; });
  document.addEventListener('keydown', function (e) {
    if (e.key !== 'Escape') return;
    if (!$('ask-mask').hidden) { askClose(null); return; }
    if (!$('mark-mask').hidden) { $('mark-mask').hidden = true; return; }
    if (!$('manage-mask').hidden) closeManage();
  });
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Enter' && !$('ask-mask').hidden) return; // 输入弹窗自己处理回车
    if (e.key === 'Enter' && !$('mark-mask').hidden && e.target.tagName !== 'BUTTON') submitMark();
  });

  load().catch(function (e) { toast(e && e.message ? e.message : '加载失败', true); });
})();
