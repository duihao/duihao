-- 016 视频监控 V2：凭证入箱 / 录像计划 / PTZ 能力
--
-- 三件事：
--   1) 相机账号密码不再内嵌在 rtsp 地址里。address 里只留 host 与路径，
--      用户名单独一列，密码走 credentials 包（DPAPI / AES-GCM）存密文。
--      库文件被拷走时，密码读不出来 —— 这是「凭证不出对号」的最基本要求。
--   2) 录像计划存 JSON：空 = 全天录（与 v1 的 24/7 行为一致，老数据不用迁）。
--   3) ptz 记能力与通道：'hik:1' / 'dahua:1' / '' （空 = 不支持，页面不给按钮）。
--
-- 口径：
--   · plan 里的时间用 "HH:MM"，跨午夜（to < from）表示吃到第二天；
--   · cred_seal 为空 = 相机不需要账号密码（少数开放设备）；
--   · 换台机器解不开密文时不静默失败：页面会提示「凭证已失效，请重填」。

alter table cameras add column cred_user text not null default '';
alter table cameras add column cred_seal text not null default '';
alter table cameras add column plan      text not null default '';
alter table cameras add column ptz       text not null default '';

-- 计划循环每 30s 扫一次「该录的相机」，这个索引让它别全表扫
create index if not exists idx_cam_active on cameras(uid, enabled, record);
