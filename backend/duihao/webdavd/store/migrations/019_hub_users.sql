-- 019 NAS 内部多用户。
--
-- 口径（2026-10-31 定稿）：
--   - NAS 账号（hub_users）与 WebDAV 共享账号（webdavshares.json）解耦：
--     挂载客户端仍走默认共享账号（全盘读写），业务权限只管 NAS 网页；
--   - role: admin 全权（忽略 perms）；member 按 perms 走，每模块 "r"|"rw"，
--     缺条目 = 无权限；users 应用不在 perms 里（仅 admin，自己改密除外）；
--   - 第一个账号由服务端引导生成：admin + 随机密码，uid 沿用既有唯一共享的
--     账号名（老数据各表 uid 无缝延续）；明文密码经 credentials.Seal 落在
--     hub_secrets（连接管理页可查看），改密时同步更新；
--   - hub_secrets 是通用密文键值袋（admin.password / share.<user>.password），
--     只存 credentials.Seal 产出的密文，库泄了也读不出明文。
create table if not exists hub_users(
  uid        text primary key,
  name       text not null unique,
  pass_hash  text not null,
  role       text not null default 'member',
  perms      text not null default '{}',
  enabled    integer not null default 1,
  created_at text not null default (datetime('now','localtime'))
);
create table if not exists hub_secrets(
  k          text primary key,
  v          text not null,
  updated_at text not null default (datetime('now','localtime'))
);
