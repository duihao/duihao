<template>
  <!-- uTools 桌面小球：圆形品牌钮 + 半透明外圈。左键 = 与 Alt+Space 相同的
       切换（App.PaletteToggle）；按住拖拽 = 移动窗口，松手记住落点；右键 =
       原生操作菜单（主窗口 / 设置 / 退出，App.ShowFloatMenu）。 -->
  <div
    ref="el"
    class="fi-wrap"
    :class="{ dragging }"
    @pointerdown="onDown"
    @pointermove="onMove"
    @pointerup="onUp"
    @pointercancel="onUp"
    @lostpointercapture="onUp"
    @click="onClick"
    @contextmenu.prevent="onMenu"
  >
    <img class="fi-logo" src="/float-icon.png" alt="对号" draggable="false" />
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { Window } from '@wailsio/runtime'
import { HideFloatMenu, PaletteToggle, ShowFloatMenu } from '../bindings/github.com/ys-ll/uniterm/app'

const el = ref<HTMLDivElement | null>(null)
const dragging = ref(false)

const POS_KEY = 'duihao.floatPos'
const BALL = 44
const DRAG_SLOP = 4

// 拖拽位移必须用 **screenX/screenY**（绝对屏幕坐标），不能用 clientX/clientY：
// 拖的时候窗口本身在动，clientX 是相对窗口的，窗口一动就抵掉一部分位移 ——
// 实测只走到 76% 就掉队，跳变大时直接停住。screenX 与窗口位置无关，位移恒
// 等于光标位移；它和 Window.Position/SetPosition 同为 DIP，可直接运算（wails
// 内部再转物理像素，别自己乘 devicePixelRatio）。
// 为什么不在 Go 消息层做：WebView2 的内容窗口属于 msedgewebview2.exe 另一个
// 进程，鼠标消息不经过宿主 WndProc，SetWindowLongPtr 收不到 WM_LBUTTONDOWN。
const DRAG_THRESHOLD = DRAG_SLOP

interface DragState {
  pointerId: number
  startScreenX: number
  startScreenY: number
  lastScreenX: number
  lastScreenY: number
  winX: number
  winY: number
  ready: boolean
  moved: boolean
}

let drag: DragState | null = null
let lastDragMoved = false

function onDown(e: PointerEvent) {
  if (e.button !== 0) return
  // 拖拽/单击开始就收起右键菜单（菜单是独立窗口，不会自己知道球被点了）
  void HideFloatMenu()
  lastDragMoved = false
  // 同步捕获指针：鼠标移出 44px 小窗也继续收到 move，拖拽不脱手
  try {
    el.value?.setPointerCapture(e.pointerId)
  } catch {
    /* 捕获失败则退化为仅窗内拖动 */
  }
  dragging.value = true
  drag = {
    pointerId: e.pointerId,
    startScreenX: e.screenX,
    startScreenY: e.screenY,
    lastScreenX: e.screenX,
    lastScreenY: e.screenY,
    winX: 0,
    winY: 0,
    ready: false,
    moved: false,
  }
  // 窗口原点只取一次；取到之前的 move 也照记，就绪后按绝对差值一次算准
  Window.Position()
    .then((pos) => {
      if (!drag || drag.pointerId !== e.pointerId) return
      drag.winX = pos.x
      drag.winY = pos.y
      drag.ready = true
      applyDrag()
    })
    .catch(() => {
      dragging.value = false
      drag = null
    })
}

function applyDrag() {
  if (!drag || !drag.ready) return
  const x = Math.round(drag.winX + (drag.lastScreenX - drag.startScreenX))
  const y = Math.round(drag.winY + (drag.lastScreenY - drag.startScreenY))
  void Window.SetPosition(x, y)
}

function onMove(e: PointerEvent) {
  if (!drag || e.pointerId !== drag.pointerId) return
  drag.lastScreenX = e.screenX
  drag.lastScreenY = e.screenY
  if (!drag.moved) {
    const dx = e.screenX - drag.startScreenX
    const dy = e.screenY - drag.startScreenY
    if (Math.abs(dx) < DRAG_THRESHOLD && Math.abs(dy) < DRAG_THRESHOLD) return
    drag.moved = true
  }
  applyDrag()
}

