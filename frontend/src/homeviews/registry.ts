// M19 插件化产品线：插件首页视图注册表。
// 视图随版本编译内置（静态 import），禁止运行时注册 —— 防插件越权注入任意视图。
// 新增插件首页 = homeviews/<plugin>/ 下加组件 + 本表加一行。
// M21 起 'life' 是 NAS 首页（不属于任何发行版插件）。M23 把它从「主窗口的
// 一种身份」拆成独立主窗：/?w=nas 由 NasStandalone 直接挂 LifeHomeView，
// 这张表只剩发行版首页兜底（'life' 保留给老配置的 edition.home=<name>）。
import type { Component } from 'vue'
import DavHomeView from './webdav/DavHomeView.vue'
import LifeHomeView from './life/LifeHomeView.vue'

export const HOME_VIEWS: Record<string, Component> = {
  webdav: DavHomeView,
  life: LifeHomeView
}
