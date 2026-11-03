/* 远程访问：IPv6 直连状态 / DDNS / frp 可视化配置 / 公网总开关。
   轮询 5 秒一次 status（IPv6 地址、frpc 运行态、DDNS 上次结果）。 */
(function () {
  'use strict';

  var APP = 'remote';
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
  function toast(msg, bad) {
    var t = el('div', 'vtoast' + (bad ? ' bad' : ''), msg);
    document.body.appendChild(t);
    setTimeout(function () { t.classList.add('show'); }, 10);
    setTimeout(function () {
      t.classList.remove('show');
      setTimeout(function () { t.remove(); }, 300);
    }, 3200);
  }

  function setTag(id, text, cls) {
    var n = $(id);
    n.textContent = text;
    n.className = 'rm-tag' + (cls ? ' ' + cls : '');
  }

  // ---------- 表单回填保护 ----------
  // 页面每 5 秒 refresh 一次 status，并把结果写回表单。若无条件回填，用户刚在
  // 下拉里选了「阿里云解析」还没点保存，下一轮轮询就把它拨回已保存的「腾讯
  // DNSPod」——看着就是「选不中 / 选完自动跳回去」。给会被回填的字段各挂一个
  // 「用户动过」标记：动过就不再覆盖，保存成功才清标记，让服务端值接管。
  var touched = {};
  ['r-ddns-provider', 'r-ddns-domain', 'r-ddns-sub', 'r-ddns-interval',
   'r-frp-addr', 'r-frp-port', 'r-frp-user'].forEach(function (id) {
    var e = $(id);
    if (!e) return;
    var mark = function () { touched[id] = true; };
    e.addEventListener('input', mark);
    e.addEventListener('change', mark);
  });
  function free(id) { return !touched[id]; }
  function settle(ids) { ids.forEach(function (id) { delete touched[id]; }); }

  // ---------- 隧道行 ----------

  function tunnelRow(p) {
    var row = el('div', 'rm-tunnel');
    var name = el('input'); name.value = p.name || ''; name.placeholder = '名字（如 dav）';
    var type = el('select');
    ['tcp', 'http', 'https'].forEach(function (t) {
      var o = el('option'); o.value = t; o.textContent = t; type.appendChild(o);
    });
    type.value = p.type || 'tcp';
    var lport = el('input'); lport.type = 'number'; lport.value = p.localPort || (window.lastLocalPort || 0); lport.placeholder = '本地端口';
    var extra = el('input');
    extra.placeholder = '对外端口';
    extra.value = p.remotePort || '';
    var domain = el('input');
    domain.placeholder = '访问域名（http/https 用）';
    domain.value = p.customDomain || '';
    var del = el('button', 'dl-ghost rm-del', '删');

    // 两个字段**始终可填**：tcp 用对外端口、http/https 用访问域名，另一项由后端
    // 按类型忽略（frpc.go 生成配置时只会写对应的那一项）。
    // 早先按类型把输入框 display:none，但外层 label 还占着 grid 单元格，
    // 结果列宽被空格子吃掉、剩下的输入框被挤成一条缝，看着像「填不了」。
    function refresh() {
      var isTCP = type.value === 'tcp';
      var lp = window.lastLocalPort || 18080;
      extra.placeholder = isTCP ? '如 ' + lp : '可留空';
      domain.placeholder = isTCP ? '可留空' : '如 dav.example.com';
    }
    type.addEventListener('change', refresh);
    del.addEventListener('click', function () { row.remove(); if (!$('r-tunnels').children.length) $('r-tunnel-empty').hidden = false; });
    refresh();

    row.appendChild(labeled('名字', name));
    row.appendChild(labeled('类型', type));
    row.appendChild(labeled('本地端口', lport));
    row.appendChild(labeled('对外端口', extra));
    row.appendChild(labeled('访问域名', domain));
    row.appendChild(del);
    row._get = function () {
      return {
        name: name.value.trim(),
        type: type.value,
        localPort: parseInt(lport.value, 10) || 0,
        localIP: '',
        remotePort: parseInt(extra.value, 10) || 0,
        customDomain: domain.value.trim()
      };
    };
    return row;
  }

  function labeled(text, input) {
    var l = el('label', 'rm-t');
    l.appendChild(el('span', null, text));
    l.appendChild(input);
    return l;
  }

  // ---------- 状态渲染 ----------

  var lastStatus = null;

  // 多行代码块：一个 IPv6 一条（可能不止一条），空则给一句兜底文案。
  function codeLines(el, items, empty) {
    el.textContent = '';
    if (!items || !items.length) {
      el.textContent = empty;
      return;
    }
    items.forEach(function (t) {
      var c = document.createElement('code');
      c.className = 'rm-v6-line';
      c.textContent = t;
      c.title = t;
      el.appendChild(c);
    });
  }

  function render(st) {
    lastStatus = st;

    // IPv6：本机可能有多条 GUA（PD 接口地址 + 隐私扩展临时地址），全部列出
    var v6 = st.ipv6 || {};
    var alts = (v6.addrs && v6.addrs.length) ? v6.addrs : (v6.addr ? [v6.addr] : []);
    codeLines($('r-v6-addr'), alts, '没有检测到公网 IPv6');
    $('r-v6-out').textContent = v6.addr ? (v6.out ? '通' : '有地址但出不去（多半是光猫/路由没开 IPv6）') : '—';
    setTag('r-v6-tag', v6.addr ? (v6.out ? '可用' : '地址有 · 出不去') : '本机没有', v6.addr && v6.out ? 'ok' : (v6.addr ? 'warn' : ''));
    codeLines(
      $('r-v6-url'),
      st.local ? alts.map(function (a) { return 'https://[' + a + ']:' + st.local + '/' }) : [],
      '—（端口在桌面端「连接管理」里看）'
    );

    // DDNS
    var d = st.ddns || {};
    if (free('r-ddns-provider')) $('r-ddns-provider').value = d.provider || 'dnspod';
    if (free('r-ddns-domain')) $('r-ddns-domain').value = d.domain || '';
    if (free('r-ddns-sub')) $('r-ddns-sub').value = d.sub || '';
    if (free('r-ddns-interval')) $('r-ddns-interval').value = d.interval || 10;
    $('r-ddns-token').placeholder = d.tokenSet ? '已保存（密文），留空 = 不改' : 'DNSPod：ID,Token 两段 ／ 阿里云：AccessKeyID:Secret';
    if (d.domain) {
      var ok = !!d.lastOK;
      setTag('r-ddns-tag', ok ? '正常' : (d.lastMsg ? '有异常' : '已配置'), ok ? 'ok' : 'warn');
      $('r-ddns-last').textContent = d.lastAt ? (d.lastAt + ' · ' + (d.lastMsg || '')) : '';
    }

    // 动态域名访问（IPv6 卡那一行）：取右侧 DDNS 卡**已保存**的域名（status
    // 服务端值，不是表单草稿），按后端 fullDomain 同一口径拼 FQDN（sub 空 / @
    // = 根域）。没配域名就提示去右侧配；配了但上次更新失败就补一句状态提醒。
    var fqdn = d.domain ? (d.sub && d.sub !== '@' ? d.sub + '.' + d.domain : d.domain) : '';
    if (fqdn) {
      $('r-v6-ddns-url').textContent = 'https://' + fqdn + (st.local ? ':' + st.local : '') + '/'
        + (d.lastOK ? '' : '（域名还没更新成功，看右侧提示）');
    } else {
      $('r-v6-ddns-url').textContent = '—（在右侧配置 DDNS 域名后显示）';
    }

    // frpc
    var f = st.frpc || {};
    var comp = f.comp || {};
    $('r-frp-setup').hidden = comp.state === 'ready';
    $('r-frp-comp-hint').textContent = comp.state === 'downloading' ? '正在下载…'
      : (comp.state === 'error' ? ('组件安装失败：' + (comp.error || '')) : '');
    if (free('r-frp-addr')) $('r-frp-addr').value = (f.server || {}).addr || '';
    if (free('r-frp-port')) $('r-frp-port').value = (f.server || {}).port || 7000;
    if (free('r-frp-user')) $('r-frp-user').value = (f.server || {}).user || '';
    $('r-frp-token').placeholder = (f.server || {}).tokenSet ? '已保存（密文），留空 = 不改' : 'frps 的 auth.token';
    $('r-frp-tls').checked = f.tls !== false;
    $('r-frp-enabled').checked = !!f.enabled;
    if (st.local) window.lastLocalPort = st.local;

    if (!$('r-tunnels').children.length && (f.proxies || []).length) {
      f.proxies.forEach(function (p) { $('r-tunnels').appendChild(tunnelRow(p)); });
      $('r-tunnel-empty').hidden = true;
    }
    setTag('r-frp-tag', f.running ? '运行中' : (f.enabled ? '已启用 · 未运行' : '未配置'), f.running ? 'ok' : (f.enabled ? 'warn' : ''));
    $('r-frp-run').textContent = f.running ? ('已连上 ' + ((f.server || {}).addr || '') + ':' + ((f.server || {}).port || ''))
      : (f.startErr ? ('启动失败：' + f.startErr) : '');

    // 公网开关
    var on = !!(st.public && st.public.enabled);
    if (document.activeElement !== $('r-public')) $('r-public').checked = on;
    $('r-public-label').textContent = on ? '开（公网可达）' : '关（只许局域网）';
  }

  function refresh() { api('status').then(render).catch(function () {}); }

  // ---------- 事件 ----------

  $('r-ddns-save').addEventListener('click', function () {
    api('ddns.save', {
      provider: $('r-ddns-provider').value,
      domain: $('r-ddns-domain').value.trim(),
      sub: $('r-ddns-sub').value.trim(),
      token: $('r-ddns-token').value.trim(),
      interval: parseInt($('r-ddns-interval').value, 10) || 10
    }).then(function () {
      $('r-ddns-token').value = '';
      settle(['r-ddns-provider', 'r-ddns-domain', 'r-ddns-sub', 'r-ddns-interval']);
      toast('DDNS 配置已保存');
      refresh();
    }).catch(function (e) { toast(e.message, true); });
  });

  $('r-ddns-test').addEventListener('click', function () {
    var b = $('r-ddns-test');
    b.disabled = true;
    api('ddns.test', {
      provider: $('r-ddns-provider').value,
      domain: $('r-ddns-domain').value.trim(),
      sub: $('r-ddns-sub').value.trim(),
      token: $('r-ddns-token').value.trim()
    }).then(function (r) {
      toast(r.msg || (r.ok ? '测试通过' : '测试失败'), !r.ok);
    }).catch(function (e) { toast(e.message, true); })
      .then(function () { b.disabled = false; });
  });

  $('r-ddns-now').addEventListener('click', function () {
    api('ddns.update').then(function (r) { toast(r.msg || '已更新'); refresh(); })
      .catch(function (e) { toast(e.message, true); });
  });

  $('r-tunnel-add').addEventListener('click', function () {
    $('r-tunnels').appendChild(tunnelRow({ type: 'tcp', localPort: window.lastLocalPort || 0 }));
    $('r-tunnel-empty').hidden = true;
  });

  $('r-frp-install').addEventListener('click', function () {
    var b = $('r-frp-install');
    b.disabled = true;
    api('comp.install').then(function (r) {
      toast(r.state === 'ready' ? 'frpc 已就绪' : '状态：' + r.state);
      refresh();
    }).catch(function (e) { toast(e.message, true); })
      .then(function () { b.disabled = false; });
  });

  $('r-frp-import').addEventListener('click', function () {
    var zip = window.prompt('输入 frp 离线 zip 的完整路径（官网下载页有说明）');
    if (!zip) return;
    api('comp.import', { zip: zip }).then(function () { toast('导入成功'); refresh(); })
      .catch(function (e) { toast(e.message, true); });
  });

  $('r-frp-save').addEventListener('click', function () {
    var proxies = [];
    var rows = $('r-tunnels').children;
    for (var i = 0; i < rows.length; i++) proxies.push(rows[i]._get());
    var b = $('r-frp-save');
    b.disabled = true;
    api('frpc.save', {
      enabled: $('r-frp-enabled').checked,
      tlsEnable: $('r-frp-tls').checked,
      server: {
        addr: $('r-frp-addr').value.trim(),
        port: parseInt($('r-frp-port').value, 10) || 7000,
        user: $('r-frp-user').value.trim()
      },
      token: $('r-frp-token').value.trim(),
      proxies: proxies
    }).then(function (r) {
      $('r-frp-token').value = '';
      settle(['r-frp-addr', 'r-frp-port', 'r-frp-user']);
      toast(r.msg || '已保存');
      $('r-tunnels').innerHTML = '';
      refresh();
    }).catch(function (e) { toast(e.message, true); })
      .then(function () { b.disabled = false; });
  });

  $('r-frp-stop').addEventListener('click', function () {
    api('frpc.stop').then(function () { toast('frpc 已停止'); refresh(); })
      .catch(function (e) { toast(e.message, true); });
  });

  $('r-public').addEventListener('change', function () {
    var on = $('r-public').checked;
    if (on && !window.confirm('确定要向公网开放吗？\n\n打开后，任何知道你地址的人都能看到登录页。'
      + '请确认登录密码足够强（登录连错 5 次会锁 15 分钟）。')) {
      $('r-public').checked = false;
      return;
    }
    api('public.save', { enabled: on }).then(function () {
      toast(on ? '公网访问已开启' : '公网访问已关闭');
      refresh();
    }).catch(function (e) { toast(e.message, true); });
  });

  refresh();
  setInterval(refresh, 5000);
})();
