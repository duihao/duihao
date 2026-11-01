<template>
  <!-- NAS 主窗（M23）：/?w=nas —— 独立窗口，竖版 330×780，启动默认打开。
       原来是「主窗口的普通模式身份」，现在拆出来：主窗口只剩 IT 工具那一套。
       标题栏自绘（Go 侧 Frameless + 透明），拖动走 useWindowDrag。 -->
  <div class="nas-root">
    <header class="nas-bar" @mousedown="onDragDown">
      <!-- 标题栏：logo + 「对号」（窗口标题同款，用户定稿保持「对号」） -->
      <span class="nas-brand">
        <img class="brand-mark" src="/favicon.png" alt="对号" draggable="false" />
        <b>对号</b>
      </span>
      <span class="nas-drag"></span>
      <button class="nas-tool" :title="t('life.itTools')" @click="openIT">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round">
          <rect x="3" y="4" width="18" height="12" rx="2" /><path d="M8 20h8M12 16v4" />
        </svg>
        <span>{{ t('life.itTools') }}</span>
      </button>
      <!-- 主题切换：放在「IT 工具」右侧（用户定稿）。任何窗口换主题，其它窗口
           与 NAS 网页都跟着变（后端广播 + webdavd 的 SSE），与 IT 工具窗里的
           那个入口是同一件事 -->
      <div class="nas-theme">
        <button class="nas-win" :title="t('settings.theme')" @click.stop="toggleTheme">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round">
            <circle cx="12" cy="12" r="4.2" />
            <path d="M12 2.5v2.2M12 19.3v2.2M4.2 4.2l1.6 1.6M18.2 18.2l1.6 1.6M2.5 12h2.2M19.3 12h2.2M4.2 19.8l1.6-1.6M18.2 5.8l1.6-1.6" />
          </svg>
        </button>
        <div v-if="themeOpen" class="nas-theme-menu">
          <button
            v-for="opt in themeOptions"
            :key="opt.value"
            :class="{ on: settingsStore.settings.theme === opt.value }"
            @click.stop="pickTheme(opt.value)"
          >{{ t(opt.label) }}</button>
        </div>
      </div>
      <!-- 最小化按钮已恢复（用户要求）：最小化后走悬浮球菜单 / 托盘「主窗口」
           或 IT 工具窗里的入口都能唤回。NAS 窗的隐藏走「关闭」按钮（Go 侧
           拦成隐藏，不是销毁）。 -->
      <button class="nas-win" :title="t('window.minimize')" @click="minimiseWin">
        <svg width="10" height="10" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path d="M5 12h14" /></svg>
      </button>
      <button class="nas-win close" title="关闭" @click="closeWin">
        <svg width="10" height="10" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path d="M6 6l12 12M18 6L6 18" /></svg>
      </button>
    </header>
    <LifeHomeView class="nas-body" />
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { Window } from '@wailsio/runtime'
import { ElMessage } from 'element-plus'
import LifeHomeView from './homeviews/life/LifeHomeView.vue'
import { useWindowDrag } from './composables/useWindowDrag'
import { useI18n } from './i18n'
import { useSettingsStore } from './stores/settingsStore'
import type { AppSettings } from './types/settings'
import { ShowITWindow } from '../bindings/github.com/ys-ll/uniterm/app'

const { t } = useI18n()
const settingsStore = useSettingsStore()

// 主题切换：改值 + 立刻保存（save 走 IPC，后端再把新值广播给所有窗口和
// NAS 网页；本窗口靠 settingsStore 的 watch 自己先应用，不等往返）。
const themeOpen = ref(false)
// 顺序（用户定稿）：深色 / 浅色 / 深蓝 / 深红 / 浅红 / 跟随系统（跟随系统放最后）
const themeOptions: { value: AppSettings['theme']; label: string }[] = [
  { value: 'dark', label: 'settings.themeDark' },
  { value: 'light', label: 'settings.themeLight' },
  { value: 'deep-blue', label: 'settings.themeDeepBlue' },
  { value: 'duihao-red', label: 'settings.themeDuihaoRed' },
  { value: 'duihao-light', label: 'settings.themeDuihaoLight' },
  { value: 'system', label: 'settings.themeSystem' }
]
function toggleTheme() {
  themeOpen.value = !themeOpen.value
}
function pickTheme(v: AppSettings['theme']) {
  themeOpen.value = false
  if (settingsStore.settings.theme === v) return
  settingsStore.settings.theme = v
  void settingsStore.save()
}
// 点窗外收起菜单（标题栏那块是拖动区，得放过它自己的点击）
function onDocClick() {
  themeOpen.value = false
}
const { onDragDown, syncMaximised } = useWindowDrag()

