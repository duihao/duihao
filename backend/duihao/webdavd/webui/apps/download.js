/* 离线下载：任务队列 / 进度 / 限速 / 组件按需安装。
   进度靠 2 秒轮询 list（后端从 aria2 RPC 取，前端不直连引擎）。 */
(function () {
  'use strict';

  var APP = 'download';
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
    }, 3200);
  }
  function fmtSize(n) {
    if (!n) return '0';
    if (n >= 1073741824) return (n / 1073741824).toFixed(2) + ' GB';
    if (n >= 1048576) return (n / 1048576).toFixed(1) + ' MB';
    if (n >= 1024) return Math.round(n / 1024) + ' KB';
    return n + ' B';
  }
  function fmtSpeed(n) { return n ? fmtSize(n) + '/s' : '—'; }
  function fmtEta(t) {
    if (t.status !== 'active' || !t.speed || t.total <= t.done) return '';
    var s = Math.round((t.total - t.done) / t.speed);
    if (s < 60) return '剩 ' + s + ' 秒';
    if (s < 3600) return '剩 ' + Math.round(s / 60) + ' 分钟';
    return '剩 ' + (s / 3600).toFixed(1) + ' 小时';
  }

  var STATUS = {
    waiting: { text: '排队中', cls: 'wait' },
    active: { text: '下载中', cls: 'run' },
    paused: { text: '已暂停', cls: 'pause' },
    done: { text: '已完成', cls: 'done' },
    error: { text: '出错', cls: 'bad' }
  };
  var DIRNAME = { download: '下载目录', video: '影视库', music: '音乐库' };

  var state = {
    items: [], setting: null, comp: null, disk: null, engine: false,
    timer: null, dirPinned: false, armed: {}
  };

  // ── 数据 ──
  function refresh() {
    return api('list').then(function (res) {
      state.items = res.items || [];
      state.setting = res.setting || {};
      state.comp = res.comp || {};
      state.disk = res.disk || {};
      state.engine = !!res.engine;
      paintAll();
      schedule();
    }).catch(function (e) {
      paintOffline(e.message || String(e));
      schedule();
    });
  }

  function schedule() {
    if (state.timer) clearTimeout(state.timer);
    var busy = state.items.some(function (t) { return t.status === 'active' || t.status === 'waiting'; });
    state.timer = setTimeout(refresh, busy ? 1500 : 5000);
  }

  function paintOffline(msg) {
    $('d-stats').textContent = '连不上后台：' + msg;
  }

  // ── 渲染 ──
  function paintAll() {
    var comp = state.comp || {};
    var ready = comp.state === 'ready';
    $('d-setup').hidden = ready;
    if (!ready) {
      $('d-comp-hint').textContent = comp.state === 'downloading'
        ? '正在下载 ' + (comp.name || '') + '…'
        : (comp.state === 'error' ? '上次失败：' + (comp.error || '') : '');
      $('d-install').disabled = comp.state === 'downloading';
    }

    // 磁盘剩余
    var free = state.disk && state.disk.free ? state.disk.free : 0;
    $('d-free').textContent = free ? '本卷剩余 ' + fmtSize(free) : '';

    paintStats();
    paintList();

    // 首次拿到设置时把默认落点回填（之后用户手动选过就不再覆盖）
    if (state.setting && !state.dirPinned && state.setting.autoDir) {
      $('d-dir').value = state.setting.autoDir;
    }
    $('d-engine').disabled = !state.engine;
  }

  function paintStats() {
    var active = 0, speed = 0, wait = 0, done = 0, pause = 0, bad = 0;
    state.items.forEach(function (t) {
      if (t.status === 'active') { active++; speed += t.speed || 0; }
      else if (t.status === 'waiting') wait++;
      else if (t.status === 'done') done++;
      else if (t.status === 'paused') pause++;
      else if (t.status === 'error') bad++;
    });
    var bits = [];
    if (active) bits.push('<b>' + fmtSpeed(speed) + '</b>');
    if (active) bits.push(active + ' 个在下');
    if (wait) bits.push(wait + ' 个排队');
    if (pause) bits.push(pause + ' 个暂停');
    if (bad) bits.push('<span class="dl-badn">' + bad + ' 个出错</span>');
    if (done) bits.push(done + ' 个已完成');
    var st = state.setting || {};
    if (st.downLimit > 0) bits.push('限速 ' + st.downLimit + ' KB/s');
    if (!bits.length) bits.push('队列空闲');
    $('d-stats').innerHTML = bits.join('<i>·</i>');
  }

  function paintList() {
    var box = $('d-list');
    box.innerHTML = '';
    $('d-empty').hidden = state.items.length > 0;

    state.items.forEach(function (t) {
      var meta = STATUS[t.status] || { text: t.status, cls: 'wait' };
      var pct = t.total > 0 ? Math.min(100, Math.round(t.done / t.total * 100)) : 0;
      if (t.status === 'done') pct = 100;

      var row = el('div', 'dl-task dl-' + meta.cls);
      row.setAttribute('data-id', t.id);

      var main = el('div', 'dl-t-main');
      var head = el('div', 'dl-t-head');
      head.innerHTML =
        '<span class="dl-name" title="' + esc(t.name || t.url) + '">' + esc(t.name || t.url) + '</span>' +
        '<span class="dl-badge">' + (t.kind === 'magnet' ? '磁力' : '直链') + '</span>' +
        '<span class="dl-dir">' + (DIRNAME[t.dir] || '下载目录') + '</span>';
      main.appendChild(head);

      var sub = [];
      if (t.status === 'error') {
        sub.push('<em class="dl-err">' + esc(t.err || '下载失败') + '</em>');
      } else if (t.status === 'done') {
        sub.push(fmtSize(t.total || t.done));
        if (t.finishedAt) sub.push('完成于 ' + esc(t.finishedAt.slice(5, 16)));
      } else {
        sub.push(fmtSize(t.done) + ' / ' + (t.total ? fmtSize(t.total) : '未知'));
        if (t.status === 'active') sub.push(fmtSpeed(t.speed));
        if (t.seeders) sub.push(t.seeders + ' 个做种');
        var eta = fmtEta(t);
        if (eta) sub.push(eta);
      }
      var subEl = el('div', 'dl-t-sub');
      subEl.innerHTML = sub.join('<i>·</i>');
      main.appendChild(subEl);

      var line = el('div', 'dl-barline');
      var bar = el('div', 'dl-bar');
      var fill = el('i');
      fill.style.width = pct + '%';
      bar.appendChild(fill);
      line.appendChild(bar);
      line.appendChild(el('span', 'dl-pct', pct + '%'));
      main.appendChild(line);
      row.appendChild(main);

      var acts = el('div', 'dl-t-acts');
      var stTag = el('span', 'dl-state dl-' + meta.cls, meta.text);
      acts.appendChild(stTag);

      if (t.status === 'active' || t.status === 'waiting') {
        acts.appendChild(btn('pause', '暂停'));
      } else if (t.status === 'paused' || t.status === 'error') {
        acts.appendChild(btn('resume', t.status === 'error' ? '重试' : '继续'));
      }
      if (t.status !== 'active') {
        acts.appendChild(btn('remove', state.armed[t.id] ? '确认删除' : '删除', 'dl-danger'));
      }
      row.appendChild(acts);
      box.appendChild(row);
    });
  }

  function btn(act, text, cls) {
    var b = el('button', 'dl-mini' + (cls ? ' ' + cls : ''), text);
    b.setAttribute('data-act', act);
    return b;
  }

  // ── 交互 ──
  function addTasks() {
    var raw = $('d-urls').value;
    var urls = raw.split(/[\r\n]+/).map(function (s) { return s.trim(); }).filter(Boolean);
    if (!urls.length) {
      toast('先粘一个链接进来', true);
      $('d-urls').focus();
      return;
    }
    var btnAdd = $('d-add');
    btnAdd.disabled = true;
    api('add', { urls: urls, dir: $('d-dir').value }).then(function (res) {
      btnAdd.disabled = false;
      $('d-urls').value = '';
      var msg = '已加入 ' + res.n + ' 个任务';
      if (res.skipped) msg += '；' + res.skipped;
      toast(msg);
      if (res.warn) toast(res.warn, true);
      refresh();
    }).catch(function (e) {
      btnAdd.disabled = false;
      toast(e.message || String(e), true);
    });
  }

  function onListClick(e) {
    var b = e.target.closest('button[data-act]');
    if (!b) return;
    var row = b.closest('.dl-task');
    if (!row) return;
    var id = row.getAttribute('data-id');
    var act = b.getAttribute('data-act');

    if (act === 'remove') {
      // 两次点击确认：比 confirm() 稳（WebView 里 confirm 会被拦，
      // 而且删掉的若是没下完的 20GB，误触代价太大）
      if (!state.armed[id]) {
        state.armed[id] = true;
        b.textContent = '确认删除';
        setTimeout(function () {
          delete state.armed[id];
          if (document.body.contains(b)) b.textContent = '删除';
        }, 4000);
        return;
      }
      delete state.armed[id];
      api('remove', { id: id }).then(function () { toast('已删除任务'); refresh(); })
        .catch(function (err) { toast(err.message || String(err), true); });
      return;
    }

    api(act, { id: id }).then(function () { refresh(); })
      .catch(function (err) { toast(err.message || String(err), true); });
  }

  function installComp() {
    $('d-install').disabled = true;
    api('comp.install', { id: 'aria2' }).then(function () {
      toast('开始下载组件，稍等…');
      pollComp();
    }).catch(function (e) {
      $('d-install').disabled = false;
      toast(e.message || String(e), true);
    });
  }

  function pollComp() {
    api('comp.status').then(function (s) {
      state.comp = s;
      paintAll();
      if (s.state === 'downloading') {
        setTimeout(pollComp, 1200);
      } else if (s.state === 'ready') {
        toast('组件就绪，可以开始下载了');
        refresh();
      } else if (s.state === 'error') {
        toast('组件安装失败：' + (s.error || ''), true);
      }
    }).catch(function () { setTimeout(pollComp, 3000); });
  }

  function importComp() {
    var p = window.prompt('填本机上 aria2 zip 的完整路径（例如 D:\\aria2-1.37.0-win-64bit-build1.zip）');
    if (!p) return;
    api('comp.import', { id: 'aria2', zip: p.trim() }).then(function (res) {
      toast('已导入：' + (res.dir || ''));
      refresh();
    }).catch(function (e) { toast(e.message || String(e), true); });
  }

  function openSetting() {
    var s = state.setting || {};
    $('d-maxconc').value = s.maxConc || 3;
    $('d-down').value = s.downLimit || 0;
    $('d-up').value = s.upLimit || 0;
    $('d-autodir').value = s.autoDir || 'download';
    $('d-btnet').checked = s.btNet !== false;
    $('d-seed').checked = !!s.seed;
    $('d-set-hint').textContent = state.engine ? '引擎在跑，保存后立即生效' : '';
    $('d-mask').hidden = false;
  }

  function saveSetting() {
    var body = {
      maxConc: parseInt($('d-maxconc').value, 10) || 3,
      downLimit: parseInt($('d-down').value, 10) || 0,
      upLimit: parseInt($('d-up').value, 10) || 0,
      btNet: $('d-btnet').checked,
      seed: $('d-seed').checked,
      autoDir: $('d-autodir').value
    };
    api('setting.save', body).then(function (res) {
      state.setting = res.setting;
      $('d-mask').hidden = true;
      toast('设置已保存');
      refresh();
    }).catch(function (e) { toast(e.message || String(e), true); });
  }

  function stopEngine() {
    api('engine.stop').then(function () {
      toast('引擎已停止，任务记录保留');
      refresh();
    }).catch(function (e) { toast(e.message || String(e), true); });
  }

  function bind() {
    $('d-add').addEventListener('click', addTasks);
    $('d-urls').addEventListener('keydown', function (e) {
      // Ctrl+Enter 提交（普通 Enter 用来换行）
      if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) { e.preventDefault(); addTasks(); }
    });
    $('d-dir').addEventListener('change', function () { state.dirPinned = true; });
    $('d-list').addEventListener('click', onListClick);
    $('d-install').addEventListener('click', installComp);
    $('d-import').addEventListener('click', importComp);
    $('d-set').addEventListener('click', openSetting);
    $('d-engine').addEventListener('click', stopEngine);
    $('d-x').addEventListener('click', function () { $('d-mask').hidden = true; });
    $('d-mask').addEventListener('click', function (e) {
      if (e.target === $('d-mask')) $('d-mask').hidden = true;
    });
    $('d-save').addEventListener('click', saveSetting);
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && !$('d-mask').hidden) $('d-mask').hidden = true;
    });
  }

  bind();
  refresh();
})();
