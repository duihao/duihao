-- 音乐模块（M1）：曲库 + 音乐U盘。
--
-- 口径：
--   - 曲库扫网盘 _appfiles/music/（与影视的 _appfiles/video 同款约定）；
--   - 标签来自文件本身（ID3v2 / FLAC Vorbis），扫描时写入；用户改过标题/歌手
--     置 custom=1，重扫不覆盖；
--   - 封面三路：专辑同名图（<目录>/cover.jpg）、标签内嵌图（导出落 .covers/）、
--     无图走默认；cover 存共享内斜杠路径；
--   - duration 是扫描时的近似值（MP3 按码率估算 / FLAC 精确），播放器里会拿到
--     精确值，不回写（省一次全库解析）；
--   - 歌单、播放统计、U盘导出历史各一张表，都带 uid 租户列。

create table if not exists music_tracks (
  uid          text not null,
  id           text not null,                  -- sha1(path) 前 12 位，稳定
  path         text not null,                  -- 共享内斜杠路径 /_appfiles/music/...
  dir          text not null default '',       -- 相对 music 根的子目录（'' = 根）
  title        text not null,
  artist       text not null default '',
  album        text not null default '',
  album_artist text not null default '',
  genre        text not null default '',
  year         integer not null default 0,
  track_no     integer not null default 0,
  disc_no      integer not null default 0,
  ext          text not null default '',       -- mp3 / flac / wav / m4a...
  size         integer not null default 0,
  duration     integer not null default 0,     -- 秒（近似）
  bitrate      integer not null default 0,     -- kbps（近似）
  cover        text not null default '',       -- 封面路径（共享内斜杠路径）
  mtime        text not null default '',
  custom       integer not null default 0,     -- 1 = 用户改过标签，重扫不覆盖
  created_at   text not null,
  updated_at   text not null,
  primary key (uid, id)
);
create unique index if not exists idx_music_tracks_path on music_tracks(uid, path);
create index if not exists idx_music_tracks_artist on music_tracks(uid, artist);
create index if not exists idx_music_tracks_album on music_tracks(uid, album);

create table if not exists music_playlists (
  uid        text not null,
  id         text not null,
  name       text not null,
  note       text not null default '',
  created_at text not null,
  updated_at text not null,
  primary key (uid, id)
);

create table if not exists music_pl_items (
  uid        text not null,
  pl_id      text not null,
  track_id   text not null,
  pos        integer not null default 0,
  added_at   text not null,
  primary key (uid, pl_id, track_id)
);
create index if not exists idx_music_pl_items_order on music_pl_items(uid, pl_id, pos);

create table if not exists music_stats (
  uid        text not null,
  track_id   text not null,
  play_count integer not null default 0,
  last_at    text not null default '',
  primary key (uid, track_id)
);

create table if not exists music_exports (
  uid        text not null,
  id         text not null,
  label      text not null default '',        -- 展示名（歌单名或「筛选结果」）
  drive      text not null default '',        -- 盘符，如 E:\
  dest       text not null default '',        -- 实际目标目录
  items      integer not null default 0,      -- 成功拷贝数
  skipped    integer not null default 0,      -- 跳过（已存在/超限）
  bytes      integer not null default 0,
  created_at text not null,
  primary key (uid, id)
);
