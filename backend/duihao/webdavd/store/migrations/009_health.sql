-- 健康（H1）：成员 + 报告（体检/化验/影像）+ 指标明细。
--
-- 口径：
--   - 一次体检、一张化验单、一份影像报告都是 health_reports 的一行，kind 区分；
--   - 报告是容器，指标明细在 health_metrics，趋势从指标聚合，不再单独建时间序列表；
--   - 指标冗余存 member_id / report_at，趋势查询不用 join reports；
--   - 参考范围跟着每条指标走（不同机构不同），绝不套全局标准；
--   - alias 是同义项归一后的键（ALT = 谷丙转氨酶），让跨年趋势能连成一条线；
--   - 附件只存网盘相对路径（JSON 数组），文件本体在 _appfiles/health/ 下。

create table if not exists health_members (
  uid        text    not null,
  id         text    not null,
  name       text    not null,
  relation   text    not null default '本人',   -- 本人 / 父亲 / 母亲 / 子女 / 其他
  sex        text    not null default '',       -- 男 / 女 / ''
  birth      text    not null default '',       -- YYYY-MM-DD，可空
  height     real,                              -- cm，可空（算 BMI 用）
  note       text    not null default '',
  sort       integer not null default 0,
  created_at text    not null,
  primary key (uid, id)
);

create table if not exists health_reports (
  uid         text    not null,
  id          text    not null,
  member_id   text    not null,
  kind        text    not null default 'checkup', -- checkup 体检 / lab 化验 / imaging 影像 / other 其他
  title       text    not null default '',
  org         text    not null default '',        -- 机构
  dept        text    not null default '',        -- 科室
  report_at   text    not null,                   -- YYYY-MM-DD
  summary     text    not null default '',        -- 结论/医生意见
  attachments text    not null default '[]',      -- JSON 数组：网盘相对路径
  created_at  text    not null,
  updated_at  text    not null,
  primary key (uid, id)
);
create index if not exists idx_health_reports_member on health_reports(uid, member_id, report_at desc);

create table if not exists health_metrics (
  uid        text    not null,
  id         text    not null,
  report_id  text    not null,
  member_id  text    not null,
  report_at  text    not null,
  name       text    not null,                    -- 原始项目名（按单据抄）
  alias      text    not null,                    -- 归一键，趋势按它分组
  value_num  real,                                -- 数值结果；定性项目为空
  value_text text    not null default '',         -- 定性结果（阴性 / + / 未见异常）
  unit       text    not null default '',
  ref_low    real,
  ref_high   real,
  ref_text   text    not null default '',         -- 原始参考范围文本（如 "0-40" "阴性"）
  flag       text    not null default '',         -- high / low / ''（空 = 正常或未判定）
  sort       integer not null default 0,
  primary key (uid, id)
);
create index if not exists idx_health_metrics_report on health_metrics(uid, report_id);
create index if not exists idx_health_metrics_series on health_metrics(uid, member_id, alias, report_at);
