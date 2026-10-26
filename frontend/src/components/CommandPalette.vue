<template>
  <Teleport to="body">
    <div v-if="visible" class="cp-mask" :class="{ 'cp-standalone': standalone }" @click.self="close">
      <div class="cp-box" :class="{ 'cp-box-standalone': standalone }">
        <input
          ref="inputRef"
          v-model="q"
          class="cp-input"
          :placeholder="t('palette.placeholder')"
          @keydown.down.prevent="move(1)"
          @keydown.up.prevent="move(-1)"
          @keydown.enter.prevent="run(flat[sel])"
          @keydown.tab.prevent="q = flat[sel]?.title || q"
          @keydown.esc.prevent="close"
        />
        <div class="cp-list">
          <template v-for="g in sections" :key="g.key">
            <div class="cp-group">{{ t(g.key) }}</div>

            <!-- uTools 宫格：已固定 / 最近使用（图标卡片，右键固定/取消） -->
            <div v-if="g.grid" class="cp-grid">
              <button
                v-for="it in g.items"
                :key="it.id"
                class="cp-card"
                :class="{ active: it.id === flat[sel]?.id, pinned: pinnedIds.includes(it.id) }"
                :title="it.hint || it.title"
                @click="run(it)"
                @contextmenu.prevent="togglePin(it.id)"
                @mousemove="sel = flat.findIndex(x => x.id === it.id)"
              >
                <span class="cp-card-ico" :style="{ background: (it.color || '#64748b') + '22', color: it.color || '#64748b' }">
                  <component :is="it.icon" v-if="it.icon" :size="20" />
                </span>
                <span class="cp-card-label">{{ it.title }}</span>
              </button>
            </div>

            <!-- 列表：对象级条目，选中行浮出动作条 -->
            <template v-else>
              <div
                v-for="it in g.items"
                :key="it.id"
                class="cp-item"
                :class="{ active: it.id === flat[sel]?.id }"
                @click="run(it)"
                @mousemove="sel = flat.findIndex(x => x.id === it.id)"
              >
                <span class="cp-item-title">{{ it.title }}</span>
                <span class="cp-item-hint">{{ it.hint }}</span>
                <span v-if="it.actions?.length" class="cp-acts">
                  <button
                    v-for="a in it.actions"
                    :key="a.id"
                    class="cp-act"
                    :title="a.label"
                    @click.stop="runAct(a)"
                  >{{ a.label }}</button>
                </span>
              </div>
            </template>
          </template>
          <div v-if="!flat.length" class="cp-empty">{{ t('palette.noResults') }}</div>
        </div>
        <div class="cp-foot">
          <span>↑↓ {{ t('palette.navigate') }}</span>
          <span>Enter {{ t('palette.run') }}</span>
          <span>Tab {{ t('palette.tab') }}</span>
          <span>Esc {{ t('palette.close') }}</span>
        </div>
      </div>
    </div>

    <!-- 管理对话框：palette 直达（与 PluginCenterPanel 同一套组件） -->
    <MySQLManageDialog
      v-if="panelKind === 'mysql'"
      v-model:visible="panelOpen"
      :instance-id="panelInst?.id || ''"
      :instance-name="panelInst?.name || ''"
    />
    <RedisManageDialog
      v-else-if="panelKind === 'redis'"
      v-model:visible="panelOpen"
      :instance-id="panelInst?.id || ''"
      :instance-name="panelInst?.name || ''"
    />
    <FTPManageDialog
      v-else-if="panelKind === 'ftp'"
      v-model:visible="panelOpen"
      :instance-id="panelInst?.id || ''"
      :instance-name="panelInst?.name || ''"
    />
  </Teleport>
</template>

