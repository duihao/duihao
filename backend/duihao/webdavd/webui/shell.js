/* 个人空间页面公共脚本：统一 API 调用（Cookie 会话）、错误提示、自检按钮。 */
(function(){
  'use strict';

  async function hubAPI(app, action, body){
    var url = '/-api/' + app + '/' + action;
    var r = await fetch(url, {
      method: body === undefined ? 'GET' : 'POST',
      headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body)
    });
    if (r.status === 401) { location.reload(); throw new Error('登录已过期'); }
    var ct = r.headers.get('Content-Type') || '';
    var data = ct.indexOf('application/json') >= 0 ? await r.json() : await r.text();
    if (!r.ok) {
      var msg = (data && data.error) ? data.error : ('请求失败（' + r.status + '）');
      throw new Error(msg);
    }
    return data;
  }

  // 管理员视角切换器已按需求移除（后端 as= 通道保留，仅不出现在界面上）。

  function installPing(){
    var btn = document.getElementById('ping');
    var out = document.getElementById('out');
    if (!btn || !out) return;
    btn.addEventListener('click', async function(){
      var app = document.body.getAttribute('data-app') || '';
      try {
        var j = await hubAPI(app, 'ping');
        out.textContent = JSON.stringify(j, null, 2);
      } catch (e) {
        out.textContent = '接口自检失败：' + e.message;
      }
    });
  }

  window.hubAPI = hubAPI;
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function(){ installPing(); });
  } else {
    installPing();
  }
})();
