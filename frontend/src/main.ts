import { createApp } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus from 'element-plus'
import 'element-plus/dist/index.css'
import '@fontsource-variable/jetbrains-mono'
import { ElDialog } from 'element-plus'
import App from './App.vue'
import './style.css'
import { Window, Events } from '@wailsio/runtime'
import { useSettingsStore } from './stores/settingsStore'
import { setLocale } from './i18n'

// M18-C：/?w=palette 是独立悬浮命令面板窗（用户定稿的主入口）—— 只挂
// CommandPalette，不跑主窗口的 IME/右键菜单等全局副作用，也不改窗口标题
// （标题参与后端 findMainWindow 匹配，不能覆盖成「对号」）。
// /?w=float 是桌面常驻悬浮图标窗（48×48 圆形），只挂 FloatIcon。
const wMode = new URLSearchParams(window.location.search).get('w')
const isPaletteWindow = wMode === 'palette'
const isFloatWindow = wMode === 'float'
// /?w=floatmenu：悬浮球右键菜单窗（148×136 透明小窗，Go 侧摆在球正下方）
const isFloatMenuWindow = wMode === 'floatmenu'

if (!isPaletteWindow && !isFloatWindow && !isFloatMenuWindow) {
  Window.SetTitle('对号')
}

// 独立面板窗/悬浮窗是独立 webview，index.html 的首帧主题只看
// prefers-color-scheme；若 LoadSettings 稍慢或失败就会落到浅色底，面板在
// 深色应用上看起来「泛白/半透明」。这里先用主窗口写下的缓存主题兜底
// （settingsStore.init() 之后仍会用真实设置覆盖）。
{
  const cached = localStorage.getItem('appTheme')
  if (cached) document.documentElement.dataset.theme = cached
}

// Set ElDialog draggable by default
if (ElDialog.props) {
  ElDialog.props.draggable = { type: Boolean, default: true }
}

const app = createApp(App)
const pinia = createPinia()
app.use(pinia)
app.use(ElementPlus)

const settingsStore = useSettingsStore()
// Sync locale from navigator before mount (default 'system' → resolves from
// navigator.language, zero IPC) so the first paint is already in the user's
// language; init() re-resolves with the persisted preference once loaded.
setLocale('system')
// Fire settings init without awaiting: top-level await here used to block the
// module's completion, which delayed the page load event, which delayed Wails'
// window Show() — seconds of dock-icon-but-no-window on slow first paint. All
// settings consumers are reactive (computed/watch/template), so values apply
// themselves once init lands. applyTheme() on the loaded defaults runs before
// mount anyway (init() calls it synchronously after its awaits settle).
//
// 只给主窗口跑完整 init：面板窗/悬浮窗只需主题，若也跑 init，一旦
// LoadSettings 还没就绪就会用默认值（system→浅色）applyTheme，把 index.html
// 与主窗口写下的 appTheme 缓存覆盖成浅色 —— 面板就会在深色应用上泛白。
if (!isPaletteWindow && !isFloatWindow && !isFloatMenuWindow) {
  settingsStore.init()
}

if (isPaletteWindow) {
  import('./PaletteStandalone.vue').then(({ default: PaletteStandalone }) => {
    const paletteApp = createApp(PaletteStandalone)
    paletteApp.use(pinia)
    paletteApp.use(ElementPlus)
    paletteApp.mount('#app')
  })
} else if (isFloatWindow) {
  import('./FloatIcon.vue').then(({ default: FloatIcon }) => {
    const floatApp = createApp(FloatIcon)
    floatApp.mount('#app')
  })
} else if (isFloatMenuWindow) {
  import('./FloatMenu.vue').then(({ default: FloatMenu }) => {
    const menuApp = createApp(FloatMenu)
    menuApp.mount('#app')
  })
} else {
  // 最大化/全屏 → 窗口直角（贴边不留圆角），还原 → 恢复 CSS 圆角。事件源是
  // Go 侧窗口事件（events.Windows.*）的 app:win-shape 广播；启动即最大化/
  // 全屏的场景用 IsMaximised/IsFullscreen 补一次初始状态。wails 有时会把
  // payload 再包一层 {data:...}，两种形状都兼容。
  const setWinSquare = (v: boolean) => {
    document.documentElement.classList.toggle('win-square', v)
  }
  Events.On('app:win-shape', (ev: unknown) => {
    const data = (ev as { data?: unknown } | null)?.data
    setWinSquare(data === true || (data as { data?: boolean } | null)?.data === true)
  })
  void Promise.all([Window.IsMaximised(), Window.IsFullscreen()])
    .then(([m, f]) => setWinSquare(!!m || !!f))
    .catch(() => {})
  app.mount('#app')
}

