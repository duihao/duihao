-- 017 下载（离线下载 / 任务队列）
--
-- 两条设计口径：
--   1. **任务记录与 aria2 的 gid 分离**。DB 里存的是「用户视角的任务」，
--      gid 只是当下引擎里的句柄。对号重启后 aria2 是空的，按 gid 找回不去，
--      所以状态以 DB 为准，引擎起来后按 url 重下（未完成的重新排队）。
--   2. **磁盘是真相**。任务完成与否不靠引擎回调（对号可能没开），
--      以文件是否落在目录里 + DB 状态双确认。

create table if not exists download_tasks (
  uid         text    not null,
  id          text    not null,
  gid         text    not null default '',
  name        text    not null default '',
  url         text    not null default '',
  kind        text    not null default 'http',     -- http | magnet | torrent
  dir         text    not null default 'download', -- download | video | music
  status      text    not null default 'waiting',  -- waiting|active|paused|done|error
  total       integer not null default 0,
  done        integer not null default 0,
  speed       integer not null default 0,
  seeders     integer not null default 0,          -- BT 做种者数（HTTP 恒 0）
  err         text    not null default '',
  created_at  text    not null default '',
  updated_at  text    not null default '',
  finished_at text    not null default '',
  primary key (uid, id)
);

create index if not exists idx_dl_task on download_tasks(uid, status, created_at);

-- 键值袋：加一条设置不必再动迁移（与 voice_settings 同一套做法）
create table if not exists download_settings (
  uid text not null,
  k   text not null,
  v   text not null default '',
  primary key (uid, k)
);
