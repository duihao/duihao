package webui

// ThemeAttr 返回写进 <html data-theme="…"> 的值：dark / light / system。
//
// 主题的真源在桌面端的 settings.json（AppSettings.theme），webui 这一层不该
// 认识"deep-blue / duihao-red"这些前端配色名，所以由 webdavd 在 init 里把
// 映射函数挂上来（它同时负责 SSE 推送）。没人挂时退回 system —— 跟随浏览器/
// 系统的 prefers-color-scheme，与改造前的行为一致。
var ThemeAttr = func() string { return "system" }