// Global IME guard: swallow keydown/keyup events consumed by an IME
// composition (e.g. Enter confirming a candidate word) before any
// element-level handler mistakes them for app shortcuts. xterm terminals are
// exempt — they manage their own IME pipeline.
//
// keydown: isComposing is the standard signal; keyCode 229 is the phantom code
// WKWebView reports for composition keystrokes where isComposing is unreliable.
//
// keyup: the commit key's keyup arrives AFTER compositionend, so isComposing
// is already false there. Track state via composition events instead: while
// composing, keyups are blocked; on compositionend exactly one keyup (the
// commit key's) is blocked. A real (non-IME) keydown disarms the pending
// swallow, so a mouse-click commit (no keyup) cannot swallow the user's next
// keystroke.
{
  const inTerminal = (t: EventTarget | null) =>
    !!(t as Element | null)?.closest?.('.xterm')
  let inComposition = false
  let swallowNextKeyup = false

  document.addEventListener('keydown', (e) => {
    if (inTerminal(e.target)) return
    // keyCode is deprecated, but 229 is kept deliberately: WKWebView (macOS)
    // reports composition keystrokes as the phantom keyCode 229, and `key`
    // ("Process") is not reliably set there.
    if (e.isComposing || e.key === 'Process' || e.keyCode === 229) {
      e.stopPropagation()
      return
    }
    // A real keystroke invalidates a pending swallow.
    swallowNextKeyup = false
  }, true)

  document.addEventListener('compositionstart', () => {
    inComposition = true
    swallowNextKeyup = false
  }, true)

  document.addEventListener('compositionend', () => {
    inComposition = false
    swallowNextKeyup = true
  }, true)

  document.addEventListener('keyup', (e) => {
    if (inTerminal(e.target)) return
    if (inComposition || swallowNextKeyup) {
      swallowNextKeyup = false
      e.stopPropagation()
    }
  }, true)
}

// Global context menu closer: broadcast to all menu components via window event
document.addEventListener('contextmenu', () => {
  window.dispatchEvent(new CustomEvent('global:close-context-menus'))
}, true)

interface EditableSelectionSnapshot {
  target: HTMLElement
  start?: number | null
  end?: number | null
  range?: Range
}

const isEditableTarget = (target: EventTarget | null): target is HTMLElement => {
  if (!(target instanceof HTMLElement)) return false
  return target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable
}

let rightClickSelection: EditableSelectionSnapshot | null = null

function captureEditableSelection(target: HTMLElement): EditableSelectionSnapshot {
  if (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA') {
    const input = target as HTMLInputElement | HTMLTextAreaElement
    return { target, start: input.selectionStart, end: input.selectionEnd }
  }
  const selection = window.getSelection()
  if (selection && selection.rangeCount > 0) {
    const range = selection.getRangeAt(0)
    if (target.contains(range.commonAncestorContainer)) {
      return { target, range: range.cloneRange() }
    }
  }
  return { target }
}

function restoreEditableSelection(snapshot: EditableSelectionSnapshot) {
  const { target } = snapshot
  target.focus()
  if (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA') {
    const input = target as HTMLInputElement | HTMLTextAreaElement
    if (typeof snapshot.start === 'number' && typeof snapshot.end === 'number') {
      input.setSelectionRange(snapshot.start, snapshot.end)
    }
    return
  }
  if (snapshot.range) {
    const selection = window.getSelection()
    selection?.removeAllRanges()
    selection?.addRange(snapshot.range.cloneRange())
  }
}

document.addEventListener('mousedown', (e) => {
  if (e.button !== 2 || !isEditableTarget(e.target)) {
    if (e.button === 0) rightClickSelection = null
    return
  }
  const target = e.target
  rightClickSelection = captureEditableSelection(target)
  e.preventDefault()
}, true)

document.addEventListener('select', (e) => {
  const target = e.target
  if (rightClickSelection?.target === target &&
      (target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement)) {
    const sel = target as HTMLInputElement | HTMLTextAreaElement
    if (typeof rightClickSelection.start === 'number' &&
        typeof rightClickSelection.end === 'number' &&
        (sel.selectionStart !== rightClickSelection.start ||
         sel.selectionEnd !== rightClickSelection.end)) {
      restoreEditableSelection(rightClickSelection)
    }
  }
}, true)

document.addEventListener('contextmenu', (e) => {
  const target = e.target as HTMLElement
  // Read-only log-path toast: offer copy/select-all on its plain text.
  const copyable = target.closest('.msg-copyable') as HTMLElement | null
  if (copyable) {
    e.preventDefault()
    const content = (copyable.querySelector('.el-message__content') as HTMLElement) || copyable
    window.dispatchEvent(new CustomEvent('input:contextmenu', {
      detail: { x: e.clientX, y: e.clientY, target: content, readonly: true }
    }))
    return
  }
  const tag = target.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA' || target.isContentEditable) {
    e.preventDefault()
    if (rightClickSelection?.target === target) {
      restoreEditableSelection(rightClickSelection)
    }
    rightClickSelection = null
    window.dispatchEvent(new CustomEvent('input:contextmenu', {
      detail: { x: e.clientX, y: e.clientY, target }
    }))
    return
  }
  e.preventDefault()
})
