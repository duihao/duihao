-- 收藏夹图标缓存：服务端取一次 favicon 存库，之后本地直出（断网也能显示已缓存的图标）。
-- 这是公开资源、与账号无关，故不带 uid 列；按 host 去重，正缓存 30 天、负缓存 1 天。
create table if not exists favicons (
  host       text primary key,
  mime       text not null,          -- image/png 等；'none' 表示取过但没取到（负缓存）
  data       blob not null,
  fetched_at text not null           -- RFC3339
);
