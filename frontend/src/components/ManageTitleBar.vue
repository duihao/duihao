<template>
  <!-- 管理窗口自绘顶栏：左侧 软件图标 + 名称，右侧 最小化 / 最大化 / 关闭。
       实例启停不在这里（放在标题栏下方的工具行，见各管理面板）。
       独立窗口（M20）用；既用于未知面板的独立窗外壳，也嵌进 standalone
       弹窗的 #header 插槽（弹窗=整个窗口，顶栏随弹窗走，永不被盖）。 -->
  <div class="mwt-bar" @mousedown="onDragDown">
    <span class="mwt-ico" :class="'mwt-ico-' + panel">
      <component :is="icon" :size="16" />
    </span>
    <span class="mwt-title">{{ title }}</span>
    <!-- 中间空白 = 拖动区（按钮不吃拖动） -->
    <span class="mwt-drag"></span>
    <WindowControls
      :is-maximised="isMaximised"
      variant="win"
      @minimise="onMinimise"
      @maximise="onMaximise"
      @close="onClose"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { Window } from '@wailsio/runtime'
import { HardDrive, Database, DatabaseZap, FolderUp } from '@lucide/vue'
import WindowControls from './WindowControls.vue'

const props = defineProps<{ panel: string; title: string }>()

const icon = computed(() => {
  switch (props.panel) {
    case 'mysql': return Database
    case 'redis': return DatabaseZap
    case 'ftp': return FolderUp
    default: return HardDrive
  }
})

const isMaximised = ref(false)

// ── 拖动（M20 修正）──────────────────────────────────────────────
// 不用 --wails-draggable：①它走 Go 的 startDrag（ReleaseCapture + PostMessage
// WM_NCLBUTTONDOWN），在 WebView2 子窗口占着鼠标时 ReleaseCapture 会失败，
// 窗口纹丝不动却又没有报错；②Element Plus 弹窗自带 draggable，同一次拖拽里
// 它把 dialog 的 transform 改了 —— 结果就是「窗口没动、里面的 div 动了」。
// 这里直接用鼠标屏幕坐标（DIP）+ 窗口 DIP 坐标做增量，单飞节流推给
// Window.SetPosition，行为确定、不依赖平台细节。
//   ready=false：还在等 Window.Position() 回来，只记录鼠标位置不动窗口；
//   回来后补齐这段位移，拖拽不会「慢半拍」丢距离。
interface DragState {
  sx: number
  sy: number
  cx: number
  cy: number
  wx: number
  wy: number
  ready: boolean
}
let drag: DragState | null = null
let pending: { x: number; y: number } | null = null
let inFlight = false

function num(v: unknown): number {
  const n = Number(v)
  return Number.isFinite(n) ? n : 0
}

function stopDrag() {
  drag = null
  pending = null
  window.removeEventListener('mousemove', onDragMove, true)
  window.removeEventListener('mouseup', onDragUp, true)
}

async function onDragDown(e: MouseEvent) {
  if (e.button !== 0) return
  // 最大化时贴边，不做拖动（与系统行为一致；要移动先还原）
  if (isMaximised.value) return
  const el = e.target as HTMLElement | null
  // 顶栏里的按钮/控件（最小化/最大化/关闭、启停……）不吃拖动
  if (el?.closest('button, .el-button, input, a, .ipow')) return
  e.preventDefault()
  const st: DragState = {
    sx: e.screenX, sy: e.screenY,
    cx: e.screenX, cy: e.screenY,
    wx: 0, wy: 0, ready: false
  }
  drag = st
  window.addEventListener('mousemove', onDragMove, true)
  window.addEventListener('mouseup', onDragUp, true)
  let pos: unknown = null
  try {
    pos = await Window.Position()
  } catch {
    if (drag === st) stopDrag()
    return
  }
  // 等待期间已经松手（或被下一次按下顶掉）→ 丢弃这次
  if (drag !== st) return
  const p = pos as { x?: number; y?: number; X?: number; Y?: number } | null
  st.wx = num(p?.x ?? p?.X)
  st.wy = num(p?.y ?? p?.Y)
  st.ready = true
  queueMove()
}

function onDragMove(e: MouseEvent) {
  if (!drag) return
  drag.cx = e.screenX
  drag.cy = e.screenY
  queueMove()
}

function queueMove() {
  if (!drag || !drag.ready) return
  pending = {
    x: Math.round(drag.wx + (drag.cx - drag.sx)),
    y: Math.round(drag.wy + (drag.cy - drag.sy))
  }
  flushMove()
}

// 单飞：上一帧定位没回来就不再压新请求，避免位置乱序抖动
function flushMove() {
  if (inFlight || !pending) return
  const p = pending
  pending = null
  inFlight = true
  Promise.resolve(Window.SetPosition(p.x, p.y))
    .catch(() => {})
    .finally(() => {
      inFlight = false
      flushMove()
    })
}

function onDragUp() {
  stopDrag()
}

async function syncMaximised() {
  try {
    const m = await Window.IsMaximised()
    isMaximised.value = !!m
    // 与主窗口一致：最大化贴边 → 直角，还原 → 圆角
    document.documentElement.classList.toggle('win-square', !!m)
  } catch {
    // ignore
  }
}

function onMinimise() {
  Window.Minimise()
}
async function onMaximise() {
  try {
    Window.ToggleMaximise()
  } catch {
    // ignore
  }
  // 等窗口真的切换完再读状态
  window.setTimeout(syncMaximised, 120)
}
function onClose() {
  Window.Close()
}

onMounted(() => {
  syncMaximised()
  window.addEventListener('resize', syncMaximised)
})
onUnmounted(() => {
  window.removeEventListener('resize', syncMaximised)
  stopDrag()
})
</script>

<style scoped>
.mwt-bar {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-shrink: 0;
  /* 弹窗 #header 是 justify-content:center 的居中标题样式，顶栏必须自己
     吃掉整行，才能「左图标+标题 / 右窗口按钮」两头分开（横向 flex:1 的
     规则放在 style.css，避免在纵向 flex 外壳里被拉高） */
  width: 100%;
  min-width: 0;
  height: 2.5rem;
  padding: 0 0.25rem 0 0.75rem;
  box-sizing: border-box;
  background: var(--bg-elevated);
  user-select: none;
  -webkit-user-select: none;
  /* 拖动全走 JS（见 onDragDown），显式退出 wails 的原生拖动区 */
  --wails-draggable: no-drag;
}
.mwt-ico {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.5rem;
  height: 1.5rem;
  border-radius: var(--radius-sm);
  color: #fff;
  background: var(--accent);
  flex-shrink: 0;
}
.mwt-ico-mysql { background: #0e7490; }
.mwt-ico-redis { background: #b91c1c; }
.mwt-ico-ftp { background: #6d28d9; }
.mwt-ico-webdav { background: var(--accent); }
.mwt-title {
  font-size: 0.8125rem;
  font-weight: 600;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.mwt-drag {
  flex: 1;
  height: 100%;
}
</style>
