-- 笔记改为纯 SQLite 存储（2026-10-28 用户定稿）：
-- 正文 body 直接入库，不再落网盘 Markdown 文件，因此 001 里的 notes_index（文件检索缓存）作废。
drop table if exists notes_index;

create table if not exists notes (
  uid        text    not null,
  id         text    not null,
  title      text    not null default '',
  body       text    not null default '',
  tags       text    not null default '',   -- 逗号分隔，原样保留用户输入的大小写
  pinned     integer not null default 0,
  day        text    not null,              -- 归属日期 YYYY-MM-DD（列表按日分组）
  created_at text    not null,
  updated_at text    not null,
  primary key (uid, id)
);
create index if not exists ix_notes_day on notes(uid, day desc, updated_at desc);
create index if not exists ix_notes_updated on notes(uid, updated_at desc);
