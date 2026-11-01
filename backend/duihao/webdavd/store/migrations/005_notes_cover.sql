-- 005: 笔记封面图。存网盘路径（/_appfiles/notes/...）或 dataURL，空串 = 无封面。
alter table notes add column cover text not null default '';