<script setup lang="ts">
// M18 全局呼出命令面板（uTools 式「一个输入框统治一切」）：
// 全局热键 Alt+Space（Go 侧注册）或窗口内 Ctrl+K 呼出；
// 关键字直达 —— 面板导航 / 站点 启停·打开 / 软件实例 启停·管理。
// uTools 化（Phase A）：拼音首字母/全拼匹配、条目别名关键字（插件字）、
// 最近使用置顶、Tab 补全选中项。
import { ref, computed, nextTick, watch } from 'vue'
import type { Component } from 'vue'
import { match as pinyinMatch } from 'pinyin-pro'
import {
  Globe, Database, Zap, Terminal, Monitor, Settings, Puzzle, FolderSync,
  Folder, Server, History, Waypoints, Boxes, Network, AppWindow,
} from '@lucide/vue'
import { useI18n } from '../i18n'
import { ElMessage } from 'element-plus'
import MySQLManageDialog from './MySQLManageDialog.vue'
import RedisManageDialog from './RedisManageDialog.vue'
import FTPManageDialog from './FTPManageDialog.vue'
import { ListInstances, ListPlugins, StartInstance, StopInstance } from '../../bindings/github.com/ys-ll/uniterm/backend/duihao/service/plugincenterservice'
import { ListSites, StartSite, StopSite, OpenSite, OpenSiteRoot } from '../../bindings/github.com/ys-ll/uniterm/backend/duihao/service/siteservice'
import { LoadConnections, PaletteNavigate, PaletteConnect, PaletteAction } from '../../bindings/github.com/ys-ll/uniterm/app'

const props = defineProps<{ visible: boolean }>()
const emit = defineEmits<{ (e: 'update:visible', v: boolean): void }>()
const { t } = useI18n()

// 独立悬浮面板窗（M18-C，/?w=palette）里，window CustomEvent 到不了主
// 窗口的 webview —— 导航/设置改走后端 PaletteNavigate（收面板窗+唤主窗口）。
const standalone = new URLSearchParams(window.location.search).get('w') === 'palette'

const q = ref('')
const sel = ref(0)
const inputRef = ref<HTMLInputElement | null>(null)

interface SiteView { id: string; name: string; domains: string[]; disabled?: boolean; live?: string }
interface InstanceView { id: string; plugin: string; version: string; name: string; live: string }
interface PluginView { id: string; manage?: { panel?: string } }
interface ConnView { id: string; name: string; host: string; port: number; user: string; type: string }
interface Item {
  id: string; title: string; hint: string; group: string; keys?: string[]
  // 首页网格卡片：图标 + 品牌色（uTools 插件宫格观感）
  icon?: Component; color?: string
  run: () => void | Promise<void>
  // uTools 超级面板式：选中该对象时行内浮出的动作条（点谁执行谁）
  actions?: Array<{ id: string; label: string; run: () => void | Promise<void> }>
}

// uTools「插件字」：条目别名 —— 英文缩写 + 中文词 + 全拼，输哪个都能中。
const PLUGIN_KEYS: Record<string, string[]> = {
  mysql: ['sql', 'db', '数据库', 'shujuku', '库'],
  redis: ['缓存', 'cache', 'huancun', 'kv'],
  ftp: ['文件传输', '传输', '上传', 'wenjian', 'chuanshu'],
  php: ['php'],
  nginx: ['web', 'http', '反向代理'],
}

// 连接类型的别名（uTools 插件字同款）：输 ssh/远程/终端 都能命中 SSH 连接
const CONN_KEYS: Record<string, string[]> = {
  ssh: ['终端', '服务器', '远程', 'zhongduan', 'fuwuqi', 'yuancheng', 'shell'],
  ftp: ['文件传输', '传输', 'wenjian', 'chuanshu'],
  sftp: ['文件传输', '传输', 'wenjian'],
  rdp: ['远程桌面', 'yuancheng', 'zhumian', 'windows'],
  vnc: ['远程桌面', 'yuancheng', 'zhumian'],
  telnet: ['终端', 'zhongduan'],
  database: ['数据库', 'shujuku', 'db', 'sql'],
  redis: ['缓存', 'huancun', 'kv'],
  mongodb: ['数据库', 'shujuku', 'mongo'],
  elasticsearch: ['搜索引擎', 'es', 'sousuo'],
  k8s: ['容器', 'kubernetes', 'rongqi', '集群', 'jiqun'],
  serial: ['串口', 'chuan kou'],
}

