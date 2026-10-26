# -*- coding: utf-8 -*-
"""M8：给 nginx/mysql/redis/php 四份内置清单加 settings 节 + {set:*} token 化模板。"""
import json, io, os, collections

BASE = os.path.join(os.path.dirname(__file__), '..', 'backend', 'duihao', 'pluginc', 'embed', 'builtin')

def load(name):
    p = os.path.join(BASE, name)
    raw = open(p, 'rb').read()
    bom = raw.startswith(b'\xef\xbb\xbf')
    return json.loads(raw.decode('utf-8-sig'), object_pairs_hook=collections.OrderedDict), bom

def save(name, d, bom):
    p = os.path.join(BASE, name)
    t = json.dumps(d, ensure_ascii=False, indent=2)
    open(p, 'wb').write((('\ufeff' if bom else '') + t).encode('utf-8'))
    print('updated', name)

def F(key, label, type, default='', **kw):
    f = collections.OrderedDict([('key', key), ('label', label), ('type', type)])
    if default != '': f['default'] = default
    f.update(kw)
    return f

# ───────────────────────── MySQL ─────────────────────────
m, bom = load('mysql.json')
ini = (
"[mysqld]\n"
"basedir={runtime}\n"
"datadir={dir}/data\n"
"port={port:db}\n"
"bind-address=127.0.0.1\n"
"character-set-server={set:mysql.charset}\n"
"default-storage-engine={set:mysql.engine}\n"
"{setif:mysql.skip_name_resolve:skip-name-resolve}\n"
"{setif:mysql.skip_networking:skip-networking}\n"
"{setif:mysql.log_error:log-error={dir}/logs/mysql.err}\n"
"{setif:mysql.binlog:log-bin=mysql-bin}\n"
"{setif:mysql.slow_query_log:slow_query_log=1\nlong_query_time=2}\n"
"max_connections={set:mysql.max_connections}\n"
"thread_cache_size={set:mysql.thread_cache_size}\n"
"table_open_cache={set:mysql.table_open_cache}\n"
"wait_timeout={set:mysql.wait_timeout}\n"
"sort_buffer_size={set:mysql.sort_buffer_size}K\n"
"read_buffer_size={set:mysql.read_buffer_size}K\n"
"max_allowed_packet={set:mysql.max_allowed_packet}M\n"
"tmp_table_size={set:mysql.tmp_table_size}M\n"
"max_heap_table_size={set:mysql.max_heap_table_size}M\n"
"key_buffer_size={set:mysql.key_buffer_size}M\n"
"innodb_buffer_pool_size={set:mysql.innodb_buffer_pool_size}M\n"
"innodb_log_buffer_size={set:mysql.innodb_log_buffer_size}M\n"
"innodb_redo_log_capacity={set:mysql.innodb_redo_log_capacity}M\n"
"innodb_flush_log_at_trx_commit={set:mysql.innodb_flush_log_at_trx_commit}\n"
"innodb_lock_wait_timeout={set:mysql.innodb_lock_wait_timeout}\n"
"myisam_max_sort_file_size={set:mysql.myisam_max_sort_file_size}G\n"
"myisam_sort_buffer_size={set:mysql.myisam_sort_buffer_size}M\n"
"\n[client]\n"
"port={port:db}\n"
"default-character-set={set:mysql.charset}\n"
)
m['lifecycle']['install'][1]['from'] = ini
m['config'] = {'signature': '2026-10-22-m8-settings'}
m['settings'] = {
    'configFile': 'my.ini',
    'restart': True,
    'groups': [
        {'title': '基本配置', 'fields': [
            F('mysql.port', '启动端口', 'number', '3306', portRole='db', min=1, max=65535),
            F('mysql.charset', '字符集', 'select', 'utf8mb4', options=['utf8mb4', 'utf8', 'gbk', 'latin1']),
            F('mysql.engine', '默认引擎', 'select', 'InnoDB', options=['InnoDB', 'MyISAM']),
            F('mysql.skip_name_resolve', '禁止域名解析', 'switch', '1', hint='跳过主机名解析，连接更快'),
            F('mysql.skip_networking', '禁止 TCP 连接', 'switch', '0', hint='仅允许本机命名管道/共享内存'),
            F('mysql.log_error', '错误日志', 'switch', '1'),
            F('mysql.binlog', 'BIN 日志', 'switch', '0'),
            F('mysql.slow_query_log', '慢查询日志', 'switch', '0'),
        ]},
        {'title': '性能配置', 'fields': [
            F('mysql.max_connections', '最大连接', 'number', '1000', min=10, max=100000),
            F('mysql.thread_cache_size', '线程缓存', 'number', '16'),
            F('mysql.table_open_cache', '表缓存数', 'number', '256'),
            F('mysql.wait_timeout', '等待超时', 'number', '120', unit='秒'),
            F('mysql.sort_buffer_size', '排序缓冲', 'number', '256', unit='K'),
            F('mysql.read_buffer_size', '预读缓冲', 'number', '512', unit='K'),
            F('mysql.max_allowed_packet', '单数据包', 'number', '16', unit='M'),
            F('mysql.tmp_table_size', '临时表量', 'number', '64', unit='M'),
            F('mysql.max_heap_table_size', '最大表量', 'number', '64', unit='M'),
            F('mysql.key_buffer_size', '索引缓冲', 'number', '32', unit='M'),
        ]},
        {'title': '参数配置', 'fields': [
            F('mysql.innodb_thread_concurrency', 'INNODB 并发线程', 'number', '0', hint='0 = 不限制'),
            F('mysql.innodb_flush_log_at_trx_commit', '日志清洗', 'select', '1', options=['0', '1', '2'],
              hint='1=每次提交落盘(最安全) 2=每秒 0=不控'),
            F('mysql.innodb_lock_wait_timeout', '锁定超时', 'number', '120', unit='秒'),
            F('mysql.innodb_buffer_pool_size', 'INNODB 缓冲大小', 'number', '128', unit='M'),
            F('mysql.innodb_log_buffer_size', 'INNODB 日志缓存', 'number', '16', unit='M'),
            F('mysql.innodb_redo_log_capacity', 'INNODB 日志大小', 'number', '256', unit='M'),
            F('mysql.myisam_max_sort_file_size', 'MYISAM 最大文件', 'number', '64', unit='G'),
            F('mysql.myisam_sort_buffer_size', 'MYISAM 缓冲大小', 'number', '32', unit='M'),
        ]},
    ],
}
save('mysql.json', m, bom)

