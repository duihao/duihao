-- 笔记升级为「知识卡片」形态（2026-10-28）：
-- 每篇多一个分类（cat）与阅读计数（views）；分类本身单独成表，
-- 这样「先建分类再归类」和「分类重命名」都不必依赖已有笔记。
alter table notes add column cat text not null default '';
alter table notes add column views integer not null default 0;

create table if not exists note_cats (
  uid  text not null,
  name text not null,
  sort integer not null default 0,
  primary key (uid, name)
);

create index if not exists ix_notes_cat on notes(uid, cat);
create index if not exists ix_notes_created on notes(uid, created_at);
