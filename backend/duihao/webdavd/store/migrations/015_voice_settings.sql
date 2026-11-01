-- 015 语音提醒 V2：播报引擎设置。
--
-- 一个账号一套设置（不是每条提醒一套）：引擎是「这台机器怎么出声」的全局决定，
-- 逐条配会让「换了 GPU 主机」变成要改十几条提醒。
--
-- k 取值（当前用到）：
--   engine         sapi | http          播报引擎
--   http.base      http://192.168.1.9:9880
--   http.token     密文（credentials 包产出，库里不落明文）
--   http.model     cosyvoice2 / tts-1 / fish-speech …
--   http.voice     服务端音色名
--   http.speed     语速倍率（0.5~2.0）
--
-- v 一律 text：这里是键值袋，加一个键不必再动迁移。
create table if not exists voice_settings (
  uid        text not null,
  k          text not null,
  v          text not null,
  updated_at text not null,
  primary key (uid, k)
);
