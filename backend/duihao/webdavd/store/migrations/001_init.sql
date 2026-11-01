-- S0 初始化：单库（hub.db）+ uid 列做租户隔离。
-- 规范：每张表第一列 uid（账号 = 共享用户名）；主键/唯一索引一律 (uid, ...)；
-- 所有语句由应用层带 where uid = ? 过滤。只加不改，后续变更走 002_*.sql。

create table if not exists bookmark_groups (
  uid        text    not null,
  id         text    not null,
  title      text    not null,
  sort       integer not null default 0,
  created_at text    not null,
  primary key (uid, id)
);

create table if not exists bookmarks (
  uid        text    not null,
  id         text    not null,
  group_id   text,
  title      text    not null,
  url        text    not null,
  icon       text,
  note       text,
  sort       integer not null default 0,
  created_at text    not null,
  updated_at text    not null,
  primary key (uid, id)
);
create index if not exists ix_bookmarks_group on bookmarks(uid, group_id, sort);

-- 笔记索引：正文是 WebDAV 根下的 Markdown 文件，本表只是检索缓存（可重建）。
create table if not exists notes_index (
  uid   text    not null,
  path  text    not null,           -- 相对共享根的正斜杠路径，如 笔记/2026-10-28.md
  title text,
  day   text,                       -- YYYY-MM-DD
  tags  text,                       -- 逗号分隔
  mtime text,
  size  integer not null default 0,
  primary key (uid, path)
);
create index if not exists ix_notes_day on notes_index(uid, day);

create table if not exists ledger_accounts (
  uid        text    not null,
  id         text    not null,
  name       text    not null,
  kind       text    not null default 'cash',   -- cash / bank / card / wallet
  sort       integer not null default 0,
  created_at text    not null,
  primary key (uid, id)
);

create table if not exists ledger_categories (
  uid        text    not null,
  id         text    not null,
  name       text    not null,
  kind       text    not null default 'expense', -- expense / income
  sort       integer not null default 0,
  created_at text    not null,
  primary key (uid, id)
);

create table if not exists ledger_entries (
  uid         text    not null,
  id          text    not null,
  day         text    not null,                   -- YYYY-MM-DD
  amount_cents integer not null,                  -- 分，避免浮点误差
  kind        text    not null default 'expense',
  account_id  text,
  category_id text,
  note        text,
  created_at  text    not null,
  updated_at  text    not null,
  primary key (uid, id)
);
create index if not exists ix_entries_day on ledger_entries(uid, day);

-- 影视：扫网盘目录里的视频文件建索引（文件仍是 WebDAV 资源，本表只是元数据）。
create table if not exists video_items (
  uid        text    not null,
  path       text    not null,                    -- 相对共享根的视频路径
  title      text    not null,
  kind       text    not null default 'movie',    -- movie / series
  series     text,                                -- 剧名（剧集归并用）
  season     integer,
  episode    integer,
  poster     text,                                -- 海报：同目录同名 .jpg 或外链
  duration   integer not null default 0,          -- 秒，0 = 未知
  size       integer not null default 0,
  mtime      text,
  missing    integer not null default 0,          -- 扫描时文件已不在
  updated_at text    not null,
  primary key (uid, path)
);
create index if not exists ix_video_series on video_items(uid, series, season, episode);

-- 继续观看：位置（秒）与时长，按路径记。
create table if not exists video_progress (
  uid        text    not null,
  path       text    not null,
  position   integer not null default 0,
  duration   integer not null default 0,
  updated_at text    not null,
  primary key (uid, path)
);
