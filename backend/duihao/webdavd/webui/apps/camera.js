/* 视频监控：相机列表 / 实时画面（MJPEG）/ 24 小时时间轴回放 / 组件按需安装。 */
(function () {
  'use strict';

  var APP = 'camera';
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
  function pad(n) { return n < 10 ? '0' + n : '' + n; }
  function fmtClock(sec) {
    var h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = Math.floor(sec % 60);
    return pad(h) + ':' + pad(m) + ':' + pad(s);
  }
  function fmtSize(n) {
    if (!n) return '0';
    if (n >= 1073741824) return (n / 1073741824).toFixed(1) + ' GB';
    if (n >= 1048576) return (n / 1048576).toFixed(1) + ' MB';
    return Math.round(n / 1024) + ' KB';
  }
  function todayStr() {
    var d = new Date();
    return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate());
  }

  var state = {
    items: [], comp: null, disk: null, vendors: [],
    cur: '', tab: 'live', date: todayStr(), segs: [], days: [],
    plan: [], timer: null, poll: null
  };

  // ── 载入 ──
  function load() {
    return api('list').then(function (r) {
      state.items = r.items || [];
      state.comp = r.comp || {};
      state.disk = r.disk || {};
      if (!state.cur && state.items.length) state.cur = state.items[0].id;
      paintComp();
      paintCams();
      paintDisk();
      paintMeta();
      var ready = state.comp.state === 'ready';
      $('c-setup').hidden = ready;
      $('c-empty').hidden = !ready || state.items.length > 0;
      $('c-work').hidden = !ready || state.items.length === 0;
      if (ready && state.items.length) {
        if (state.tab === 'live') ensureLive();
        else loadSegs();
      }
    }).catch(function (e) { toast(e.message, true); });
  }

  function paintComp() {
    var c = state.comp || {};
    if (c.state === 'downloading') {
      $('c-comp-bar').hidden = false;
      $('c-comp-hint').textContent = '正在下载 ffmpeg…（' + fmtSize(c.size) + ' 左右，可以先去干别的）';
      $('c-install').disabled = true;
    } else if (c.state === 'error') {
      $('c-comp-bar').hidden = true;
      $('c-comp-hint').textContent = '下载失败：' + (c.error || '未知原因') + '。可点「导入本地 zip」离线安装。';
      $('c-install').disabled = false;
    } else {
      $('c-comp-bar').hidden = true;
      $('c-install').disabled = false;
      $('c-comp-hint').textContent = c.size ? '体积约 ' + fmtSize(c.size) : '';
    }
  }

  function paintCams() {
    var box = $('c-cams');
    box.innerHTML = '';
    state.items.forEach(function (m) {
      var b = el('button', 'cc-cam' + (m.id === state.cur ? ' on' : ''));
      b.dataset.id = m.id;
      var dot = el('i', 'cc-dot ' + (m.state || 'stopped'));
      b.appendChild(dot);
      var bd = el('div', 'cc-cam-bd');
      bd.appendChild(el('b', null, m.name));
      bd.appendChild(el('span', null, camSub(m)));
      b.appendChild(bd);
      b.addEventListener('click', function () { selectCam(m.id); });
      box.appendChild(b);
    });
  }

  function camSub(m) {
    if (m.state === 'recording') return '录制中 · 保留 ' + (m.retainDays || 0) + ' 天';
    if (m.state === 'preview') return '预览中';
    if (m.enabled === 0) return '已停用';
    return m.record ? '已排期（未启动）' : '只看不录';
  }

  function paintDisk() {
    var d = state.disk || {};
    var parts = [];
    if (d.segments) parts.push(d.segments + ' 个片段');
    if (d.used) parts.push(fmtSize(d.used));
    if (d.free) parts.push('磁盘剩 ' + fmtSize(d.free));
    $('c-disk').textContent = parts.join(' · ');
  }

  function curCam() {
    for (var i = 0; i < state.items.length; i++) if (state.items[i].id === state.cur) return state.items[i];
    return null;
  }

  function paintMeta() {
    var m = curCam();
    if (!m) { $('c-meta').innerHTML = ''; return; }
    var bits = [];
    bits.push('<b>' + esc(m.name) + '</b>');
    bits.push('<span>' + (m.vendor ? esc(m.vendor) : '自定义') + '</span>');
    if (m.host) bits.push('<span>' + esc(m.host) + '</span>');
    bits.push('<span>' + (m.record ? '录像开启' : '只看不录') + '</span>');
    bits.push('<span>' + planText(m.plan) + '</span>');
    if (m.lastError) bits.push('<em class="cc-err">' + esc(m.lastError) + '</em>');
    $('c-meta').innerHTML = bits.join('<i>·</i>');
    paintPtz();
  }

  // 计划的一句话说法（列表 meta 用）
  function planText(raw) {
    var slots = parsePlan(raw);
    if (!slots.length) return '时段：全天';
    return '时段：' + slots.map(function (s) {
      var d = s.days && s.days.length
        ? s.days.map(function (n) { return '周' + '一二三四五六日'.charAt(n - 1); }).join('')
        : '每天';
      return d + ' ' + s.from + '–' + s.to;
    }).join('、');
  }

  function parsePlan(raw) {
    if (!raw) return [];
    try {
      var v = JSON.parse(raw);
      return Array.isArray(v) ? v : [];
    } catch (e) { return []; }
  }

  // ── 云台 ──
  function paintPtz() {
    var m = curCam();
    var ok = !!(m && m.ptz && state.comp && state.comp.state === 'ready');
    $('c-ptz').hidden = !ok;
    if (!ok) return;
    var kind = String(m.ptz).split(':')[0];
    var name = kind === 'hik' ? '海康 ISAPI' : (kind === 'dahua' ? '大华 CGI' : kind);
    $('c-ptz-hint').textContent = name + ' · 按住方向键转，松手停';
  }

  function ptzGo(dir) {
    var m = curCam();
    if (!m || !m.ptz) return;
    var speed = parseInt($('c-pspeed').value, 10) || 4;
    api('ptz.move', { id: m.id, dir: dir, speed: speed }).catch(function (e) {
      toast(e.message, true);
    });
  }

  // ── 实时 ──
  function ensureLive() {
    var m = curCam();
    if (!m || state.comp.state !== 'ready') return;
    var img = $('c-img');
    $('c-hint').textContent = m.urlSub || m.urlMain ? '' : '这台相机还没有地址';
    img.style.display = 'block';
    img.src = '/-api/camera/live?cam=' + encodeURIComponent(m.id) + '&t=' + Date.now();
    img.onerror = function () {
      // 断流时把 img 收起来：否则浏览器的「破图」图标和 alt 文字会跟提示语重叠
      img.style.display = 'none';
      img.removeAttribute('src'); // 清掉 src，破图图标才不会一直挂在那儿
      $('c-hint').textContent = '画面断了 —— 相机关了、改过密码，或者编码是 H.265（浏览器放不了，请把子码流改 H.264）';
      // 5 秒后自己重连一次，剩下靠用户点相机
      setTimeout(function () { if (state.cur === m.id && state.tab === 'live') ensureLive(); }, 5000);
    };
  }

  function selectCam(id) {
    state.cur = id;
    paintCams();
    paintMeta();
    if (state.tab === 'live') ensureLive();
    else loadSegs();
  }

  // ── 回放 ──
  function setTab(tab) {
    state.tab = tab;
    [].forEach.call(document.querySelectorAll('.cc-tab'), function (b) {
      b.classList.toggle('on', b.dataset.tab === tab);
    });
    $('c-pane-live').hidden = tab !== 'live';
    $('c-pane-play').hidden = tab !== 'play';
    if (tab === 'live') ensureLive();
    else loadSegs();
  }

  function loadSegs() {
    var m = curCam();
    if (!m) return;
    api('seg.list', { cam: m.id, date: state.date }).then(function (r) {
      state.segs = r.items || [];
      state.days = r.days || [];
      $('c-date').value = state.date;
      var hint = state.days.length ? '有录像的日期：' + state.days.slice(0, 4).join('、') : '这天没有录像';
      $('c-dayshint').textContent = hint;
      paintTimeline();
      paintSegs();
    }).catch(function (e) { toast(e.message, true); });
  }

  function paintTimeline() {
    var tl = $('c-tl');
    tl.innerHTML = '';
    var hours = $('c-tl-hours');
    if (!hours.childElementCount) {
      for (var i = 0; i <= 24; i += 3) {
        var sp = el('span', null, pad(i) + ':00');
        sp.style.left = (i / 24 * 100) + '%';
        hours.appendChild(sp);
      }
    }
    state.segs.forEach(function (s) {
      var t = new Date(s.startAt.replace(/-/g, '/'));
      var sec = t.getHours() * 3600 + t.getMinutes() * 60 + t.getSeconds();
      var w = Math.max(0.6, 300 / 86400 * 100);
      var blk = el('i', 'cc-blk' + (s.kind === 'event' ? ' ev' : ''));
      blk.style.left = (sec / 86400 * 100) + '%';
      blk.style.width = w + '%';
      blk.title = s.startAt + ' · ' + fmtSize(s.bytes) + (s.mark ? ' · ' + s.mark : '');
      blk.addEventListener('click', function () { playSeg(s); });
      tl.appendChild(blk);
    });
  }

  function paintSegs() {
    var box = $('c-segs');
    box.innerHTML = '';
    if (!state.segs.length) {
      box.appendChild(el('p', 'muted', '这天还没有录像片段。录像按 5 分钟一片，开录几分钟后就会出现。'));
      return;
    }
    state.segs.forEach(function (s) {
      var row = el('div', 'cc-segrow');
      var t = s.startAt.slice(11, 16);
      row.appendChild(el('b', null, t));
      row.appendChild(el('span', 'cc-segsize', fmtSize(s.bytes)));
      if (s.mark) row.appendChild(el('i', 'cc-mark', s.mark));
      var sp = el('span', 'cc-sp');
      row.appendChild(sp);
      var pb = el('button', null, '播放');
      pb.addEventListener('click', function () { playSeg(s); });
      row.appendChild(pb);
      var mb = el('button', null, s.mark ? '改标记' : '标记');
      mb.addEventListener('click', function () {
        var v = window.prompt('给这段录像加个标记（例如「有人经过」「猫」）', s.mark || '');
        if (v === null) return;
        api('seg.mark', { cam: state.cur, file: s.file, mark: v }).then(function () {
          toast('已标记'); loadSegs();
        }).catch(function (e) { toast(e.message, true); });
      });
      row.appendChild(mb);
      var db = el('button', 'cc-danger', '删除');
      db.addEventListener('click', function () {
        if (!window.confirm('删除这段录像？文件会从网盘里删掉。')) return;
        api('seg.delete', { cam: state.cur, file: s.file }).then(function () {
          toast('已删除'); loadSegs();
        }).catch(function (e) { toast(e.message, true); });
      });
      row.appendChild(db);
      box.appendChild(row);
    });
  }

  function playSeg(s) {
    var v = $('c-video');
    v.src = '/-api/camera/seg.file?cam=' + encodeURIComponent(state.cur) + '&f=' + encodeURIComponent(s.file);
    v.play().catch(function () { });
  }

  // ── 录像计划编辑 ──
  function paintPlan() {
    var box = $('c-plan-rows');
    box.innerHTML = '';
    if (!state.plan.length) {
      box.appendChild(el('p', 'muted cc-plan-empty', '现在是全天连续录 —— 想只在某些时段录，就加一段。'));
    }
    state.plan.forEach(function (slot, idx) {
      if (!slot.days) slot.days = [];
      var row = el('div', 'cc-plan-row');

      var days = el('div', 'cc-plan-days');
      '一二三四五六日'.split('').forEach(function (n, i) {
        var d = i + 1;
        var on = slot.days.indexOf(d) >= 0;
        var b = el('button', 'cc-pday' + (on ? ' on' : ''), n);
        b.type = 'button';
        b.title = '周' + n;
        b.addEventListener('click', function () {
          var k = slot.days.indexOf(d);
          if (k >= 0) slot.days.splice(k, 1);
          else slot.days.push(d);
          slot.days.sort(function (a, b2) { return a - b2; });
          paintPlan();
        });
        days.appendChild(b);
      });
      row.appendChild(days);

      var from = document.createElement('input');
      from.type = 'time';
      from.className = 'cc-in cc-in-sm';
      from.value = slot.from || '00:00';
      from.addEventListener('change', function () { slot.from = from.value; });
      row.appendChild(from);
      row.appendChild(el('span', 'muted cc-plan-dash', '→'));

      var to = document.createElement('input');
      to.type = 'time';
      to.className = 'cc-in cc-in-sm';
      to.value = slot.to || '23:59';
      to.addEventListener('change', function () { slot.to = to.value; });
      row.appendChild(to);

      var del = el('button', 'cc-plan-del', '×');
      del.type = 'button';
      del.title = '删掉这一段';
      del.addEventListener('click', function () {
        state.plan.splice(idx, 1);
        paintPlan();
      });
      row.appendChild(del);

      box.appendChild(row);
    });
    var emptyDay = state.plan.some(function (s) { return !s.days || !s.days.length; });
    $('c-plan-hint').textContent = !state.plan.length
      ? '不填 = 全天连续录'
      : (emptyDay ? '没勾星期的那段 = 每天' : '共 ' + state.plan.length + ' 段 · 时段外自动停录');
  }

  // ── 相机弹窗 ──
  var editing = '';

  function openDlg(m) {
    editing = m ? m.id : '';
    $('c-dlg-title').textContent = m ? '修改相机' : '添加相机';
    $('c-name').value = m ? m.name : '';
    $('c-vendor').value = m && m.vendor ? m.vendor : 'hikvision';
    $('c-host').value = m ? m.host : '';
    $('c-user').value = m && m.credUser ? m.credUser : '';
    $('c-pass').value = '';
    $('c-pass').placeholder = m && m.hasCred
      ? '已存密码，留空则不修改' : '相机密码';
    $('c-main').value = m ? m.urlMain : '';
    $('c-sub').value = m ? m.urlSub : '';
    $('c-transport').value = m ? (m.transport || 'tcp') : 'tcp';
    $('c-record').value = m && m.record === 0 ? '0' : '1';
    $('c-retain').value = m ? m.retainDays : 7;
    $('c-del').hidden = !m;
    state.plan = m ? parsePlan(m.plan) : [];
    paintPlan();
    paintTip();
    $('c-adv').open = !!(m && m.urlMain);
    $('c-mask').hidden = false;
    setTimeout(function () { $('c-name').focus(); }, 50);
  }

  function closeDlg() { $('c-mask').hidden = true; editing = ''; }

  function paintTip() {
    var v = $('c-vendor').value;
    var p = null;
    for (var i = 0; i < state.vendors.length; i++) if (state.vendors[i].key === v) p = state.vendors[i];
    $('c-row-tpl').hidden = v === 'custom';
    $('c-tip').textContent = p ? (p.tip || p.note || '') : '';
  }

  function saveCam() {
    var v = $('c-vendor').value;
    var body = {
      id: editing || undefined,
      name: $('c-name').value.trim(),
      vendor: v,
      host: $('c-host').value.trim(),
      urlMain: $('c-main').value.trim(),
      urlSub: $('c-sub').value.trim(),
      transport: $('c-transport').value,
      record: parseInt($('c-record').value, 10),
      retainDays: parseInt($('c-retain').value, 10) || 0,
      plan: JSON.stringify(state.plan)
    };
    if (!body.name) { toast('请给相机起个名字', true); return; }
    if (v !== 'custom' && !body.host) { toast('请填相机 IP', true); return; }
    if (v !== 'custom') {
      body.tpl = { vendor: v, host: body.host, user: $('c-user').value.trim(), pass: $('c-pass').value, channel: parseInt($('c-ch').value, 10) || 1 };
    }
    api('cam.save', body).then(function (r) {
      closeDlg();
      state.cur = r.id;
      toast('已保存');
      load();
    }).catch(function (e) { toast(e.message, true); });
  }

  function testLook() {
    var v = $('c-vendor').value;
    var url = $('c-sub').value.trim() || $('c-main').value.trim();
    if (!url && v !== 'custom' && $('c-host').value.trim()) {
      // 用模板临时拼一个子码流地址试看（不落库）
      var host = $('c-host').value.trim();
      var user = $('c-user').value.trim() || 'admin';
      var pass = $('c-pass').value;
      var ch = parseInt($('c-ch').value, 10) || 1;
      var tpl = {
        hikvision: 'rtsp://{u}:{p}@{h}:554/Streaming/Channels/{c}02',
        dahua: 'rtsp://{u}:{p}@{h}:554/cam/realmonitor?channel={c}&subtype=1',
        uniview: 'rtsp://{u}:{p}@{h}:554/media/video2',
        tplink: 'rtsp://{u}:{p}@{h}:554/stream2',
        ezviz: 'rtsp://admin:{p}@{h}:554/Streaming/Channels/102'
      }[v];
      if (tpl) {
        url = tpl.replace('{u}', user).replace('{p}', pass).replace('{h}', host).replace('{c}', ch);
      }
    }
    if (!url) { toast('先填地址或 IP', true); return; }
    var w = window.open('/-api/camera/live?url=' + encodeURIComponent(url), '_blank', 'width=900,height=620');
    if (!w) toast('弹窗被拦了，检查浏览器设置', true);
  }

  // ── 抽屉 ──
  function openSheet(title, bodyEl) {
    $('c-dlg2-title').textContent = title;
    var box = $('c-dlg2-body');
    box.innerHTML = '';
    box.appendChild(bodyEl);
    $('c-mask2').hidden = false;
  }

  function showLogs() {
    api('log.list', { cam: state.cur || '' }).then(function (r) {
      var box = el('div');
      if (!r.items.length) box.appendChild(el('p', 'muted', '还没有记录。'));
      r.items.forEach(function (it) {
        var row = el('div', 'cc-logrow');
        row.appendChild(el('i', 'cc-logdot ' + (it.level || 'info')));
        row.appendChild(el('span', 'cc-logtime', (it.at || '').slice(5, 16)));
        row.appendChild(el('b', null, it.msg));
        box.appendChild(row);
      });
      openSheet('运行日志', box);
    }).catch(function (e) { toast(e.message, true); });
  }

  function discover() {
    var box = el('div');
    box.appendChild(el('p', 'muted', '正在扫描局域网里的 ONVIF 相机（约 3 秒）…'));
    openSheet('发现相机', box);
    api('discover', {}).then(function (r) {
      var b2 = el('div');
      if (!r.items.length) {
        b2.appendChild(el('p', 'muted', '没扫到。可能是相机没开 ONVIF（到相机 Web 后台打开），或者不在同一个网段。也可以直接「添加相机」手填 IP。'));
      }
      (r.items || []).forEach(function (it) {
        var row = el('div', 'cc-findrow');
        row.appendChild(el('b', null, it.ip));
        row.appendChild(el('span', 'cc-segsize', it.hint || ''));
        var sp = el('span', 'cc-sp'); row.appendChild(sp);
        var b = el('button', null, '添加到监控');
        b.addEventListener('click', function () {
          $('c-mask2').hidden = true;
          openDlg(null);
          $('c-host').value = it.ip;
          $('c-name').value = it.ip;
        });
        row.appendChild(b);
        b2.appendChild(row);
      });
      openSheet('发现相机', b2);
    }).catch(function (e) { toast(e.message, true); });
  }

  function cleanup() {
    if (!window.confirm('按每台相机的「保留天数」清理旧录像，继续？')) return;
    api('retention.run', {}).then(function (r) {
      toast('清理了 ' + (r.files || 0) + ' 个片段，释放 ' + fmtSize(r.freed));
      load();
    }).catch(function (e) { toast(e.message, true); });
  }

  function installComp() {
    api('comp.install', { id: 'ffmpeg' }).then(function () {
      toast('开始下载，完成后这里会自动刷新');
      state.poll = setInterval(function () {
        api('comp.status').then(function (c) {
          state.comp = c;
          paintComp();
          if (c.state === 'ready') {
            clearInterval(state.poll);
            state.poll = null;
            toast('组件就绪');
            load();
          } else if (c.state === 'error') {
            clearInterval(state.poll);
            state.poll = null;
            load();
          }
        }).catch(function () { });
      }, 2000);
    }).catch(function (e) { toast(e.message, true); });
  }

  function importComp() {
    var p = window.prompt('把 ffmpeg 的 zip 完整路径粘进来（例如 D:\\Downloads\\ffmpeg-master-latest-win64-lgpl.zip）');
    if (!p) return;
    api('comp.import', { id: 'ffmpeg', zip: p }).then(function () {
      toast('导入完成'); load();
    }).catch(function (e) { toast(e.message, true); });
  }

  // ── 事件绑定 ──
  function bind() {
    [].forEach.call(document.querySelectorAll('.cc-tab'), function (b) {
      b.addEventListener('click', function () { setTab(b.dataset.tab); });
    });
    $('c-add').addEventListener('click', function () { openDlg(null); });
    $('c-empty-add').addEventListener('click', function () { openDlg(null); });
    $('c-edit').addEventListener('click', function () { var m = curCam(); if (m) openDlg(m); });
    $('c-find').addEventListener('click', discover);
    $('c-logs').addEventListener('click', showLogs);
    $('c-clean').addEventListener('click', cleanup);
    $('c-install').addEventListener('click', installComp);
    $('c-import').addEventListener('click', importComp);
    $('c-x').addEventListener('click', closeDlg);
    $('c-x2').addEventListener('click', function () { $('c-mask2').hidden = true; });
    $('c-save').addEventListener('click', saveCam);
    $('c-test').addEventListener('click', testLook);
    $('c-vendor').addEventListener('change', paintTip);
    $('c-snap').addEventListener('click', function () {
      if (!state.cur) { toast('先选一台相机', true); return; }
      api('snap', { id: state.cur }).then(function (r) { toast('已存到 ' + r.path); })
        .catch(function (e) { toast(e.message, true); });
    });
    $('c-pop').addEventListener('click', function () {
      if (!state.cur) { toast('先选一台相机', true); return; }
      window.open('/-api/camera/live?cam=' + encodeURIComponent(state.cur), '_blank', 'width=960,height=640');
    });
    $('c-rec-on').addEventListener('click', function () {
      if (!state.cur) { toast('先选一台相机', true); return; }
      api('rec.start', { id: state.cur }).then(function () { toast('已开始录像'); load(); })
        .catch(function (e) { toast(e.message, true); });
    });
    $('c-rec-off').addEventListener('click', function () {
      if (!state.cur) return;
      api('rec.stop', { id: state.cur }).then(function () { toast('已停止'); load(); })
        .catch(function (e) { toast(e.message, true); });
    });
    $('c-del').addEventListener('click', function () {
      if (!editing || !window.confirm('删除这台相机？（已录的片段会留在网盘里）')) return;
      api('cam.delete', { ids: [editing] }).then(function () {
        closeDlg(); state.cur = ''; toast('已删除'); load();
      }).catch(function (e) { toast(e.message, true); });
    });
    $('c-prev').addEventListener('click', function () { shiftDay(-1); });
    $('c-next').addEventListener('click', function () { shiftDay(1); });
    $('c-today').addEventListener('click', function () { state.date = todayStr(); loadSegs(); });
    $('c-date').addEventListener('change', function () { state.date = $('c-date').value; loadSegs(); });

    // 录像计划：加一段（默认工作日 08:00-18:00，符合大多数人的直觉）
    $('c-plan-add').addEventListener('click', function () {
      state.plan.push({ days: [1, 2, 3, 4, 5], from: '08:00', to: '18:00' });
      paintPlan();
    });

    // 云台：按住转、松手停（mouseleave 兜住「按住拖出去」的情况）
    [].forEach.call(document.querySelectorAll('#c-ptz [data-dir]'), function (b) {
      var dir = b.dataset.dir;
      var hold = dir !== 'stop';
      b.addEventListener('mousedown', function (e) { e.preventDefault(); ptzGo(dir); });
      b.addEventListener('mouseup', function () { if (hold) ptzGo('stop'); });
      b.addEventListener('mouseleave', function () { if (hold) ptzGo('stop'); });
      b.addEventListener('touchstart', function (e) { e.preventDefault(); ptzGo(dir); }, { passive: false });
      b.addEventListener('touchend', function () { if (hold) ptzGo('stop'); });
      if (!hold) b.addEventListener('click', function () { ptzGo('stop'); });
    });
    $('c-pspeed').addEventListener('input', function () {
      $('c-pspeed-v').textContent = $('c-pspeed').value;
    });
    $('c-preset-go').addEventListener('click', function () {
      var m = curCam();
      if (!m) return;
      api('ptz.preset', { id: m.id, act: 'goto', n: parseInt($('c-preset').value, 10) || 1 })
        .then(function () { toast('已转到预置位 ' + $('c-preset').value); })
        .catch(function (e) { toast(e.message, true); });
    });
    $('c-preset-set').addEventListener('click', function () {
      var m = curCam();
      if (!m) return;
      var n = parseInt($('c-preset').value, 10) || 1;
      if (!window.confirm('把当前镜头位置存成预置位 ' + n + '？会覆盖原来的。')) return;
      api('ptz.preset', { id: m.id, act: 'set', n: n })
        .then(function () { toast('已存为预置位 ' + n); })
        .catch(function (e) { toast(e.message, true); });
    });
  }

  function shiftDay(d) {
    var t = new Date(state.date.replace(/-/g, '/'));
    t.setDate(t.getDate() + d);
    state.date = t.getFullYear() + '-' + pad(t.getMonth() + 1) + '-' + pad(t.getDate());
    loadSegs();
  }

  function boot() {
    bind();
    $('c-date').value = state.date;
    api('presets').then(function (r) {
      state.vendors = r.vendors || [];
      var sel = $('c-vendor');
      sel.innerHTML = '';
      state.vendors.forEach(function (v) {
        var o = el('option', null, v.name);
        o.value = v.key;
        sel.appendChild(o);
      });
    }).catch(function () { });
    load();
    // 轻量轮询：卡片上的「录制中」状态要跟着引擎变
    setInterval(function () {
      if (document.hidden) return;
      api('list').then(function (r) {
        state.items = r.items || [];
        state.disk = r.disk || {};
        paintCams(); paintDisk(); paintMeta();
      }).catch(function () { });
    }, 5000);
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', boot);
  else boot();
})();
