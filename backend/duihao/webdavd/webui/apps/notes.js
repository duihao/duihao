/* 笔记（知识卡片形态）：顶部统计 + 搜索/日期/排序 + 分类筛选 + 卡片网格 + 弹窗富文本编辑。
   数据全在本机单库（/-api/notes/*）；正文按 HTML 存，服务端过白名单清洗。 */
(function () {
  'use strict';

  // 惰性取用：shell.js 在 head 里已就绪，但仍按调用时解析，避免加载顺序变化时拿到 undefined
  var api = function () { return window.hubAPI.apply(null, arguments); };
  var $ = function (id) { return document.getElementById(id); };

  var state = {
    q: '', cat: '', range: 'all', from: '', to: '', sort: 'new',
    view: 'cards',      // cards=卡片流 / cats=分类汇总
    page: 1, pageSize: 24, total: 0,
    notes: [], cats: [], stats: {},
    cur: null,          // 正在编辑的笔记（null = 新建）
    viewNote: null,     // 浏览中的笔记
    editCat: '',        // 编辑弹窗里选中的分类
    cover: '',          // 当前编辑笔记的封面（网盘路径或 dataURL）
    dirty: false,
    saving: false,
    seq: 0,
    srcMode: false,
    searchTimer: null,
    pendingUpload: '',  // 隐藏文件输入当前的上传用途：cover/img/video/audio/file
    pick: { mode: '', dir: '/', sel: null, items: [] } // 网盘选择器状态
  };

  /* ── 媒体类型（按扩展名）── */

  var IMG_EXT = 'png,jpg,jpeg,gif,webp,bmp,svg,ico,avif';
  var VIDEO_EXT = 'mp4,webm,ogv,mov,m4v,mkv';
  var AUDIO_EXT = 'mp3,wav,ogg,m4a,flac,aac';

  function extOf(name) {
    var i = name.lastIndexOf('.');
    return i < 0 ? '' : name.slice(i + 1).toLowerCase();
  }
  function kindOf(name) {
    var e = extOf(name);
    if (IMG_EXT.split(',').indexOf(e) >= 0) return 'img';
    if (VIDEO_EXT.split(',').indexOf(e) >= 0) return 'video';
    if (AUDIO_EXT.split(',').indexOf(e) >= 0) return 'audio';
    return 'file';
  }

  // multipart 上传（hubAPI 只发 JSON，这里直接 fetch，同源带 Cookie）
  async function uploadFile(file) {
    var fd = new FormData();
    fd.append('file', file, file.name);
    var res = await fetch('/-api/notes/upload', { method: 'POST', body: fd, credentials: 'same-origin' });
    var j = await res.json().catch(function () { return {}; });
    if (!res.ok) throw new Error(j.error || '上传失败');
    return j;
  }

  // 把网盘条目插进正文：图片/视频/音频/文件各有形态
  function insertItem(it) {
    var k = kindOf(it.name);
    var html;
    if (k === 'img') {
      html = '<img src="' + it.url + '" alt="' + esc(it.name) + '">';
    } else if (k === 'video') {
      html = '<p><video src="' + it.url + '" controls preload="metadata"></video></p>';
    } else if (k === 'audio') {
      html = '<p><audio src="' + it.url + '" controls preload="metadata"></audio></p>';
    } else {
      html = '<p><a href="' + it.url + '" download="' + esc(it.name) + '">📎 ' + esc(it.name) + '</a></p>';
    }
    insertHTML(html);
  }

  function insertHTML(html) {
    if (state.srcMode) {
      // 源码模式：插到光标处
      var ta = $('body-src');
      var s = ta.selectionStart || ta.value.length;
      ta.value = ta.value.slice(0, s) + html + ta.value.slice(ta.selectionEnd || s);
      markDirty();
      return;
    }
    exec('insertHTML', html);
  }

  function toast(msg, err) {
    var t = $('toast');
    t.textContent = msg;
    t.className = 'hub-toast on' + (err ? ' err' : '');
    clearTimeout(t._tid);
    t._tid = setTimeout(function () { t.className = 'hub-toast'; }, 1800);
  }

  function esc(s) {
    return String(s == null ? '' : s).replace(/[&<>"]/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c];
    });
  }

  /* ── 日期 ── */

  function pad(n) { return String(n).padStart(2, '0'); }

  function today() {
    var d = new Date();
    return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate());
  }

  function stamp(iso) {
    var d = new Date(iso);
    if (isNaN(d.getTime())) return iso || '';
    return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) +
      ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes());
  }

  /* ── 列表 ── */

  async function load() {
    var seq = ++state.seq;
    try {
      var j = await api('notes', 'list', {
        q: state.q, cat: state.cat, range: state.range,
        from: state.from, to: state.to, sort: state.sort,
        page: state.page, pageSize: state.pageSize
      });
      if (seq !== state.seq) return;   // 过期响应丢弃
      state.notes = j.notes || [];
      state.cats = j.cats || [];
      state.stats = j.stats || {};
      state.total = j.total || 0;
      state.page = j.page || 1;
      paintStats();
      paintCats();
      paintList();
      paintFoot();
      if (state.view === 'cats') loadCatStats();
    } catch (e) {
      toast(e.message, true);
    }
  }

  /* ── 分类汇总视图（与账本分类统计同形态，共用同一套筛选）── */

  async function loadCatStats() {
    try {
      var j = await api('notes', 'catstats', {
        q: state.q, cat: state.cat, range: state.range,
        from: state.from, to: state.to
      });
      state.total = j.total || 0;
      paintCatPanel(j.rows || []);
      paintFoot();
    } catch (e) {
      toast(e.message, true);
    }
  }

  function paintCatPanel(rows) {
    var total = 0, views = 0;
    rows.forEach(function (r) { total += r.count; views += r.views || 0; });
    var html = '<h4 class="lg-statsec-h"><span>分类汇总</span>' +
      '<b>' + total + ' 篇</b><span class="lg-dim">累计阅读 ' + views + ' 次</span></h4>';
    html += '<div class="lg-stat-group">' + rows.map(function (r) {
      var pct = total > 0 ? Math.max(1, Math.round(r.count * 100 / total)) : 0;
      return '<div class="lg-stat-row"><span class="nm">' + esc(r.cat || '未分类') + '</span>' +
        '<span class="bar"><i style="width:' + pct + '%"></i></span>' +
        '<span class="cnt">' + r.count + ' 篇</span>' +
        '<span class="amt">阅读 ' + (r.views || 0) + '</span></div>';
    }).join('') + '</div>';
    $('cat-panel').innerHTML = html;
    $('cat-panel').hidden = rows.length === 0;
    $('cat-empty').hidden = rows.length > 0;
  }

  var DIMS = [
    ['week', '本周'], ['lastWeek', '上周'],
    ['month', '本月'], ['lastMonth', '上月'], ['year', '今年'], ['lastYear', '去年']
  ];

  function paintStats() {
    var s = state.stats || {};
    $('stat-total').textContent = s.total || 0;
    $('stat-views').textContent = s.views || 0;
    $('stat-today').textContent = s.today || 0;
    $('stat-yesterday').textContent = s.yesterday || 0;
    $('stat-cats').textContent = '覆盖 ' + (s.cats || 0) + ' 个分类';
    var box = $('dims');
    box.innerHTML = '';
    DIMS.forEach(function (d) {
      var el = document.createElement('div');
      el.className = 'kb-dim';
      el.innerHTML = '<span class="t"></span><b></b>';
      el.querySelector('.t').textContent = d[1] + '新增';
      el.querySelector('b').textContent = s[d[0]] || 0;
      box.appendChild(el);
    });
  }

  function chip(text, on, onclick) {
    var b = document.createElement('button');
    b.className = 'kb-chip' + (on ? ' on' : '');
    b.textContent = text;
    b.onclick = onclick;
    return b;
  }

  // 左侧分类导航条目：名称 + 计数徽标
  function sideItem(name, count, on, onclick) {
    var b = document.createElement('button');
    b.className = 'kb-side-item' + (on ? ' on' : '');
    b.innerHTML = '<span class="n"></span><span class="c"></span>';
    b.querySelector('.n').textContent = name;
    b.querySelector('.c').textContent = count || '';
    b.onclick = onclick;
    return b;
  }

  function paintCats() {
    var box = $('cats');
    box.innerHTML = '';
    var used = 0;
    state.cats.forEach(function (c) { used += c.count || 0; });
    var total = (state.stats && state.stats.total) || 0;

    box.appendChild(sideItem('全部', total, state.cat === '', function () { pickCat(''); }));
    if (total > used) { // 有笔记没归类时，给一个「未分类」入口
      box.appendChild(sideItem('未分类', total - used, state.cat === '__none__', function () { pickCat('__none__'); }));
    }
    state.cats.forEach(function (c) {
      box.appendChild(sideItem(c.name, c.count, state.cat === c.name,
        function () { pickCat(c.name); }));
    });
    $('cat-count').textContent = '共 ' + state.cats.length + ' 类';
  }

  function pickCat(name) {
    state.cat = (state.cat === name ? '' : name);
    state.page = 1;
    load();
  }

  // 底部：总数 + 分页（卡片/汇总两视图共用，总都是当前筛选命中的篇数）
  function paintFoot() {
    var pages = Math.max(1, Math.ceil(state.total / state.pageSize));
    if (state.page > pages) state.page = pages;
    $('foot-total').textContent = '共 ' + state.total + ' 篇笔记' +
      (pages > 1 ? '（本页 ' + state.notes.length + ' 篇）' : '');
    $('pg-info').textContent = state.page + ' / ' + pages;
    $('pg-prev').disabled = state.page <= 1;
    $('pg-next').disabled = state.page >= pages;
    $('pager').hidden = pages <= 1;
  }

  function paintList() {
    var box = $('list');
    box.innerHTML = '';
    if (!state.notes.length) {
      var e = document.createElement('div');
      e.className = 'empty-hint muted';
      e.textContent = (state.q || state.cat || state.from || state.to || state.range !== 'all')
        ? '没有匹配的笔记，换个关键词或清除筛选试试'
        : '还没有笔记，点右上「+ 添加笔记」开始写';
      box.appendChild(e);
      return;
    }
    state.notes.forEach(function (n) { box.appendChild(card(n)); });
  }

  function card(n) {
    var el = document.createElement('article');
    el.className = 'kb-card';

    var cover = document.createElement('div');
    cover.className = 'kb-cover' + (n.cover ? ' has-img' : '');
    if (n.cover) {
      var im = document.createElement('img');
      im.src = n.cover;
      im.alt = '';
      im.loading = 'lazy';
      cover.appendChild(im);
    } else {
      cover.innerHTML = '<svg width="34" height="34" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"><path d="M6 3h8l4 4v14H6z"/><path d="M14 3v4h4"/><path d="M9 12h6M9 16h4"/></svg>';
    }
    if (n.cat) {
      var tag = document.createElement('span');
      tag.className = 'kb-cover-cat';
      tag.textContent = n.cat;
      cover.appendChild(tag);
    }
    if (n.pinned) {
      var pin = document.createElement('span');
      pin.className = 'kb-cover-pin';
      pin.textContent = '置顶';
      cover.appendChild(pin);
    }
    el.appendChild(cover);

    var bodyEl = document.createElement('div');
    bodyEl.className = 'kb-card-body';

    var t = document.createElement('h3');
    t.className = 'kb-ct';
    t.textContent = n.title || '无标题';
    bodyEl.appendChild(t);

    var ex = document.createElement('p');
    ex.className = 'kb-ex';
    ex.textContent = n.excerpt || '（暂无正文）';
    bodyEl.appendChild(ex);

    var meta = document.createElement('div');
    meta.className = 'kb-meta';
    meta.innerHTML = '<span>' + esc(stamp(n.createdAt || n.day)) + '</span>' +
      '<span class="kb-views">阅读 ' + (n.views || 0) + ' 次</span>';
    var acts = document.createElement('span');
    acts.className = 'kb-card-acts';
    var bEdit = document.createElement('button');
    bEdit.textContent = '修改';
    bEdit.onclick = function (ev) { ev.stopPropagation(); openNote(n.id); };
    var bDel = document.createElement('button');
    bDel.className = 'danger';
    bDel.textContent = '删除';
    bDel.onclick = function (ev) { ev.stopPropagation(); delNote(n.id); };
    acts.appendChild(bEdit);
    acts.appendChild(bDel);
    meta.appendChild(acts);
    bodyEl.appendChild(meta);

    el.appendChild(bodyEl);
    el.onclick = function () { openView(n.id); };
    return el;
  }

  /* ── 浏览视图（点卡片先看，编辑走这里的「编辑」按钮） ── */

  async function openView(id) {
    try {
      var j = await api('notes', 'get', { id: id });
      var n = j.note;
      state.viewNote = n;
      $('view-title').textContent = n.title || '无标题';

      var meta = $('view-meta');
      meta.innerHTML = '';
      if (n.cat) {
        var c = document.createElement('span');
        c.className = 'kb-view-cat';
        c.textContent = n.cat;
        meta.appendChild(c);
      }
      var t = document.createElement('span');
      t.textContent = stamp(n.createdAt || n.day);
      meta.appendChild(t);

      var cv = $('view-cover');
      if (n.cover) { cv.src = n.cover; cv.hidden = false; } else { cv.hidden = true; }

      var body = n.body || '';
      $('view-body').innerHTML = body.indexOf('<') >= 0 ? body : '<p>' + esc(body).replace(/\n/g, '<br>') + '</p>';
      $('view-views').textContent = '阅读 ' + (n.views || 0) + ' 次' + (n.updatedAt ? ' · 上次保存 ' + stamp(n.updatedAt) : '');
      resetMax('view-mask', 'view-max');
      showMask('view-mask', true);
    } catch (e) {
      toast(e.message, true);
    }
  }

  /* ── 编辑器 ── */

  function showMask(id, on) { $(id).hidden = !on; }

  function setSrcMode(on) {
    var ed = $('body'), src = $('body-src');
    if (on === state.srcMode) return;
    if (on) { src.value = ed.innerHTML; } else { ed.innerHTML = src.value; }
    state.srcMode = on;
    ed.hidden = on;
    src.hidden = !on;
    $('btn-src').textContent = on ? '返回编辑模式' : '切换源码模式';
    $('toolbar').hidden = on;
  }

  function pickEditCat(name) {
    state.editCat = (state.editCat === name ? '' : name);
    paintEditCats();
    markDirty();
  }

  function paintEditCats() {
    var box = $('edit-cats');
    box.innerHTML = '';
    box.appendChild(chip('未分类', state.editCat === '', function () { pickEditCat(''); }));
    state.cats.forEach(function (c) {
      box.appendChild(chip(c.name, state.editCat === c.name, function () { pickEditCat(c.name); }));
    });
  }

  function fill(n) {
    state.cur = n;
    state.editCat = n.cat || '';
    state.cover = n.cover || '';
    state.dirty = false;
    $('edit-head').textContent = n.id ? '修改笔记' : '添加新笔记';
    $('title').value = n.title || '';
    paintCover();
    var body = n.body || '';
    // 历史纯文本笔记（不含标签）转成段落，换行才不会被 HTML 吃掉
    $('body').innerHTML = body.indexOf('<') >= 0 ? body : esc(body).replace(/\n/g, '<br>');
    $('body-src').value = $('body').innerHTML;
    state.srcMode = false;
    $('body').hidden = false;
    $('body-src').hidden = true;
    $('toolbar').hidden = false;
    $('btn-src').textContent = '切换源码模式';
    $('btn-del').hidden = !n.id;
    $('saved').textContent = n.updatedAt ? '上次保存 ' + stamp(n.updatedAt) : '';
    $('hint').textContent = '';
    paintEditCats();
    resetMax('edit-mask', 'edit-max');
    showMask('edit-mask', true);
    $('title').focus();
  }

  function blank() {
    fill({ id: '', title: '', body: '', cat: state.cat === '__none__' ? '' : state.cat,
      day: today(), pinned: false, views: 0, updatedAt: '' });
  }

  async function openNote(id) {
    try {
      var j = await api('notes', 'get', { id: id });
      fill(j.note);
    } catch (e) {
      toast(e.message, true);
    }
  }

  function markDirty() {
    state.dirty = true;
    $('hint').textContent = '未保存…';
  }

  function editorHTML() {
    return state.srcMode ? $('body-src').value : $('body').innerHTML;
  }

  async function save() {
    if (state.saving) return;
    var title = $('title').value.trim();
    var body = editorHTML();
    var hasText = body.replace(/<[^>]*>/g, '').replace(/&nbsp;/g, ' ').trim();
    if (!title && !hasText) { toast('写点什么再保存吧'); return; }
    state.saving = true;
    $('hint').textContent = '保存中…';
    try {
      var payload = {
        id: (state.cur && state.cur.id) || '',
        title: title, body: body, cat: state.editCat,
        day: (state.cur && state.cur.day) || today(),
        pinned: !!(state.cur && state.cur.pinned),
        cover: state.cover
      };
      await api('notes', 'save', payload);
      state.dirty = false;
      showMask('edit-mask', false);
      toast('已保存');
      load();
    } catch (e) {
      $('hint').textContent = '保存失败';
      toast(e.message, true);
    } finally {
      state.saving = false;
    }
  }

  async function delNote(id) {
    if (!id) return;
    if (!confirm('删除这篇笔记？此操作不可撤销。')) return;
    try {
      await api('notes', 'delete', { id: id });
      toast('已删除');
      if (state.cur && state.cur.id === id) showMask('edit-mask', false);
      if (state.viewNote && state.viewNote.id === id) showMask('view-mask', false);
      load();
    } catch (e) {
      toast(e.message, true);
    }
  }

  /* ── 富文本工具栏（contenteditable + execCommand，离线可用，不拉外部编辑器） ── */

  function exec(cmd, val) {
    var ed = $('body');
    ed.focus();
    try { document.execCommand(cmd, false, val || null); } catch (e) { /* 老浏览器忽略 */ }
    markDirty();
    syncFmt();
  }

  function syncFmt() {
    var v = '';
    try { v = (document.queryCommandValue('formatBlock') || '').toLowerCase(); } catch (e) { }
    var sel = $('fmt');
    sel.value = (v === 'h2' || v === 'h3') ? v : 'p';
  }

  /* ── 分类管理 ── */

  function paintCatList() {
    var box = $('cat-list');
    box.innerHTML = '';
    if (!state.cats.length) {
      var e = document.createElement('div');
      e.className = 'muted';
      e.textContent = '还没有分类，先在上面添加一个。';
      box.appendChild(e);
      return;
    }
    state.cats.forEach(function (c) {
      var row = document.createElement('div');
      row.className = 'kb-cat-row';
      row.innerHTML = '<span class="n"></span>';
      row.querySelector('.n').textContent = c.name + (c.count ? '（' + c.count + '）' : '');
      var bEdit = document.createElement('button');
      bEdit.className = 'edit';
      bEdit.textContent = '修改';
      bEdit.onclick = function () { renameCat(c.name); };
      var bDel = document.createElement('button');
      bDel.className = 'del';
      bDel.textContent = '删除';
      bDel.onclick = function () { removeCat(c.name, c.count); };
      row.appendChild(bEdit);
      row.appendChild(bDel);
      box.appendChild(row);
    });
  }

  async function addCat(name) {
    name = (name || '').trim();
    if (!name) return;
    try {
      await api('notes', 'cat.save', { name: name });
      toast('已添加分类 ' + name);
      await load();
      paintCatList();
    } catch (e) { toast(e.message, true); }
  }

  async function renameCat(name) {
    var next = prompt('重命名分类（该分类下的笔记会一起改）', name);
    if (next == null) return;
    next = next.trim();
    if (!next || next === name) return;
    try {
      await api('notes', 'cat.save', { old: name, name: next });
      toast('已重命名');
      if (state.editCat === name) state.editCat = next;
      await load();
      paintCatList();
      paintEditCats();
    } catch (e) { toast(e.message, true); }
  }

  async function removeCat(name, count) {
    var tip = count ? '删除分类「' + name + '」？该分类下 ' + count + ' 篇笔记会变成「未分类」，笔记本身不删。'
      : '删除分类「' + name + '」？';
    if (!confirm(tip)) return;
    try {
      await api('notes', 'cat.delete', { name: name });
      toast('已删除分类');
      if (state.cat === name) state.cat = '';
      if (state.editCat === name) state.editCat = '';
      await load();
      paintCatList();
      paintEditCats();
    } catch (e) { toast(e.message, true); }
  }

  /* ── 导出（数据自救：库里的东西一定要能拿出来） ── */

  async function exportAll() {
    try {
      var j = await api('notes', 'export', {});
      var blob = new Blob([JSON.stringify(j, null, 2)], { type: 'application/json' });
      var a = document.createElement('a');
      a.href = URL.createObjectURL(blob);
      a.download = '笔记导出-' + today() + '.json';
      a.click();
      setTimeout(function () { URL.revokeObjectURL(a.href); }, 4000);
      toast('已导出 ' + j.count + ' 篇');
    } catch (e) {
      toast(e.message, true);
    }
  }

  /* ── 封面 ── */

  function paintCover() {
    var has = !!state.cover;
    $('cover-img').hidden = !has;
    $('cover-ph').hidden = has;
    $('btn-cover-del').hidden = !has;
    if (has) $('cover-img').src = state.cover;
  }

  /* ── 网盘选择器（插正文 / 设封面共用） ── */

  function fmtSize(n) {
    if (n == null) return '';
    if (n < 1024) return n + ' B';
    if (n < 1048576) return (n / 1024).toFixed(1) + ' KB';
    if (n < 1073741824) return (n / 1048576).toFixed(1) + ' MB';
    return (n / 1073741824).toFixed(2) + ' GB';
  }

  function openPick(mode) {
    state.pick = { mode: mode, dir: '/', sel: null, items: [] };
    $('pick-kind').value = mode === 'cover' ? 'img' : 'all';
    loadPick();
    showMask('pick-mask', true);
  }

  async function loadPick() {
    paintCrumb();
    try {
      var j = await api('notes', 'pick.list', { dir: state.pick.dir });
      state.pick.items = j.items || [];
      state.pick.sel = null;
      paintPickList();
    } catch (e) {
      toast(e.message, true);
    }
  }

  function paintCrumb() {
    var box = $('pick-crumb');
    box.innerHTML = '';
    var mk = function (label, p) {
      var b = document.createElement('button');
      b.type = 'button';
      b.textContent = label;
      b.onclick = function () { state.pick.dir = p; loadPick(); };
      return b;
    };
    box.appendChild(mk('📂 网盘', '/'));
    if (state.pick.dir !== '/') {
      var acc = '';
      state.pick.dir.split('/').filter(Boolean).forEach(function (seg) {
        acc += '/' + seg;
        box.appendChild(document.createTextNode(' › '));
        box.appendChild(mk(seg, acc));
      });
    }
  }

  function paintPickList() {
    var box = $('pick-list');
    var kind = $('pick-kind').value;
    box.innerHTML = '';
    var items = state.pick.items.filter(function (it) {
      return it.dir || kind === 'all' || kindOf(it.name) === kind;
    });
    if (!items.length) {
      box.innerHTML = '<p class="muted" style="padding:.8rem">这里没有匹配的文件。</p>';
      paintPickSel();
      return;
    }
    var ICONS = { img: '🖼️', video: '🎬', audio: '🎵', file: '📄' };
    items.forEach(function (it) {
      var row = document.createElement('div');
      row.className = 'kb-pick-row' + (state.pick.sel && state.pick.sel.url === it.url ? ' on' : '');
      if (it.dir) {
        row.innerHTML = '<span class="ic">📁</span><span class="n"></span><span class="m"></span>';
        row.querySelector('.n').textContent = it.name;
        row.onclick = function () {
          state.pick.dir = (state.pick.dir === '/' ? '' : state.pick.dir) + '/' + it.name;
          loadPick();
        };
      } else {
        row.innerHTML = '<span class="ic">' + (ICONS[kindOf(it.name)] || '📄') + '</span><span class="n"></span><span class="m"></span>';
        row.querySelector('.n').textContent = it.name;
        row.querySelector('.m').textContent = fmtSize(it.size);
        row.onclick = function () { state.pick.sel = it; paintPickList(); };
        row.ondblclick = function () { state.pick.sel = it; confirmPick(); };
      }
      box.appendChild(row);
    });
    paintPickSel();
  }

  function paintPickSel() {
    var s = state.pick.sel;
    $('pick-selname').textContent = s ? '已选：' + s.name : '';
    $('pick-ok').disabled = !s;
  }

  function confirmPick() {
    var it = state.pick.sel;
    if (!it || it.dir) return;
    showMask('pick-mask', false);
    if (state.pick.mode === 'cover') {
      state.cover = it.url;
      paintCover();
      markDirty();
      toast('封面已设置，保存后生效');
    } else {
      insertItem(it);
    }
  }

  /* ── 事件 ── */

  $('q').addEventListener('input', function () {
    var v = this.value.trim();
    clearTimeout(state.searchTimer);
    state.searchTimer = setTimeout(function () { state.q = v; state.page = 1; load(); }, 300);
  });
  $('q').addEventListener('keydown', function (e) {
    if (e.key === 'Enter') { clearTimeout(state.searchTimer); state.q = this.value.trim(); state.page = 1; load(); }
  });
  $('from').addEventListener('change', function () { state.from = this.value; state.page = 1; load(); });
  $('to').addEventListener('change', function () { state.to = this.value; state.page = 1; load(); });
  $('sort').addEventListener('change', function () { state.sort = this.value; state.page = 1; load(); });

  // 快速时间筛选：手动日期优先；点维度时清掉手动日期，再点当前维度取消
  $('ranges').addEventListener('click', function (e) {
    var b = e.target.closest('button[data-range]');
    if (!b) return;
    var r = b.getAttribute('data-range');
    if (state.range === r && r !== 'all') r = 'all';
    state.range = r;
    state.from = ''; state.to = '';
    state.page = 1;
    $('from').value = ''; $('to').value = '';
    this.querySelectorAll('button').forEach(function (x) { x.classList.toggle('on', x.getAttribute('data-range') === r); });
    load();
  });

  // 底部分页
  $('pg-prev').onclick = function () { if (state.page > 1) { state.page--; load(); } };
  $('pg-next').onclick = function () { state.page++; load(); };

  // 视图切换：卡片流 / 分类汇总（共用同一套筛选）
  $('viewtabs').addEventListener('click', function (e) {
    var b = e.target.closest('button[data-view]');
    if (!b) return;
    var v = b.getAttribute('data-view');
    if (state.view === v) return;
    state.view = v;
    this.querySelectorAll('button').forEach(function (x) { x.classList.toggle('on', x === b); });
    $('cardview').hidden = v !== 'cards';
    $('catview').hidden = v !== 'cats';
    if (v === 'cats') loadCatStats();
  });

  // 弹窗一键放大 / 还原（浏览、编辑两个弹窗）
  function resetMax(maskId, btnId) {
    $(maskId).querySelector('.kb-dlg').classList.remove('kb-maxi');
    var b = $(btnId);
    b.textContent = '⛶';
    b.title = '放大';
  }
  function bindMax(btnId) {
    $(btnId).addEventListener('click', function () {
      var dlg = this.closest('.kb-dlg');
      var on = dlg.classList.toggle('kb-maxi');
      this.textContent = on ? '❐' : '⛶';
      this.title = on ? '还原' : '放大';
    });
  }
  bindMax('view-max');
  bindMax('edit-max');

  $('btn-clear').onclick = function () {
    state.q = ''; state.from = ''; state.to = ''; state.cat = ''; state.range = 'all'; state.sort = 'new';
    state.page = 1;
    $('q').value = ''; $('from').value = ''; $('to').value = ''; $('sort').value = 'new';
    $('ranges').querySelectorAll('button').forEach(function (x) { x.classList.toggle('on', x.getAttribute('data-range') === 'all'); });
    load();
  };

  $('btn-new').onclick = blank;
  $('btn-save').onclick = save;
  $('btn-del').onclick = function () { if (state.cur && state.cur.id) delNote(state.cur.id); };
  $('btn-export').onclick = exportAll;
  $('btn-src').onclick = function () { setSrcMode(!state.srcMode); };
  $('edit-close').onclick = function () {
    if (state.dirty && !confirm('有未保存的修改，确定关闭？')) return;
    showMask('edit-mask', false);
  };

  // 浏览弹窗：编辑按钮关掉浏览、带入编辑；删除后浏览也关
  $('view-close').onclick = function () { showMask('view-mask', false); };
  $('btn-view-edit').onclick = function () {
    showMask('view-mask', false);
    if (state.viewNote) fill(state.viewNote);
  };
  $('btn-view-del').onclick = function () {
    if (state.viewNote && state.viewNote.id) delNote(state.viewNote.id);
  };

  $('btn-cats').onclick = $('btn-cats2').onclick = function () {
    paintCatList();
    showMask('cat-mask', true);
    $('cat-name').focus();
  };
  $('cat-close').onclick = function () { showMask('cat-mask', false); };
  $('cat-add').onclick = function () { addCat($('cat-name').value).then(function () { $('cat-name').value = ''; }); };
  $('cat-name').addEventListener('keydown', function (e) {
    if (e.key === 'Enter') { addCat(this.value); this.value = ''; }
  });

  $('new-cat').addEventListener('keydown', async function (e) {
    if (e.key !== 'Enter') return;
    var name = this.value.trim();
    if (!name) return;
    this.value = '';
    try {
      await api('notes', 'cat.save', { name: name });
      state.editCat = name;
      await load();
      paintEditCats();
      markDirty();
    } catch (err) { toast(err.message, true); }
  });

  // 工具栏：mousedown 阻止默认，避免点击按钮时编辑区失焦丢选区
  $('toolbar').addEventListener('mousedown', function (e) {
    if (e.target.closest('button')) e.preventDefault();
  });
  $('toolbar').addEventListener('click', function (e) {
    var b = e.target.closest('button');
    if (!b) return;
    var act = b.getAttribute('data-act');
    if (act) {
      if (act === 'ins-pick') { openPick('insert'); return; }
      var conf = { 'ins-img': 'img', 'ins-video': 'video', 'ins-audio': 'audio', 'ins-file': 'file' }[act];
      state.pendingUpload = conf;
      var inp = $('ins-file-input');
      inp.accept = conf === 'img' ? 'image/*' : conf === 'video' ? 'video/*' : conf === 'audio' ? 'audio/*' : '';
      inp.value = '';
      inp.click();
      return;
    }
    var cmd = b.getAttribute('data-cmd');
    if (cmd === 'link') {
      var url = prompt('链接地址（http/https）', 'https://');
      if (!url) return;
      exec('createLink', url);
      return;
    }
    exec(cmd, b.getAttribute('data-val'));
  });

  // 隐藏文件输入：选图/视频/文件 → 上传进网盘 _appfiles/notes/ → 插入正文或设为封面
  $('ins-file-input').addEventListener('change', async function () {
    var f = this.files && this.files[0];
    this.value = '';
    if (!f) return;
    var mode = state.pendingUpload || 'file';
    $('hint').textContent = '上传中…';
    try {
      var j = await uploadFile(f);
      $('hint').textContent = '';
      if (mode === 'cover') {
        state.cover = j.url;
        paintCover();
        markDirty();
        toast('封面已上传，保存后生效');
      } else {
        insertItem({ name: j.name, url: j.url });
      }
    } catch (e) {
      $('hint').textContent = '';
      toast(e.message, true);
    }
  });

  // 封面
  $('btn-cover-up').onclick = function () {
    state.pendingUpload = 'cover';
    var inp = $('ins-file-input');
    inp.accept = 'image/*';
    inp.value = '';
    inp.click();
  };
  $('btn-cover-pick').onclick = function () { openPick('cover'); };
  $('btn-cover-del').onclick = function () { state.cover = ''; paintCover(); markDirty(); };

  // 网盘选择弹窗
  $('pick-close').onclick = function () { showMask('pick-mask', false); };
  $('pick-kind').addEventListener('change', paintPickList);
  $('pick-ok').onclick = confirmPick;
  $('pick-mask').addEventListener('click', function (e) {
    if (e.target === this) showMask('pick-mask', false);
  });
  $('fmt').addEventListener('change', function () {
    var v = this.value === 'p' ? 'p' : this.value;
    exec('formatBlock', v);
  });
  $('body').addEventListener('input', markDirty);
  $('body').addEventListener('keyup', syncFmt);
  $('body').addEventListener('mouseup', syncFmt);
  $('body-src').addEventListener('input', markDirty);
  $('title').addEventListener('input', markDirty);

  document.addEventListener('keydown', function (e) {
    if ((e.ctrlKey || e.metaKey) && e.key === 's' && !$('edit-mask').hidden) {
      e.preventDefault();
      save();
    }
    if (e.key === 'Escape') {
      if (!$('pick-mask').hidden) showMask('pick-mask', false);
      else if (!$('cat-mask').hidden) showMask('cat-mask', false);
      else if (!$('view-mask').hidden) showMask('view-mask', false);
      else if (!$('edit-mask').hidden && !state.dirty) showMask('edit-mask', false);
    }
  });
  $('view-mask').addEventListener('click', function (e) {
    if (e.target === this) showMask('view-mask', false);
  });
  $('edit-mask').addEventListener('click', function (e) {
    if (e.target === this && !state.dirty) showMask('edit-mask', false);
  });
  $('cat-mask').addEventListener('click', function (e) {
    if (e.target === this) showMask('cat-mask', false);
  });

  window.addEventListener('beforeunload', function (e) {
    if (state.dirty) { e.preventDefault(); e.returnValue = ''; }
  });

  load();
})();
