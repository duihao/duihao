-- 收藏夹（原「导航」）：网站补「账号」「密码提示」两列 + 通用键值表。
-- note 列复用为「描述」；密码提示只存部分字符 + * 号，本模块不是密码保险箱。
alter table bookmarks add column account  text not null default '';
alter table bookmarks add column pwd_hint text not null default '';

-- 通用键值表（uid 隔离）：记各模块的一次性标记（如默认数据是否已播种）。
create table if not exists hub_meta (
  uid text not null,
  key text not null,
  val text not null default '',
  primary key (uid, key)
);
