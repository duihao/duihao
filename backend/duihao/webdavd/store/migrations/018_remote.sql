-- 018 远程访问（IPv6 直连 + DDNS + frp 内网穿透）。
--
-- 一个账号一套设置：远程访问是「这台机器怎么被外面摸到」的全局决定。
-- 传输方式的三条路线共用这张键值袋：
--   ① IPv6 直连（自签证书，免域名免备案）
--   ② frp 中继（frpc 进程，下载器模式取包）
--   ③ DDNS（现在给 IPv6 直连用，将来 ACME 自动签发也吃这份 DNS 令牌）
--
-- k 取值（当前用到）：
--   public.enabled        1|0         公网可达总开关（默认 0：只许局域网）
--   ipv6.mode             off|direct  IPv6 直连开关
--   ddns.provider         dnspod|aliyun
--   ddns.domain           example.com（主域）
--   ddns.sub              duihao（子域，@ = 根域）
--   ddns.token            密文（credentials 包产出；DNSPod = id:token，阿里 = AccessKey:Secret）
--   ddns.interval         更新间隔分钟（默认 10，最小 5）
--   ddns.last_ip          上次写进去的地址（避免重复调 API）
--   ddns.last_ok          1|0         上次更新结果
--   ddns.last_msg         上次更新的人话结果
--   ddns.last_at          上次更新时间
--   frpc.enabled          1|0         frp 开关
--   frpc.config           JSON：FrpConfig 结构（见 apps/remote/frpc.go）
--   frpc.state            last message（启动失败原因等）
--
-- v 一律 text：键值袋，加键不必再动迁移。
create table if not exists remote_settings (
  uid        text not null,
  k          text not null,
  v          text not null,
  updated_at text not null,
  primary key (uid, k)
);
