-- 健康 H3：日常测量 / 用药 / 就诊。
--
-- 口径：
--   - 日常测量（体重、血压、血糖、心率、体温、血氧、腰围）是「单点自测」，没有报告容器，
--     所以独立建表，不塞进 health_metrics（否则报告列表里会冒出一堆假报告）；
--   - 血压一次测两个数（收缩压/舒张压），统一用 v1/v2 两列承载，趋势时拆成两条线；
--   - 用药与就诊都是「按日期排队的事件」，字段各不相同，各建一张表比塞 JSON 好查；
--   - 隐私开关这类一次性标记走 hub_meta（uid + key），不新建配置表。

create table if not exists health_vitals (
  uid         text    not null,
  id          text    not null,
  member_id   text    not null,
  kind        text    not null,                  -- weight / bp / glucose / pulse / temp / spo2 / waist
  measured_at text    not null,                  -- YYYY-MM-DD HH:MM（本地时间，排序用字符串即可）
  v1          real    not null,                  -- 主值（血压为收缩压）
  v2          real,                              -- 次值（仅血压：舒张压）
  unit        text    not null default '',
  note        text    not null default '',
  created_at  text    not null,
  primary key (uid, id)
);
create index if not exists idx_health_vitals_member on health_vitals(uid, member_id, measured_at desc);

create table if not exists health_meds (
  uid        text    not null,
  id         text    not null,
  member_id  text    not null,
  name       text    not null,
  dose       text    not null default '',        -- 单次剂量，如「5mg · 1片」
  freq       text    not null default '',        -- 频次，如「每日一次 早餐后」
  route      text    not null default '',        -- 口服 / 外用 / 注射
  start_at   text    not null default '',        -- YYYY-MM-DD
  end_at     text    not null default '',        -- 空 = 仍在用
  status     text    not null default 'active',  -- active 在服 / stopped 已停
  note       text    not null default '',
  created_at text    not null,
  updated_at text    not null,
  primary key (uid, id)
);
create index if not exists idx_health_meds_member on health_meds(uid, member_id, status, start_at desc);

create table if not exists health_visits (
  uid        text    not null,
  id         text    not null,
  member_id  text    not null,
  visit_at   text    not null,                   -- YYYY-MM-DD
  org        text    not null default '',
  dept       text    not null default '',
  doctor     text    not null default '',
  reason     text    not null default '',        -- 就诊原因 / 主诉
  diagnosis  text    not null default '',
  advice     text    not null default '',        -- 医嘱
  cost       real,                               -- 费用（元），可空
  note       text    not null default '',
  created_at text    not null,
  updated_at text    not null,
  primary key (uid, id)
);
create index if not exists idx_health_visits_member on health_visits(uid, member_id, visit_at desc);
