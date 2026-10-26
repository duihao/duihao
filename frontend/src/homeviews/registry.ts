// M19 插件化产品线：插件首页视图注册表。
// 视图随版本编译内置（静态 import），禁止运行时注册 —— 防插件越权注入任意视图。
// 新增插件首页 = homeviews/<plugin>/ 下加组件 + 本表加一行。
import type { Component } from 'vue'
import DavHomeView from './webdav/DavHomeView.vue'

export const HOME_VIEWS: Record<string, Component> = {
  webdav: DavHomeView,
}
