/* 账本：两级收支分类 + 记账 + 统计 + 导出。
   数据全在本机单库（/-api/ledger/*），金额一律按「分」传输，前端只负责元↔分换算。 */
(function () {
  'use strict';

  // 惰性取用：shell.js 在 <head> 已就绪，仍按调用时解析，避免加载顺序变化时拿到 undefined
  var api = function () { return window.hubAPI.apply(null, arguments); };
  var $ = function (id) { return document.getElementById(id); };

  var state = {
    q: '', kind: '', groupId: '', catId: '', view: 'list', range: 'all', from: '', to: '',
    entries: [], groups: [], accounts: [], peers: [], stats: {},
    sel: {}, edit: null, editKind: 'expense', receipt: '',
    catTab: 'expense', pickMode: 'peer', seq: 0, timer: null
  };

  /* ── 基础工具 ── */

  function toast(msg, err) {
    var t = $('toast');
    t.textContent = msg;
    t.className = 'hub-toast on' + (err ? ' err' : '');
    clearTimeout(t._tid);
    t._tid = setTimeout(function () { t.className = 'hub-toast'; }, 1900);
  }

  function esc(s) {
    return String(s == null ? '' : s).replace(/[&<>"]/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c];
    });
  }

  function pad(n) { return String(n).padStart(2, '0'); }
  function today() {
    var d = new Date();
    return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate());
  }

  function money(cents) {
    var s = (Math.abs(cents || 0) / 100).toFixed(2);
    var p = s.split('.');
    p[0] = p[0].replace(/\B(?=(\d{3})+(?!\d))/g, ',');
    return p.join('.');
  }

  function parseAmount(v) {
    var f = parseFloat(String(v == null ? '' : v).replace(/[^\d.]/g, ''));
    if (!isFinite(f) || f <= 0) return 0;
    return Math.round(f * 100);
  }

  function stamp(iso, day) {
    var d = new Date(iso);
    if (isNaN(d.getTime())) return day || '';
    return day + ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
  }

  function hue(name) {
    var h = 0;
    for (var i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) % 360;
    return h;
  }

  function showMask(id, on) { $(id).hidden = !on; }

  function download(name, text, mime) {
    var blob = new Blob([text], { type: mime || 'text/csv;charset=utf-8' });
    var a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = name;
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(function () { URL.revokeObjectURL(a.href); }, 2000);
  }

  /* ── 列表 ── */

  async function load() {
    var seq = ++state.seq;
    try {
      var j = await api('ledger', 'list', {
        q: state.q, kind: state.kind, groupId: state.groupId, catId: state.catId,
        range: state.range, from: state.from, to: state.to
      });
      if (seq !== state.seq) return;
      state.entries = j.entries || [];
      state.groups = j.groups || [];
      state.accounts = j.accounts || [];
      state.peers = j.peers || [];
      state.stats = j.stats || {};
      // 清掉已不存在的选中项
      var live = {};
      state.entries.forEach(function (e) { if (state.sel[e.id]) live[e.id] = true; });
      state.sel = live;
      paintStats();
      paintChips();
      paintRows();
      if (state.view === 'stats') loadStats();
    } catch (e) {
      toast(e.message, true);
    }
  }

  var RANGE_LABELS = {
    all: '全部账目', today: '今日', yesterday: '昨日', week: '本周', lastweek: '上周',
    month: '本月', lastmonth: '上月', year: '今年', lastyear: '去年'
  };

  function periodLabel() {
    if (state.from || state.to) return '自定义区间';
    return RANGE_LABELS[state.range] || '全部账目';
  }

  function paintStats() {
    var s = state.stats || {};
    $('st-assets').textContent = '¥' + money(s.assets);
    $('st-income').textContent = '+¥' + money(s.income);
    $('st-expense').textContent = '-¥' + money(s.expense);
    var label = periodLabel();
    $('st-period-in').textContent = label;
    $('st-period-out').textContent = label;
  }

  function paintChips() {
    var exp = state.groups.filter(function (g) { return g.kind === 'expense'; });
    var inc = state.groups.filter(function (g) { return g.kind === 'income'; });

    function line(box, list, kind, allLabel) {
      var html = '<button class="lg-chip' + (state.groupId === '' && state.kind === kind ? ' on' : '') +
        '" data-kind="' + kind + '" data-gid="">' + allLabel + '</button>';
      list.forEach(function (g) {
        html += '<button class="lg-chip' + (state.groupId === g.id ? ' on' : '') +
          '" data-kind="' + kind + '" data-gid="' + g.id + '">' + esc(g.name) + '</button>';
      });
      box.innerHTML = html;
    }
    line($('chips-expense'), exp, 'expense', '全部支出');
    line($('chips-income'), inc, 'income', '全部收入');

    // 二级标签：仅当选中了某个一级分类时展开它的二级
    function sub(rowId, boxId, list, gid) {
      var row = $(rowId), box = $(boxId);
      var g = list.filter(function (x) { return x.id === gid; })[0];
      if (!g || !g.cats.length) { row.hidden = true; box.innerHTML = ''; return; }
      row.hidden = false;
      var html = '<button class="lg-chip' + (state.catId === '' ? ' on' : '') + '" data-cid="">全部</button>';
      g.cats.forEach(function (c) {
        html += '<button class="lg-chip' + (state.catId === c.id ? ' on' : '') +
          '" data-cid="' + c.id + '">' + esc(c.name) + '</button>';
      });
      box.innerHTML = html;
    }
    sub('sub-expense-row', 'chips-expense-sub', exp, state.kind === 'expense' ? state.groupId : '');
    sub('sub-income-row', 'chips-income-sub', inc, state.kind === 'income' ? state.groupId : '');
  }

  function paintRows() {
    var box = $('rows');
    var items = state.entries;
    $('empty').hidden = items.length > 0;

    box.innerHTML = items.map(function (e) {
      var isIncome = e.kind === 'income';
      var catHtml;
      if (e.groupName && e.catName) {
        catHtml = '<i>' + esc(e.groupName) + '</i><em>/</em><b>' + esc(e.catName) + '</b>';
      } else if (e.groupName) {
        catHtml = '<b>' + esc(e.groupName) + '</b>';
      } else {
        catHtml = '<i class="lg-dim">未分类</i>';
      }
      var h = hue(e.groupName || '?');
      return '<div class="lg-row" data-id="' + e.id + '">' +
        '<span class="c-ck"><input type="checkbox" data-ck="' + e.id + '"' + (state.sel[e.id] ? ' checked' : '') + '></span>' +
        '<span class="c-main">' +
          '<span class="lg-ico" style="--h:' + h + '">' + esc((e.groupName || '?').slice(0, 1)) + '</span>' +
          '<span class="lg-main-txt"><b>' + (e.note ? esc(e.note) : '<i class="lg-dim">（无备注）</i>') + '</b></span>' +
        '</span>' +
        '<span class="c-cat">' + catHtml + '</span>' +
        '<span class="c-peer">' + (e.peer ? esc(e.peer) : '<i class="lg-dim">—</i>') + '</span>' +
        '<span class="c-acc">' + (e.accountName ? esc(e.accountName) : '<i class="lg-dim">—</i>') + '</span>' +
        '<span class="c-receipt">' + (e.hasReceipt ? '<button class="link-btn" data-act="receipt">有</button>' : '<i class="lg-dim">无</i>') + '</span>' +
        '<span class="c-time">' + esc(stamp(e.createdAt, e.day)) + '</span>' +
        '<span class="c-amt ' + (isIncome ? 'is-in' : 'is-out') + '">' + (isIncome ? '+' : '-') + '¥' + money(e.amount) + '</span>' +
        '<span class="c-op">' +
          '<button class="lg-op-edit" data-act="edit">修改</button>' +
          '<button class="lg-op-del" data-act="del">删除</button>' +
        '</span>' +
      '</div>';
    }).join('');

    paintFoot();
    paintSel();
  }

  function paintFoot() {
    var st = state.stats || {};
    $('foot').textContent = '已加载 ' + state.entries.length + ' 条记录' +
      (state.entries.length ? '（共 ' + (st.total || 0) + ' 条）' : '') +
      (state.groupId || state.catId || state.kind || state.q || state.from || state.to || state.range !== 'all' ? ' · 已筛选' : '');
  }

  function paintSel() {
    var ids = Object.keys(state.sel);
    var bar = $('selbar');
    bar.hidden = ids.length === 0;
    if (ids.length) $('sel-info').textContent = '已选中 ' + ids.length + ' 条';
    var all = state.entries.length > 0 && ids.length === state.entries.length;
    $('ck-all').checked = all;
  }

  /* ── 分类统计视图 ── */

  async function loadStats() {
    try {
      var j = await api('ledger', 'catstats', {
        q: state.q, kind: state.kind, groupId: state.groupId, catId: state.catId,
        range: state.range, from: state.from, to: state.to
      });
      paintStatView(j.rows || []);
    } catch (e) {
      toast(e.message, true);
    }
  }

  function paintStatView(rows) {
    var exp = rows.filter(function (r) { return r.kind === 'expense'; });
    var inc = rows.filter(function (r) { return r.kind === 'income'; });
    paintStatSec($('stat-expense'), '支出分类', exp, 'is-out', '-');
    paintStatSec($('stat-income'), '收入分类', inc, 'is-in', '+');
    $('stat-empty').hidden = rows.length > 0;
    $('stat-expense').hidden = !exp.length;
    $('stat-income').hidden = !inc.length;
  }

  function paintStatSec(box, label, rows, cls, sign) {
    var total = 0, count = 0;
    rows.forEach(function (r) { total += r.sum; count += r.count; });
    var html = '<h4 class="lg-statsec-h"><span>' + label + '</span>' +
      '<b class="' + cls + '">' + sign + '¥' + money(total) + '</b>' +
      '<span class="lg-dim">' + count + ' 笔</span></h4>';
    // 一级分组（后端已按金额降序，同组保持首次出现顺序）
    var order = [], map = {};
    rows.forEach(function (r) {
      var key = r.groupName || '未分类';
      if (!map[key]) { map[key] = { name: key, sum: 0, count: 0, cats: [] }; order.push(key); }
      map[key].sum += r.sum;
      map[key].count += r.count;
      map[key].cats.push(r);
    });
    html += order.map(function (key) {
      var g = map[key];
      var inner = g.cats.map(function (r) {
        var pct = total > 0 ? Math.max(1, Math.round(r.sum * 100 / total)) : 0;
        return '<div class="lg-stat-row ' + cls + '"><span class="nm">' + esc(r.catName || '未细分') + '</span>' +
          '<span class="bar"><i style="width:' + pct + '%"></i></span>' +
          '<span class="cnt">' + r.count + ' 笔</span>' +
          '<span class="amt ' + cls + '">' + sign + '¥' + money(r.sum) + '</span></div>';
      }).join('');
      return '<div class="lg-stat-group"><div class="lg-stat-gname"><span>' + esc(key) + '</span>' +
        '<b>' + sign + '¥' + money(g.sum) + '</b><span class="lg-dim">' + g.count + ' 笔</span></div>' + inner + '</div>';
    }).join('');
    box.innerHTML = html;
  }

  /* ── 记账弹窗 ── */

  function kinds(kind) {
    return state.groups.filter(function (g) { return g.kind === kind; });
  }

  function fillGroups(keepGid, keepCid) {
    var list = kinds(state.editKind);
    var gsel = $('group');
    gsel.innerHTML = list.map(function (g) {
      return '<option value="' + g.id + '">' + esc(g.name) + '</option>';
    }).join('');
    if (keepGid && list.some(function (g) { return g.id === keepGid; })) gsel.value = keepGid;
    fillCats(keepCid);
  }

  function fillCats(keepCid) {
    var g = kinds(state.editKind).filter(function (x) { return x.id === $('group').value; })[0];
    var csel = $('cat');
    var cats = g ? g.cats : [];
    csel.innerHTML = '<option value="">（不选二级分类）</option>' + cats.map(function (c) {
      return '<option value="' + c.id + '">' + esc(c.name) + '</option>';
    }).join('');
    if (keepCid && cats.some(function (c) { return c.id === keepCid; })) csel.value = keepCid;
  }

  function fillAccounts(keepID) {
    var sel = $('account');
    sel.innerHTML = state.accounts.length
      ? state.accounts.map(function (a) {
        return '<option value="' + a.id + '">' + esc(a.name) + '</option>';
      }).join('')
      : '<option value="">（无账户，去「管理」添加）</option>';
    if (keepID && state.accounts.some(function (a) { return a.id === keepID; })) sel.value = keepID;
  }

  function fillPeers() {
    $('peer-chips').innerHTML = state.peers.map(function (n) {
      return '<button type="button" class="lg-chip" data-peer="' + esc(n) + '">' + esc(n) + '</button>';
    }).join('');
  }

  // 分类/账户/对象在别的弹窗里改过之后，记账弹窗的下拉要跟着刷新（尽量保留原值）
  function refreshEntrySelects() {
    if ($('entry-mask').hidden) return;
    fillGroups($('group').value, $('cat').value);
    fillAccounts($('account').value);
    fillPeers();
  }

  function paintReceipt() {
    var has = !!state.receipt;
    $('receipt-prev').hidden = !has;
    $('btn-receipt').classList.toggle('on', has);
    if (has) $('receipt-img').src = state.receipt;
  }

  function setEditKind(kind) {
    state.editKind = kind;
    $('k-expense').classList.toggle('on', kind === 'expense');
    $('k-income').classList.toggle('on', kind === 'income');
    fillGroups('', '');
  }

  function openEntry(e) {
    state.edit = e || null;
    state.receipt = (e && e.receipt) || '';
    $('entry-head').textContent = e ? '修改记账' : '新建记账';
    $('entry-del').hidden = !e;
    $('amount').value = e ? (e.amount / 100).toFixed(2) : '';
    $('peer').value = e ? (e.peer || '') : '';
    $('note').value = e ? (e.note || '') : '';
    $('day').value = e ? e.day : today();
    setEditKind(e ? e.kind : 'expense');
    fillGroups(e ? e.groupId : '', e ? e.catId : '');
    fillAccounts(e ? e.accountId : '');
    fillPeers();
    paintReceipt();
    showMask('entry-mask', true);
    setTimeout(function () { $('amount').focus(); }, 30);
  }

  async function saveEntry() {
    var amount = parseAmount($('amount').value);
    if (!amount) { toast('请填写记账金额', true); $('amount').focus(); return; }
    var gid = $('group').value;
    if (!gid) { toast('请先在「管理分类」里建一个一级分类', true); return; }
    var body = {
      kind: state.editKind,
      amount: amount,
      groupId: gid,
      catId: $('cat').value,
      accountId: $('account').value,
      peer: $('peer').value.trim(),
      note: $('note').value.trim(),
      day: $('day').value || today(),
      receipt: state.receipt
    };
    if (state.edit) body.id = state.edit.id;
    try {
      await api('ledger', 'save', body);
      showMask('entry-mask', false);
      toast(state.edit ? '已修改' : '已记账');
      await load();
    } catch (e) {
      toast(e.message, true);
    }
  }

  async function delEntry(id, quiet) {
    if (!quiet && !confirm('删除这条记录？删除后不可恢复。')) return;
    try {
      await api('ledger', 'delete', { id: id });
      delete state.sel[id];
      toast('已删除');
      await load();
    } catch (e) {
      toast(e.message, true);
    }
  }

  /* ── 分类管理 ── */

  function paintCatBody() {
    var kind = state.catTab;
    $('cat-tabs').querySelectorAll('button').forEach(function (b) {
      b.classList.toggle('on', b.getAttribute('data-kind') === kind);
    });
    var list = kinds(kind);
    if (!list.length) {
      $('cat-body').innerHTML = '<p class="muted" style="padding:1rem">还没有一级分类，点底部「+ 新增一级分类」建一个。</p>';
      return;
    }
    $('cat-body').innerHTML = list.map(function (g) {
      var chips = g.cats.map(function (c) {
        if (c.sys) return '<button type="button" class="lg-cat-chip sys" data-sys="1">' + esc(c.name) + '</button>';
        return '<span class="lg-cat-wrap">' +
          '<button type="button" class="lg-cat-chip" data-cid="' + c.id + '" data-cname="' + esc(c.name) + '">' + esc(c.name) + '</button>' +
          '<button type="button" class="lg-cat-x" data-cid="' + c.id + '" title="删除">×</button></span>';
      }).join('');
      return '<div class="lg-cat-group" data-gid="' + g.id + '">' +
        '<div class="lg-cat-gname">' +
          '<span>' + esc(g.name) + (g.sys ? ' <i class="lg-dim">系统</i>' : '') + '</span>' +
          (g.sys ? '' :
            '<button type="button" class="lg-g-edit" data-gid="' + g.id + '" data-gname="' + esc(g.name) + '">改名</button>' +
            '<button type="button" class="lg-g-del" data-gid="' + g.id + '">删除</button>') +
        '</div>' +
        '<div class="lg-cat-chips">' + chips +
          '<button type="button" class="lg-cat-add" data-gid="' + g.id + '">+ 添加分类</button>' +
        '</div>' +
      '</div>';
    }).join('');
  }

  function openCat() {
    paintCatBody();
    showMask('cat-mask', true);
  }

  async function addGroup() {
    var name = prompt('新的一级分类名称（如「宠物」「投资」）');
    if (!name || !name.trim()) return;
    try {
      await api('ledger', 'group.save', { kind: state.catTab, name: name.trim() });
      toast('已新增一级分类');
      await load();
      paintCatBody();
    } catch (e) {
      toast(e.message, true);
    }
  }

  /* ── 对象 / 账户管理 ── */

  function paintPick() {
    var rows = state.pickMode === 'peer'
      ? state.peers.map(function (n) { return { id: n, name: n }; })
      : state.accounts;
    if (!rows.length) {
      $('pick-list').innerHTML = '<p class="muted" style="padding:.6rem 0">还没有，先在上方输入框添加一个。</p>';
      return;
    }
    $('pick-list').innerHTML = rows.map(function (it) {
      return '<div class="lg-pick-row" data-id="' + esc(it.id) + '" data-name="' + esc(it.name) + '">' +
        '<input value="' + esc(it.name) + '">' +
        '<button type="button" data-act="save">保存</button>' +
        '<button type="button" data-act="del" class="danger">删除</button>' +
      '</div>';
    }).join('');
  }

  function openPick(mode) {
    state.pickMode = mode;
    $('pick-head').textContent = mode === 'peer' ? '管理对象' : '管理结算账户';
    $('pick-name').value = '';
    paintPick();
    showMask('pick-mask', true);
  }

  /* ── 详情 ── */

  async function openDetail(id) {
    try {
      var j = await api('ledger', 'get', { id: id });
      var e = j.entry;
      var line = function (k, v) {
        return '<div class="lg-detail-row"><span>' + k + '</span><b>' + (v || '—') + '</b></div>';
      };
      var html = '<div class="lg-detail-amt ' + (e.kind === 'income' ? 'is-in' : 'is-out') + '">' +
        (e.kind === 'income' ? '+' : '-') + '¥' + money(e.amount) + '</div>';
      html += line('收支类型', e.kind === 'income' ? '收入' : '支出');
      html += line('分类', esc(e.groupName) + (e.catName ? ' / ' + esc(e.catName) : ''));
      html += line('收支对象', esc(e.peer));
      html += line('结算账户', esc(e.accountName));
      html += line('记账时间', esc(stamp(e.createdAt, e.day)));
      html += line('备注', esc(e.note));
      html += '<div class="lg-detail-row col"><span>凭证</span>' +
        (e.receipt ? '<img class="lg-detail-img" src="' + e.receipt + '" alt="凭证">' : '<b>无凭证</b>') + '</div>';
      $('detail-body').innerHTML = html;
      showMask('detail-mask', true);
    } catch (err) {
      toast(err.message, true);
    }
  }

  /* ── 事件绑定 ── */

  function bind() {
    // 搜索
    $('q').addEventListener('input', function () {
      state.q = this.value.trim();
      clearTimeout(state.timer);
      state.timer = setTimeout(load, 300);
    });
    $('q').addEventListener('keydown', function (ev) {
      if (ev.key === 'Enter') { clearTimeout(state.timer); load(); }
    });
    ['from', 'to'].forEach(function (id) {
      $(id).addEventListener('change', function () { state[id] = this.value; load(); });
    });
    $('btn-clear').addEventListener('click', function () {
      state.q = ''; state.from = ''; state.to = '';
      $('q').value = ''; $('from').value = ''; $('to').value = '';
      load();
    });

    // 分类标签（两级：一级 chip 选中后展开二级；点已选中的再点一次 = 取消）
    function chipClick(ev) {
      var b = ev.target.closest('.lg-chip');
      if (!b) return;
      var cid = b.getAttribute('data-cid');
      if (cid !== null) { // 二级分类 chip（含「全部」data-cid=""）
        if (state.catId === cid) return;
        state.catId = cid;
        paintChips();
        load();
        return;
      }
      var gid = b.getAttribute('data-gid') || '';
      var kind = b.getAttribute('data-kind');
      if (state.groupId === gid && state.kind === kind) {
        state.groupId = ''; state.kind = ''; state.catId = '';
      } else {
        state.groupId = gid; state.kind = kind; state.catId = '';
      }
      paintChips();
      load();
    }
    $('chips-expense').addEventListener('click', chipClick);
    $('chips-income').addEventListener('click', chipClick);
    $('chips-expense-sub').addEventListener('click', chipClick);
    $('chips-income-sub').addEventListener('click', chipClick);

    // 视图切换：明细 / 分类统计
    $('views').addEventListener('click', function (ev) {
      var b = ev.target.closest('button[data-view]');
      if (!b) return;
      var v = b.getAttribute('data-view');
      if (state.view === v) return;
      state.view = v;
      this.querySelectorAll('button').forEach(function (x) { x.classList.toggle('on', x === b); });
      $('listview').hidden = v !== 'list';
      $('statview').hidden = v !== 'stats';
      if (v === 'stats') loadStats();
    });

    // 时间范围
    $('ranges').addEventListener('click', function (ev) {
      var b = ev.target.closest('button[data-range]');
      if (!b) return;
      state.range = b.getAttribute('data-range');
      state.from = ''; state.to = '';
      $('from').value = ''; $('to').value = '';
      this.querySelectorAll('button').forEach(function (x) { x.classList.toggle('on', x === b); });
      load();
    });

    // 顶部按钮
    $('btn-new').addEventListener('click', function () { openEntry(null); });
    $('btn-cats').addEventListener('click', openCat);
    $('btn-export').addEventListener('click', async function () {
      try {
        var j = await api('ledger', 'export', {});
        if (!j.count) { toast('还没有可导出的记录'); return; }
        download('账本导出-' + today() + '.csv', j.csv);
        toast('已导出 ' + j.count + ' 条');
      } catch (e) { toast(e.message, true); }
    });

    // 表格：行内操作 / 勾选
    $('rows').addEventListener('click', function (ev) {
      var row = ev.target.closest('.lg-row');
      if (!row) return;
      var id = row.getAttribute('data-id');
      var act = ev.target.getAttribute('data-act');
      if (ev.target.hasAttribute('data-ck')) {
        if (ev.target.checked) state.sel[id] = true; else delete state.sel[id];
        paintSel();
        return;
      }
      if (act === 'del') { delEntry(id); return; }
      if (act === 'edit') {
        api('ledger', 'get', { id: id }).then(function (j) { openEntry(j.entry); })
          .catch(function (e) { toast(e.message, true); });
        return;
      }
      if (act === 'receipt') { openDetail(id); return; }
      openDetail(id);
    });

    $('ck-all').addEventListener('change', function () {
      var on = this.checked;
      state.sel = {};
      if (on) state.entries.forEach(function (e) { state.sel[e.id] = true; });
      paintRows();
    });
    $('btn-sel-none').addEventListener('click', function () { state.sel = {}; paintRows(); });
    $('btn-del-sel').addEventListener('click', async function () {
      var ids = Object.keys(state.sel);
      if (!ids.length) return;
      if (!confirm('删除选中的 ' + ids.length + ' 条记录？删除后不可恢复。')) return;
      try {
        var j = await api('ledger', 'bulk.delete', { ids: ids });
        state.sel = {};
        toast('已删除 ' + (j.deleted || 0) + ' 条');
        await load();
      } catch (e) { toast(e.message, true); }
    });

    // 记账弹窗
    $('entry-close').addEventListener('click', function () { showMask('entry-mask', false); });
    $('entry-save').addEventListener('click', saveEntry);
    $('entry-del').addEventListener('click', function () {
      if (!state.edit) return;
      delEntry(state.edit.id).then(function () { showMask('entry-mask', false); });
    });
    $('k-expense').addEventListener('click', function () { setEditKind('expense'); });
    $('k-income').addEventListener('click', function () { setEditKind('income'); });
    $('group').addEventListener('change', function () { fillCats(''); });
    $('entry-manage-cat').addEventListener('click', function () {
      state.catTab = state.editKind;
      openCat();
    });
    $('entry-manage-acc').addEventListener('click', function () { openPick('account'); });
    $('entry-manage-peer').addEventListener('click', function () { openPick('peer'); });
    $('peer-chips').addEventListener('click', function (ev) {
      var b = ev.target.closest('[data-peer]');
      if (b) $('peer').value = b.getAttribute('data-peer');
    });

    // 凭证：选图 → canvas 压到最长边 1280 → dataURL 入库
    $('btn-receipt').addEventListener('click', function () { $('receipt-file').click(); });
    $('receipt-file').addEventListener('change', function () {
      var f = this.files && this.files[0];
      this.value = '';
      if (!f) return;
      if (!/^image\//.test(f.type)) { toast('请选择图片文件', true); return; }
      shrink(f, function (data) {
        if (!data) { toast('图片读取失败', true); return; }
        state.receipt = data;
        paintReceipt();
      });
    });
    $('receipt-del').addEventListener('click', function () {
      state.receipt = '';
      paintReceipt();
    });

    // 分类管理弹窗
    $('cat-close').addEventListener('click', function () {
      showMask('cat-mask', false);
      refreshEntrySelects();
    });
    $('cat-tabs').addEventListener('click', function (ev) {
      var b = ev.target.closest('button[data-kind]');
      if (!b) return;
      state.catTab = b.getAttribute('data-kind');
      paintCatBody();
    });
    $('cat-add-group').addEventListener('click', addGroup);
    $('cat-body').addEventListener('click', async function (ev) {
      var t = ev.target;
      try {
        if (t.classList.contains('lg-cat-add')) {
          var gid = t.getAttribute('data-gid');
          var name = prompt('新的二级分类名称');
          if (!name || !name.trim()) return;
          await api('ledger', 'cat.save', { groupId: gid, name: name.trim() });
          toast('已添加分类');
        } else if (t.classList.contains('lg-cat-chip')) {
          if (t.getAttribute('data-sys') === '1') { toast('系统分类不可修改（可另建自己的）'); return; }
          var cid = t.getAttribute('data-cid');
          var cname = t.getAttribute('data-cname');
          var nn = prompt('修改分类名称', cname);
          if (!nn || !nn.trim() || nn.trim() === cname) return;
          await api('ledger', 'cat.save', { id: cid, groupId: t.closest('.lg-cat-group').getAttribute('data-gid'), name: nn.trim() });
          toast('已改名');
        } else if (t.classList.contains('lg-cat-x')) {
          if (!confirm('删除这个二级分类？历史账目不受影响。')) return;
          await api('ledger', 'cat.delete', { id: t.getAttribute('data-cid') });
          toast('已删除分类');
        } else if (t.classList.contains('lg-g-edit')) {
          var gname = t.getAttribute('data-gname');
          var ng = prompt('修改一级分类名称', gname);
          if (!ng || !ng.trim() || ng.trim() === gname) return;
          await api('ledger', 'group.save', { id: t.getAttribute('data-gid'), name: ng.trim() });
          toast('已改名');
        } else if (t.classList.contains('lg-g-del')) {
          if (!confirm('删除这个一级分类（含其下二级分类）？历史账目不受影响。')) return;
          await api('ledger', 'group.delete', { id: t.getAttribute('data-gid') });
          toast('已删除一级分类');
        } else {
          return;
        }
        await load();
        paintCatBody();
        refreshEntrySelects();
      } catch (e) {
        toast(e.message, true);
      }
    });

    // 对象 / 账户弹窗
    $('pick-close').addEventListener('click', function () {
      showMask('pick-mask', false);
      refreshEntrySelects();
    });
    $('pick-add').addEventListener('click', async function () {
      var name = $('pick-name').value.trim();
      if (!name) return;
      try {
        await api('ledger', state.pickMode === 'peer' ? 'peer.save' : 'account.save', { name: name });
        $('pick-name').value = '';
        await load();
        paintPick();
        toast('已添加');
      } catch (e) { toast(e.message, true); }
    });
    $('pick-name').addEventListener('keydown', function (ev) {
      if (ev.key === 'Enter') $('pick-add').click();
    });
    $('pick-list').addEventListener('click', async function (ev) {
      var t = ev.target;
      var row = t.closest('.lg-pick-row');
      if (!row) return;
      var id = row.getAttribute('data-id');
      var old = row.getAttribute('data-name');
      var input = row.querySelector('input');
      try {
        if (t.getAttribute('data-act') === 'save') {
          var name = input.value.trim();
          if (!name) { toast('名称不能为空', true); return; }
          if (state.pickMode === 'peer') {
            if (name !== old) await api('ledger', 'peer.save', { old: old, name: name });
          } else {
            await api('ledger', 'account.save', { id: id, name: name });
          }
          toast('已保存');
        } else if (t.getAttribute('data-act') === 'del') {
          if (!confirm('删除「' + old + '」？历史账目里的名称会保留。')) return;
          if (state.pickMode === 'peer') await api('ledger', 'peer.delete', { name: old });
          else await api('ledger', 'account.delete', { id: id });
          toast('已删除');
        } else {
          return;
        }
        await load();
        paintPick();
        refreshEntrySelects();
      } catch (e) { toast(e.message, true); }
    });

    // 详情
    $('detail-close').addEventListener('click', function () { showMask('detail-mask', false); });

    // 点遮罩关闭 / Esc 关闭
    document.querySelectorAll('.mask').forEach(function (m) {
      m.addEventListener('click', function (ev) { if (ev.target === m) m.hidden = true; });
    });
    document.addEventListener('keydown', function (ev) {
      if (ev.key === 'Escape') {
        document.querySelectorAll('.mask').forEach(function (m) { m.hidden = true; });
      }
    });
  }

  /* ── 图片压缩 ── */

  function shrink(file, cb) {
    var img = new Image();
    var url = URL.createObjectURL(file);
    img.onload = function () {
      var max = 1280;
      var w = img.width, h = img.height;
      if (w > max || h > max) {
        var k = Math.min(max / w, max / h);
        w = Math.round(w * k);
        h = Math.round(h * k);
      }
      var cv = document.createElement('canvas');
      cv.width = w;
      cv.height = h;
      cv.getContext('2d').drawImage(img, 0, 0, w, h);
      URL.revokeObjectURL(url);
      var out = cv.toDataURL('image/jpeg', 0.82);
      if (out.length > 2.4e6) out = cv.toDataURL('image/jpeg', 0.55);
      cb(out);
    };
    img.onerror = function () {
      URL.revokeObjectURL(url);
      cb('');
    };
    img.src = url;
  }

  /* ── 启动 ── */

  bind();
  load();
})();
