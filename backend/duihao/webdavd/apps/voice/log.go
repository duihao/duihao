package voice

import "log"

// Logf 语音模块的日志出口。
//
// 为什么不用标准 log：GUI 进程的 stderr 没有落点，播报失败这类关键报错会整个
// 消失（2026-10-30 真机踩过：弹窗弹了、声音没有，报错却哪儿都找不到）。
// webdavd 启动时把进程日志注入进来；不注入退回标准 log（测试/独立进程）。
var Logf = log.Printf
