-- S4 影视：S0 骨架的 video_items 从未写入过任何数据（当时只有 ping 接口），
-- 这里按影视平台口径重建：一部影片一行（目录型影片聚合多个视频文件到 files JSON）。
drop table if exists video_items;

create table video_items (
  uid        text    not null,
  id         text    not null,                     -- 影片 id：sha1(分类/名称) 前 12 位（重扫稳定）
  cat_name   text    not null default '',          -- 分类 = _appfiles/video 一级子目录名；'' = 未分类
  title      text    not null,                     -- 展示名（目录名 / 去扩展名的文件名）
  custom     integer not null default 0,           -- 1 = 用户改过标题，重扫不覆盖
  year       integer,                              -- 年份（从名称提取或用户填写）
  kind       text    not null default 'movie',     -- movie / series / variety / anime / doc
  tags       text    not null default '',          -- 类型标签（逗号分隔：科幻,悬疑）
  area       text    not null default '',          -- 地区
  director   text    not null default '',
  actors     text    not null default '',
  rating     real,                                 -- 评分 0~10，NULL = 未评
  duration   integer not null default 0,           -- 主文件时长（秒），0 = 未知
  intro      text    not null default '',          -- 简介
  poster     text,                                 -- 海报相对路径（目录内 poster.jpg 等）
  files      text    not null default '[]',        -- [{name,path,url,ep,size}] 视频文件列表
  size       integer not null default 0,           -- 视频文件总字节
  mtime      text,
  created_at text    not null,
  updated_at text    not null,
  primary key (uid, id)
);
create index if not exists ix_video_cat on video_items(uid, cat_name, updated_at);
create index if not exists ix_video_year on video_items(uid, year);

-- 分类目录表（扫 _video 一级子目录自动维护；用户不改目录就不改名）
create table video_cats (
  uid   text    not null,
  name  text    not null,
  sort  integer not null default 0,
  primary key (uid, name)
);

-- 进度表补影片 id（继续观看要联到影片），老行 item_id 为空即视为无效历史。
alter table video_progress add column item_id text;
create index if not exists ix_progress_item on video_progress(uid, item_id, updated_at);

-- user_version 由 store 迁移器按文件序号写入。