// IT 工具窗＝主窗口（侧栏 + 标签页 + 终端）。它常驻后台，这里只是唤起。
// 失败要出声：早期版本这条路静默失败时，用户只看到「点了没反应」。
async function openIT() {
  try {
    await ShowITWindow()
  } catch (e: any) {
    ElMessage.error('IT: ' + String(e?.message || e))
  }
}
// 关闭按钮由 Go 侧拦成「隐藏」（主窗常驻），这里照常调 Close 即可。
function closeWin() {
  void Window.Close()
}
function minimiseWin() {
  void Window.Minimise()
}

onMounted(() => {
  window.addEventListener('resize', syncMaximised)
  document.addEventListener('click', onDocClick)
  void syncMaximised()
})
onBeforeUnmount(() => document.removeEventListener('click', onDocClick))
</script>

<style scoped>
.nas-root {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  border-radius: var(--win-radius);
  overflow: hidden;
  background: var(--bg-base);
  /* 整窗不出滚动条：内容区自己按高度收敛（见 LifeHomeView 的 .nas-body 覆盖） */
  overflow-y: hidden;
}
.nas-bar {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  flex-shrink: 0;
  height: 2.25rem;
  padding: 0 0.3rem 0 0.75rem;
  box-sizing: border-box;
  background: var(--bg-elevated);
  user-select: none;
  /* 拖动全走 JS（useWindowDrag），显式退出 wails 的原生拖动区 */
  --wails-draggable: no-drag;
}
.nas-brand {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  font-size: 0.8125rem;
  color: var(--text-primary);
  white-space: nowrap;
}
.brand-mark {
  width: 1rem;
  height: 1rem;
  border-radius: 0.25rem;
  flex: 0 0 auto;
  /* 图标与标题一起被拖动，别让浏览器当成可拖文件 */
  -webkit-user-drag: none;
}
.nas-brand b {
  font-weight: 700;
  letter-spacing: 0.02em;
}
.nas-drag {
  flex: 1;
  height: 100%;
}
.nas-tool {
  display: inline-flex;
  align-items: center;
  gap: 0.2rem;
  height: 1.5rem;
  padding: 0 0.4rem;
  border: 1px solid var(--border-subtle);
  border-radius: 999px;
  background: transparent;
  color: var(--text-secondary);
  font-size: 0.625rem;
  font-family: inherit;
  cursor: pointer;
  white-space: nowrap;
}
.nas-tool:hover {
  color: var(--accent);
  border-color: var(--accent);
}
.nas-win {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.325rem;
  height: 1.325rem;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  flex-shrink: 0;
}
.nas-win svg {
  stroke: currentColor;
  fill: none;
}
.nas-win:hover {
  background: var(--bg-surface);
  color: var(--text-primary);
}
.nas-theme {
  position: relative;
  flex: 0 0 auto;
}
.nas-theme-menu {
  position: absolute;
  right: 0;
  top: calc(100% + 0.3rem);
  z-index: 30;
  display: flex;
  flex-direction: column;
  min-width: 6.6rem;
  padding: 0.25rem;
  background: var(--bg-overlay);
  border: 1px solid var(--border-subtle);
  border-radius: 0.5rem;
  box-shadow: 0 12px 28px rgba(0, 0, 0, 0.35);
}
.nas-theme-menu button {
  padding: 0.32rem 0.5rem;
  border: 0;
  border-radius: 0.3rem;
  background: transparent;
  color: var(--text-secondary);
  font: inherit;
  font-size: 0.6875rem;
  text-align: left;
  white-space: nowrap;
  cursor: pointer;
}
.nas-theme-menu button:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}
.nas-theme-menu button.on {
  color: var(--accent);
  font-weight: 600;
}
.nas-theme-menu button.on::after {
  content: '✓';
  float: right;
  margin-left: 0.5rem;
}
.nas-win.close:hover {
  background: #e81123;
  color: #fff;
}
.nas-body {
  flex: 1;
  min-height: 0;
}
</style>
