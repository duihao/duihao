/* NAS 网页主题跟随（M24）。
 *
 * 页面首帧的 <html data-theme> 由服务端写死 —— 打开页面就是对的，不会先白
 * 后黑闪一下。这里只负责「增量」：应用里切了主题，已经开着的页面要跟着变。
 *
 * 走 SSE 而不是轮询：主题变化是低频事件，轮询要么慢要么白烧 CPU。EventSource
 * 自带断线重连（3s 起），服务端每 15s 发一次注释行保活。 */
(function () {
  if (window.EventSource === undefined) return;
  var es;
  try {
    es = new EventSource('/-api/theme/stream');
  } catch (e) {
    return;
  }
  es.onmessage = function (ev) {
    var d = null;
    try {
      d = JSON.parse(ev.data);
    } catch (e) {
      return;
    }
    if (d && d.theme) document.documentElement.setAttribute('data-theme', d.theme);
  };
})();