// 宫格图标与配色（uTools 首页每项一个彩色圆角图标）
const NAV_META: Record<string, { icon: Component; color: string }> = {
  plugins: { icon: Puzzle, color: '#8b5cf6' },
  sites: { icon: Globe, color: '#3b82f6' },
  connections: { icon: Waypoints, color: '#0ea5e9' },
  files: { icon: Folder, color: '#f59e0b' },
  monitor: { icon: Monitor, color: '#10b981' },
  tunnels: { icon: Network, color: '#6366f1' },
  quickCommands: { icon: Terminal, color: '#14b8a6' },
  history: { icon: History, color: '#94a3b8' },
  personalization: { icon: Boxes, color: '#ec4899' },
}
const PLUGIN_META: Record<string, { icon: Component; color: string }> = {
  mysql: { icon: Database, color: '#6366f1' },
  redis: { icon: Zap, color: '#ef4444' },
  ftp: { icon: FolderSync, color: '#06b6d4' },
  nginx: { icon: Server, color: '#0ea5e9' },
  php: { icon: Terminal, color: '#8b5cf6' },
}
const CONN_META: Record<string, { icon: Component; color: string }> = {
  ssh: { icon: Terminal, color: '#22c55e' },
  telnet: { icon: Terminal, color: '#22c55e' },
  mosh: { icon: Terminal, color: '#22c55e' },
  ftp: { icon: FolderSync, color: '#06b6d4' },
  sftp: { icon: FolderSync, color: '#06b6d4' },
  scp: { icon: FolderSync, color: '#06b6d4' },
  rdp: { icon: Monitor, color: '#3b82f6' },
  vnc: { icon: Monitor, color: '#3b82f6' },
  database: { icon: Database, color: '#6366f1' },
  redis: { icon: Zap, color: '#ef4444' },
  mongodb: { icon: Database, color: '#10b981' },
  elasticsearch: { icon: Database, color: '#f59e0b' },
  k8s: { icon: Boxes, color: '#326ce5' },
}
const SETTINGS_META = { icon: Settings, color: '#64748b' }
const SITE_META = { icon: Globe, color: '#3b82f6' }
const MAIN_META = { icon: AppWindow, color: '#0ea5e9' }

// 「创建」类动作：调后端 PaletteAction（收面板 + 唤主窗口 + 广播
// app:palette-action），主窗口打开对应表单。主窗口模式下 emit 同样可达。
function createAction(name: string, titleKey: string, icon: Component, color: string, keys: string[]): Item {
  return {
    id: `new:${name}`, group: 'palette.groupCreate', title: t(titleKey), hint: '', keys,
    icon, color,
    run: () => { bumpRecent(`new:${name}`); void PaletteAction(name) },
  }
}

const sites = ref<SiteView[]>([])
const insts = ref<InstanceView[]>([])
const conns = ref<ConnView[]>([])
const managePanel = ref<Record<string, string>>({})

// 与 PluginCenterPanel 相同的管理面板映射（manage.panel: mysql/redis/ftp）
const PANELS = ['mysql', 'redis', 'ftp']
const panelOpen = ref(false)
const panelInst = ref<InstanceView | null>(null)
const panelKind = ref('')

watch(() => props.visible, async (v) => {
  if (!v) return
  q.value = ''
  sel.value = 0
  await loadData()
  nextTick(() => inputRef.value?.focus())
}, { immediate: true })
watch(q, () => { sel.value = 0 })