function onUp(e: PointerEvent) {
  if (!drag || e.pointerId !== drag.pointerId) return
  try {
    el.value?.releasePointerCapture(e.pointerId)
  } catch {
    /* 已释放则忽略 */
  }
  dragging.value = false
  const moved = drag.moved
  if (moved) {
    lastDragMoved = true
    // 记住落点，下次启动恢复
    Window.Position()
      .then((pos) => savePos(pos))
      .catch(() => {})
  }
  // click 在 pointerup 之后才派发（多半赶在定时器前，drag 还在）；万一定时器
  // 先跑，靠 lastDragMoved 兜底，下一次 pointerdown 会重置它。
  setTimeout(() => {
    drag = null
  }, 0)
}

function savePos(p: { x: number; y: number }) {
  try {
    localStorage.setItem(POS_KEY, JSON.stringify({ x: Math.round(p.x), y: Math.round(p.y) }))
  } catch {
    /* 存储不可用则不记忆 */
  }
}

async function onClick() {
  if (lastDragMoved) {
    lastDragMoved = false
    return
  }
  if (drag?.moved) return
  try {
    await PaletteToggle()
  } catch {
    /* 面板窗不可用时忽略 */
  }
}

// 右键菜单：由 Go 侧把独立的 148×136 菜单窗摆到球正下方（App.ShowFloatMenu）。
// 44px 的球窗画不出 148 宽的卡片，所以菜单必须是自己的窗口；选项点击在
// FloatMenu.vue 里调 FloatMenuAction 执行。
async function onMenu() {
  try {
    await ShowFloatMenu()
  } catch {
    /* 菜单窗不可用时忽略 */
  }
}

// 启动定位：默认主屏右缘偏下（uTools 同款），有记忆位就恢复并钳回屏幕内
// （换显示器后不会丢球）。Go 侧首显钩子不摆位置，避免盖掉用户拖拽落点。
onMounted(() => {
  const maxX = Math.max(0, window.screen.availWidth - BALL)
  const maxY = Math.max(0, window.screen.availHeight - BALL)
  let x = maxX - 12
  let y = Math.round(window.screen.availHeight * 0.58) - BALL / 2
  try {
    const saved = localStorage.getItem(POS_KEY)
    if (saved) {
      const p = JSON.parse(saved) as { x?: number; y?: number }
      if (typeof p.x === 'number' && typeof p.y === 'number') {
        x = Math.min(Math.max(0, Math.round(p.x)), maxX)
        y = Math.min(Math.max(0, Math.round(p.y)), maxY)
      }
    }
  } catch {
    /* 解析失败用默认位 */
  }
  void Window.SetPosition(x, y)
})
</script>

<style>
/* 非 scoped：scoped 会给选择器追加 data-v 属性，html/body/#app 匹配不上，
   全局 style.css 的深色 body 背景（+ 1px padding 内缩 = 白边线）就会透出来。 */
html, body, #app {
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
/* 半透明外圈：44px 窗口内一圈淡色圆环，球体 34px 居中，环带约 5px。
   白色半透明在浅色/深色桌面上都能看出轮廓；外描 1px 极淡黑边保证白桌面
   上也有边界。 */
.fi-wrap {
  box-sizing: border-box;
  width: 44px;
  height: 44px;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.14);
  border: 1px solid rgba(255, 255, 255, 0.45);
  box-shadow:
    0 0 0 1px rgba(0, 0, 0, 0.07),
    inset 0 0 6px rgba(255, 255, 255, 0.12),
    0 2px 10px rgba(0, 0, 0, 0.22);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  user-select: none;
  touch-action: none;
  transition:
    background 0.12s ease,
    border-color 0.12s ease,
    transform 0.12s ease;
}
.fi-wrap:hover {
  background: rgba(255, 255, 255, 0.22);
  border-color: rgba(255, 255, 255, 0.65);
}
.fi-wrap.dragging {
  transform: scale(1.06);
  border-color: rgba(255, 255, 255, 0.7);
  background: rgba(255, 255, 255, 0.26);
  cursor: grabbing;
}
.fi-logo {
  display: block;
  width: 34px;
  height: 34px;
  object-fit: cover;
  border-radius: 50%;
  pointer-events: none;
  user-select: none;
  -webkit-user-drag: none;
}
</style>