# ───────────────────────── Redis ─────────────────────────
r, bom = load('redis.json')
conf = (
"bind {set:redis.bind}\n"
"port {port:redis}\n"
"timeout {set:redis.timeout}\n"
"maxclients {set:redis.maxclients}\n"
"databases {set:redis.databases}\n"
"maxmemory {set:redis.maxmemory}mb\n"
"{setif:redis.requirepass:requirepass {set:redis.requirepass}}\n"
"dir {dir}/data\n"
"save \"\"\n"
"appendonly no\n"
"protected-mode yes\n"
"maxmemory-policy allkeys-lru\n"
)
r['lifecycle']['install'][1]['from'] = conf
r['config'] = {'signature': '2026-10-22-m8-settings'}
r['settings'] = {
    'configFile': 'redis.conf',
    'restart': True,
    'groups': [
        {'title': '基本配置', 'fields': [
            F('redis.bind', '绑定 IP', 'text', '127.0.0.1', hint='0.0.0.0 = 允许局域网访问（注意安全）'),
            F('redis.port', '绑定端口', 'number', '6379', portRole='redis', min=1, max=65535),
            F('redis.timeout', '连接超时', 'number', '65', unit='秒', hint='0 = 不限制'),
            F('redis.maxclients', '最大连接数', 'number', '10000'),
            F('redis.databases', '数据库数量', 'number', '16'),
            F('redis.requirepass', '密码', 'password', '', hint='留空 = 无密码'),
            F('redis.maxmemory', '最大内存', 'number', '1024', unit='MB'),
        ]},
    ],
}
save('redis.json', r, bom)