async function loadData() {
  try {
    const [ss, ii, pp, cc] = await Promise.all([
      ListSites() as unknown as Promise<SiteView[]>,
      ListInstances() as unknown as Promise<InstanceView[]>,
      ListPlugins() as unknown as Promise<PluginView[]>,
      LoadConnections() as unknown as Promise<{ connections?: ConnView[] }>,
    ])
    sites.value = ss || []
    insts.value = ii || []
    conns.value = cc?.connections || []
    const mp: Record<string, string> = {}
    for (const p of pp || []) {
      const panel = p.manage?.panel || ''
      if (PANELS.includes(panel)) mp[p.id] = panel
    }
    managePanel.value = mp
  } catch (e) {
    console.error('palette load', e)
  }
}

function close() {
  emit('update:visible', false)
}

function navTo(view: string) {
  if (standalone) {
    void PaletteNavigate(view)
    return
  }
  window.dispatchEvent(new CustomEvent('app:set-sidebar-view', { detail: { view } }))
  close()
}

function openManage(inst: InstanceView) {
  panelInst.value = inst
  panelKind.value = managePanel.value[inst.plugin] || ''
  if (!panelKind.value) return
  emit('update:visible', false)
  panelOpen.value = true
}

function toast(fn: () => Promise<unknown>, ok?: () => void) {
  return async () => {
    try {
      await fn()
      ok?.()
    } catch (e) {
      ElMessage.error(String((e as Error)?.message || e))
    }
    close()
  }
}

const items = computed<Item[]>(() => {
  const list: Item[] = []
  // 创建（新建连接 / 新建站点）
  list.push(createAction('connection', 'sidebar.newConnection', CONN_META.ssh.icon, CONN_META.ssh.color, ['create', 'new', 'xinjian', '新建', '添加', 'add']))
  list.push(createAction('site', 'sites.new', SITE_META.icon, SITE_META.color, ['create', 'new', 'xinjian', '新建', '添加', 'add', 'wangzhan', '网站']))
  // 导航（复用既有 Tab 标题键）
  const navs: Array<[string, string]> = [
    ['plugins', 'sidebar.plugins'],
    ['sites', 'sidebar.sites'],
    ['connections', 'header.connections'],
    ['files', 'header.files'],
    ['monitor', 'header.monitor'],
    ['tunnels', 'tunnels.tunnelsTab'],
    ['quickCommands', 'quickCommands.quickCommandsTab'],
    ['history', 'quickCommands.historyTab'],
    ['personalization', 'sidebar.personalization'],
  ]
  for (const [view, label] of navs) {
    const meta = NAV_META[view]
    list.push({ id: `nav:${view}`, group: 'palette.groupNav', title: t(label), hint: '', keys: [view], icon: meta?.icon, color: meta?.color, run: () => navTo(view) })
  }
  list.push({ id: 'app:settings', group: 'palette.groupNav', title: t('settings.title'), hint: '', keys: ['settings', 'shezhi', '偏好', 'config'], icon: SETTINGS_META.icon, color: SETTINGS_META.color, run: () => { if (standalone) { void PaletteNavigate('settings') ; return } window.dispatchEvent(new CustomEvent('app:palette-settings')) ; close() } })
  // 打开主窗口（独立面板窗专用：收面板 + 唤主窗口，不导航到具体视图）
  list.push({ id: 'app:main', group: 'palette.groupNav', title: t('palette.openMain'), hint: '', keys: ['main', 'zhuchuangkou', '主窗口', 'home', 'window'], icon: MAIN_META.icon, color: MAIN_META.color, run: () => { if (standalone) { void PaletteNavigate('') ; return } close() } })
  // 站点（对象级条目：一行=一个站点，动作条浮出，不再每动作一条）
  for (const s of sites.value) {
    const title = s.name || s.domains?.[0] || s.id
    const running = s.live === 'running'
    const sk = ['site', 'wangzhan', 'zhan', ...(s.domains || []), ...title.split(/[\s._-]+/)]
    list.push({
      id: `site:${s.id}`, group: 'palette.groupSites', title, hint: s.domains?.[0] || '', keys: sk,
      icon: SITE_META.icon, color: SITE_META.color,
      run: toast(() => running ? StopSite(s.id) : StartSite(s.id), () => bumpRecent(`site:${s.id}`)),
      actions: [
        { id: 'toggle', label: running ? t('sites.stop') : t('sites.start'), run: toast(() => running ? StopSite(s.id) : StartSite(s.id), () => bumpRecent(`site:${s.id}`)) },
        { id: 'open', label: t('palette.openSite'), run: toast(() => OpenSite(s.id), () => bumpRecent(`site:${s.id}`)) },
        { id: 'root', label: t('palette.openRoot'), run: toast(() => OpenSiteRoot(s.id), () => bumpRecent(`site:${s.id}`)) },
      ],
    })
  }
  // 实例（对象级条目：启停为主键，有管理面板的加「管理」直达）
  for (const i of insts.value) {
    const title = i.name || `${i.plugin} ${i.version}`
    const running = i.live === 'running'
    const ik = [...(PLUGIN_KEYS[i.plugin] || []), ...title.split(/[\s._-]+/)]
    const acts: Array<{ id: string; label: string; run: () => void | Promise<void> }> = [
      { id: 'toggle', label: running ? t('plugins.stop') : t('plugins.start'), run: toast(() => running ? StopInstance(i.id) : StartInstance(i.id), () => bumpRecent(`inst:${i.id}`)) },
    ]
    if (managePanel.value[i.plugin]) {
      acts.push({ id: 'manage', label: t('plugins.manage'), run: () => { bumpRecent(`inst:${i.id}`); openManage(i) } })
    }
    list.push({
      id: `inst:${i.id}`, group: 'palette.groupInstances', title, hint: i.id, keys: ik,
      icon: PLUGIN_META[i.plugin]?.icon || Puzzle, color: PLUGIN_META[i.plugin]?.color || '#8b5cf6',
      run: acts[0].run, actions: acts,
    })
  }
  // 连接（SSH/FTP/RDP/数据库…）：一行=一个连接，Enter/点行直接发起连接 ——
  // 走后端 PaletteConnect（收面板窗 + 唤起主窗口 + 广播 app:palette-connect，
  // 主窗口落成普通连接建 Tab，与侧栏双击同路径；独立面板窗里也能用）。
  for (const c of conns.value) {
    const title = c.name || `${c.user}@${c.host}`
    const ck = [c.type, c.host, c.user, ...title.split(/[\s._@-]+/), ...(CONN_KEYS[c.type] || [])]
    list.push({
      id: `conn:${c.id}`, group: 'palette.groupConns', title,
      hint: `${c.user}@${c.host}:${c.port}`, keys: ck,
      icon: CONN_META[c.type]?.icon || Waypoints, color: CONN_META[c.type]?.color || '#0ea5e9',
      run: () => { bumpRecent(`conn:${c.id}`); void PaletteConnect(c.id) },
    })
  }
  return list
})

