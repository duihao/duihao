-- 音乐模块（M2 卡拉OK）：歌词存库 + 消声播放（纯前端 DSP，不落库）。
--
-- 口径：
--   - 歌词来源：扫描时认音频同目录同名 .lrc（晴天.mp3 ↔ 晴天.lrc），
--     解析成 [{t,text}] JSON 存 lyrics 列；.lrc 没了或内容变了重扫会同步；
--   - 只认行级时间戳（[mm:ss.xx]），逐字增强标签 <...> 直接剥掉；
--   - 不做在线歌词源下载 —— 断网可用，歌词文件用户自己放。

alter table music_tracks add column lyrics text not null default '';
