/* 桌面窗口外壳：给「对号 NAS」内置窗口画右上角的 最小化 / 最大化 / 关闭，
 * 并让顶栏空白处可以拖动窗口。
 *
 * 背景：这些模块页在客户端里是 Frameless 窗口打开的（没有原生标题栏，见
 * app_window.go），页面拿不到 Wails 运行时，所以按钮和拖动都走本机 HTTP 桥
 * /-shell/<act>?w=<token>（见 webdavd/shell.go）。票由开窗时追加在 URL 上
 * （_dw=…）；用手机或电脑浏览器打开同一页面时没有票，脚本第一段就 return，
 * 页面一个像素都不变。
 */
(function () {
  'use strict';

  // ── 票：首次带 _dw 进页面时记进 sessionStorage，之后在模块间跳转也不会丢 ──
  var token = '';
  try {
    var q = new URLSearchParams(location.search);
    var t = q.get('_dw');
    if (t && /^[0-9a-f]{32}$/.test(t)) {
      token = t;
      sessionStorage.setItem('dh.win', t);
    } else {
      var saved = sessionStorage.getItem('dh.win') || '';
      if (/^[0-9a-f]{32}$/.test(saved)) token = saved;
    }
  } catch (e) { /* 隐私模式：当成浏览器打开 */ }
  if (!token) return;

  var BAR_SEL = '.hub-top';

  // 发一个窗口动作。cb 拿到的是执行**之后**的真实最大化状态（Go 那边查的
  // IsMaximised），「最大化 / 向下还原」的图标一律照它画 —— 前端自己翻转会走散。
  function shell(act, cb) {
    // 不 await：拖动是阻塞式的一次系统调用，等它回来这一帧就僵住了
    try {
      fetch('/-shell/' + act + '?w=' + token, { method: 'POST', keepalive: true })
        .then(function (r) { return r.ok ? r.json() : null; })
        .then(function (j) { if (j && cb) cb(!!j.max); })
        .catch(function () { /* 主程序已退出：什么都不用做 */ });
    } catch (e) { /* 同上 */ }
  }

  // 图标 10×10、1.1 描边：与 Windows 11 标题栏按钮的视觉重量对齐
  var ICON_MIN = '<svg viewBox="0 0 10 10" width="10" height="10" aria-hidden="true">' +
    '<path d="M1.5 5.5h7" stroke="currentColor" stroke-width="1.1" stroke-linecap="round"/></svg>';
  var ICON_MAX = '<svg viewBox="0 0 10 10" width="10" height="10" aria-hidden="true">' +
    '<rect x="1.6" y="1.6" width="6.8" height="6.8" fill="none" stroke="currentColor" stroke-width="1.1"/></svg>';
  var ICON_RESTORE = '<svg viewBox="0 0 10 10" width="10" height="10" aria-hidden="true">' +
    '<rect x="1.4" y="3.1" width="5.5" height="5.5" fill="none" stroke="currentColor" stroke-width="1.1"/>' +
    '<path d="M3.4 3.1V1.4h5.2v5.2H6.9" fill="none" stroke="currentColor" stroke-width="1.1"/></svg>';
  var ICON_CLOSE = '<svg viewBox="0 0 10 10" width="10" height="10" aria-hidden="true">' +
    '<path d="M1.8 1.8l6.4 6.4M8.2 1.8L1.8 8.2" stroke="currentColor" stroke-width="1.1" stroke-linecap="round"/></svg>';

  var STYLE = [
    // 圆角 12px = 主窗的 --win-radius（frontend/src/style.css: 0.75rem）。模块窗是
    // 透明窗体，原生那一层没有任何底色与圆角，全靠这几条画：背景色块 + 顶栏两角。
    // 不这么画就只剩 DWM 的 8px 圆角，两窗并排能看出模块窗的角更小
    //（用户 2026-11-02 要求「弹出窗圆角与主窗一致」）。
    ':root{--win-r:12px}',
    'html.is-shell,body.is-shell{background:transparent}',
    'body.is-shell::before{content:"";position:fixed;inset:0;background:var(--bg);' +
    'border-radius:var(--win-r);z-index:-1;pointer-events:none}',
    // 顶栏接管标题栏：贴边、撑高、不可选中；右侧留出按钮的位置。
    // border-bottom:0 是必须的 —— 主窗标题栏（NasStandalone 的 .nas-bar）只靠
    // 底色差分层、没有分割线，模块窗若留着那条 1px 线，两窗并排一眼就能看出
    // 不是同一个壳（用户 2026-11-02 指出）。
    'body.is-shell .hub-top{padding:0 0 0 1.25rem;min-height:2.9rem;border-bottom:0;' +
    'border-radius:var(--win-r) var(--win-r) 0 0;' +
    '-webkit-user-select:none;user-select:none;cursor:default}',
    // 最大化 / 贴边铺满时直角（主窗同样处理：窗口事件切 html.win-square）
    'html.win-square body.is-shell::before,html.win-square body.is-shell .hub-top{border-radius:0}',
    '.win-ctl{display:none;align-self:stretch;align-items:stretch;margin:0 0 0 .35rem}',
    'body.is-shell .win-ctl{display:flex}',
    '.win-btn{display:flex;align-items:center;justify-content:center;width:46px;min-width:46px;' +
    'border:0;border-radius:0;background:transparent;color:var(--mut);padding:0;cursor:default;' +
    'transition:background-color .12s ease,color .12s ease}',
    '.win-btn:hover{background:var(--hov);color:var(--tx)}',
    '.win-btn:active{background:color-mix(in srgb,var(--hov) 72%,#000)}',
    '.win-btn.close:hover{background:#e81123;color:#fff}',
    '.win-btn.close:active{background:#c50f1f;color:#fff}',
    '.win-btn svg{display:block;pointer-events:none}'
  ].join('');

  var maxBtn = null;

  // 按真实状态画图标：maximised=true → 叠框（向下还原），false → 单框（最大化）
  function setMaxIcon(maximised) {
    // 最大化时窗口贴边，圆角要收掉（主窗同样是「最大化→直角」）
    document.documentElement.classList.toggle('win-square', !!maximised);
    if (!maxBtn) return;
    maxBtn.innerHTML = maximised ? ICON_RESTORE : ICON_MAX;
    maxBtn.title = maximised ? '向下还原' : '最大化';
    maxBtn.setAttribute('aria-label', maxBtn.title);
  }

  function build(bar) {
    var box = document.createElement('div');
    box.className = 'win-ctl';

    var min = document.createElement('button');
    min.type = 'button';
    min.className = 'win-btn';
    min.title = '最小化';
    min.setAttribute('aria-label', '最小化');
    min.innerHTML = ICON_MIN;

    var max = document.createElement('button');
    max.type = 'button';
    max.className = 'win-btn';
    max.title = '最大化';
    max.setAttribute('aria-label', '最大化');
    max.innerHTML = ICON_MAX;

    var close = document.createElement('button');
    close.type = 'button';
    close.className = 'win-btn close';
    close.title = '关闭';
    close.setAttribute('aria-label', '关闭');
    close.innerHTML = ICON_CLOSE;

    min.addEventListener('click', function (e) { e.stopPropagation(); shell('min'); });
    max.addEventListener('click', function (e) {
      e.stopPropagation();
      shell('max', setMaxIcon);
    });
    close.addEventListener('click', function (e) { e.stopPropagation(); shell('close'); });

    box.appendChild(min);
    box.appendChild(max);
    box.appendChild(close);
    bar.appendChild(box);
    maxBtn = max;
    return box;
  }

  // 顶栏按下即拖动：交给系统按「按住标题栏」处理，贴边 / 双击最大化 / 拖到
  // 另一块屏都由 Windows 自己算，比在 JS 里算坐标发 SetPosition 稳得多。
  var lastDown = 0;

  function bindDrag(bar) {
    bar.addEventListener('mousedown', function (e) {
      if (e.button !== 0) return;
      var el = e.target;
      // 按钮、链接、账号下拉、输入框照常点，不要变成拖窗口
      if (el && el.closest && el.closest('.win-ctl,a,button,summary,details,input,select,textarea,label')) return;
      e.preventDefault();

      var now = Date.now();
      if (now - lastDown < 320) {
        // 同一处连按两下 = 双击标题栏：最大化 / 还原（不做拖动，否则第一下
        // 就被系统接管，dblclick 事件永远不会来）
        lastDown = 0;
        var max = bar.querySelector('.win-btn:nth-child(2)');
        if (max) max.click();
        return;
      }
      lastDown = now;
      shell('drag');
    });
  }

  function init() {
    var bar = document.querySelector(BAR_SEL);
    if (!bar) return;
    var s = document.createElement('style');
    s.id = 'win-ctl-style';
    s.textContent = STYLE;
    document.head.appendChild(s);
    document.body.classList.add('is-shell');
    document.documentElement.classList.add('is-shell');
    build(bar);
    bindDrag(bar);
    if (maxBtn) maxBtn.blur();

    // 首帧对表：窗口可能本来就是最大化状态（上一页最大化后切过来的），
    // 不查一下就会画成「最大化」——用户说的「图标用反了」正是这个。
    shell('state', setMaxIcon);
    // 拖拽还原、双击标题栏、Win+↑、贴边吸附都是系统直接改窗口，
    // 尺寸一变就重新对表一次（防抖，避免拖动缩放时打满请求）。
    var tmr = 0;
    window.addEventListener('resize', function () {
      clearTimeout(tmr);
      tmr = setTimeout(function () { shell('state', setMaxIcon); }, 160);
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init, { once: true });
  } else {
    init();
  }
})();
