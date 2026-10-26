<template>
  <CommandPalette :key="showKey" :visible="true" @update:visible="onVisible" />
</template>

<script setup lang="ts">
// M18-C：独立悬浮面板窗的前端入口（Go 侧以 /?w=palette 打开第二个无边框
// 置顶窗口加载本入口，只挂 CommandPalette —— 用户定稿：面板窗是主入口，
// 导航/连接/创建动作经后端 PaletteNavigate/PaletteConnect/PaletteAction
// 打开旧主窗口）。'palette:open'（热键再次呼出）通过重建组件让数据重载、
// 输入框重新聚焦；关闭 = Window.Hide() 隐藏本窗口（webview 常驻，下次秒开）。
import { ref } from 'vue'
import { Events, Window } from '@wailsio/runtime'
import CommandPalette from './components/CommandPalette.vue'

const showKey = ref(0)

function onVisible(v: boolean) {
  if (!v) void Window.Hide()
}

Events.On('palette:open', () => {
  showKey.value++
})

// 失焦自动收起（uTools 式）：面板置顶悬浮，若点击主窗口/其他程序而不隐藏，
// 会一直压在操作界面上。但 Show+Focus 过程中可能有一个瞬态 blur（样式刷新/
// 焦点切换时序），直接 Hide 会「弹一下就没了」——所以延迟 150ms 后复查
// document.hasFocus()：焦点真的在别处才收，自己重新拿到焦点就不收。
let blurTimer: number | undefined
window.addEventListener('blur', () => {
  window.clearTimeout(blurTimer)
  blurTimer = window.setTimeout(() => {
    if (!document.hasFocus()) void Window.Hide()
  }, 150)
})
</script>

<style>
/* 非 scoped（scoped 的 data-v 属性选择器匹配不到 html/body/#app）。
   面板窗整体透明：#app 的主题渐变背景在这里必须关掉，否则 cp-box 四周的
   留白处透出的是渐变底而非桌面；窗口圆角形状由全局 body 的
   border-radius + contain:paint 提供。 */
html, body, #app {
  background: transparent !important;
}
#app {
  border-radius: 0;
  overflow: visible;
}
</style>
