-- 014 视频监控（可选模块：用得到才下 ffmpeg）
--
-- 口径：
--   · 相机只存「地址 + 参数」，凭证不落库（走 credentials/kdf，v1 先允许 URL 内嵌，后续换保险箱）；
--   · 录像 = ffmpeg -c copy -f segment 每 5 分钟一片，落 _appfiles/camera/<camId>/；
--   · 片段索引由扫描目录同步而来（不是唯一真相，目录才是），删片同时删索引行；
--   · 每张表首列 uid，主键 (uid, …)，SQL 一律带 where uid=?。

create table if not exists cameras (
  uid         text not null,
  id          text not null,
  name        text not null,
  vendor      text not null default '',        -- hikvision | dahua | tplink | custom
  host        text not null default '',        -- 展示用（发现出来的 IP）
  url_main    text not null default '',         -- 主码流 rtsp
  url_sub     text not null default '',         -- 子码流 rtsp（预览 + 录像优先用它）
  transport   text not null default 'tcp',      -- tcp | udp
  record      integer not null default 1,       -- 1 录 0 只预览
  retain_days integer not null default 7,       -- 保留天数（0=不限，靠磁盘守护兜底）
  enabled     integer not null default 1,
  sort        integer not null default 0,
  state       text not null default 'stopped',  -- stopped | recording | preview | error
  last_error  text not null default '',
  created_at  text not null,
  updated_at  text not null,
  primary key (uid, id)
);

create table if not exists camera_segments (
  uid      text not null,
  id       text not null,
  cam_id   text not null,
  path     text not null,                       -- 相对网盘根：_appfiles/camera/<cam>/<file>.mp4
  start_at text not null,                       -- 2006-01-02 15:04:05（由文件名解析）
  end_at   text not null default '',
  bytes    integer not null default 0,
  kind     text not null default 'continuous',  -- continuous | event
  mark     text not null default '',            -- 事件备注（手动标记）
  primary key (uid, id)
);
create index if not exists idx_cam_seg on camera_segments(uid, cam_id, start_at);

create table if not exists camera_logs (
  uid    text not null,
  id     text not null,
  cam_id text not null default '',
  level  text not null default 'info',          -- info | warn | error
  msg    text not null,
  at     text not null,
  primary key (uid, id)
);
create index if not exists idx_cam_log on camera_logs(uid, at);
