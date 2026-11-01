/* 用户管理：列表 + 新建/编辑（角色 + 每模块读写）+ 改密 + 删除。 */
(function () {
  'use strict';

  var MODULE_NAMES = {
    files: '网盘', video: '影视', music: '音乐', download: '下载', notes: '笔记', ledger: '账本',
    health: '健康', home: '收藏夹', voice: '语音提醒', monitor: '监控', remote: '远程'
  };
  var rows = document.getElementById('u-rows');
  var editor = document.getElementById('u-editor');
  var edTitle = document.getElementById('u-ed-title');
  var edUID = document.getElementById('u-ed-uid');
  var fName = document.getElementById('u-f-name');
  var fPass = document.getElementById('u-f-pass');
  var fRole = document.getElementById('u-f-role');
  var fPerms = document.getElementById('u-f-perms');
  var me = '';
  var users = [];
  // 编辑态：null = 新建；否则记录目标 uid（改密/改权限/改名都走它）
  var editing = null;
  // 本次打开是否只改密码（改密模式不动名字/角色/权限）
  var edPassOnly = false;
  // 本次编辑的账号对象（判断 admin / 我自己）
  var edUser = null;

  function buildPermBoxes(perms) {
    fPerms.innerHTML = '';
    var list = document.createElement('div');
    list.className = 'u-perm-grid';
    (MODULE_ORDER || []).forEach(function (m) {
      var cur = (perms && perms[m]) || '';
      var line = document.createElement('label');
      line.className = 'u-perm';
      var sel = document.createElement('select');
      sel.dataset.module = m;
      [['', '无权限'], ['r', '只读'], ['rw', '读写']].forEach(function (pair) {
        var o = document.createElement('option');
        o.value = pair[0]; o.textContent = pair[1];
        if (cur === pair[0]) o.selected = true;
        sel.appendChild(o);
      });
      line.appendChild(document.createTextNode(MODULE_NAMES[m] || m));
      line.appendChild(sel);
      list.appendChild(line);
    });
    fPerms.appendChild(list);
  }

  function collectPerms() {
    var out = {};
    fPerms.querySelectorAll('select[data-module]').forEach(function (sel) {
      // 空值也要落库：显式「无权限」（files 缺键会被当成可读的兼容兜底）
      out[sel.dataset.module] = sel.value;
    });
    return out;
  }

  function render() {
    rows.innerHTML = '';
    if (!users.length) {
      rows.innerHTML = '<tr><td colspan="5" class="muted">还没有账号</td></tr>';
      return;
    }
    users.forEach(function (u) {
      var tr = document.createElement('tr');
      function td(text, cls) {
        var d = document.createElement('td');
        if (cls) d.className = cls;
        d.textContent = text;
        return d;
      }
      tr.appendChild(td(u.uid + (u.self ? '（我）' : ''), 'u-name'));
      tr.appendChild(td(u.role === 'admin' ? '管理员' : '成员'));
      tr.appendChild(td(u.enabled ? '启用' : '停用', u.enabled ? '' : 'muted'));
      tr.appendChild(td(u.createdAt || '—', 'muted'));
      var ops = document.createElement('td');
      ops.className = 'u-ops';
      function btn(text, fn, primary) {
        var b = document.createElement('button');
        b.className = 'u-link' + (primary ? ' primary' : '');
        b.textContent = text;
        b.addEventListener('click', fn);
        ops.appendChild(b);
      }
      btn('编辑', function () { openEditor(u); });
      btn('改密', function () { openEditor(u, true); });
      if (!u.self) btn(u.enabled ? '停用' : '启用', function () { toggle(u); });
      if (!u.self) btn('删除', function () { del(u); }, true);
      tr.appendChild(ops);
      rows.appendChild(tr);
    });
  }

  function openEditor(u, passOnly) {
    editing = u ? u.uid : null;
    edPassOnly = !!passOnly;
    edUser = u || null;
    edTitle.textContent = editing ? (passOnly ? '修改密码' : '编辑用户') : '新建用户';
    edUID.textContent = editing || '';
    fName.value = editing || '';
    // 用户名随时可改（含第一个 admin）：改名会把账号和各模块数据一起迁到新名
    fName.disabled = !!passOnly;
    fName.placeholder = passOnly ? '' : '改名后原账号的数据一并迁移';
    fPass.value = '';
    fPass.placeholder = editing ? '留空 = 不改密码' : '至少 6 位';
    fRole.value = (u && u.role) || 'member';
    buildPermBoxes(u ? u.perms : defaultFullPerms());
    syncPermVisibility();
    editor.hidden = false;
    editor.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  }

  function defaultFullPerms() {
    var out = {};
    (MODULE_ORDER || []).forEach(function (m) { out[m] = 'rw'; });
    return out;
  }

  function syncPermVisibility() {
    fPerms.hidden = fRole.value === 'admin';
  }

  fRole.addEventListener('change', syncPermVisibility);
  document.getElementById('u-cancel').addEventListener('click', function () { editor.hidden = true; });

  document.getElementById('u-new').addEventListener('click', function () { openEditor(null); });
  document.getElementById('u-mypass').addEventListener('click', function () {
    var self = null;
    users.forEach(function (u) { if (u.self) self = u; });
    openEditor(self || { uid: me, role: 'member' }, true);
  });

  document.getElementById('u-save').addEventListener('click', async function () {
    try {
      if (!editing) {
        await hubAPI('users', 'create', {
          name: fName.value.trim(), password: fPass.value,
          role: fRole.value, perms: collectPerms()
        });
      } else {
        var target = editing;
        // 改名先行（账号+各模块数据一起迁），后续更新全部指向新名
        var newName = fName.value.trim();
        if (!edPassOnly && newName && newName !== editing) {
          await hubAPI('users', 'rename', { uid: editing, name: newName });
          target = newName;
        }
        if (fPass.value) {
          await hubAPI('users', 'passwd', { uid: target, password: fPass.value });
        }
        if (!edPassOnly) {
          if (fRole.value === 'admin') {
            await hubAPI('users', 'update', { uid: target, role: 'admin', perms: {}, enabled: true });
          } else {
            // 编辑成员：角色 + 权限 + 启用一起提交
            var u = users.find(function (x) { return x.uid === editing; }) || {};
            await hubAPI('users', 'update', {
              uid: target, role: 'member', perms: collectPerms(),
              enabled: u.enabled !== false
            });
          }
        }
        if (target !== editing && editing === me) {
          // 改了自己的用户名：旧会话已失效，回登录页用新名重新进
          location.href = '/-logout';
          return;
        }
      }
      editor.hidden = true;
      await load();
    } catch (e) {
      alert(e.message || '保存失败');
    }
  });

  async function toggle(u) {
    try {
      await hubAPI('users', 'update', {
        uid: u.uid, role: u.role, perms: u.perms || {}, enabled: !u.enabled
      });
      await load();
    } catch (e) { alert(e.message || '操作失败'); }
  }

  async function del(u) {
    if (!confirm('删除用户「' + u.uid + '」？其数据（笔记/账本等按账号隔离）保留但无法登录。')) return;
    try {
      await hubAPI('users', 'delete', { uid: u.uid });
      await load();
    } catch (e) { alert(e.message || '删除失败'); }
  }

  var MODULE_ORDER = null;

  async function load() {
    try {
      var j = await hubAPI('users', 'list');
      me = j.me || '';
      users = j.users || [];
      if (!MODULE_ORDER && j.modules) MODULE_ORDER = j.modules;
      render();
    } catch (e) {
      rows.innerHTML = '';
      var tr = document.createElement('tr');
      var td = document.createElement('td');
      td.colSpan = 5; td.className = 'muted'; td.textContent = '加载失败：' + (e.message || e);
      tr.appendChild(td); rows.appendChild(tr);
    }
  }

  load();
})();
