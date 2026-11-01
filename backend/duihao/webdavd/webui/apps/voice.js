/* 语音提醒：写一句话 + 挑个说法 + 定个时间，到点由本机念出来（不需要页面开着）。 */
(function () {
  'use strict';

  var APP = 'voice';
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
  function pad(n) { return (n < 10 ? '0' : '') + n; }
  function fmtDur(ms) {
    if (!ms || ms < 0) return '0:00';
    var s = Math.round(ms / 1000);
    return Math.floor(s / 60) + ':' + pad(s % 60);
  }
  function parseAt(s) {
    // "2026-10-30 11:53" → Date（本地时区）
    if (!s) return null;
    var m = /^(\d{4})-(\d{2})-(\d{2}) (\d{2}):(\d{2})$/.exec(s);
    if (!m) return null;
    return new Date(+m[1], +m[2] - 1, +m[3], +m[4], +m[5], 0, 0);
  }
  function relTime(d) {
    if (!d) return '';
    var diff = d.getTime() - Date.now();
    var mins = Math.round(diff / 60000);
    if (mins < 0) return '已过期';
    if (mins < 1) return '就在此刻';
    if (mins < 60) return mins + ' 分钟后';
    var hrs = Math.round(mins / 60);
    if (hrs < 24) return hrs + ' 小时后';
    return Math.round(hrs / 24) + ' 天后';
  }

  // ── 状态 ──
  var state = {
    period: 'all', items: [], sel: {}, presets: null,
    editing: null, voice: 'huihui-std', tone: 'normal', periodIn: 'daily', weeks: {},
    playing: null, audioEl: new Audio(), waveCache: {},
    engine: null, kindPicked: 'sapi', clearToken: false,
  };

  // ── 预设 ──
  function loadPresets() {
    return api('presets').then(function (res) {
      state.presets = res;
      state.engine = res.engine || null;
      paintEngineChip();
      renderVoices();
      renderTones();
      renderPeriods();
      renderWeeks();
    });
  }

  // 左栏那句「播报引擎 · X」+ 弹窗里音色的说明，都跟着引擎走
  function paintEngineChip() {
    var e = state.engine || { kind: 'sapi' };
    var gpu = e.kind === 'http';
    $('v-engine').textContent = '播报引擎 · ' + (gpu ? 'GPU 主机' : '本机');
    var hint = $('v-voicehint');
    if (hint) {
      hint.textContent = gpu
        ? 'GPU 合成' + (e.base ? ' · ' + hostOf(e.base) : '（地址未填）')
        : '本机 SAPI 语音，断网可用';
    }
  }

  function hostOf(base) {
    return String(base || '').replace(/^https?:\/\//i, '').replace(/\/.*$/, '');
  }

  function renderVoices() {
    var box = $('v-voicecards');
    box.innerHTML = '';
    (state.presets.voices || []).forEach(function (v) {
      var d = el('button', 'vc-vcard' + (v.key === state.voice ? ' on' : ''));
      d.type = 'button';
      d.dataset.key = v.key;
      d.innerHTML =
        '<span class="vc-vico"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round">' +
        '<path d="M12 15.5a3 3 0 0 1-3-3v-5a3 3 0 0 1 6 0v5a3 3 0 0 1-3 3z"/>' +
        '<path d="M5.5 11.5v1a6.5 6.5 0 0 0 13 0v-1M12 19v2.5"/></svg>' +
        (v.key === state.voice ? '<i class="vc-vok">✓</i>' : '') + '</span>' +
        '<b>' + esc(v.name) + '</b><em>' + esc(v.desc || '') + '</em>';
      d.addEventListener('click', function () {
        state.voice = v.key;
        renderVoices();
      });
      box.appendChild(d);
    });
  }

  function renderTones() {
    var box = $('v-tones');
    box.innerHTML = '';
    (state.presets.tones || []).forEach(function (t) {
      var b = el('button', 'vc-chip' + (t.key === state.tone ? ' on' : ''), t.name);
      b.type = 'button';
      b.addEventListener('click', function () {
        state.tone = t.key;
        renderTones();
      });
      box.appendChild(b);
    });
  }

  function renderPeriods() {
    var box = $('v-periods');
    box.innerHTML = '';
    (state.presets.periods || []).forEach(function (p) {
      var b = el('button', 'vc-chip' + (p.key === state.periodIn ? ' on' : ''), p.name);
      b.type = 'button';
      b.title = p.hint || '';
      b.addEventListener('click', function () {
        state.periodIn = p.key;
        renderPeriods();
        syncPeriodRows();
      });
      box.appendChild(b);
    });
  }

  function renderWeeks() {
    var box = $('v-weeks');
    box.innerHTML = '';
    ['一', '二', '三', '四', '五', '六', '日'].forEach(function (n, i) {
      var k = i + 1;
      var b = el('button', 'vc-chip' + (state.weeks[k] ? ' on' : ''), '周' + n);
      b.type = 'button';
      b.addEventListener('click', function () {
        state.weeks[k] = !state.weeks[k];
        renderWeeks();
      });
      box.appendChild(b);
    });
  }

  function syncPeriodRows() {
    var p = state.periodIn;
    $('v-row-time').hidden = p === 'repeat';
    $('v-row-repeat').hidden = p !== 'repeat';
    $('v-row-week').hidden = p !== 'weekly';
    // 单次：日期就是提醒日；其余周期：日期是「从哪天开始生效」
    $('v-date').closest('.vc-f').querySelector('span').textContent =
      p === 'once' ? '提醒日期' : '开始日期';
  }

  // ── 列表 ──
  function load() {
    return api('list', { period: state.period }).then(function (res) {
      state.items = res.items || [];
      paintCounts(res.counts || {});
      renderGrid();
    });
  }

  function paintCounts(counts) {
    var navs = document.querySelectorAll('.vc-navb');
    for (var i = 0; i < navs.length; i++) {
      var p = navs[i].dataset.p;
      var n = counts[p] || 0;
      navs[i].querySelector('i').textContent = n ? n : '';
      navs[i].classList.toggle('on', p === state.period);
    }
  }

  function statusOf(it) {
    if (!it.enabled) return { k: 'paused', t: '已停用' };
    if (!it.nextAt) return { k: 'done', t: '已结束' };
    var d = parseAt(it.nextAt);
    if (d && d.getTime() - Date.now() < 24 * 3600 * 1000) return { k: 'soon', t: '即将到点' };
    return { k: 'plan', t: '已排期' };
  }

  var PERIOD_NAME = {
    once: '单次', repeat: '重复', daily: '每日', weekly: '每周',
    monthly: '每月', quarterly: '每季', yearly: '每年',
  };

  function ruleText(it) {
    var p = it.period;
    if (p === 'repeat') {
      return '每 ' + it.intervalMin + ' 分钟 · ' + it.dayFrom + '–' + it.dayTo;
    }
    if (p === 'weekly') {
      var names = ['一', '二', '三', '四', '五', '六', '日'];
      var days = (it.weekdays || '').split(',').filter(Boolean).map(function (n) {
        return '周' + (names[+n - 1] || n);
      });
      return days.length ? days.join('、') : '每周';
    }
    return PERIOD_NAME[p] || p;
  }

  function renderGrid() {
    var grid = $('v-grid');
    grid.innerHTML = '';
    $('v-empty').hidden = state.items.length > 0;
    $('v-all').checked = false;
    state.sel = {};
    syncBulk();

    state.items.forEach(function (it) {
      var st = statusOf(it);
      var card = el('article', 'vc-card');
      card.dataset.id = it.id;
      card.dataset.audio = it.audio || '';

      var next = parseAt(it.nextAt);
      card.innerHTML =
        '<header class="vc-card-h">' +
          '<input type="checkbox" class="vc-ck">' +
          '<b class="vc-title">' + esc(it.title) + '</b>' +
          '<span class="vc-pill ' + st.k + '">' + st.t + '</span>' +
        '</header>' +
        '<div class="vc-wave"><canvas></canvas>' +
          '<button class="vc-playb" title="试听这段语音">' +
            '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M8 5.5v13l11-6.5z"/></svg></button>' +
          '<span class="vc-dur">' + fmtDur(it.audioMs) + '</span></div>' +
        '<div class="vc-when">' +
          '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="8.5"/><path d="M12 7.5V12l3 1.8"/></svg>' +
          '<b>' + (it.nextAt || '不再触发') + '</b>' +
          '<span>' + (it.nextAt ? relTime(next) + ' · ' + ruleText(it) : ruleText(it)) + '</span>' +
        '</div>' +
        '<p class="vc-text">' + esc(it.content || '（只念标题）') + '</p>' +
        '<footer class="vc-card-f">' +
          '<button data-act="again" class="vc-ghost">重播</button>' +
          '<span class="vc-sp"></span>' +
          '<button data-act="toggle" class="vc-ghost">' + (it.enabled ? '停用' : '启用') + '</button>' +
          '<button data-act="edit" class="vc-ghost">修改</button>' +
          '<button data-act="del" class="vc-ghost vc-danger">删除</button>' +
        '</footer>';

      // 勾选（不冒泡到卡片，避免以后加整卡点击时把默认行为吃掉）
      var ck = card.querySelector('.vc-ck');
      ck.addEventListener('click', function (e) { e.stopPropagation(); });
      ck.addEventListener('change', function () {
        if (ck.checked) state.sel[it.id] = true; else delete state.sel[it.id];
        card.classList.toggle('sel', ck.checked);
        syncBulk();
      });

      card.querySelector('.vc-playb').addEventListener('click', function () {
        playCard(card, it);
      });
      card.querySelectorAll('[data-act]').forEach(function (b) {
        b.addEventListener('click', function () {
          var act = b.dataset.act;
          if (act === 'edit') openDlg(it);
          else if (act === 'again') replay(it);
          else if (act === 'toggle') toggleOne(it);
          else if (act === 'del') delOne(it);
        });
      });

      grid.appendChild(card);
      if (it.audio) paintWave(card.querySelector('canvas'), it.audio);
    });
  }

  // ── 波形（真波形：解码 wav 取峰值，不是假柱子） ──
  var actx = null;
  function audioCtx() {
    if (!actx) {
      var C = window.AudioContext || window.webkitAudioContext;
      if (!C) return null;
      actx = new C();
    }
    return actx;
  }

  function paintWave(canvas, file) {
    // 卡片刚插入时还没布局，宽度拿到 0 → 波形只铺半截，所以等一帧再画
    requestAnimationFrame(function () { paintWaveNow(canvas, file); });
  }

  function paintWaveNow(canvas, file) {
    var dpr = window.devicePixelRatio || 1;
    var w = Math.round(canvas.getBoundingClientRect().width) || 240;
    var h = Math.round(canvas.getBoundingClientRect().height) || 44;
    canvas.width = Math.round(w * dpr);
    canvas.height = Math.round(h * dpr);
    var g = canvas.getContext('2d');
    g.scale(dpr, dpr);
    var accent = getComputedStyle(document.documentElement).getPropertyValue('--acc').trim() || '#f0247e';
    var url = '/-api/voice/audio?f=' + encodeURIComponent(file);

    function draw(peaks) {
      g.clearRect(0, 0, w, h);
      var n = peaks.length, gap = 2, bw = Math.max(1.4, (w - gap * (n - 1)) / n);
      g.fillStyle = accent;
      g.globalAlpha = 0.55;
      for (var i = 0; i < n; i++) {
        var bh = Math.max(2, peaks[i] * (h - 6));
        g.fillRect(i * (bw + gap), (h - bh) / 2, bw, bh);
      }
      g.globalAlpha = 1;
    }

    var flat = [];
    for (var i = 0; i < 34; i++) flat.push(0.16 + (i % 5 === 0 ? 0.1 : 0));
    draw(flat);

    if (state.waveCache[file]) { draw(state.waveCache[file]); return; }
    var ctx = audioCtx();
    if (!ctx) return;
    fetch(url).then(function (r) { return r.arrayBuffer(); }).then(function (buf) {
      return new Promise(function (ok, bad) { ctx.decodeAudioData(buf, ok, bad); });
    }).then(function (ab) {
      var data = ab.getChannelData(0);
      var n = 34, step = Math.max(1, Math.floor(data.length / n));
      var peaks = [];
      for (var i = 0; i < n; i++) {
        var mx = 0;
        for (var j = 0; j < step; j++) {
          var v = Math.abs(data[i * step + j] || 0);
          if (v > mx) mx = v;
        }
        peaks.push(Math.min(1, mx * 1.6));
      }
      // 归一：最响的柱占满，弱音也看得见
      var top = Math.max.apply(null, peaks) || 1;
      peaks = peaks.map(function (p) { return Math.max(0.08, p / top); });
      state.waveCache[file] = peaks;
      draw(peaks);
    }).catch(function () { /* 解码失败就留平柱，不影响使用 */ });
  }

  // ── 播放（页面内播 wav；到点出声是后端直接念） ──
  function playUrl(url, btn) {
    if (state.playing && state.playing.url === url && !state.audioEl.paused) {
      state.audioEl.pause();
      clearPlayIcon();
      return;
    }
    clearPlayIcon();
    state.audioEl.pause();
    state.audioEl.src = url;
    state.audioEl.play().catch(function (e) {
      toast('播放失败：' + e.message, true);
    });
    state.playing = { url: url, btn: btn };
    if (btn) btn.classList.add('on');
  }
  function clearPlayIcon() {
    if (state.playing && state.playing.btn) state.playing.btn.classList.remove('on');
  }
  state.audioEl.addEventListener('ended', clearPlayIcon);

  function playCard(card, it) {
    if (!it.audio) {
      toast('这条没有合成音频（可能是保存时合成失败），可直接点「重播」让本机念', true);
      return;
    }
    playUrl('/-api/voice/audio?f=' + encodeURIComponent(it.audio), card.querySelector('.vc-playb'));
  }

  // 让后端立刻念一遍（和到点听到的完全一致）
  function replay(it) {
    api('play', { id: it.id }).then(function () {
      toast('本机正在念：「' + it.title + '」');
    }).catch(function (e) { toast(e.message, true); });
  }

  // ── 弹窗 ──
  function today() {
    var d = new Date();
    return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate());
  }

  function openDlg(it, tpl) {
    state.editing = it ? it.id : null;
    $('v-dlg-title').textContent = it ? '修改语音提醒' : '添加语音提醒';
    $('v-del').hidden = !it;

    var d = it || tpl || {};
    $('v-title').value = d.title || '';
    $('v-content').value = d.content || '';
    state.voice = d.voice || 'huihui-std';
    // 换过引擎后，老提醒存的音色键可能已不在当前列表里（huihui-std ↔ lan:xxx）
    var pool = (state.presets && state.presets.voices) || [];
    if (pool.length && !pool.some(function (v) { return v.key === state.voice; })) {
      state.voice = pool[0].key;
    }
    state.tone = d.tone || (tpl ? 'warm' : 'normal');
    state.periodIn = d.period || 'daily';
    $('v-times').value = d.times == null ? 1 : d.times;
    $('v-action').value = d.action || 'none';
    $('v-arg').value = d.actionArg || '';
    $('v-date').value = d.startDate || today();
    $('v-time').value = d.atTime || '09:00';
    $('v-iv').value = d.intervalMin || 60;
    $('v-from').value = d.dayFrom || '09:00';
    $('v-to').value = d.dayTo || '18:00';
    state.weeks = {};
    (d.weekdays || '').split(',').filter(Boolean).forEach(function (n) { state.weeks[+n] = true; });
    if (!Object.keys(state.weeks).length) {
      var wd = new Date().getDay() || 7;
      state.weeks[wd] = true;
    }

    renderVoices();
    renderTones();
    renderPeriods();
    renderWeeks();
    syncPeriodRows();
    syncActionRow();
    $('v-mask').hidden = false;
    setTimeout(function () { $('v-title').focus(); }, 30);
  }

  function closeDlg() { $('v-mask').hidden = true; state.editing = null; }

  function syncActionRow() {
    var a = $('v-action').value;
    $('v-argwrap').hidden = a === 'none';
    $('v-action-hint').textContent = a === 'none'
      ? '白名单动作，不会执行任意命令'
      : (a === 'url' ? '到点会调用系统默认浏览器打开' : '到点会调用资源管理器打开（要在本机存在）');
  }

  function collectForm() {
    var weeks = Object.keys(state.weeks).filter(function (k) { return state.weeks[k]; })
      .sort(function (a, b) { return a - b; }).join(',');
    var body = {
      title: $('v-title').value.trim(),
      content: $('v-content').value.trim(),
      voice: state.voice,
      tone: state.tone,
      times: parseInt($('v-times').value, 10) || 0,
      period: state.periodIn,
      weekdays: weeks,
      intervalMin: parseInt($('v-iv').value, 10) || 60,
      dayFrom: $('v-from').value || '09:00',
      dayTo: $('v-to').value || '18:00',
      startDate: $('v-date').value || today(),
      atTime: $('v-time').value || '09:00',
      action: $('v-action').value || 'none',
      actionArg: $('v-arg').value.trim(),
    };
    if (state.editing) body.id = state.editing;
    return body;
  }

  function save() {
    var body = collectForm();
    if (!body.title) { toast('标题不能为空', true); $('v-title').focus(); return; }
    var btn = $('v-save');
    btn.disabled = true;
    btn.textContent = '合成中…';
    api('save', body).then(function (res) {
      closeDlg();
      if (res.warn) toast(res.warn, true);
      else toast(res.nextAt ? '已排好：下次 ' + res.nextAt : '已保存（这条不会再触发）');
      return load();
    }).catch(function (e) {
      toast(e.message, true);
    }).then(function () {
      btn.disabled = false;
      btn.textContent = '确认提交';
    });
  }

  function delOne(it) {
    if (!confirm('删除「' + it.title + '」？')) return;
    api('delete', { ids: [it.id] }).then(load).catch(function (e) { toast(e.message, true); });
  }

  function toggleOne(it) {
    api('update', { ids: [it.id], enabled: !it.enabled }).then(function () {
      toast(it.enabled ? '已停用' : '已启用');
      return load();
    }).catch(function (e) { toast(e.message, true); });
  }

  function syncBulk() {
    var n = Object.keys(state.sel).length;
    $('v-bulk-del').hidden = n === 0;
    $('v-bulk-toggle').hidden = n === 0;
    $('v-bulk-del').textContent = '删除（' + n + '）';
    var allOn = n > 0 && state.items.filter(function (x) { return state.sel[x.id]; })
      .every(function (x) { return x.enabled; });
    $('v-bulk-toggle').textContent = (allOn ? '停用' : '启用') + '（' + n + '）';
    $('v-bulk-toggle').dataset.on = allOn ? '0' : '1';
  }

  // ── 抽屉：触发记录 / 本机语音 ──
  function openSheet(title, html) {
    $('v-sheet-title').textContent = title;
    $('v-sheet-body').innerHTML = html;
    $('v-sheet-mask').hidden = false;
  }

  function showLogs() {
    api('log.list').then(function (res) {
      var items = res.items || [];
      if (!items.length) {
        openSheet('触发记录', '<p class="muted">还没有触发过。到点后这里会留下记录（含错过/失败原因）。</p>');
        return;
      }
      var rows = items.map(function (x) {
        return '<div class="vc-logrow">' +
          '<span class="vc-logdot' + (x.ok ? '' : ' bad') + '"></span>' +
          '<b>' + esc(x.title) + '</b>' +
          '<span class="muted">' + esc(x.at) + '</span>' +
          (x.msg ? '<em>' + esc(x.msg) + '</em>' : '') +
          '</div>';
      }).join('');
      openSheet('触发记录', rows);
    }).catch(function (e) { toast(e.message, true); });
  }

  function showVoices() {
    api('voices').then(function (res) {
      var items = res.items || [];
      var onGPU = state.engine && state.engine.kind === 'http';
      var lead = onGPU
        ? '<p class="muted">当前用 GPU 主机合成，下面这些本机语音只在 GPU 连不上时兜底（到点照样有人念，只是嗓子换回系统的）。</p>'
        : '<p class="muted">本机 Windows 语音引擎（SAPI5）里装的语音。想加更多声音，去「设置 → 时间和语言 → 语音」装中文语音包。</p>';
      var html = items.length
        ? lead + items.map(function (v) {
            return '<div class="vc-logrow"><b>' + esc(v.name) + '</b>' +
              '<span class="muted">' + esc(v.lang) + '</span></div>';
          }).join('')
        : lead + '<p class="muted">没读到本机语音引擎。语音提醒需要 Windows 的 SAPI5 语音（系统自带）。</p>';
      openSheet('本机语音', html);
    }).catch(function (e) { toast(e.message, true); });
  }

  // ── 播报引擎（只切「调本机 SAPI」还是「调局域网 GPU 主机的 HTTP 接口」） ──
  function openEngine() {
    var e = state.engine || { kind: 'sapi', speed: 1 };
    state.kindPicked = e.kind === 'http' ? 'http' : 'sapi';
    state.clearToken = false;
    $('v-ebase').value = e.base || '';
    $('v-emodel').value = e.model || '';
    $('v-evoice').value = e.voice || '';
    $('v-espeed').value = e.speed || 1;
    $('v-etoken').value = '';
    $('v-etoken').placeholder = e.tokenSet
      ? '已存令牌，留空则不修改' : '不填 = 请求里不带 Authorization';
    $('v-etoken-hint').innerHTML = e.tokenSet
      ? '已存令牌（不回显）；留空保持不变 <button type="button" id="v-etokclear" class="vc-linkb">清除</button>'
      : '服务端要求鉴权时才填；存进本机保险箱，不进数据库、不回显';
    var clr = $('v-etokclear');
    if (clr) {
      clr.addEventListener('click', function () {
        state.clearToken = true;
        $('v-etoken-hint').textContent = '保存后清除已存令牌';
        $('v-etoken').placeholder = '不填 = 请求里不带 Authorization';
      });
    }
    $('v-etvoices').hidden = true;
    $('v-etvoices').innerHTML = '';
    $('v-etest-hint').textContent = '按上面填的地址真请求一次，不会保存';
    $('v-etest-hint').style.color = '';
    paintEKinds();
    syncEFields();
    $('v-emask').hidden = false;
  }

  function closeEngine() { $('v-emask').hidden = true; }

  function paintEKinds() {
    var cards = $('v-ekinds').querySelectorAll('.vc-ecard');
    for (var i = 0; i < cards.length; i++) {
      cards[i].classList.toggle('on', cards[i].dataset.k === state.kindPicked);
    }
  }

  function syncEFields() {
    var gpu = state.kindPicked === 'http';
    $('v-efields').hidden = !gpu;
    $('v-esapihint').hidden = gpu;
  }

  function testEngineConn() {
    var b = $('v-etest');
    var hint = $('v-etest-hint');
    b.disabled = true;
    hint.textContent = '正在请求…';
    hint.style.color = '';
    api('engine.test', {
      base: $('v-ebase').value.trim(),
      model: $('v-emodel').value.trim(),
      token: $('v-etoken').value.trim(),
    }).then(function (res) {
      hint.textContent = '通了（' + res.ms + 'ms）· 读到 ' + res.count + ' 个音色';
      hint.style.color = '#16a34a';
      var box = $('v-etvoices');
      var items = res.voices || [];
      if (!items.length) {
        box.hidden = true;
        box.innerHTML = '';
        toast(res.hint || '能连上，但服务端没返回音色列表', true);
        return;
      }
      box.hidden = false;
      box.innerHTML = '<b>服务端音色（点一个填进上面的默认音色）</b>' +
        items.map(function (v) {
          return '<button type="button" class="vc-chip" data-v="' + esc(v.id) + '">' +
            esc(v.name || v.id) + '</button>';
        }).join('');
      box.querySelectorAll('[data-v]').forEach(function (c) {
        c.addEventListener('click', function () {
          $('v-evoice').value = c.dataset.v;
          toast('已填入默认音色：' + c.dataset.v);
        });
      });
    }).catch(function (e) {
      hint.textContent = e.message;
      hint.style.color = 'var(--danger)';
    }).then(function () { b.disabled = false; });
  }

  function saveEngineSettings() {
    var b = $('v-esave');
    var body = {
      kind: state.kindPicked,
      base: $('v-ebase').value.trim(),
      model: $('v-emodel').value.trim(),
      voice: $('v-evoice').value.trim(),
      speed: parseFloat($('v-espeed').value) || 1,
      token: $('v-etoken').value.trim(),
      clearToken: state.clearToken,
    };
    b.disabled = true;
    b.textContent = '保存中…';
    api('settings.save', body).then(function (res) {
      state.engine = res.engine || null;
      closeEngine();
      paintEngineChip();
      toast(state.engine && state.engine.kind === 'http'
        ? '已切到 GPU 主机，下条提醒就用新音色' : '已切回本机语音');
      return loadPresets();
    }).catch(function (e) {
      toast(e.message, true);
    }).then(function () {
      b.disabled = false;
      b.textContent = '保存';
    });
  }

  // ── 模板 ──
  var TPL = {
    sit: {
      title: '久坐提醒',
      content: '你已经坐了一个小时了。起来走两步、看看窗外，腰和眼睛都会谢谢你。',
      period: 'repeat', intervalMin: 60, dayFrom: '09:00', dayTo: '18:00',
      tone: 'urge', times: 1,
    },
    water: {
      title: '喝水提醒',
      content: '该喝水了，宝贝。别等渴了才想起来 —— 身体缺水的时候，注意力早就掉了。',
      period: 'repeat', intervalMin: 90, dayFrom: '08:30', dayTo: '21:00',
      tone: 'warm', times: 1,
    },
  };

  // ── 绑定 ──
  function bind() {
    $('v-nav').addEventListener('click', function (e) {
      var b = e.target.closest('.vc-navb');
      if (!b) return;
      state.period = b.dataset.p;
      load();
    });

    $('v-all').addEventListener('change', function () {
      var on = $('v-all').checked;
      state.sel = {};
      document.querySelectorAll('.vc-card').forEach(function (card) {
        card.querySelector('.vc-ck').checked = on;
        card.classList.toggle('sel', on);
        if (on) state.sel[card.dataset.id] = true;
      });
      syncBulk();
    });

    $('v-new').addEventListener('click', function () { openDlg(null); });
    $('v-empty-new').addEventListener('click', function () { openDlg(null); });
    $('v-tpl-sit').addEventListener('click', function () { openDlg(null, TPL.sit); });
    $('v-tpl-water').addEventListener('click', function () { openDlg(null, TPL.water); });

    $('v-bulk-del').addEventListener('click', function () {
      var ids = Object.keys(state.sel);
      if (!ids.length) return;
      if (!confirm('删除选中的 ' + ids.length + ' 条提醒？')) return;
      api('delete', { ids: ids }).then(function () { toast('已删除 ' + ids.length + ' 条'); return load(); })
        .catch(function (e) { toast(e.message, true); });
    });

    $('v-bulk-toggle').addEventListener('click', function () {
      var ids = Object.keys(state.sel);
      if (!ids.length) return;
      var on = $('v-bulk-toggle').dataset.on === '1';
      api('update', { ids: ids, enabled: on }).then(function () {
        toast((on ? '已启用 ' : '已停用 ') + ids.length + ' 条');
        return load();
      }).catch(function (e) { toast(e.message, true); });
    });

    $('v-x').addEventListener('click', closeDlg);
    $('v-cancel').addEventListener('click', closeDlg);
    $('v-mask').addEventListener('click', function (e) {
      if (e.target === $('v-mask')) closeDlg();
    });
    $('v-sheet-mask').addEventListener('click', function (e) {
      if (e.target === $('v-sheet-mask')) $('v-sheet-mask').hidden = true;
    });
    $('v-sheet-x').addEventListener('click', function () { $('v-sheet-mask').hidden = true; });

    $('v-save').addEventListener('click', save);
    $('v-del').addEventListener('click', function () {
      if (!state.editing) return;
      var it = state.items.filter(function (x) { return x.id === state.editing; })[0];
      if (!it) return;
      if (!confirm('删除「' + it.title + '」？')) return;
      api('delete', { ids: [it.id] }).then(function () {
        closeDlg();
        toast('已删除');
        return load();
      }).catch(function (e) { toast(e.message, true); });
    });

    $('v-action').addEventListener('change', syncActionRow);
    $('v-preview').addEventListener('click', function () {
      var body = collectForm();
      if (!body.content && !body.title) { toast('先写点内容再试听', true); return; }
      var b = $('v-preview');
      b.disabled = true;
      b.textContent = '正在念…';
      api('play', { voice: body.voice, tone: body.tone, title: body.title, content: body.content })
        .catch(function (e) { toast(e.message, true); })
        .then(function () {
          b.disabled = false;
          b.textContent = '▶ 试听这句';
        });
    });

    // 看看卡片：到点弹窗的预览（不写库、不动排期），排版不对时不用等到点才发现
    $('v-preview-card').addEventListener('click', function () {
      var body = collectForm();
      if (!body.content && !body.title) { toast('先写点内容再看卡片', true); return; }
      var b = $('v-preview-card');
      b.disabled = true;
      api('notify.test', { title: body.title, content: body.content })
        .then(function (r) {
          if (r && r.shown === false) toast('没检测到对号桌面端，卡片只能在桌面窗口里显示', true);
          else toast('卡片已弹到屏幕右上角');
        })
        .catch(function (e) { toast(e.message, true); })
        .then(function () { b.disabled = false; });
    });

    $('v-logs').addEventListener('click', showLogs);
    $('v-voices').addEventListener('click', showVoices);

    // 播报引擎
    $('v-engine').addEventListener('click', openEngine);
    $('v-ex').addEventListener('click', closeEngine);
    $('v-ecancel').addEventListener('click', closeEngine);
    $('v-emask').addEventListener('click', function (e) {
      if (e.target === $('v-emask')) closeEngine();
    });
    $('v-ekinds').addEventListener('click', function (e) {
      var c = e.target.closest('.vc-ecard');
      if (!c) return;
      state.kindPicked = c.dataset.k;
      paintEKinds();
      syncEFields();
    });
    $('v-etest').addEventListener('click', testEngineConn);
    $('v-esave').addEventListener('click', saveEngineSettings);

    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape') {
        if (!$('v-mask').hidden) closeDlg();
        else if (!$('v-emask').hidden) closeEngine();
        else if (!$('v-sheet-mask').hidden) $('v-sheet-mask').hidden = true;
      }
    });
  }

  // ── 启动 ──
  bind();
  loadPresets()
    .then(load)
    .catch(function (e) {
      toast('加载失败：' + e.message, true);
      $('v-empty').hidden = false;
    });
})();
