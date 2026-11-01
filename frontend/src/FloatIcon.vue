<template>
  <!-- uTools 桌面小球：只有一枚圆形品牌图标，窗口背景全透明（无外圈、无底色、
       无投影）。左键 = 与 Alt+Space 相同的切换（App.PaletteToggle）；按住拖拽
       = 移动窗口，松手记住落点；右键 = 原生操作菜单（主窗口 / 设置 / 退出，
       App.ShowFloatMenu）。 -->
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
    <span class="fi-ring" aria-hidden="true"></span>
    <img class="fi-logo" src="/float-icon.png" alt="对号" draggable="false" />
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { Window } from '@wailsio/runtime'
import { HideFloatMenu, PaletteToggle, ShowFloatMenu } from '../bindings/github.com/ys-ll/uniterm/app'

const el = ref<HTMLDivElement | null>(null)
const dragging = ref(false)

const POS_KEY = 'duihao.floatPos.v2'
// 球窗逻辑尺寸：40px 图标 + 两侧各 4px 白边 = 48，横向再留 12×2（阴影左右
// 羽化）、纵向留上 10 / 下 20（阴影 `0 5px 12px` 往下落）。改环宽/阴影必须
// 重算，并同步 main.go 里 floatWin 的 Width/Height/MinWidth/MinHeight 和两处
// forceWindowSize（系统会按最小跟踪尺寸把窗掰大，不掰回来环和阴影会被裁）。
const BALL_W = 72
const BALL_H = 78
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

// 启动定位（用户定稿）：默认**居右 10px、距底 100px**（按窗口外框算，不是图标
// 外缘 —— 窗内右侧留了 12px 阴影、底部留了 20px，肉眼看到的是图标比这再进一点）。
// 有记忆位就恢复并钳回屏幕内（换显示器 / 改分辨率后不会丢球）。
// 注意 POS_KEY 升到 .v2：默认位变了，旧记忆会让新默认位永远看不到。
onMounted(() => {
  // 钳制边界用「窗口完全在屏内」的真极限；默认位另算（含 10 / 100 的留白），
  // 否则用户把球拖到最右下角，重启会被默认位的留白拉回来。
  const maxX = Math.max(0, window.screen.availWidth - BALL_W)
  const maxY = Math.max(0, window.screen.availHeight - BALL_H)
  let x = Math.max(0, maxX - 10)
  let y = Math.max(0, maxY - 100)
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
   全局 style.css 的深色 body 背景（+ 1px padding 内缩 = 白边线）就会透出来。
   **完全透明**：这里必须把全局的 #app 主题渐变、body::before 噪点都压掉，
   否则窗口里会留一层几乎看不见的灰膜（噪点 opacity 0.015 在浅色桌面上
   仍能看出一个方块）。 */
html, body, #app {
  margin: 0 !important;
  padding: 0 !important;
  border: none !important;
  border-radius: 0 !important;
  background: none !important;
  background-color: transparent !important;
  box-shadow: none !important;
  overflow: hidden;
  width: 100%;
  height: 100%;
}
/* style.css 的 body::before 噪点纹理：整窗一层，透明窗上就是灰膜 */
html body::before {
  display: none !important;
}
</style>

<style scoped>
/* 图标 + 一圈 4px 纯白描边 + 描边外的向下投影：窗口 72×78（横向 48+24、
   纵向 48+30，阴影要落在窗口内，否则被边界硬裁成一道直线）。
   圆心 (36,34)，即左右留 12、上留 10、下留 20 —— 下留得多是为了装阴影。
   窗口背景仍是全透明，四角透出壁纸。 */
.fi-wrap {
  box-sizing: border-box;
  display: grid;
  place-items: center;
  width: 72px;
  height: 78px;
  padding: 10px 12px 20px;
  background: transparent;
  border: none;
  cursor: pointer;
  user-select: none;
  touch-action: none;
}
/* 白环 + 投影都画在 40px 的环元素上（与图标同尺寸、同格叠放）：
   ① `0 0 0 4px #fff` 是外扩 4 的实心白环（外径 48）；
   ② `0 4px 10px rgba(0,0,0,.22)` 是白环之外的向下投影 —— 前一层不透明，
      阴影在环内侧看不见，只在环外侧露出一圈，正好是「浮」起来的感觉。
   用 box-shadow 而不是 border：不吃盒模型，图标不会被挤小。 */
/* 白边留 30% 透明度（alpha .7）：纯白边在浅色壁纸上太"死"，半透明后
   既压得住图标边缘，又不会像贴纸；下层的 0 5px 12px 投影会透上来一点，
   底缘自然变柔。 */
.fi-ring {
  grid-area: 1 / 1;
  width: 40px;
  height: 40px;
  border-radius: 50%;
  box-shadow:
    0 0 0 4px rgba(255, 255, 255, 0.7),
    0 5px 12px rgba(0, 0, 0, 0.26);
  pointer-events: none;
}
/* 悬停只提亮图标本身，不放大、不加底色（放大已按要求去掉） */
.fi-wrap:hover .fi-logo {
  filter: brightness(1.07) saturate(1.04);
}
.fi-wrap.dragging {
  cursor: grabbing;
}
.fi-logo {
  grid-area: 1 / 1;
  display: block;
  width: 40px;
  height: 40px;
  object-fit: cover;
  border-radius: 50%;
  pointer-events: none;
  user-select: none;
  -webkit-user-drag: none;
  transition: filter 0.12s ease;
}
</style>
