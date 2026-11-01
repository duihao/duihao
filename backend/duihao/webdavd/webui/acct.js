// acct.js 顶栏右上角「账号」块的行为：下拉关闭 + 修改密码弹窗。
//
// 只做两件事，不引任何框架：
//   1) 开合交给原生 <details>（模板页与网盘文件管理页都没有公共 JS 运行时），
//      这里只补「点空白处 / 按 Esc 关掉」和清掉浏览器默认的三角标；
//   2) 「修改密码」弹一个极简表单，直接打 /-api/users/passwd —— 后端口径见
//      apps/users：管理员重置自己不用旧密码，成员必须验旧密码。
//
// 样式随脚本注入（acct-css）：两类页面各有一套 CSS，硬编码两遍迟早走样。
(function () {
  'use strict';

  // 只注入「弹窗」这部分样式：顶栏账号块本身的样式在 shared.css / davCSS 里
  // （脚本没跑起来时 header 也不该变形），弹窗只有点了才存在，随脚本注入最省。
  var CSS = [
    '.acct-mask{position:fixed;inset:0;z-index:9999;background:rgba(0,0,0,.45);display:flex;align-items:center;justify-content:center;padding:1rem}',
    '.acct-mask[hidden]{display:none}',
    '.acct-dlg{width:min(21rem,100%);background:var(--card);color:var(--tx);border:1px solid var(--ln);border-radius:14px;',
    '  box-shadow:0 24px 60px rgba(0,0,0,.35);padding:1.1rem 1.15rem 1rem;display:flex;flex-direction:column;gap:.6rem}',
    '.acct-dlg h3{margin:0;font-size:.95rem;font-weight:600}',
    '.acct-dlg label{display:flex;flex-direction:column;gap:.25rem;font-size:.75rem;color:var(--mut)}',
    '.acct-dlg input{border:1px solid var(--ln);border-radius:8px;background:var(--hov);color:var(--tx);',
    '  padding:.42rem .5rem;font:inherit;font-size:.82rem;outline:none}',
    '.acct-dlg input:focus{border-color:var(--acc)}',
    '.acct-err{color:var(--danger);font-size:.74rem;min-height:1rem}',
    '.acct-ft{display:flex;align-items:center;justify-content:flex-end;gap:.5rem;margin-top:.2rem}',
    '.acct-ft button{padding:.35rem .85rem;border-radius:8px;border:1px solid var(--ln);background:var(--card);',
    '  color:var(--tx);font:inherit;font-size:.8rem;cursor:pointer}',
    '.acct-ft button.primary{background:var(--acc);border-color:var(--acc);color:#fff}',
    '.acct-ft button:disabled{opacity:.6;cursor:default}'
  ].join('\n');

  function injectCss() {
    if (document.getElementById('acct-css')) return;
    var st = document.createElement('style');
    st.id = 'acct-css';
    st.textContent = CSS;
    (document.head || document.documentElement).appendChild(st);
  }

  function closeMenus() {
    var open = document.querySelectorAll('details.hub-acct[open]');
    for (var i = 0; i < open.length; i++) open[i].removeAttribute('open');
  }

  // 点菜单外面（含另一个账号块）与 Esc 都收起
  document.addEventListener('click', function (e) {
    var t = e.target;
    if (t && t.closest && t.closest('details.hub-acct')) return;
    closeMenus();
  });
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') closeMenus();
  });

  // ---- 修改密码 ----------------------------------------------------------
  var mask = null;

  function buildDialog(needOld) {
    var wrap = document.createElement('div');
    wrap.className = 'acct-mask';
    wrap.innerHTML =
      '<form class="acct-dlg" autocomplete="off">' +
      '  <h3>修改密码</h3>' +
      (needOld
        ? '  <label>旧密码<input type="password" name="old" autocomplete="current-password"></label>'
        : '') +
      '  <label>新密码<input type="password" name="p1" autocomplete="new-password" placeholder="至少 6 位"></label>' +
      '  <label>确认新密码<input type="password" name="p2" autocomplete="new-password"></label>' +
      '  <div class="acct-err"></div>' +
      '  <div class="acct-ft">' +
      '    <button type="button" data-x>取消</button>' +
      '    <button type="submit" class="primary">保存</button>' +
      '  </div>' +
      '</form>';
    document.body.appendChild(wrap);
    return wrap;
  }

  function closeDialog() {
    if (mask && mask.parentNode) mask.parentNode.removeChild(mask);
    mask = null;
  }

  function openDialog() {
    injectCss();
    closeDialog();
    var role = (document.body.getAttribute('data-role') || '').trim();
    mask = buildDialog(role !== 'admin');
    var form = mask.querySelector('form');
    var err = mask.querySelector('.acct-err');
    var btn = mask.querySelector('button[type="submit"]');

    mask.addEventListener('click', function (e) {
      if (e.target === mask) closeDialog();
    });
    mask.querySelector('[data-x]').addEventListener('click', closeDialog);
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      err.textContent = '';
      var oldPw = form.old ? form.old.value : '';
      var p1 = form.p1.value;
      var p2 = form.p2.value;
      if (role !== 'admin' && !oldPw) { err.textContent = '请输入旧密码'; return; }
      if (p1.length < 6) { err.textContent = '新密码至少 6 位'; return; }
      if (p1 !== p2) { err.textContent = '两次输入的新密码不一致'; return; }
      btn.disabled = true;
      btn.textContent = '保存中…';
      fetch('/-api/users/passwd', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        // uid 不传：后端默认取当前登录账号，顺带避免越权改别人的密码
        body: JSON.stringify({ password: p1, old: oldPw })
      }).then(function (r) {
        return r.json().catch(function () { return {}; }).then(function (j) {
          if (!r.ok) throw new Error(j && j.error ? j.error : ('HTTP ' + r.status));
          return j;
        });
      }).then(function () {
        closeDialog();
        toast('密码已修改');
      }).catch(function (ex) {
        err.textContent = String(ex && ex.message ? ex.message : ex);
      }).then(function () {
        btn.disabled = false;
        btn.textContent = '保存';
      });
    });
    var first = mask.querySelector('input');
    if (first) first.focus();
  }

  // 极简 toast：不引第三方库，2 秒后自己消失
  function toast(msg) {
    var el = document.createElement('div');
    el.textContent = msg;
    el.style.cssText = 'position:fixed;left:50%;bottom:2rem;transform:translateX(-50%);z-index:10000;' +
      'background:rgba(20,20,24,.92);color:#fff;font-size:.8rem;padding:.5rem .9rem;border-radius:999px;' +
      'box-shadow:0 8px 24px rgba(0,0,0,.3)';
    document.body.appendChild(el);
    setTimeout(function () { if (el.parentNode) el.parentNode.removeChild(el); }, 2000);
  }

  document.addEventListener('click', function (e) {
    var t = e.target;
    var btn = t && t.closest ? t.closest('[data-acct="passwd"]') : null;
    if (!btn) return;
    e.preventDefault();
    closeMenus();
    openDialog();
  });

  injectCss();
})();
