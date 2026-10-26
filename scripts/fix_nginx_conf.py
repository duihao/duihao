import json, io, collections

p = 'backend/duihao/pluginc/embed/builtin/nginx.json'
raw = open(p, 'rb').read()
bom = raw.startswith(b'\xef\xbb\xbf')
d = json.loads(raw.decode('utf-8-sig'), object_pairs_hook=collections.OrderedDict)

conf = """worker_processes 1;
pid {dir}/logs/nginx.pid;
error_log {dir}/logs/error.log;

events {
    worker_connections 256;
}

http {
    # MIME 表取自运行时自带 conf/mime.types（否则 css/js/图片类型全错）
    include {runtime}/conf/mime.types;
    default_type application/octet-stream;

    # 响应编码固定 UTF-8：不写这一行，中文页面会被浏览器按 GBK 猜成乱码
    charset utf-8;
    charset_types text/html text/css application/javascript application/json text/xml application/xml text/plain;

    access_log {dir}/logs/access.log;
    client_body_temp_path {dir}/temp/client_body_temp;
    proxy_temp_path {dir}/temp/proxy_temp;
    fastcgi_temp_path {dir}/temp/fastcgi_temp;
    uwsgi_temp_path {dir}/temp/uwsgi_temp;
    scgi_temp_path {dir}/temp/scgi_temp;

    include {dir}/sites/*.conf;

    server {
        listen {port:http};
        server_name localhost;
        charset utf-8;
        location / {
            root {dir}/www;
            index index.html index.htm;
        }
    }
}
"""

index_html = """<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>对号 · Nginx</title>
<style>
  body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;
       font-family:"Segoe UI","Microsoft YaHei",system-ui,sans-serif;
       background:linear-gradient(160deg,#1a1016,#241319);color:#f3e9ee}
  .card{padding:2.25rem 2.75rem;border-radius:14px;background:#241a20;
        border:1px solid #3a2a33;box-shadow:0 12px 40px rgba(0,0,0,.4)}
  h1{margin:0 0 .625rem;font-size:1.125rem;font-weight:600;color:#F5133C}
  p{margin:.25rem 0;font-size:.8125rem;line-height:1.7;color:#c9b6c1}
  code{color:#E01A83;font-size:.8125rem}
</style>
</head>
<body>
<div class="card">
  <h1>对号 · Nginx 已就绪</h1>
  <p>监听端口 <code>{port:http}</code></p>
  <p>默认根目录 <code>{dir}/www</code></p>
  <p>站点请到「网站」面板添加，每个站点独立端口。</p>
</div>
</body>
</html>
"""

for act in d['lifecycle']['install']:
    if act.get('action') == 'write' and act.get('dst', '').endswith('nginx.conf'):
        act['from'] = conf
    if act.get('action') == 'write' and act.get('dst', '').endswith('index.html'):
        act['from'] = index_html

d['config']['signature'] = '2026-10-22-charset-utf8'

t = json.dumps(d, ensure_ascii=False, indent=2)
open(p, 'wb').write((('\ufeff' if bom else '') + t).encode('utf-8'))
print('nginx.json updated; signature =', d['config']['signature'])