// —— 最近使用（uTools 首屏逻辑）：localStorage 记 id 频次，取前 6 条 ——
const RECENT_KEY = 'palette.recent'
const recentMap = ref<Record<string, number>>(loadRecent())
function loadRecent(): Record<string, number> {
  try { return JSON.parse(localStorage.getItem(RECENT_KEY) || '{}') } catch { return {} }
}
function bumpRecent(id: string) {
  const m = { ...recentMap.value }
  m[id] = (m[id] || 0) + 1
  // 只留频次最高的 20 条，防无限膨胀
  const top = Object.entries(m).sort((a, b) => b[1] - a[1]).slice(0, 20)
  recentMap.value = Object.fromEntries(top)
  try { localStorage.setItem(RECENT_KEY, JSON.stringify(recentMap.value)) } catch { /* ignore */ }
}

// —— 已固定（uTools「已固定」宫格）：右键卡片切换固定状态 ——
const PIN_KEY = 'palette.pinned'
const pinnedIds = ref<string[]>(loadPinned())
function loadPinned(): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(PIN_KEY) || '[]')
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []
  } catch { return [] }
}
function togglePin(id: string) {
  const set = new Set(pinnedIds.value)
  const on = !set.has(id)
  if (on) set.add(id)
  else set.delete(id)
  pinnedIds.value = [...set]
  try { localStorage.setItem(PIN_KEY, JSON.stringify(pinnedIds.value)) } catch { /* ignore */ }
  ElMessage.success(t(on ? 'palette.pinned' : 'palette.unpinned'))
}

