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
      <!-- 「IT 工具」入口已挪到下方宫格「IT 专业工具」组标题行（用户定稿）：
           宫格四项本来就是亮起 IT 窗直达对应页面，顶栏这个按钮跟它重叠；
           顶栏精简成 logo+主题+最小化+关闭。 -->
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

    <!-- 启动欢迎页（M52）：进软件第一眼 —— logo + 口号 + 启动进度 + 版权。
         盖满整窗 1.9s 后淡出，点任意处可提前跳过。只在本组件挂载时跑一次：
         窗口最小化 / 隐藏（Go 侧拦成 hide）不会卸载组件，唤回时不会重播。 -->
    <transition name="nas-sp">
      <div v-if="splash" class="nas-splash" @click="splash = false">
        <span class="nas-splash-halo" aria-hidden="true"></span>
        <div class="nas-splash-in">
          <img class="nas-splash-logo" src="/favicon.png" alt="对号" draggable="false" />
          <p class="nas-splash-slogan">
            <b>{{ t('splash.brand') }}</b><span class="sep">·</span>{{ t('splash.slogan') }}
          </p>
          <div class="nas-splash-bar"><i></i></div>
          <p class="nas-splash-boot">{{ t('splash.booting') }}</p>
        </div>
        <p class="nas-splash-copy">{{ copyLine }}</p>
      </div>
    </transition>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { Window } from '@wailsio/runtime'
import LifeHomeView from './homeviews/life/LifeHomeView.vue'
import { useWindowDrag } from './composables/useWindowDrag'
import { useI18n } from './i18n'
import { useSettingsStore } from './stores/settingsStore'
import type { AppSettings } from './types/settings'
import { GetAppInfo } from '../bindings/github.com/ys-ll/uniterm/app'

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
// 关闭按钮由 Go 侧拦成「隐藏」（主窗常驻），这里照常调 Close 即可。
function closeWin() {
  void Window.Close()
}
function minimiseWin() {
  void Window.Minimise()
}

// 启动欢迎页开关：挂载即显示，1.9s 自动收（点一下提前收）。见模板里的 .nas-splash。
const splash = ref(true)
let splashTimer: number | undefined

// 版权行 = 「版权所有 © 对号科技有限公司 · 对号 v<版本>」。版本号从后端取
// （release 构建由 ldflags 注入 main.Version）；本地/开发构建是 "dev"，
// 展示回落成 1.0 —— 只影响这行小字，不碰 devBuild 的判定逻辑。
const ver = ref('1.0')
void GetAppInfo()
  .then((info: { version?: string }) => {
    const v = String(info?.version || '').trim()
    if (v && v !== 'dev') ver.value = v
  })
  .catch(() => {})
const copyLine = computed(() => `${t('splash.copyright')} · ${t('splash.brand')} v${ver.value}`)

onMounted(() => {
  window.addEventListener('resize', syncMaximised)
  document.addEventListener('click', onDocClick)
  void syncMaximised()
  // 启动欢迎页：让第一眼看到品牌 + 口号，同时遮住首帧数据还没回来的空壳
  splashTimer = window.setTimeout(() => (splash.value = false), 1900)
})
onBeforeUnmount(() => {
  document.removeEventListener('click', onDocClick)
  if (splashTimer) window.clearTimeout(splashTimer)
})
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

/* ---------- 启动欢迎页（M52）----------
   版式照「品牌时刻」：中央 logo + 品牌名·口号 + 启动进度 + 贴底版权。
   底色跟主题走（.nas-root 同款 --bg-base），上面叠一团玫红光晕，
   免得一整片纯色显得死板；logo 轻微呼吸、进度条横向扫动，都在 2s 内看完。 */
.nas-splash {
  position: fixed;
  inset: 0;
  z-index: 60;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  border-radius: var(--win-radius, 0);
  background: var(--bg-base);
  cursor: default;
  user-select: none;
}
/* 光晕用品牌玫红（--brand-subtle）：浅色主题下也是淡淡一层，不压字 */
.nas-splash-halo {
  position: absolute;
  top: 18%;
  width: 240px;
  height: 240px;
  border-radius: 50%;
  background: radial-gradient(circle, var(--brand-subtle), transparent 70%);
  animation: nasHalo 3.4s ease-in-out infinite;
}
.nas-splash-in {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: center;
  width: 100%;
  padding: 0 1.25rem;
  /* 整体上移一档：垂直居中会让「版权贴底 + 内容居中」的上半部显得空，
     上移 ~60px 后 logo 落在窗口 1/3 处（与参考版式一致），下方留给版权 */
  margin-bottom: 6rem;
  text-align: center;
}
.nas-splash-logo {
  display: block;
  width: 60px;
  height: 60px;
  border-radius: 15px;
  box-shadow: 0 10px 26px rgba(0, 0, 0, 0.32);
  animation: nasBreathe 2.8s ease-in-out infinite;
}
.nas-splash-slogan {
  margin: 0.95rem 0 0;
  font-size: 0.8125rem;
  line-height: 1.45;
  color: var(--text-secondary);
  /* 330 宽里长口号（德/法/俄）会换行 —— 允许两行，别硬压字号 */
  max-width: 17rem;
}
.nas-splash-slogan b {
  color: var(--text-primary);
  font-weight: 600;
  letter-spacing: 0.06em;
}
.nas-splash-slogan .sep {
  margin: 0 0.4em;
  color: var(--brand);
  font-weight: 700;
}
.nas-splash-bar {
  position: relative;
  width: 7rem;
  height: 3px;
  margin-top: 1.15rem;
  overflow: hidden;
  border-radius: 2px;
  background: var(--brand-subtle);
}
.nas-splash-bar i {
  position: absolute;
  top: 0;
  left: 0;
  width: 38%;
  height: 100%;
  border-radius: 2px;
  background: linear-gradient(90deg, transparent, var(--brand), var(--brand-bright), transparent);
  animation: nasBar 1.15s ease-in-out infinite;
}
.nas-splash-boot {
  margin: 0.62rem 0 0;
  font-size: 0.625rem;
  color: var(--text-muted);
}
.nas-splash-copy {
  position: absolute;
  bottom: 0.9rem;
  left: 0;
  right: 0;
  margin: 0;
  padding: 0 1rem;
  font-size: 0.5625rem;
  line-height: 1.4;
  color: var(--text-muted);
  opacity: 0.72;
  text-align: center;
}
@keyframes nasBreathe {
  0%, 100% { transform: scale(1); opacity: 0.94; }
  50% { transform: scale(1.045); opacity: 1; }
}
@keyframes nasHalo {
  0%, 100% { opacity: 0.5; transform: scale(1); }
  50% { opacity: 0.95; transform: scale(1.07); }
}
@keyframes nasBar {
  0% { transform: translateX(-100%); }
  100% { transform: translateX(320%); }
}
/* 淡出：260ms，比常见弹层快一点 —— 用户等的是内容，不是动画 */
.nas-sp-leave-active { transition: opacity 0.26s ease; }
.nas-sp-leave-to { opacity: 0; }
@media (prefers-reduced-motion: reduce) {
  .nas-splash-logo,
  .nas-splash-halo,
  .nas-splash-bar i { animation: none; }
}
</style>
