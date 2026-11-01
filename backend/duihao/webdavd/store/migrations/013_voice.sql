-- 语音秘书（V1）：到点让本机替你说话。
--
-- 口径：
--   - 一条事件 = 一段文案 + 一个音色/语气 + 一套到点规则，到点由**后端**直接调
--     Windows SAPI 出声（不需要页面开着，断网也响）；
--   - 文案合成成 wav 落网盘 _appfiles/voice/<sha1>.wav，只为前端试听与波形；
--     播报不依赖文件（文件丢了照样能念）；
--   - next_at 与 at_time 一律存本地时区的 'YYYY-MM-DD HH:MM' / 'HH:MM'，
--     同格式可直接按字符串比较大小（调度查询就是这么干的）；
--   - period：once 单次 / repeat 每 N 分钟（久坐、喝水、番茄钟）/ daily / weekly /
--     monthly / quarterly / yearly；
--   - 到点动作白名单只有 none|url|path（播报、打开网址、打开文件），不执行任意命令。

create table if not exists voice_events (
  uid          text    not null,
  id           text    not null,
  title        text    not null default '',
  content      text    not null default '',
  voice        text    not null default 'huihui-std',
  tone         text    not null default 'normal',
  times        integer not null default 1,          -- 播放次数，0 = 只记录不播
  period       text    not null default 'once',
  weekdays     text    not null default '',         -- weekly：1,3,5（1=周一 … 7=周日）
  interval_min integer not null default 60,         -- repeat：每 N 分钟
  day_from     text    not null default '09:00',    -- repeat：当天窗口起
  day_to       text    not null default '18:00',    -- repeat：当天窗口止
  start_date   text    not null default '',         -- YYYY-MM-DD
  at_time      text    not null default '',         -- HH:MM
  next_at      text    not null default '',         -- 下次触发；空 = 不再触发（已结束）
  enabled      integer not null default 1,
  audio        text    not null default '',         -- wav 文件名（仅缓存）
  audio_ms     integer not null default 0,
  action       text    not null default 'none',     -- none | url | path
  action_arg   text    not null default '',
  last_at      text    not null default '',
  created_at   text    not null default '',
  updated_at   text    not null default '',
  primary key (uid, id)
);

create index if not exists voice_events_due on voice_events(enabled, next_at);

-- 触发流水：页面上看「响过没有」，出问题也有据可查（不存正文，只存标题）。
create table if not exists voice_logs (
  uid      text    not null,
  id       text    not null,
  event_id text    not null default '',
  title    text    not null default '',
  at       text    not null,
  ok       integer not null default 1,
  msg      text    not null default '',
  primary key (uid, id)
);

create index if not exists voice_logs_at on voice_logs(uid, at);