// 过滤（uTools 式）：英文子串 + 中文拼音全拼/首字母匹配，按命中质量排序
// （词首前缀 > 包含；同分按最近使用频次）。
const filtered = computed<Item[]>(() => {
  const kw = q.value.trim().toLowerCase()
  if (!kw) return items.value
  const scored: Array<{ it: Item; s: number }> = []
  for (const it of items.value) {
    const t = it.title.toLowerCase()
    let s = -1
    if (t.startsWith(kw)) s = 100
    else if (it.hint.toLowerCase().startsWith(kw)) s = 90
    else if (t.includes(kw)) s = 80
    else {
      // 拼音：match('网站管理', 'wz') / ('wangzhan') 命中返回下标数组
      const p = pinyinMatch(it.title, kw) || pinyinMatch(it.hint, kw)
      if (p) s = 60
      else {
        for (const k of it.keys || []) {
          const kl = k.toLowerCase()
          if (kl === kw) { s = Math.max(s, 95); break }
          if (kl.startsWith(kw)) s = Math.max(s, 70)
          else if (kl.includes(kw)) s = Math.max(s, 50)
          else if (/[\u4e00-\u9fff]/.test(kl) && (pinyinMatch(kl, kw))) s = Math.max(s, 55)
        }
      }
    }
    if (s >= 0) scored.push({ it, s: s + (recentMap.value[it.id] || 0) * 0.1 })
  }
  return scored.sort((a, b) => b.s - a.s).map(x => x.it)
})

// 首页（空查询）= uTools 式宫格：已固定 + 最近使用，再接各组列表；
// 输入关键字 = 分组列表（每行带动作条）。
interface Section { key: string; items: Item[]; grid?: boolean }
const sections = computed<Section[]>(() => {
  const out: Section[] = []
  const order = ['palette.groupCreate', 'palette.groupNav', 'palette.groupConns', 'palette.groupSites', 'palette.groupInstances']
  if (!q.value.trim()) {
    const byId = new Map(items.value.map(it => [it.id, it]))
    const pinned = pinnedIds.value.map(id => byId.get(id)).filter((x): x is Item => !!x)
    const recent = Object.entries(recentMap.value)
      .sort((a, b) => b[1] - a[1])
      .map(([id]) => byId.get(id))
      .filter((x): x is Item => !!x && !pinnedIds.value.includes(x.id))
      .slice(0, 8)
    if (pinned.length) out.push({ key: 'palette.groupPinned', items: pinned, grid: true })
    if (recent.length) out.push({ key: 'palette.groupRecent', items: recent, grid: true })
    // 宫格里已展示的不再在下方列表重复
    const shown = new Set(out.flatMap(s => s.items.map(i => i.id)))
    for (const key of order) {
      const its = items.value.filter(it => it.group === key && !shown.has(it.id))
      if (its.length) out.push({ key, items: its })
    }
    return out
  }
  for (const key of order) {
    const its = filtered.value.filter(it => it.group === key)
    if (its.length) out.push({ key, items: its })
  }
  return out
})
const flat = computed<Item[]>(() => sections.value.flatMap(g => g.items))

function move(d: number) {
  const n = flat.value.length
  if (!n) return
  sel.value = (sel.value + d + n) % n
}

function run(it?: Item) {
  if (!it) return
  void it.run()
}

// 动作条按钮：toast 包装的闭包自带「成功后关面板」，直接跑
function runAct(a: { run: () => void | Promise<void> }) {
  void a.run()
}
</script>