# ───────────────────────── Nginx ─────────────────────────
n, bom = load('nginx.json')
conf_tpl = (
"worker_processes {set:nginx.worker_processes};\n"
"pid {dir}/logs/nginx.pid;\n"
"error_log {dir}/logs/error.log {set:nginx.error_log_level};\n"
"{setif:nginx.server_names_hash:server_names_hash_bucket_size {set:nginx.server_names_hash};}\n"
"\n"
"events {\n"
"    worker_connections {set:nginx.worker_connections};\n"
"}\n"
"\n"
"http {\n"
"    # MIME 表取自运行时自带 conf/mime.types（否则 css/js/图片类型全错）\n"
"    include {runtime}/conf/mime.types;\n"
"    default_type application/octet-stream;\n"
"\n"
"    # 响应编码固定 UTF-8：不写这一行，中文页面会被浏览器按 GBK 猜成乱码\n"
"    charset utf-8;\n"
"\n"
"    access_log {dir}/logs/access.log;\n"
"    client_body_temp_path {dir}/temp/client_body_temp;\n"
"    proxy_temp_path {dir}/temp/proxy_temp;\n"
"    fastcgi_temp_path {dir}/temp/fastcgi_temp;\n"
"    uwsgi_temp_path {dir}/temp/uwsgi_temp;\n"
"    scgi_temp_path {dir}/temp/scgi_temp;\n"
"\n"
"    client_max_body_size {set:nginx.client_max_body_size}m;\n"
"    keepalive_timeout {set:nginx.keepalive_timeout}s;\n"
"    keepalive_requests {set:nginx.keepalive_requests};\n"
"    sendfile {setonoff:nginx.sendfile};\n"
"    tcp_nopush {setonoff:nginx.tcp_nopush};\n"
"    tcp_nodelay {setonoff:nginx.tcp_nodelay};\n"
"    reset_timedout_connection {setonoff:nginx.reset_timedout};\n"
"\n"
"    include {dir}/sites/*.conf;\n"
"\n"
"    server {\n"
"        listen {port:http};\n"
"        server_name localhost;\n"
"        charset utf-8;\n"
"        location / {\n"
"            root {dir}/www;\n"
"            index {set:nginx.index};\n"
"        }\n"
"    }\n"
"}\n"
)
# 找到写 nginx.conf 的动作（write 且 dst 以 nginx.conf 结尾），保留 index.html 模板
for a in n['lifecycle']['install']:
    if a.get('action') == 'write' and a.get('dst', '').endswith('nginx.conf'):
        a['from'] = conf_tpl
n['config']['signature'] = '2026-10-22-m8-settings'
n['settings'] = {
    'configFile': 'nginx.conf',
    'restart': False,
    'groups': [
        {'title': '基本配置', 'fields': [
            F('nginx.port', '启动端口', 'number', '80', portRole='http', min=1, max=65535),
            F('nginx.index', '网站首页', 'text', 'index.html index.htm',
              placeholder='index.php index.html'),
            F('nginx.error_log_level', '日志级别', 'select', 'error',
              options=['debug', 'info', 'notice', 'warn', 'error', 'crit']),
            F('nginx.server_names_hash', '域名长度', 'number', '256', unit='b',
              hint='server_name 总长度过大时调大'),
            F('nginx.error_page', '错误页面重定向', 'switch', '0',
              hint='由站点模板自行提供 /error/ 页面时开启'),
        ]},
        {'title': '性能配置', 'fields': [
            F('nginx.worker_processes', '工作进程', 'number', '4', min=1, max=64, unit='个'),
            F('nginx.worker_connections', '并发连接', 'number', '4096', min=64, max=100000, unit='个'),
            F('nginx.client_max_body_size', '请求大小', 'number', '50', min=1, max=4096, unit='M'),
            F('nginx.keepalive_timeout', '长连接超时', 'number', '65', min=1, max=600, unit='秒'),
            F('nginx.keepalive_requests', '长连接次数', 'number', '100', min=1, max=10000, unit='次'),
            F('nginx.sendfile', '发送文件', 'switch', '1'),
            F('nginx.tcp_nopush', 'tcp_nopush', 'switch', '0'),
            F('nginx.tcp_nodelay', 'tcp_nodelay', 'switch', '1'),
            F('nginx.reset_timedout', '重置连接', 'switch', '1'),
        ]},
    ],
}
save('nginx.json', n, bom)

