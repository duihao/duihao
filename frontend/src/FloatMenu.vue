<template>
  <!-- 悬浮球右键菜单卡片（uTools 同款）：96×234 透明窗里居中画一张 68×206
       圆角卡片，四周 14px 留白给柔和投影透出来。菜单窗由 Go 侧摆在球正下方
       （App.ShowFloatMenu），收起由 Go 的前台监听 / 选项点击 / Esc 触发。
       M23 起每个入口对应一个窗口：主窗口（NAS）/ IT工具 / 设置 / 官网（官网放最后，
       退出项已去掉 —— 退出走托盘菜单）。 -->
  <div class="fm-root">
    <div class="fm-card">
      <button class="fm-item" @click="pick(1)">主窗口</button>
      <button class="fm-item" @click="pick(2)">IT工具</button>
      <div class="fm-sep"></div>
      <button class="fm-item" @click="pick(4)">设置</button>
      <button class="fm-item" @click="pick(3)">官网</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'
import { FloatMenuAction, HideFloatMenu } from '../bindings/github.com/duihao/duihao/app'

function pick(cmd: number) {
  void FloatMenuAction(cmd)
}

function hide() {
  void HideFloatMenu()
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') hide()
}

onMounted(() => window.addEventListener('keydown', onKey))
onUnmounted(() => window.removeEventListener('keydown', onKey))
</script>

<style>
/* 非 scoped：本窗只画一张卡片，全局 body 的深色渐变底必须清掉，否则圆角
   外的区域会是一块方形底色。 */
html,
body,
#app {
  margin: 0 !important;
  padding: 0 !important;
  border: none !important;
  background: transparent !important;
  overflow: hidden;
  width: 100%;
  height: 100%;
}
</style>

<style scoped>
.fm-root {
  box-sizing: border-box;
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 14px; /* 投影留白：窗口比卡片大一圈，40px 大扩散的影子才能透全 */
}

.fm-card {
  box-sizing: border-box;
  width: 68px;
  /* 4 项（主窗口 / IT工具 / 设置 / 官网）+ 1 条分隔：
     36×4 + (1+3+3) + gap 1×4 + 内边距 5×2 = 165。
     改项数必须同步 main.go 与 app_windows.go 的 floatMenuH。 */
  height: 165px;
  padding: 5px;
  border-radius: 12px;
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 1px;
  background: var(--win-menu-bg, #ffffff);
  border: 1px solid var(--win-menu-border, rgba(0, 0, 0, 0.06));
  /* 羽化投影：收紧版——小扩散贴地影 + 中等模糊，边缘淡出更快、不发虚 */
  box-shadow:
    0 2px 5px rgba(0, 0, 0, 0.08),
    0 8px 18px rgba(0, 0, 0, 0.18);
  user-select: none;
  overflow: hidden;
}

.fm-item {
  appearance: none;
  border: 0;
  background: transparent;
  color: var(--win-menu-fg, #1f2328);
  font-size: 13px;
  font-family: inherit;
  height: 36px;
  border-radius: 8px;
  cursor: pointer;
  transition: background 0.1s ease, color 0.1s ease;
}

.fm-item:hover {
  background: var(--win-menu-hover, rgba(0, 0, 0, 0.06));
}

.fm-sep {
  height: 1px;
  margin: 3px 8px;
  background: var(--win-menu-border, rgba(0, 0, 0, 0.08));
}

:global(html[data-theme='dark']) .fm-card {
  --win-menu-bg: #2b2d31;
  --win-menu-fg: #e6e6e6;
  --win-menu-border: rgba(255, 255, 255, 0.08);
  --win-menu-hover: rgba(255, 255, 255, 0.08);
  box-shadow:
    0 2px 5px rgba(0, 0, 0, 0.3),
    0 8px 18px rgba(0, 0, 0, 0.42);
}

:global(html[data-theme='dark']) .fm-item:hover {
  background: rgba(255, 255, 255, 0.08);
}
</style>