<style scoped>
.cp-mask {
  position: fixed;
  inset: 0;
  z-index: 3000;
  background: var(--scrim);
  display: flex;
  justify-content: center;
  align-items: flex-start;
  padding-top: 10vh;
}
.cp-box {
  width: 40rem;
  max-width: 90vw;
  max-height: 70vh;
  display: flex;
  flex-direction: column;
  background: var(--bg-elevated);
  border: 1px solid var(--border-subtle);
  border-radius: 0.5rem;
  box-shadow: var(--shadow-lg);
  overflow: hidden;
}
/* 独立面板窗（/?w=palette）：窗口本身就是面板（uTools 形态）。遮罩透明，
   面板撑满窗口并留出四周空隙让投影落在透明窗体上；圆角与窗口 CSS 圆角
   （body 1rem）一致，视觉上即「大圆角悬浮面板」。 */
.cp-mask.cp-standalone {
  background: transparent;
  padding: 0.625rem 0.75rem 0.875rem;
}
.cp-box.cp-box-standalone {
  width: 100%;
  max-width: 100%;
  height: 100%;
  max-height: 100%;
  border-radius: var(--win-radius);
}
.cp-input {
  width: 100%;
  box-sizing: border-box;
  padding: 0.8rem 1rem;
  font-size: 1rem;
  color: var(--text-primary);
  background: transparent;
  border: none;
  outline: none;
  border-bottom: 1px solid var(--border-subtle);
}
.cp-list {
  flex: 1;
  overflow-y: auto;
  padding: 0.25rem 0;
}
.cp-group {
  padding: 0.4rem 1rem 0.15rem;
  font-size: 0.7rem;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}
.cp-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 1rem;
  padding: 0.45rem 1rem;
  cursor: pointer;
  color: var(--text-primary);
}
/* uTools 宫格：图标卡片，自动换行 */
.cp-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 0.4rem 0.5rem;
  padding: 0.2rem 0.75rem 0.5rem;
}
.cp-card {
  width: 4.6rem;
  padding: 0.5rem 0.25rem 0.45rem;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.35rem;
  border: 1px solid transparent;
  border-radius: 0.5rem;
  background: transparent;
  cursor: pointer;
  font-family: inherit;
}
.cp-card.active {
  background: var(--bg-active);
  border-color: var(--border-subtle);
}
.cp-card.pinned .cp-card-label::after {
  content: '';
  display: inline-block;
  width: 0.3rem;
  height: 0.3rem;
  margin-left: 0.25rem;
  border-radius: 50%;
  background: var(--accent, #3b82f6);
  vertical-align: middle;
}
.cp-card-ico {
  width: 2.2rem;
  height: 2.2rem;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 0.55rem;
}
.cp-card-label {
  font-size: 0.72rem;
  color: var(--text-secondary);
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cp-card.active .cp-card-label {
  color: var(--text-primary);
}
.cp-item.active {
  background: var(--bg-active);
}
.cp-item-title {
  font-size: 0.85rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cp-item-hint {
  font-size: 0.72rem;
  color: var(--text-muted);
  flex-shrink: 0;
}
.cp-acts {
  display: none;
  gap: 0.4rem;
  flex-shrink: 0;
}
.cp-item.active .cp-acts {
  display: flex;
}
.cp-item.active .cp-item-hint {
  display: none;
}
.cp-act {
  font-size: 0.72rem;
  line-height: 1.4;
  padding: 0.15rem 0.6rem;
  border: 1px solid var(--border-subtle);
  border-radius: 0.9rem;
  background: var(--bg-elevated);
  color: var(--text-secondary);
  cursor: pointer;
  white-space: nowrap;
}
.cp-act:hover {
  background: var(--bg-active);
  color: var(--text-primary);
}
.cp-empty {
  padding: 1.5rem;
  text-align: center;
  color: var(--text-muted);
  font-size: 0.85rem;
}
.cp-foot {
  display: flex;
  gap: 1rem;
  padding: 0.35rem 1rem;
  border-top: 1px solid var(--border-subtle);
  color: var(--text-muted);
  font-size: 0.7rem;
}
</style>