# ───────────────────────── PHP ─────────────────────────
p, bom = load('php.json')
# php.ini 从运行时复制到实例目录（render），启动加 -c 指向实例配置
p['lifecycle']['install'] = [
    {'action': 'mkdir', 'path': '{dir}/tmp'},
    {'action': 'render', 'src': 'php.ini-development', 'dst': '{dir}/php.ini-production-copy',
     'vars': []},
]
p['lifecycle']['install'] = [
    {'action': 'mkdir', 'path': '{dir}/tmp'},
]
# php.ini 不存在于部分 zip？官方 nts zip 带 php.ini-development / php.ini-production。
# render 源缺失会让装包失败 —— 稳妥起见用 copy 动作 + 容错？copy 也硬失败。
# 折中：start args 加 -c {runtime}（直接用运行时自带 php.ini-development 改名？）
# 最稳：把 php.ini 放实例目录由「配置文件 Tab」按需生成 —— 首装不 copy，
# start 参数加 -c {dir}/php.ini，文件不存在时 php-cgi 回落默认配置；
# 用户在设置 Tab 保存时生成。为可控起见，首装 write 一份最小 php.ini。
p['lifecycle']['install'] = [
    {'action': 'mkdir', 'path': '{dir}/tmp'},
    {'action': 'write', 'dst': '{dir}/php.ini', 'from':
     "; 对号生成 · 最小 php.ini（完整配置可在下方「配置文件」Tab 编辑）\n"
     "memory_limit = {set:php.memory_limit}M\n"
     "upload_max_filesize = {set:php.upload_max_filesize}M\n"
     "post_max_size = {set:php.post_max_size}M\n"
     "max_execution_time = {set:php.max_execution_time}\n"
     "max_input_time = {set:php.max_input_time}\n"
     "expose_php = {setonoff:php.expose_php}\n"
     "display_errors = {setonoff:php.display_errors}\n"
     "log_errors = On\n"
     "error_log = {dir}/logs/php.err\n"
     "date.timezone = {set:php.timezone}\n"
     "extension_dir = ext\n"
     "extension=curl\n"
     "extension=mbstring\n"
     "extension=openssl\n"
     "extension=pdo_mysql\n"
     "extension=fileinfo\n"
    },
]
p['start']['args'] = ['-c', '{dir}/php.ini', '-b', '127.0.0.1:{port:fcgi}']
p['lifecycle']['install'].append(
    {'action': 'mkdir', 'path': '{dir}/logs'})
p['config'] = {'signature': '2026-10-22-m8-settings'}
p['settings'] = {
    'configFile': 'php.ini',
    'restart': True,
    'groups': [
        {'title': '性能与限制', 'fields': [
            F('php.memory_limit', '内存限制', 'number', '256', unit='M'),
            F('php.upload_max_filesize', '上传上限', 'number', '64', unit='M'),
            F('php.post_max_size', 'POST 上限', 'number', '64', unit='M',
              hint='应不小于上传上限'),
            F('php.max_execution_time', '执行超时', 'number', '120', unit='秒'),
            F('php.max_input_time', '输入超时', 'number', '120', unit='秒'),
            F('php.display_errors', '显示错误', 'switch', '0', hint='生产环境建议关闭'),
            F('php.expose_php', '暴露 PHP 版本', 'switch', '0'),
            F('php.timezone', '时区', 'select', 'Asia/Shanghai',
              options=['Asia/Shanghai', 'UTC', 'Asia/Tokyo', 'America/New_York', 'Europe/London']),
        ]},
    ],
}
save('php.json', p, bom)
print('ALL-DONE')
