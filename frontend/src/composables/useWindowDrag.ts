// 无边框窗口的拖动（把 M20 在管理窗上验证过的那套逻辑抽出来复用）。
//
// 为什么不用 --wails-draggable：①它走 Go 的 startDrag（ReleaseCapture +
// PostMessage WM_NCLBUTTONDOWN），WebView2 子窗口占着鼠标时 ReleaseCapture
// 会失败，窗口纹丝不动也没有报错；②Element Plus 弹窗自带 draggable，同一次
// 拖拽里它把 dialog 的 transform 改了 —— 结果是「窗口没动、里面的 div 动了」。
//
// 这里直接用鼠标屏幕坐标 + 窗口 DIP 坐标做增量，单飞节流推给
// Window.SetPosition，行为确定、不依赖平台细节。
//
// 目前调用方：NAS 主窗（NasStandalone）。ManageTitleBar 里还留着一份等价
// 实现（那份连带最大化同步），后续可以一起收敛过来。
import { onUnmounted, ref } from 'vue'
import { Window } from '@wailsio/runtime'

interface DragState {
  sx: number
  sy: number
  cx: number
  cy: number
  wx: number
  wy: number
  ready: boolean
}

function num(v: unknown): number {
  const n = Number(v)
  return Number.isFinite(n) ? n : 0
}

/**
 * 返回 mousedown 处理器；绑到自绘标题栏上即可拖动窗口。
 * @param blocked 命中这些选择器时不启动拖动（按钮/输入框/链接等）
 */
export function useWindowDrag(blocked = 'button, .el-button, input, a') {
  const maximised = ref(false)
  let drag: DragState | null = null
  let pending: { x: number; y: number } | null = null
  let inFlight = false

  function stopDrag() {
    drag = null
    pending = null
    window.removeEventListener('mousemove', onDragMove, true)
    window.removeEventListener('mouseup', onDragUp, true)
  }

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

  function queueMove() {
    if (!drag || !drag.ready) return
    pending = {
      x: Math.round(drag.wx + (drag.cx - drag.sx)),
      y: Math.round(drag.wy + (drag.cy - drag.sy))
    }
    flushMove()
  }

  function onDragMove(e: MouseEvent) {
    if (!drag) return
    drag.cx = e.screenX
    drag.cy = e.screenY
    queueMove()
  }

  function onDragUp() {
    stopDrag()
  }

  async function onDragDown(e: MouseEvent) {
    if (e.button !== 0) return
    if (maximised.value) return
    const el = e.target as HTMLElement | null
    if (el?.closest(blocked)) return
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

  /** 与主窗口一致：最大化贴边 → 直角，还原 → 恢复 CSS 圆角 */
  async function syncMaximised() {
    try {
      const m = await Window.IsMaximised()
      maximised.value = !!m
      document.documentElement.classList.toggle('win-square', !!m)
    } catch {
      // ignore
    }
  }

  onUnmounted(stopDrag)

  return { onDragDown, maximised, syncMaximised }
}
