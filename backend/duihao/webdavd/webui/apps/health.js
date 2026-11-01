/* 健康：报告与指标（H1）、趋势（H2）、日常/用药/就诊（H3）、附件/导出/隐私（H4）。
   数据来自 /-api/health/*；高低只按每条指标自带的参考范围判，不做医学解读。 */
(function () {
  'use strict';

  var $ = function (id) { return document.getElementById(id); };
  var esc = function (s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  };
  var num = function (v) {
    if (v == null) return '';
    var n = Number(v);
    if (!isFinite(n)) return String(v);
    return String(Math.round(n * 100) / 100);
  };
  // 隐私模式：数值打码（临时显示期间照常）
  function vnum(v) {
    if (state.privacy && !state.revealed) return '••';
    return num(v);
  }
  function today() {
    var d = new Date();
    var p = function (n) { return (n < 10 ? '0' : '') + n; };
    return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate());
  }
  function nowLocal() {
    var d = new Date();
    var p = function (n) { return (n < 10 ? '0' : '') + n; };
    return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) + 'T' +
      p(d.getHours()) + ':' + p(d.getMinutes());
  }

  var state = {
    members: [], memberId: '', sec: 'overview',
    q: '', page: 1, pages: 1, items: [],
    cur: null,      // 当前详情
    editId: '',     // 正在编辑的报告 id（空 = 新建）
    memEditId: '',  // 正在编辑的成员 id
    series: [], seriesKey: '',
    vitals: [], vkinds: [], vkind: 'weight', vlatest: {},
    meds: [], medEditId: '', visits: [], visitEditId: '',
    privacy: false, revealed: false, revealTimer: null
  };

  function api(action, body) {
    return window.hubAPI('health', action, body || {});
  }

  function toast(msg, bad) {
    var t = document.createElement('div');
    t.className = 'vtoast' + (bad ? ' bad' : '');
    t.textContent = msg;
    document.body.appendChild(t);
    setTimeout(function () { t.classList.add('show'); }, 10);
    setTimeout(function () {
      t.classList.remove('show');
      setTimeout(function () { t.remove(); }, 300);
    }, 2400);
  }

  var KIND = { checkup: '体检', lab: '化验', imaging: '影像', other: '其他' };
  var SRC = { report: '报告', vital: '自测' };

  // ── 顶栏 / 成员 ──

  function loadMembers() {
    return api('member.list').then(function (j) {
      state.members = j.members || [];
      if (!state.memberId && state.members.length) state.memberId = state.members[0].id;
      if (state.memberId && !state.members.some(function (m) { return m.id === state.memberId; })) {
        state.memberId = state.members.length ? state.members[0].id : '';
      }
      paintMembers();
    });
  }

  function paintMembers() {
    var box = $('h-members');
    box.innerHTML = '';
    state.members.forEach(function (m) {
      var b = document.createElement('button');
      b.className = 'hh-chip' + (m.id === state.memberId ? ' on' : '');
      b.innerHTML = '<span class="hh-chip-n">' + esc(m.name) + '</span>' +
        (m.relation && m.relation !== m.name ? '<i>' + esc(m.relation) + '</i>' : '');
      b.title = m.id === state.memberId ? '再点一次编辑这位成员' : '切换到 ' + m.name;
      b.onclick = function () {
        if (state.memberId === m.id) { openMember(m); return; }
        state.memberId = m.id;
        state.page = 1;
        state.seriesKey = '';
        paintMembers();
        refresh();
      };
      box.appendChild(b);
    });
  }

  // ── 分区切换 ──

  var SECS = ['overview', 'reports', 'detail', 'trends', 'daily', 'meds', 'visits'];

  function setSec(sec) {
    state.sec = sec;
    SECS.forEach(function (s) {
      var el = $('h-' + s);
      if (el) el.hidden = s !== sec;
    });
    var navSec = sec === 'detail' ? 'reports' : sec;  // 详情属于报告分区，左侧保持高亮
    Array.prototype.forEach.call(document.querySelectorAll('.hh-navb'), function (b) {
      b.classList.toggle('on', b.getAttribute('data-sec') === navSec);
    });
    paintPrivHint();
    var main = $('h-main');
    if (main) main.scrollTop = 0;
  }

  function refresh() {
    if (state.sec === 'detail' && state.cur) { openDetail(state.cur.report.id); return; }
    switch (state.sec) {
      case 'reports': state.page = 1; loadReports(true); return;
      case 'trends': loadTrends(); return;
      case 'daily': loadVitals(); return;
      case 'meds': loadMeds(); return;
      case 'visits': loadVisits(); return;
      default: setSec('overview'); loadOverview();
    }
  }

  // ── 隐私模式 ──

  function paintPrivHint() {
    var hint = $('h-privhint');
    var btn = $('h-privacy');
    if (!hint || !btn) return;
    hint.hidden = !state.privacy;
    hint.classList.toggle('revealed', state.revealed);
    btn.textContent = state.privacy ? (state.revealed ? '隐藏数值' : '显示数值') : '隐私模式';
    btn.classList.toggle('act', state.privacy);
    var show = $('h-priv-show');
    if (show) show.textContent = state.revealed ? '隐藏' : '临时显示 15 秒';
  }

  function revealFor(sec) {
    state.revealed = true;
    clearTimeout(state.revealTimer);
    paintPrivHint();
    rerender();
    state.revealTimer = setTimeout(function () {
      state.revealed = false;
      paintPrivHint();
      rerender();
    }, sec * 1000);
  }

  function rerender() {
    switch (state.sec) {
      case 'detail': if (state.cur) paintDetail(state.cur); return;
      case 'trends': paintPick(); paintChart(); paintPoints(); return;
      case 'daily': paintVitals(); return;
      case 'meds': paintMeds(); return;
      case 'visits': paintVisits(); return;
      case 'reports': if (state.items.length) paintReports(); return;
      default: loadOverview();
    }
  }

  function loadSettings() {
    return api('settings.get').then(function (j) {
      state.privacy = !!j.privacy;
      paintPrivHint();
    }).catch(function () { });
  }

  // ── 概览 ──

  function loadOverview() {
    api('overview', { memberId: state.memberId }).then(function (j) {
      var last = j.last || null;
      var abn = j.abnormal || [];
      var cards = [
        { k: '报告', v: j.total || 0, s: '份' },
        { k: '最近一次', v: last ? last.reportAt : '—', s: last ? (KIND[last.kind] || '') : '还没有记录' },
        { k: '需留意', v: j.abnormalTotal || abn.length, s: '项指标超出参考范围' }
      ];
      $('h-stats').innerHTML = cards.map(function (c) {
        return '<div class="hh-stat"><div class="hh-stat-k">' + esc(c.k) + '</div>' +
          '<div class="hh-stat-v">' + esc(c.v) + '</div>' +
          '<div class="hh-stat-s">' + esc(c.s) + '</div></div>';
      }).join('');

      var box = $('h-abn');
      if (!abn.length) {
        box.innerHTML = '<div class="hh-empty muted">' + (j.total ? '最近的数据没有超出参考范围的项目。' : '还没有数据。新建一份报告后，超范围的指标会出现在这里。') + '</div>';
      } else {
        box.innerHTML = abn.map(function (a) {
          var v = a.value != null ? vnum(a.value) : (a.valueText || '—');
          return '<button class="hh-abn-item" data-rid="' + esc(a.reportId) + '">' +
            '<span class="hh-dot ' + (a.flag === 'high' ? 'hi' : 'lo') + '"></span>' +
            '<span class="hh-abn-n">' + esc(a.name) + '</span>' +
            '<b class="' + (a.flag === 'high' ? 'hi' : 'lo') + '">' + esc(v) + '</b>' +
            '<span class="hh-abn-u">' + esc(a.unit || '') + '</span>' +
            '<span class="hh-abn-r">参考 ' + esc(a.refText || '—') + '</span>' +
            '<span class="hh-abn-d">' + esc(a.reportAt) + '</span></button>';
        }).join('');
        Array.prototype.forEach.call(box.querySelectorAll('.hh-abn-item'), function (b) {
          b.onclick = function () { openDetail(b.getAttribute('data-rid')); };
        });
      }

      api('report.list', { memberId: state.memberId, page: 1, pageSize: 5 }).then(function (r) {
        $('h-recent').innerHTML = (r.items || []).length
          ? r.items.map(itemHTML).join('')
          : '<div class="hh-empty muted">还没有报告记录。</div>';
        bindItems($('h-recent'));
        $('h-rep-n').textContent = r.total || '';
        $('h-rep-n').hidden = !r.total;
      }).catch(function () { });
      // 用药在服数顺手更新到左栏
      api('med.list', { memberId: state.memberId, status: 'active' }).then(function (m) {
        $('h-med-n').textContent = m.active || '';
        $('h-med-n').hidden = !m.active;
      }).catch(function () { });
    }).catch(function (e) { toast(e.message, true); });
  }

  // ── 报告列表 ──

  function itemHTML(it) {
    var bad = it.abnormal > 0
      ? '<span class="hh-bad">' + it.abnormal + ' 项异常</span>'
      : '<span class="hh-ok">' + (it.total ? '未见异常' : '无指标') + '</span>';
    return '<button class="hh-item" data-id="' + esc(it.id) + '">' +
      '<div class="hh-item-date"><b>' + esc((it.reportAt || '').slice(0, 4)) + '</b><span>' +
      esc((it.reportAt || '').slice(5)) + '</span></div>' +
      '<div class="hh-item-main"><b>' + esc(it.title) + '</b>' +
      '<span>' + esc([it.org, it.dept].filter(Boolean).join(' · ') || '未填机构') + '</span></div>' +
      '<div class="hh-item-r"><span class="hh-tag">' + esc(KIND[it.kind] || it.kind) + '</span>' +
      '<span class="hh-item-n">' + it.total + ' 项</span>' + bad + '</div></button>';
  }

  function bindItems(box) {
    Array.prototype.forEach.call(box.querySelectorAll('.hh-item'), function (b) {
      b.onclick = function () { openDetail(b.getAttribute('data-id')); };
    });
  }

  function paintReports() {
    var box = $('h-list');
    box.innerHTML = state.items.map(itemHTML).join('');
    bindItems(box);
    $('h-empty').hidden = state.items.length > 0;
    $('h-empty').textContent = state.q
      ? '没有匹配「' + state.q + '」的报告或指标'
      : '还没有报告。点左下「新建报告」，把体检单或化验单的项目抄进来。';
    $('h-more').hidden = state.page >= state.pages;
  }

  function loadReports(reset) {
    if (reset) state.page = 1;
    api('report.list', {
      memberId: state.memberId, q: state.q, page: state.page, pageSize: 20
    }).then(function (j) {
      state.items = reset ? (j.items || []) : state.items.concat(j.items || []);
      state.pages = j.pages || 1;
      paintReports();
    }).catch(function (e) { toast(e.message, true); });
  }

  // ── 详情 ──

  function openDetail(id) {
    api('report.get', { id: id }).then(function (j) {
      state.cur = j;
      setSec('detail');
      paintDetail(j);
      window.scrollTo(0, 0);
      var main = $('h-main');
      if (main) main.scrollTop = 0;
    }).catch(function (e) { toast(e.message, true); });
  }

  function paintDetail(j) {
    var r = j.report || {};
    $('h-dtitle').textContent = r.title || '';
    $('h-dsub').innerHTML = [r.reportAt, KIND[r.kind] || r.kind, r.org, r.dept]
      .filter(Boolean).map(esc).join(' · ');

    var metrics = j.metrics || [];
    $('h-mt').innerHTML = metrics.length ? metrics.map(metricHTML).join('')
      : '<tr><td colspan="4" class="hh-none muted">这份报告还没有指标明细，点右上「编辑」补录。</td></tr>';

    var sum = $('h-sum');
    if (r.summary) {
      sum.hidden = false;
      sum.innerHTML = '<b>结论</b><p>' + esc(r.summary).replace(/\n/g, '<br>') + '</p>';
    } else sum.hidden = true;

    paintAtts(r.attachments || []);
  }

  function refText(m) {
    if (m.refText) return m.refText;
    if (m.refLow != null && m.refHigh != null) return vnum(m.refLow) + ' – ' + vnum(m.refHigh);
    if (m.refHigh != null) return '< ' + vnum(m.refHigh);
    if (m.refLow != null) return '> ' + vnum(m.refLow);
    return '—';
  }

  function metricHTML(m) {
    var v = m.value != null ? vnum(m.value) : (m.valueText || '—');
    var cls = m.flag === 'high' ? 'hi' : (m.flag === 'low' ? 'lo' : '');
    var mark = m.flag === 'high' ? '偏高' : (m.flag === 'low' ? '偏低' : '');

    var cmp = '<span class="hh-first">首次</span>';
    if (m.prev) {
      if (m.value != null && m.prev.value != null) {
        var d = m.value - m.prev.value;
        var arrow = Math.abs(d) < 1e-9 ? '持平' : (d > 0 ? '↑' : '↓');
        cmp = '<span class="' + (d > 0 ? 'up' : d < 0 ? 'down' : '') + '">' + arrow + ' ' +
          vnum(m.prev.value) + '</span><em>' + esc((m.prev.reportAt || '').slice(0, 7)) + '</em>';
      } else {
        cmp = '<span class="hh-first">上次 ' + esc(m.prev.valueText || '—') + '</span>';
      }
    }

    return '<tr class="' + cls + '">' +
      '<td><span class="hh-mname">' + esc(m.name) + '</span>' +
      (mark ? '<span class="hh-flag ' + cls + '">' + mark + '</span>' : '') + '</td>' +
      '<td class="hh-v ' + cls + '">' + esc(v) +
      (m.unit ? '<span class="hh-unit">' + esc(m.unit) + '</span>' : '') + '</td>' +
      '<td class="hh-ref">' + refText(m) + '</td>' +
      '<td class="hh-cmp">' + cmp + '</td></tr>';
  }

  // ── 附件 ──

  var IMG = /\.(jpe?g|png|gif|webp|bmp|heic|svg)$/i;

  function attName(p) {
    var s = String(p || '').split('/').pop() || p;
    try { return decodeURIComponent(s); } catch (e) { return s; }
  }

  function paintAtts(atts) {
    var box = $('h-atts');
    $('h-atts-n').textContent = atts.length ? atts.length + ' 个' : '';
    if (!atts.length) {
      box.innerHTML = '<div class="hh-empty muted">还没有附件。可把体检单扫描件、化验单照片加进来。</div>';
      return;
    }
    box.innerHTML = atts.map(function (p) {
      var src = esc(p);
      var thumb = IMG.test(p)
        ? '<img src="' + src + '" alt="" loading="lazy">'
        : '<span class="hh-att-ic">PDF</span>';
      return '<div class="hh-att" data-path="' + src + '">' +
        '<a class="hh-att-a" href="' + src + '" target="_blank" rel="noopener">' + thumb +
        '<span class="hh-att-n">' + esc(attName(p)) + '</span></a>' +
        '<button class="hh-att-del" title="移除附件">×</button></div>';
    }).join('');
    Array.prototype.forEach.call(box.querySelectorAll('.hh-att-del'), function (b) {
      b.onclick = function () {
        var card = b.closest('.hh-att');
        var path = card.getAttribute('data-path');
        if (!confirm('移除附件「' + attName(path) + '」？\n网盘里的文件会一并删除。')) return;
        var left = (state.cur.report.attachments || []).filter(function (p) { return p !== path; });
        api('attach.delete', { path: path }).catch(function () { /* 文件可能已手动删过 */ })
          .then(function () {
            return api('attach.save', { reportId: state.cur.report.id, attachments: left });
          }).then(function () {
            state.cur.report.attachments = left;
            paintAtts(left);
            toast('已移除');
          }).catch(function (e) { toast(e.message, true); });
      };
    });
  }

  function uploadAtts(files) {
    if (!state.cur) return;
    var fid = state.cur.report.id;
    var paths = (state.cur.report.attachments || []).slice();
    var i = 0;
    function next() {
      if (i >= files.length) {
        api('attach.save', { reportId: fid, attachments: paths }).then(function () {
          state.cur.report.attachments = paths;
          paintAtts(paths);
          toast('已添加附件');
        }).catch(function (e) { toast(e.message, true); });
        return;
      }
      var f = files[i++];
      var fd = new FormData();
      fd.append('file', f);
      fetch('/-api/health/attach.upload', { method: 'POST', body: fd }).then(function (r) {
        if (r.status === 401) { location.reload(); throw new Error('登录已过期'); }
        return r.json().then(function (j) {
          if (!r.ok) throw new Error((j && j.error) || ('上传失败（' + r.status + '）'));
          return j;
        });
      }).then(function (j) {
        paths.push(j.path);
        next();
      }).catch(function (e) {
        toast('「' + f.name + '」' + e.message, true);
        next();
      });
    }
    next();
  }

  // ── 趋势 ──

  function loadTrends() {
    api('trend.list', { memberId: state.memberId }).then(function (j) {
      state.series = j.series || [];
      if (!state.series.length) {
        $('h-pick').innerHTML = '<div class="hh-empty muted">还没有可画趋势的数据。录入带数值的报告指标，或用「日常」记几笔体重/血压，这里就有线了。</div>';
        $('h-chart').hidden = true;
        $('h-pts').innerHTML = '';
        return;
      }
      $('h-chart').hidden = false;
      // 默认选第一条（点最多、最近更新）
      if (!state.seriesKey || !state.series.some(function (s) { return s.key === state.seriesKey; })) {
        state.seriesKey = state.series[0].key;
      }
      paintPick();
      paintChart();
      paintPoints();
    }).catch(function (e) { toast(e.message, true); });
  }

  function seriesByKey(k) {
    for (var i = 0; i < state.series.length; i++) if (state.series[i].key === k) return state.series[i];
    return null;
  }

  function paintPick() {
    var box = $('h-pick');
    box.innerHTML = state.series.map(function (s) {
      var last = s.points[s.points.length - 1];
      var cls = 'hh-pickb' + (s.key === state.seriesKey ? ' on' : '') + (s.count < 2 ? ' dim' : '');
      return '<button class="' + cls + '" data-key="' + esc(s.key) + '">' +
        '<span class="hh-pick-n">' + esc(s.name) + '</span>' +
        '<b>' + esc(vnum(last.value)) + '</b>' +
        '<em>' + s.count + ' 次</em></button>';
    }).join('');
    Array.prototype.forEach.call(box.querySelectorAll('.hh-pickb'), function (b) {
      b.onclick = function () {
        state.seriesKey = b.getAttribute('data-key');
        paintPick(); paintChart(); paintPoints();
      };
    });
  }

  function dateOf(at) {
    var d = Date.parse(String(at).slice(0, 10) + 'T00:00:00');
    return isNaN(d) ? 0 : d;
  }

  function fmtShort(at) {
    var s = String(at || '');
    return s.slice(0, 7);
  }

  function paintChart() {
    var s = seriesByKey(state.seriesKey);
    var svg = $('h-svg');
    if (!s) { svg.innerHTML = ''; return; }
    var pts = s.points || [];
    $('h-ch-name').textContent = s.name + (s.unit ? '（' + s.unit + '）' : '');
    var d1 = pts.length ? pts[pts.length - 1].value - pts[0].value : 0;
    var meta = pts.length + ' 次记录 · ' + fmtShort(pts[0].at) + ' → ' + fmtShort(pts[pts.length - 1].at);
    if (pts.length > 1) {
      meta += ' · 变化 ' + (d1 > 0 ? '+' : '') + vnum(Math.round(d1 * 100) / 100);
    }
    $('h-ch-meta').textContent = meta;

    var W = 720, H = 260, pl = 54, pr = 18, pt = 18, pb = 34;
    var lo = pts[0].value, hi = pts[0].value;
    pts.forEach(function (p) { lo = Math.min(lo, p.value); hi = Math.max(hi, p.value); });
    if (s.refLow != null) lo = Math.min(lo, s.refLow);
    if (s.refHigh != null) hi = Math.max(hi, s.refHigh);
    if (hi - lo < 1e-9) { hi += 1; lo -= 1; }
    var pad = (hi - lo) * 0.14;
    lo -= pad; hi += pad;

    var t0 = dateOf(pts[0].at), t1 = dateOf(pts[pts.length - 1].at);
    if (t1 - t0 < 86400000) { t0 -= 43200000; t1 += 43200000; } // 单点/同一天：给一天宽度
    var X = function (at) { return pl + (dateOf(at) - t0) / (t1 - t0) * (W - pl - pr); };
    var Y = function (v) { return pt + (hi - v) / (hi - lo) * (H - pt - pb); };

    var g = [];
    // 参考带
    if (s.refLow != null || s.refHigh != null) {
      var yTop = Y(s.refHigh != null ? s.refHigh : hi);
      var yBot = Y(s.refLow != null ? s.refLow : lo);
      g.push('<rect x="' + pl + '" y="' + yTop.toFixed(1) + '" width="' + (W - pl - pr) +
        '" height="' + Math.max(1, yBot - yTop).toFixed(1) + '" fill="rgba(47,158,107,.10)"/>');
      if (s.refHigh != null) g.push('<line x1="' + pl + '" x2="' + (W - pr) + '" y1="' + yTop.toFixed(1) +
        '" y2="' + yTop.toFixed(1) + '" stroke="rgba(47,158,107,.45)" stroke-dasharray="4 4" stroke-width="1"/>');
      if (s.refLow != null) g.push('<line x1="' + pl + '" x2="' + (W - pr) + '" y1="' + yBot.toFixed(1) +
        '" y2="' + yBot.toFixed(1) + '" stroke="rgba(47,158,107,.45)" stroke-dasharray="4 4" stroke-width="1"/>');
    }
    // 横向网格 + y 轴刻度
    var step = (hi - lo) / 4;
    var dec = step >= 20 ? 0 : (step >= 2 ? 1 : 2); // 刻度别出现 57.38 这种小数尾巴
    for (var i = 0; i <= 4; i++) {
      var v = lo + (hi - lo) * i / 4;
      var y = Y(v);
      g.push('<line x1="' + pl + '" x2="' + (W - pr) + '" y1="' + y.toFixed(1) + '" y2="' + y.toFixed(1) +
        '" stroke="var(--ln)" stroke-width="1"/>');
      g.push('<text x="' + (pl - 8) + '" y="' + (y + 3.5).toFixed(1) +
        '" text-anchor="end" font-size="10" fill="var(--mut)">' +
        esc(state.privacy && !state.revealed ? '••' : v.toFixed(dec)) + '</text>');
    }
    // x 轴日期（首 / 中 / 末）
    var xs = [pts[0].at, pts[Math.floor((pts.length - 1) / 2)].at, pts[pts.length - 1].at];
    var seenX = {};
    xs.forEach(function (at, idx) {
      if (seenX[at]) return;
      seenX[at] = 1;
      var x = X(at);
      var anchor = idx === 0 ? 'start' : (idx === 2 ? 'end' : 'middle');
      g.push('<text x="' + x.toFixed(1) + '" y="' + (H - 12) + '" text-anchor="' + anchor +
        '" font-size="10" fill="var(--mut)">' + esc(fmtShort(at)) + '</text>');
    });

    // 折线 + 面积
    var line = pts.map(function (p) { return X(p.at).toFixed(1) + ',' + Y(p.value).toFixed(1); }).join(' ');
    if (pts.length > 1) {
      g.push('<polygon points="' + line + ' ' + X(pts[pts.length - 1].at).toFixed(1) + ',' + (H - pb) +
        ' ' + X(pts[0].at).toFixed(1) + ',' + (H - pb) + '" fill="rgba(240,36,126,.07)"/>');
      g.push('<polyline points="' + line + '" fill="none" stroke="var(--acc)" stroke-width="2" ' +
        'stroke-linejoin="round" stroke-linecap="round"/>');
    }
    pts.forEach(function (p) {
      var col = p.flag === 'high' ? '#e5484d' : (p.flag === 'low' ? '#c9821b' : 'var(--acc)');
      var tip = String(p.at).slice(0, 10) + '  ' + num(p.value) + (s.unit || '') +
        (p.flag === 'high' ? '（偏高）' : p.flag === 'low' ? '（偏低）' : '') +
        ' · ' + (SRC[p.src] || p.src);
      g.push('<circle cx="' + X(p.at).toFixed(1) + '" cy="' + Y(p.value).toFixed(1) + '" r="3.6" fill="' +
        col + '" stroke="var(--card)" stroke-width="1.2"><title>' + esc(tip) + '</title></circle>');
    });
    svg.innerHTML = g.join('');

    var foot = [];
    if (s.refLow != null || s.refHigh != null) {
      foot.push('绿色带 = ' + (s.refCommon ? '该项目的常见区间' : '最近一份报告的参考范围') +
        (s.refLow != null && s.refHigh != null ? '（' + vnum(s.refLow) + ' – ' + vnum(s.refHigh) + '）' :
          s.refHigh != null ? '（≤ ' + vnum(s.refHigh) + '）' : '（≥ ' + vnum(s.refLow) + '）'));
    }
    if (s.points.some(function (p) { return p.flag; })) foot.push('红点偏高 / 橙点偏低');
    if (foot.length) foot.push('仅作记录，不作诊断');
    $('h-ch-foot').textContent = foot.join(' · ');
    $('h-ch-foot').hidden = !foot.length;
  }

  function paintPoints() {
    var s = seriesByKey(state.seriesKey);
    if (!s) { $('h-pts').innerHTML = ''; return; }
    $('h-pts').innerHTML = s.points.slice().reverse().map(function (p) {
      var ref = (p.refLow != null || p.refHigh != null)
        ? (p.refLow != null ? vnum(p.refLow) : '') + ' – ' + (p.refHigh != null ? vnum(p.refHigh) : '')
        : '—';
      var mark = p.flag === 'high' ? '<span class="hh-flag hi">偏高</span>'
        : p.flag === 'low' ? '<span class="hh-flag lo">偏低</span>' : '';
      var src = p.src === 'report'
        ? '<button class="hh-lnk" data-rid="' + esc(p.reportId) + '">报告</button>'
        : '<span class="muted">自测</span>';
      return '<tr><td class="hh-ref">' + esc(String(p.at).slice(0, 16)) + '</td>' +
        '<td class="hh-v">' + esc(vnum(p.value)) + (s.unit ? '<span class="hh-unit">' + esc(s.unit) + '</span>' : '') + '</td>' +
        '<td class="hh-ref">' + ref + '</td><td>' + mark + '</td><td>' + src + '</td></tr>';
    }).join('');
    Array.prototype.forEach.call($('h-pts').querySelectorAll('.hh-lnk'), function (b) {
      b.onclick = function () { openDetail(b.getAttribute('data-rid')); };
    });
  }

  // ── 日常自测 ──

  function loadVitals() {
    api('vital.list', { memberId: state.memberId }).then(function (j) {
      state.vkinds = j.kinds || [];
      state.vitals = j.items || [];
      state.vlatest = j.latest || {};
      if (!state.vkinds.some(function (k) { return k.kind === state.vkind; }) && state.vkinds.length) {
        state.vkind = state.vkinds[0].kind;
      }
      paintVKinds();
      paintVitals();
    }).catch(function (e) { toast(e.message, true); });
  }

  function vkindOf(k) {
    for (var i = 0; i < state.vkinds.length; i++) if (state.vkinds[i].kind === k) return state.vkinds[i];
    return null;
  }

  function paintVKinds() {
    var box = $('h-vk');
    box.innerHTML = state.vkinds.map(function (k) {
      var last = state.vlatest[k.kind];
      return '<button class="hh-vkb' + (k.kind === state.vkind ? ' on' : '') + '" data-k="' + esc(k.kind) + '">' +
        '<span>' + esc(k.name) + '</span>' +
        (last ? '<b>' + esc(vnum(last.display)) + '</b>' : '<b class="muted">未记</b>') +
        '</button>';
    }).join('');
    Array.prototype.forEach.call(box.querySelectorAll('.hh-vkb'), function (b) {
      b.onclick = function () { state.vkind = b.getAttribute('data-k'); paintVKinds(); paintVKindIO(); };
    });
    paintVKindIO();
  }

  function paintVKindIO() {
    var k = vkindOf(state.vkind);
    if (!k) return;
    $('h-vunit').textContent = k.unit || '';
    $('h-v2-wrap').hidden = !k.dual;
    $('h-v1').placeholder = k.dual ? '收缩压' : '数值';
    var hint = '点一下类型再填数值。';
    if (k.low && k.high) hint += '常见区间 ' + k.low + '–' + k.high + ' ' + k.unit + '，超出只做浅色标记，不作诊断。';
    if (k.dual) hint = '血压填两个数：收缩压 / 舒张压。常见 90–140 / 60–90 mmHg。';
    $('h-vhint').textContent = hint + ' 同一天多测几次就多记几条。';
    if (!$('h-vtime').value) $('h-vtime').value = nowLocal();
  }

  function paintVitals() {
    var box = $('h-vlist');
    if (!state.vitals.length) {
      box.innerHTML = '<div class="hh-empty muted">还没有自测记录。上面选类型、填数值，点「记录」即可。</div>';
      return;
    }
    box.innerHTML = state.vitals.map(function (v) {
      var cls = v.flag === 'high' ? 'hi' : (v.flag === 'low' ? 'lo' : '');
      return '<div class="hh-vitem ' + cls + '">' +
        '<div class="hh-vitem-k">' + esc(v.kindName) + '</div>' +
        '<div class="hh-vitem-v">' + esc(vnum(v.display)) +
        '<span class="hh-unit">' + esc(v.unit || '') + '</span></div>' +
        '<div class="hh-vitem-t">' + esc(String(v.measuredAt).replace(' ', ' · ')) + '</div>' +
        '<div class="hh-vitem-n">' + esc(v.note || '') + '</div>' +
        '<button class="hh-rowdel" data-id="' + esc(v.id) + '" title="删除">×</button></div>';
    }).join('');
    Array.prototype.forEach.call(box.querySelectorAll('.hh-rowdel'), function (b) {
      b.onclick = function () {
        if (!confirm('删除这条记录？')) return;
        api('vital.delete', { id: b.getAttribute('data-id') }).then(function () {
          toast('已删除');
          loadVitals();
        }).catch(function (e) { toast(e.message, true); });
      };
    });
  }

  function saveVital() {
    var k = vkindOf(state.vkind);
    var v1 = $('h-v1').value.trim();
    if (v1 === '') { toast('请填数值', true); return; }
    var body = {
      memberId: state.memberId, kind: state.vkind,
      measuredAt: $('h-vtime').value || nowLocal(),
      v1: Number(v1),
      note: $('h-vnote').value.trim()
    };
    if (k && k.dual) {
      var v2 = $('h-v2').value.trim();
      if (v2 === '') { toast('血压要填舒张压', true); return; }
      body.v2 = Number(v2);
    }
    $('h-vsave').disabled = true;
    api('vital.save', body).then(function () {
      toast('已记录');
      $('h-v1').value = ''; $('h-v2').value = ''; $('h-vnote').value = '';
      $('h-vtime').value = nowLocal();
      loadVitals();
    }).catch(function (e) { toast(e.message, true); })
      .finally(function () { $('h-vsave').disabled = false; });
  }

  // ── 用药 ──

  function loadMeds() {
    api('med.list', { memberId: state.memberId }).then(function (j) {
      state.meds = j.items || [];
      paintMeds();
      $('h-med-n').textContent = j.active || '';
      $('h-med-n').hidden = !j.active;
    }).catch(function (e) { toast(e.message, true); });
  }

  function medHTML(m) {
    var sub = [m.dose, m.freq, m.route].filter(Boolean).join(' · ') || '未填用法';
    var when = [m.startAt ? m.startAt + ' 起' : '', m.endAt ? m.endAt + ' 停' : ''].filter(Boolean).join(' · ');
    var tag = m.status === 'active'
      ? '<span class="hh-ok">在服</span>'
      : '<span class="muted">已停</span>';
    return '<div class="hh-med' + (m.status === 'stopped' ? ' off' : '') + '">' +
      '<div class="hh-med-top"><b>' + esc(m.name) + '</b>' + tag + '</div>' +
      '<div class="hh-med-sub">' + esc(sub) + '</div>' +
      (when ? '<div class="hh-med-when">' + esc(when) + '</div>' : '') +
      (m.note ? '<div class="hh-med-note">' + esc(m.note) + '</div>' : '') +
      '<div class="hh-med-acts">' +
      '<button data-act="edit" data-id="' + esc(m.id) + '">编辑</button>' +
      '<button data-act="toggle" data-id="' + esc(m.id) + '">' + (m.status === 'active' ? '停用' : '恢复') + '</button>' +
      '<button data-act="del" data-id="' + esc(m.id) + '" class="hh-danger">删除</button></div></div>';
  }

  function paintMeds() {
    var on = state.meds.filter(function (m) { return m.status !== 'stopped'; });
    var off = state.meds.filter(function (m) { return m.status === 'stopped'; });
    $('h-med-list').innerHTML = on.length ? on.map(medHTML).join('')
      : '<div class="hh-empty muted">还没有在服的药。点「+ 添加」把医生开的药记进来。</div>';
    $('h-med-old').innerHTML = off.length ? off.map(medHTML).join('')
      : '<div class="hh-empty muted">没有历史记录。</div>';
    Array.prototype.forEach.call(document.querySelectorAll('.hh-med-acts button'), function (b) {
      b.onclick = function () {
        var id = b.getAttribute('data-id');
        var m = state.meds.filter(function (x) { return x.id === id; })[0];
        var act = b.getAttribute('data-act');
        if (act === 'edit') { openMed(m); return; }
        if (act === 'del') {
          if (!confirm('删除「' + m.name + '」这条用药记录？')) return;
          api('med.delete', { id: id }).then(function () { toast('已删除'); loadMeds(); })
            .catch(function (e) { toast(e.message, true); });
          return;
        }
        // toggle
        api('med.save', {
          id: id, memberId: state.memberId, name: m.name, dose: m.dose, freq: m.freq, route: m.route,
          startAt: m.startAt, endAt: m.endAt, note: m.note,
          status: m.status === 'active' ? 'stopped' : 'active'
        }).then(function () {
          toast(m.status === 'active' ? '已标记停用' : '已恢复在服');
          loadMeds();
        }).catch(function (e) { toast(e.message, true); });
      };
    });
  }

  function openMed(m) {
    state.medEditId = m ? m.id : '';
    $('h-dtitle2').textContent = m ? '编辑用药' : '添加用药';
    $('h-dname').value = m ? m.name : '';
    $('h-ddose').value = m ? m.dose : '';
    $('h-dfreq').value = m ? m.freq : '';
    $('h-droute').value = m ? (m.route || '') : '口服';
    $('h-dstart').value = m ? m.startAt : today();
    $('h-dend').value = m ? m.endAt : '';
    $('h-dnote').value = m ? m.note : '';
    $('h-dstop').checked = m ? m.status === 'stopped' : false;
    $('h-dmask').hidden = false;
    $('h-dname').focus();
  }

  function saveMed() {
    var body = {
      id: state.medEditId, memberId: state.memberId,
      name: $('h-dname').value.trim(),
      dose: $('h-ddose').value.trim(),
      freq: $('h-dfreq').value.trim(),
      route: $('h-droute').value,
      startAt: $('h-dstart').value,
      endAt: $('h-dend').value,
      note: $('h-dnote').value.trim(),
      status: $('h-dstop').checked ? 'stopped' : 'active'
    };
    if (!body.name) { toast('请填药名', true); return; }
    api('med.save', body).then(function () {
      $('h-dmask').hidden = true;
      toast('已保存');
      loadMeds();
    }).catch(function (e) { toast(e.message, true); });
  }

  // ── 就诊 ──

  function loadVisits() {
    api('visit.list', { memberId: state.memberId, q: state.q, page: 1, pageSize: 60 }).then(function (j) {
      state.visits = j.items || [];
      paintVisits();
    }).catch(function (e) { toast(e.message, true); });
  }

  function paintVisits() {
    var box = $('h-vs-list');
    if (!state.visits.length) {
      box.innerHTML = '<div class="hh-empty muted">还没有就诊记录。点「+ 添加」记一次门诊/复查。</div>';
      return;
    }
    box.innerHTML = state.visits.map(function (v) {
      var head = [v.org, v.dept].filter(Boolean).join(' · ') || '（未填机构）';
      var sub = [v.reason, v.diagnosis].filter(Boolean).join(' → ');
      return '<div class="hh-vs">' +
        '<div class="hh-vs-date"><b>' + esc((v.visitAt || '').slice(0, 4)) + '</b><span>' +
        esc((v.visitAt || '').slice(5)) + '</span></div>' +
        '<div class="hh-vs-main"><b>' + esc(head) + '</b>' +
        '<span>' + esc(sub || '未填原因/诊断') + '</span>' +
        (v.advice ? '<span class="hh-vs-adv">医嘱：' + esc(v.advice) + '</span>' : '') +
        (v.doctor ? '<span class="muted">医生：' + esc(v.doctor) + '</span>' : '') + '</div>' +
        '<div class="hh-vs-r">' + (v.cost != null ? '<span class="hh-tag">¥' + esc(vnum(v.cost)) + '</span>' : '') +
        '<div class="hh-vs-acts">' +
        '<button data-act="edit" data-id="' + esc(v.id) + '">编辑</button>' +
        '<button data-act="del" data-id="' + esc(v.id) + '" class="hh-danger">删除</button>' +
        '</div></div></div>';
    }).join('');
    Array.prototype.forEach.call(box.querySelectorAll('.hh-vs-acts button'), function (b) {
      b.onclick = function () {
        var id = b.getAttribute('data-id');
        var v = state.visits.filter(function (x) { return x.id === id; })[0];
        if (b.getAttribute('data-act') === 'edit') { openVisit(v); return; }
        if (!confirm('删除 ' + v.visitAt + ' 这条就诊记录？')) return;
        api('visit.delete', { id: id }).then(function () { toast('已删除'); loadVisits(); })
          .catch(function (e) { toast(e.message, true); });
      };
    });
  }

  function openVisit(v) {
    state.visitEditId = v ? v.id : '';
    $('h-stitle').textContent = v ? '编辑就诊' : '添加就诊';
    $('h-sdate').value = v ? v.visitAt : today();
    $('h-sorg').value = v ? v.org : '';
    $('h-sdept').value = v ? v.dept : '';
    $('h-sdoctor').value = v ? v.doctor : '';
    $('h-sreason').value = v ? v.reason : '';
    $('h-sdiag').value = v ? v.diagnosis : '';
    $('h-sadv').value = v ? v.advice : '';
    $('h-scost').value = v && v.cost != null ? v.cost : '';
    $('h-smask').hidden = false;
    $('h-sorg').focus();
  }

  function saveVisit() {
    var body = {
      id: state.visitEditId, memberId: state.memberId,
      visitAt: $('h-sdate').value,
      org: $('h-sorg').value.trim(),
      dept: $('h-sdept').value.trim(),
      doctor: $('h-sdoctor').value.trim(),
      reason: $('h-sreason').value.trim(),
      diagnosis: $('h-sdiag').value.trim(),
      advice: $('h-sadv').value.trim(),
      note: '',
      cost: $('h-scost').value.trim() === '' ? null : Number($('h-scost').value)
    };
    if (!body.visitAt) { toast('请选日期', true); return; }
    if (!body.org && !body.reason && !body.diagnosis) { toast('机构、原因、诊断至少填一项', true); return; }
    api('visit.save', body).then(function () {
      $('h-smask').hidden = true;
      toast('已保存');
      loadVisits();
    }).catch(function (e) { toast(e.message, true); });
  }

  // ── 报告编辑 ──

  function rowHTML(m) {
    m = m || {};
    return '<tr>' +
      '<td><input class="hh-i-name" value="' + esc(m.name || '') + '" placeholder="项目名"></td>' +
      '<td><input class="hh-i-val" value="' + (m.value != null ? esc(num(m.value)) : '') + '" inputmode="decimal"></td>' +
      '<td><input class="hh-i-unit" value="' + esc(m.unit || '') + '" placeholder="U/L"></td>' +
      '<td><input class="hh-i-low" value="' + (m.refLow != null ? esc(num(m.refLow)) : '') + '" inputmode="decimal"></td>' +
      '<td><input class="hh-i-high" value="' + (m.refHigh != null ? esc(num(m.refHigh)) : '') + '" inputmode="decimal"></td>' +
      '<td><input class="hh-i-text" value="' + esc(m.valueText || '') + '" placeholder="阴性"></td>' +
      '<td><button class="hh-rowdel" title="删除这一行">×</button></td></tr>';
  }

  function addRow(m) {
    var tr = document.createElement('tbody');
    tr.innerHTML = rowHTML(m);
    var row = tr.firstChild;
    row.querySelector('.hh-rowdel').onclick = function () { row.remove(); };
    $('h-rows').appendChild(row);
  }

  function collectRows() {
    var out = [];
    Array.prototype.forEach.call($('h-rows').querySelectorAll('tr'), function (tr) {
      var f = function (c) { var el = tr.querySelector('.' + c); return el ? el.value.trim() : ''; };
      var name = f('hh-i-name');
      if (!name) return;
      var n = function (c) { var v = f(c); if (v === '') return null; var x = Number(v); return isFinite(x) ? x : null; };
      out.push({
        name: name,
        value: n('hh-i-val'),
        unit: f('hh-i-unit'),
        refLow: n('hh-i-low'),
        refHigh: n('hh-i-high'),
        valueText: f('hh-i-text'),
        refText: ''
      });
    });
    return out;
  }

  function openEditor(id) {
    state.editId = id || '';
    $('h-rtitle').textContent = id ? '编辑报告' : '新建报告';
    $('h-rmem').innerHTML = state.members.map(function (m) {
      return '<option value="' + esc(m.id) + '">' + esc(m.name) + '</option>';
    }).join('');
    $('h-rmem').value = state.memberId;
    $('h-rmask').hidden = false;

    if (!id) {
      $('h-rt').value = '';
      $('h-rorg').value = '';
      $('h-rdept').value = '';
      $('h-rsum').value = '';
      $('h-rkind').value = 'checkup';
      $('h-rdate').value = today();
      $('h-rows').innerHTML = '';
      addRow(); addRow(); addRow();
      return;
    }
    api('report.get', { id: id }).then(function (j) {
      var r = j.report || {};
      $('h-rmem').value = r.memberId || state.memberId;
      $('h-rkind').value = r.kind || 'checkup';
      $('h-rdate').value = r.reportAt || '';
      $('h-rt').value = r.title || '';
      $('h-rorg').value = r.org || '';
      $('h-rdept').value = r.dept || '';
      $('h-rsum').value = r.summary || '';
      $('h-rows').innerHTML = '';
      (j.metrics || []).forEach(function (m) {
        addRow({ name: m.name, value: m.value, unit: m.unit, refLow: m.refLow, refHigh: m.refHigh, valueText: m.valueText });
      });
      if (!(j.metrics || []).length) addRow();
    }).catch(function (e) { toast(e.message, true); });
  }

  function saveReport() {
    var metrics = collectRows();
    var body = {
      id: state.editId,
      memberId: $('h-rmem').value,
      kind: $('h-rkind').value,
      title: $('h-rt').value.trim(),
      org: $('h-rorg').value.trim(),
      dept: $('h-rdept').value.trim(),
      reportAt: $('h-rdate').value,
      summary: $('h-rsum').value.trim(),
      // 编辑已有报告时保留原附件清单（附件走 attach.save 单独维护）
      attachments: (state.editId && state.cur && state.cur.report && state.cur.report.id === state.editId)
        ? (state.cur.report.attachments || []) : [],
      metrics: metrics
    };
    if (!body.reportAt) { toast('请选择报告日期', true); return; }
    $('h-rsave').disabled = true;
    api('report.save', body).then(function (j) {
      $('h-rmask').hidden = true;
      toast('已保存' + (j.abnormal ? '，其中 ' + j.abnormal + ' 项超出参考范围' : ''));
      if (state.memberId !== body.memberId) {
        state.memberId = body.memberId;
        paintMembers();
      }
      state.cur = null;
      setSec('reports');
      state.page = 1;
      loadReports(true);
      api('overview', { memberId: state.memberId }).then(function (o) {
        $('h-rep-n').textContent = o.total || '';
        $('h-rep-n').hidden = !o.total;
      }).catch(function () { });
    }).catch(function (e) {
      toast(e.message, true);
    }).finally(function () { $('h-rsave').disabled = false; });
  }

  // ── 成员编辑 ──

  function openMember(m) {
    state.memEditId = m ? m.id : '';
    $('h-mtitle').textContent = m ? '编辑成员' : '添加成员';
    $('h-mname').value = m ? m.name : '';
    $('h-mrel').value = m ? (m.relation || '本人') : '其他';
    $('h-msex').value = m ? (m.sex || '') : '';
    $('h-mbirth').value = m ? (m.birth || '') : '';
    $('h-mheight').value = m && m.height != null ? m.height : '';
    $('h-mmask').hidden = false;
    $('h-mname').focus();
  }

  function saveMember() {
    var h = $('h-mheight').value.trim();
    var body = {
      id: state.memEditId,
      name: $('h-mname').value.trim(),
      relation: $('h-mrel').value,
      sex: $('h-msex').value,
      birth: $('h-mbirth').value,
      height: h === '' ? null : Number(h),
      note: ''
    };
    if (!body.name) { toast('请填姓名', true); return; }
    api('member.save', body).then(function (j) {
      $('h-mmask').hidden = true;
      if (!state.memEditId) state.memberId = j.id;
      toast('已保存');
      return loadMembers().then(refresh);
    }).catch(function (e) { toast(e.message, true); });
  }

  // ── 导出 ──

  function doExport() {
    api('export').then(function (j) {
      var blob = new Blob([JSON.stringify(j, null, 2)], { type: 'application/json' });
      var url = URL.createObjectURL(blob);
      var a = document.createElement('a');
      a.href = url;
      a.download = '对号健康-' + today().replace(/-/g, '') + '.json';
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(function () { URL.revokeObjectURL(url); }, 3000);
      var c = j.counts || {};
      toast('已导出：报告 ' + (c.reports || 0) + ' 份 · 自测 ' + (c.vitals || 0) + ' 条');
    }).catch(function (e) { toast(e.message, true); });
  }

  // ── 绑定 ──

  Array.prototype.forEach.call(document.querySelectorAll('.hh-navb'), function (b) {
    b.onclick = function () {
      var sec = b.getAttribute('data-sec');
      if (sec === 'reports') { state.page = 1; setSec('reports'); loadReports(true); return; }
      if (sec === 'trends') { setSec('trends'); loadTrends(); return; }
      if (sec === 'daily') { setSec('daily'); loadVitals(); return; }
      if (sec === 'meds') { setSec('meds'); loadMeds(); return; }
      if (sec === 'visits') { setSec('visits'); loadVisits(); return; }
      setSec('overview');
      loadOverview();
    };
  });

  $('h-new').onclick = function () { openEditor(''); };
  $('h-addmem').onclick = function () { openMember(null); };
  $('h-export').onclick = doExport;
  $('h-privacy').onclick = function () {
    if (!state.privacy) {
      state.privacy = true;
      state.revealed = false;
      api('settings.save', { privacy: true }).catch(function () { });
      paintPrivHint();
      rerender();
      toast('隐私模式已开启');
      return;
    }
    if (!state.revealed) { revealFor(15); return; }
    state.revealed = false;
    clearTimeout(state.revealTimer);
    paintPrivHint();
    rerender();
  };
  $('h-priv-show').onclick = function () {
    if (state.revealed) {
      state.revealed = false;
      clearTimeout(state.revealTimer);
      paintPrivHint(); rerender();
    } else revealFor(15);
  };
  $('h-priv-off').onclick = function () {
    state.privacy = false;
    state.revealed = false;
    clearTimeout(state.revealTimer);
    api('settings.save', { privacy: false }).catch(function () { });
    paintPrivHint();
    rerender();
    toast('隐私模式已关闭');
  };

  var searchTimer = null;
  $('h-q').addEventListener('input', function () {
    clearTimeout(searchTimer);
    var v = this.value.trim();
    searchTimer = setTimeout(function () {
      state.q = v;
      if (state.sec === 'visits') { loadVisits(); return; }
      if (state.sec !== 'reports') setSec('reports');
      state.page = 1;
      loadReports(true);
    }, 280);
  });

  $('h-more-b').onclick = function () { state.page += 1; loadReports(false); };
  $('h-back').onclick = function () {
    state.cur = null;
    setSec('reports');
    state.page = 1;
    loadReports(true);
  };
  $('h-edit').onclick = function () {
    if (state.cur) openEditor(state.cur.report.id);
  };
  $('h-del').onclick = function () {
    if (!state.cur) return;
    if (!confirm('删除这份报告？其中的指标明细一并删除，不可恢复。\n附件文件留在网盘里，不会被删。')) return;
    api('report.delete', { id: state.cur.report.id }).then(function () {
      toast('已删除');
      state.cur = null;
      setSec('reports');
      state.page = 1;
      loadReports(true);
    }).catch(function (e) { toast(e.message, true); });
  };

  $('h-att-up').onclick = function () { $('h-file').click(); };
  $('h-file').onchange = function () {
    if (this.files && this.files.length) uploadAtts(Array.prototype.slice.call(this.files));
    this.value = '';
  };

  $('h-addrow').onclick = function () { addRow(); };
  $('h-vsave').onclick = saveVital;
  $('h-med-new').onclick = function () { openMed(null); };
  $('h-dsave').onclick = saveMed;
  $('h-vis-new').onclick = function () { openVisit(null); };
  $('h-ssave').onclick = saveVisit;
  $('h-msave').onclick = saveMember;
  $('h-mcancel').onclick = function () { $('h-mmask').hidden = true; };
  $('h-rsave').onclick = saveReport;
  $('h-rcancel').onclick = function () { $('h-rmask').hidden = true; };
  $('h-dcancel').onclick = function () { $('h-dmask').hidden = true; };
  $('h-scancel').onclick = function () { $('h-smask').hidden = true; };

  document.addEventListener('keydown', function (e) {
    if (e.key !== 'Escape') return;
    var masks = ['h-rmask', 'h-mmask', 'h-dmask', 'h-smask'];
    for (var i = 0; i < masks.length; i++) {
      if (!$(masks[i]).hidden) { $(masks[i]).hidden = true; return; }
    }
    if (state.sec === 'detail') { $('h-back').click(); }
  });

  // 弹窗遮罩点击关闭（点在遮罩本身而非弹窗上）
  ['h-mmask', 'h-rmask', 'h-dmask', 'h-smask'].forEach(function (id) {
    $(id).addEventListener('mousedown', function (e) {
      if (e.target === this) this.hidden = true;
    });
  });

  loadSettings().then(loadMembers).then(function () {
    setSec('overview');
    loadOverview();
  }).catch(function (e) { toast(e.message, true); });
})();
