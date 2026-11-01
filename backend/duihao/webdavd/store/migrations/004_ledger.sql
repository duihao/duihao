-- 账本升级：两级收支分类（一级组 + 二级分类）、收支对象、账户名与凭证快照。
-- 只加不改：老的 ledger_categories 保留不动（历史数据仍可读，新逻辑不再用它）。

-- 账目冗余快照：删分类/账户后历史账目照旧能显示
alter table ledger_entries add column group_id text;
alter table ledger_entries add column group_name text;
alter table ledger_entries add column cat_name text;
alter table ledger_entries add column account_name text;
alter table ledger_entries add column peer text;
alter table ledger_entries add column receipt text;   -- dataURL（前端压缩后的缩略图），空=无凭证

create table if not exists ledger_groups (
  uid        text    not null,
  id         text    not null,
  name       text    not null,
  kind       text    not null default 'expense',   -- expense / income
  sort       integer not null default 0,
  sys        integer not null default 0,           -- 1 = 系统预置（只读）
  created_at text    not null,
  primary key (uid, id)
);
create unique index if not exists ux_ledger_groups on ledger_groups(uid, kind, name);

create table if not exists ledger_cats (
  uid        text    not null,
  id         text    not null,
  group_id   text    not null,
  name       text    not null,
  sort       integer not null default 0,
  sys        integer not null default 0,
  created_at text    not null,
  primary key (uid, id)
);
create unique index if not exists ux_ledger_cats on ledger_cats(uid, group_id, name);
create index if not exists ix_ledger_cats_group on ledger_cats(uid, group_id, sort);

-- 收支对象（常用往来方：小明、公司财务…）
create table if not exists ledger_peers (
  uid  text    not null,
  name text    not null,
  sort integer not null default 0,
  primary key (uid, name)
);

-- 每账号一次性标记（默认分类只播种一次，用户删光后不会被重新长回来）
create table if not exists ledger_meta (
  uid text not null,
  k   text not null,
  v   text not null,
  primary key (uid, k)
);

create index if not exists ix_entries_kind_day on ledger_entries(uid, kind, day);
