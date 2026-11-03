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
const isNasWindow = wMode === 'nas'
const isPaletteWindow = wMode === 'palette'
const isFloatWindow = wMode === 'float'
// /?w=floatmenu：悬浮球右键菜单窗（148×136 透明小窗，Go 侧摆在球正下方）
const isFloatMenuWindow = wMode === 'floatmenu'
// /?w=manage：管理面板独立窗口（M20）—— 无边框卡片窗，内容仍是同一个管理
// 面板组件（standalone 模式不套 el-dialog），窗口标题由 Go 侧设置。
const isManageWindow = wMode === 'manage'
// /?w=notify：语音提醒到点弹出的右上角卡片（V2）—— 380×124 透明无边框小窗，
// 只挂 NotifyStandalone。窗口本身由 Go 侧创建（notify_window.go），这里只渲染。
const isNotifyWindow = wMode === 'notify'
// /?w=member：会员中心独立窗（B8）—— 880×620 无边框卡片窗，窗口由
// member_window.go 创建。NAS 主窗只有 330 宽，会员中心的套餐卡 + 二维码在
// 竖窗里塞不下，所以做成独立窗而不是 el-dialog。
const isMemberWindow = wMode === 'member'
// /?w=v6：IPv6 直连弹窗（860×660 无边框窗，v6_window.go 创建）—— 设置里
// 「IPv6 远程访问」不再是跳到网盘网页的 /-remote，而是这个向导式窗口：
// 检测 -> 激活 -> 地址/域名/解绑，一条线走完。
const isV6Window = wMode === 'v6'
// /?w=fixip：固定内网地址弹窗（520×640 无边框窗，fixip_window.go 创建）——
// 设置里点「固定内网地址」不再是「即将上线」，而是真把当前网卡从 DHCP 切成
// 固定 IP（后端 netsh + UAC）。
const isFixIpWindow = wMode === 'fixip'

// M36：IT 工具窗（原主窗）标题 = 「对号 IT工具」，与起始页大标题「对号 · IT工具」
// 同一套叫法（用户定稿：标题栏不要只写「对号」）。仍以「对号」开头、且不含
// 「 · 」，后端 findMainWindow 的前缀匹配照旧成立；NAS 主窗标题是「对号」，
// 反而更容易区分。
if (!isPaletteWindow && !isFloatWindow && !isFloatMenuWindow && !isManageWindow && !isNotifyWindow && !isNasWindow && !isMemberWindow && !isV6Window && !isFixIpWindow) {
  Window.SetTitle('对号 IT工具')
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
if (!isPaletteWindow && !isFloatWindow && !isFloatMenuWindow && !isNotifyWindow) {
  settingsStore.init()
}

// 轻窗（命令面板 / 悬浮球 / 悬浮菜单 / 提醒卡）不跑完整的 settingsStore.init()
// —— 那会被 LoadSettings 的 IPC 拖慢，面板要求「按下热键立刻出来」。但它们同样
// 得跟着主题走：任一窗口保存设置时后端广播 store:settings:changed，这里只取
// theme 一项，解析出具体明暗后落到 <html data-theme> 并刷新首帧缓存。
if (isPaletteWindow || isFloatWindow || isFloatMenuWindow || isNotifyWindow) {
  let lastRawTheme = ''
  const applyThemeLite = (raw: string) => {
    lastRawTheme = raw
    const t =
      raw === 'system'
        ? window.matchMedia('(prefers-color-scheme: dark)').matches
          ? 'dark'
          : 'light'
        : raw
    document.documentElement.dataset.theme = t
    try {
      localStorage.setItem('appTheme', t)
    } catch {
      // 隐私模式等：本次会话照样生效
    }
  }
  // 选的是「跟随系统」时，系统明暗一变就得重解一次（其它档位忽略）
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
    if (lastRawTheme === 'system') applyThemeLite('system')
  })
  Events.On('store:settings:changed', (ev: unknown) => {
    const d = (ev as { data?: { theme?: string } } | null)?.data
    if (d?.theme) applyThemeLite(d.theme)
  })
}

if (isNasWindow) {
  document.documentElement.classList.add('win-main')
  // NAS 主窗沿用品牌玫红（原来普通模式靠 ui-home 换肤，现在整窗搬过来）
  document.documentElement.classList.add('ui-home')
  import('./NasStandalone.vue').then(({ default: NasStandalone }) => {
    const nasApp = createApp(NasStandalone)
    nasApp.use(pinia)
    nasApp.use(ElementPlus)
    nasApp.mount('#app')
  })
} else if (isPaletteWindow) {
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
} else if (isNotifyWindow) {
  // 卡片自己画圆角与阴影（style.css 的 html.win-notify 把 body 底色清成透明），
  // 不跑 settingsStore.init：这张卡要「瞬间」出现，不能等 IPC 往返。
  document.documentElement.classList.add('win-notify')
  import('./NotifyStandalone.vue').then(({ default: NotifyStandalone }) => {
    const notifyApp = createApp(NotifyStandalone)
    notifyApp.mount('#app')
  })
} else if (isManageWindow) {
  // 管理窗与主窗口同款外观：加 win-main 才有 body::after 的 1px 描边；
  // win-manage 供全局 CSS 收敛弹窗遮罩层（style.css 末段）。
  document.documentElement.classList.add('win-main')
  document.documentElement.classList.add('win-manage')
  import('./ManageStandalone.vue').then(({ default: ManageStandalone }) => {
    const manageApp = createApp(ManageStandalone)
    manageApp.use(pinia)
    manageApp.use(ElementPlus)
    manageApp.mount('#app')
  })
} else if (isMemberWindow) {
  // 会员中心独立窗：同款描边 + 圆角（win-main），语言/主题跟主窗口一致。
  // ui-home = 品牌玫红 accent（与 NAS 主窗一致，会员是 NAS 侧入口）。
  document.documentElement.classList.add('win-main')
  document.documentElement.classList.add('ui-home')
  import('./MembershipStandalone.vue').then(({ default: MembershipStandalone }) => {
    const memberApp = createApp(MembershipStandalone)
    memberApp.use(pinia)
    memberApp.use(ElementPlus)
    memberApp.mount('#app')
  })
} else if (isV6Window) {
  // IPv6 直连弹窗：同款描边/圆角 + 品牌玫红
  document.documentElement.classList.add('win-main')
  document.documentElement.classList.add('ui-home')
  import('./V6Standalone.vue').then(({ default: V6Standalone }) => {
    const v6App = createApp(V6Standalone)
    v6App.use(pinia)
    v6App.use(ElementPlus)
    v6App.mount('#app')
  })
} else if (isFixIpWindow) {
  // 固定内网地址弹窗：同款描边/圆角 + 品牌玫红
  document.documentElement.classList.add('win-main')
  document.documentElement.classList.add('ui-home')
  import('./FixIpStandalone.vue').then(({ default: FixIpStandalone }) => {
    const fxApp = createApp(FixIpStandalone)
    fxApp.use(pinia)
    fxApp.use(ElementPlus)
    fxApp.mount('#app')
  })
} else {
  // 标记「这是主窗口」：窗口描边（style.css 的 body::after）只给主窗口画，
  // 悬浮球（48px 圆形）与悬浮菜单是透明小窗，套 1px 方框会露出四角。
  document.documentElement.classList.add('win-main')
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
  // M23：主窗口恒为「IT 工具」（功能蓝），不再按 uiMode 换肤 —— 品牌玫红那套
  // 外观整体搬进了 NAS 主窗（见上面的 isNasWindow 分支）。
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
