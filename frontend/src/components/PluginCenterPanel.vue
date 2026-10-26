<template>
  <div class="runtime-panel">
    <div class="rp-head">
      <div class="rp-head-main">
        <span class="rp-title">{{ t('plugins.runtimeTitle') }}</span>
        <span class="rp-sub">{{ t('plugins.runtimeHint') }}</span>
      </div>
      <!-- 右侧：软件市场 + 数据目录（软件自带的运行时/实例/站点/日志都在数据目录下） -->
      <div class="rp-head-ops">
        <button class="rp-market" :title="t('sched.title')" @click="scheduleOpen = true">
          <svg width="11" height="11" viewBox="0 0 16 16" fill="none" aria-hidden="true">
            <circle cx="8" cy="8" r="6.1" stroke="currentColor" stroke-width="1.3" />
            <path d="M8 4.6V8l2.4 1.5" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" />
          </svg>
          <span>{{ t('sched.button') }}</span>
        </button>
        <button class="rp-market" :title="t('market.title')" @click="marketOpen = true">
          <svg width="11" height="11" viewBox="0 0 16 16" fill="none" aria-hidden="true">
            <path d="M2 6.6V13a1 1 0 0 0 1 1h10a1 1 0 0 0 1-1V6.6" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" />
            <path d="M1.6 6.6 3.3 2.7A1 1 0 0 1 4.2 2.1h7.6a1 1 0 0 1 .9.6l1.7 3.9" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" />
            <path d="M6.2 9.6h3.6" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" />
          </svg>
          <span>{{ t('market.button') }}</span>
        </button>
        <button v-if="dataRoot" class="rp-path" :title="dataRoot" @click="onOpenDataRoot">
          <span class="rp-path-label">{{ t('plugins.dataDir') }}</span>
          <code>{{ dataRoot }}</code>
          <span v-if="portable" class="rp-path-tag">portable</span>
        </button>
      </div>
    </div>

    <div class="rp-grid">
      <!-- 一个软件一张卡：显示当前运行（或默认）的版本；
           其它版本统一收进「多版本」弹窗，避免同一软件刷出多张重复卡。 -->
      <div
        v-for="g in groups"
        :key="g.plugin.id"
        class="rp-card"
        :class="{ 'rp-card-off': !primaryInst(g) }"
        :title="g.plugin.description || ''"
      >
        <!-- 第 1 行：状态灯 · 名称 · 当前版本 ……… 多版本入口 -->
        <div class="rp-top">
          <span class="rp-dot" :class="dotClass(primaryInst(g))" />
          <span class="rp-name">{{ g.plugin.name }}</span>
          <span v-if="primaryInst(g)" class="rp-ver-tag">{{ verTag(g) }}</span>
          <button class="rp-vers" :title="t('plugins.versionsHint')" @click="openVersions(g.plugin)">
            <span>{{ t('plugins.versions') }}</span>
            <span v-if="g.insts.length > 1" class="rp-vers-n">{{ g.insts.length }}</span>
          </button>
        </div>

        <!-- 第 2 行：启动/停止 + 管理 + 「⋯」菜单（配置/日志/端口/卸载）
             「管理」只在清单声明了 manage 能力时出现（MySQL/Redis/FTP 的内置连接管理面）。 -->
        <div v-if="primaryList(g).length" class="rp-actions">
          <template v-for="i in primaryList(g)" :key="i.id">
            <el-button
              size="small"
              class="rp-primary"
              :type="i.live === 'running' ? 'warning' : 'success'"
              :loading="busy === i.id"
              @click="onToggle(i, g.insts, g.plugin.multiRun)"
            >{{ i.live === 'running' ? t('plugins.stop') : t('plugins.start') }}</el-button>
            <el-button
              v-if="g.plugin.manage"
              size="small"
              type="primary"
              plain
              class="rp-manage"
              :disabled="i.live !== 'running' && !manageOfflineOk(g.plugin)"
              :title="i.live === 'running' ? t('plugins.manageHint') : (manageOfflineOk(g.plugin) ? t('plugins.manageOffline') : t('plugins.manageNeedStart'))"
              @click="openManage(g.plugin, i)"
            >{{ t('plugins.manage') }}</el-button>
            <el-dropdown trigger="click" popper-class="rp-more-popper" @command="(cmd) => onMore(cmd, i)">
              <el-button size="small" class="rp-more" :title="t('plugins.more')">⋯</el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item command="settings">{{ t('plugins.settings') }}</el-dropdown-item>
                  <el-dropdown-item command="logs">{{ t('plugins.logs') }}</el-dropdown-item>
                  <el-dropdown-item v-if="portsText(i)" class="rp-port-line" divided>
                    {{ t('plugins.ports') }} {{ portValue(i) }}
                  </el-dropdown-item>
                  <el-dropdown-item command="setPort" divided>{{ t('plugins.changePort') }}</el-dropdown-item>
                  <el-dropdown-item command="copyPort">{{ t('plugins.copyPort') }}</el-dropdown-item>
                  <el-dropdown-item command="remove" divided>{{ t('plugins.removeInstance') }}</el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </template>
        </div>
        <!-- 未装：一个「安装」按钮（装清单默认版本），要挑版本就进「多版本」 -->
        <div v-else class="rp-actions">
          <el-button
            size="small" type="primary" class="rp-primary"
            :loading="busy === g.plugin.id"
            @click="onInstall(g.plugin)"
          >{{ t('plugins.install') }}</el-button>
        </div>
      </div>
    </div>

    <div class="rp-note">{{ t('plugins.toolsNote') }}</div>

    <div v-if="warnings.length" class="rp-warnings">
      <div class="rp-warn-title">{{ t('plugins.manifestWarnings') }}</div>
      <div v-for="(w, i) in warnings" :key="i" class="rp-warn-item">{{ w }}</div>
    </div>

    <el-drawer v-model="logDrawer" :title="logTitle" size="60%">
      <pre class="rp-log">{{ logText }}</pre>
    </el-drawer>

    <!-- 多版本管理（宝塔式）：一个软件的全部版本，安装/启动/配置/卸载都在这里 -->
    <PluginVersionDialog
      v-model="versionsOpen"
      :plugin="versionsPlugin"
      :instances="versionsInsts"
      :defaults="prefVersion"
      @refresh="refresh"
      @installed="setPref"
      @default="setPref"
      @settings="onSettings"
      @logs="onLogs"
    />

    <!-- 软件市场：按分类浏览可安装的软件与版本（分类来自清单 category，可扩展） -->
    <SoftwareMarketDialog
      v-model="marketOpen"
      :plugins="plugins"
      :instances="instances"
      :manifest-dir="manifestDir"
      :defaults="prefVersion"
      @refresh="refresh"
      @installed="setPref"
      @default="setPref"
      @versions="openVersions"
      @settings="onSettings"
      @logs="onLogs"
    />

    <!-- 通用可视化设置对话框（按清单 settings schema 动态渲染） -->
    <SettingsDialog
      v-model="settingsOpen"
      :instance-id="settingsInst?.id || ''"
      :instance-name="settingsInst?.name || ''"
      :running="settingsInst?.live === 'running'"
      @saved="refresh"
    />

    <!-- 运行时管理面（M11）：数据库 / Redis / FTP 账号 —— 卡片「管理」直达，不套中间层 -->
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
    <WebDavManageDialog
      v-else-if="panelKind === 'webdav'"
      v-model:visible="panelOpen"
      :instance-id="panelInst?.id || ''"
      :instance-name="panelInst?.name || ''"
    />

    <!-- 定时任务：库备份 / Redis 备份 / 日志切割 -->
    <ScheduleDialog v-model:visible="scheduleOpen" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, reactive } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from '../i18n'
import SettingsDialog from './SettingsDialog.vue'
import PluginVersionDialog from './PluginVersionDialog.vue'
import SoftwareMarketDialog from './SoftwareMarketDialog.vue'
import ScheduleDialog from './ScheduleDialog.vue'
import MySQLManageDialog from './MySQLManageDialog.vue'
import RedisManageDialog from './RedisManageDialog.vue'
import FTPManageDialog from './FTPManageDialog.vue'
import WebDavManageDialog from './WebDavManageDialog.vue'
import {
  ListPlugins, CreateInstance, ListInstances,
  StartInstance, StopInstance, RemoveInstance, InstanceLogs, ManifestWarnings,
  DiagnoseInstancePort, AutoFixInstancePort,
  SetInstancePort, DataRoot, IsPortable, OpenDataRoot,
} from '../../bindings/github.com/ys-ll/uniterm/backend/duihao/service/plugincenterservice'

// bindings 为 any 模块（见 types/bindings.d.ts），本地声明视图形状。
interface VersionInfo { version: string; url?: string; sha256?: string; size?: number; installed: boolean }
interface ManageView { panel: string }
interface PluginView {
  id: string; name: string; kind: string; category: string; description: string
  versions: string[]; installedVersions: string[]; defaultVersion: string
  workers: number; hasInstance: boolean; multiRun: boolean
  versionInfos?: VersionInfo[]
  manage?: ManageView
}
interface InstanceView {
  id: string; plugin: string; version: string; name: string
  ports: Record<string, number>; workerPorts?: Record<string, number[]>
  workers: number; state: string; runtimeDir: string
  live: string; pids: number[]; runtimeSource: string
}
interface Group { plugin: PluginView; insts: InstanceView[] }

const { t } = useI18n()
const plugins = ref<PluginView[]>([])
const instances = ref<InstanceView[]>([])
const warnings = ref<string[]>([])
const busy = ref('')
const dataRoot = ref('')
const portable = ref(false)

const logDrawer = ref(false)
const logTitle = ref('')
const logText = ref('')

// 可视化设置对话框状态
const settingsOpen = ref(false)
const settingsInst = ref<InstanceView | null>(null)

function onSettings(inst: InstanceView) {
  settingsInst.value = inst
  settingsOpen.value = true
}

// 「管理」= 该软件的内置连接管理界面，卡片上直接打开，不套中间层。
// 打开哪个界面由清单 manage.panel 决定（mysql / redis / ftp），前端不写死软件 id。
const PANELS = ['mysql', 'redis', 'ftp', 'webdav']
// 停机也可用的管理面：WebDAV 的共享必须先建好才能启动（服务端校验至少一个
// 启用共享），若「管理」要求运行中就成了死锁——这些面板对已装实例停机放行。
const PANEL_OFFLINE_OK = ['webdav']
const panelOpen = ref(false)
const panelInst = ref<InstanceView | null>(null)
const panelKind = ref('')

function manageOfflineOk(p: PluginView) {
  return PANEL_OFFLINE_OK.includes(p.manage?.panel || '')
}

function openManage(p: PluginView, inst: InstanceView) {
  const panel = p.manage?.panel || ''
  if (!PANELS.includes(panel)) {
    ElMessage.warning(t('plugins.manageUnsupported'))
    return
  }
  panelInst.value = inst
  panelKind.value = panel
  panelOpen.value = true
}

// process（下载运行时）+ embedded（编译进本体的服务，如 FTPServer）进面板；
// cli（composer 等）不作为面板条目，由站点操作按需调用。
const runtimePlugins = computed(() =>
  plugins.value.filter(p => p.kind === 'process' || p.kind === 'embedded'))

// 「上次安装/选择的版本」：市场或多版本弹窗里选版本安装后，左侧卡片显示该版本。
// 存在 localStorage，重启后仍保持（运行中的实例优先级更高）。
const PREF_KEY = 'duihao.pluginPrefVersion'
const prefVersion = reactive<Record<string, string>>(loadPref())
function loadPref(): Record<string, string> {
  try {
    return JSON.parse(localStorage.getItem(PREF_KEY) || '{}')
  } catch {
    return {}
  }
}
function setPref(pluginId: string, version: string) {
  prefVersion[pluginId] = version
  try {
    localStorage.setItem(PREF_KEY, JSON.stringify({ ...prefVersion }))
  } catch {
    /* 隐私模式等写失败，忽略 */
  }
  // 该软件另有版本正在运行：卡片仍显示运行中的那个（事实优先），说明一下免得以为没生效
  const running = instances.value.find(i => i.plugin === pluginId && i.live === 'running')
  if (running && running.version !== version) {
    ElMessage.info(t('plugins.defaultButRunning', { def: version, run: running.version }))
  }
}

// 软件市场弹窗
const marketOpen = ref(false)
// 定时任务弹窗（库备份 / 日志切割）
const scheduleOpen = ref(false)
// 用户自定义清单目录（市场底部提示：丢 JSON 进去即可扩展分类与软件）
const manifestDir = computed(() => {
  const d = dataRoot.value
  if (!d) return ''
  const sep = d.includes('\\') ? '\\' : '/'
  return `${d}${sep}config${sep}plugins`
})

// 按软件分组（phpStudy 式）：一个软件一张卡，卡上只显示当前运行（或默认）的版本。
const groups = computed<Group[]>(() =>
  runtimePlugins.value.map(p => ({
    plugin: p,
    insts: instances.value.filter(i => i.plugin === p.id),
  })))

// 卡上显示的版本：优先运行中的实例，其次上次装/选的版本，再退默认版本。
function primaryInst(g: Group): InstanceView | undefined {
  const list = g.insts
  if (!list.length) return undefined
  return list.find(i => i.live === 'running')
    || list.find(i => i.version === prefVersion[g.plugin.id])
    || list.find(i => i.version === g.plugin.defaultVersion)
    || list[0]
}
// 模板里用数组包一层，避免在模板表达式中做 undefined 收窄（vue-tsc 不认）
function primaryList(g: Group): InstanceView[] {
  const i = primaryInst(g)
  return i ? [i] : []
}
function verTag(g: Group): string {
  const i = primaryInst(g)
  if (!i) return ''
  return i.version === 'builtin' ? t('plugins.builtin') : 'v' + i.version
}

// 「多版本」弹窗：列出该软件全部版本（安装 / 启动 / 配置 / 日志 / 卸载）
const versionsOpen = ref(false)
const versionsPlugin = ref<PluginView | null>(null)
const versionsInsts = computed(() =>
  instances.value.filter(i => i.plugin === versionsPlugin.value?.id))
function openVersions(p: PluginView) {
  versionsPlugin.value = p
  versionsOpen.value = true
}

function dotClass(inst?: InstanceView): string {
  if (!inst) return 'rp-dot-off'
  const map: Record<string, string> = {
    running: 'rp-dot-run', starting: 'rp-dot-wait', stopping: 'rp-dot-wait', error: 'rp-dot-err',
  }
  return map[inst.live] || 'rp-dot-off'
}
function portsText(inst: InstanceView): string {
  const parts = Object.entries(inst.ports || {}).map(([k, v]) => `${k}:${v}`)
  return parts.join(' · ')
}
// 「⋯」菜单里的端口展示：只出端口号，多端口用 " / " 连接
function portValue(inst: InstanceView): string {
  return Object.values(inst.ports || {}).join(' / ')
}

async function refresh() {
  try {
    plugins.value = (await ListPlugins()) as unknown as PluginView[]
    instances.value = (await ListInstances()) as unknown as InstanceView[]
    warnings.value = (await ManifestWarnings()) as unknown as string[]
    dataRoot.value = String(await DataRoot())
    portable.value = Boolean(await IsPortable())
  } catch (e) {
    console.error('refresh plugins', e)
  }
}

async function onOpenDataRoot() {
  try {
    await OpenDataRoot()
  } catch (e) {
    ElMessage.error(String(parseErr(e)))
  }
}

async function onInstall(p: PluginView) {
  busy.value = p.id
  try {
    await CreateInstance(p.id, p.defaultVersion, '', [])
    ElMessage.success(t('plugins.instanceCreated'))
    await refresh()
  } catch (e) {
    ElMessage.error(String(parseErr(e)))
  } finally {
    busy.value = ''
  }
}

async function onToggle(inst: InstanceView, siblings: InstanceView[], multiRun = false) {
  const isStart = inst.live !== 'running'
  // 单版本运行：启动时后端会自动停掉同插件其它在跑的版本（multiRun 插件如 PHP 不会）
  const wasSiblingRunning = isStart && !multiRun &&
    siblings.some(x => x.id !== inst.id && x.live === 'running')
  busy.value = inst.id
  try {
    if (!isStart) await StopInstance(inst.id)
    else await StartInstance(inst.id)
    await refresh()
    // 单版本运行（multiRun=false）：启动时后端已自动停掉同插件其他版本
    if (isStart && wasSiblingRunning) {
      ElMessage.info(t('plugins.versionSwitched'))
    }
  } catch (e) {
    const msg = String(parseErr(e))
    if (isStart && /占用|不可用|权限/.test(msg)) {
      await offerPortFix(inst, msg)
    } else {
      ElMessage.error(msg)
    }
    await refresh()
  } finally {
    busy.value = ''
  }
}

// 端口被占（典型：phpstudy 常驻 80/8080/9000）→ 诊断 + 一键换端
async function offerPortFix(inst: InstanceView, reason: string) {
  const ports = inst.ports || {}
  const role = 'http' in ports ? 'http' : Object.keys(ports)[0]
  if (!role) {
    ElMessage.error(reason)
    return
  }
  try {
    const d = (await DiagnoseInstancePort(inst.id, role)) as unknown as {
      port: number; free: boolean; occupier?: string; suggest?: number; message?: string
    }
    if (d.free || !d.suggest) {
      ElMessage.error(d.message || reason)
      return
    }
    try {
      await ElMessageBox.confirm(
        t('sites.portFixMsg', { port: d.port, occ: d.occupier || '—', next: d.suggest }),
        t('sites.portFixTitle'),
        { type: 'warning', confirmButtonText: t('sites.portFixOk'), cancelButtonText: t('common.cancel') },
      )
    } catch {
      return
    }
    const next = (await AutoFixInstancePort(inst.id, role)) as unknown as number
    ElMessage.success(t('sites.portFixDone', { port: next }))
    await onToggle(inst, [], true)
  } catch (e2) {
    ElMessage.error(String(parseErr(e2)))
  }
}

async function onEditPort(inst: InstanceView) {
  const ports = inst.ports || {}
  // 只开放 http 端口修改：php 的 fcgi 端口被站点 fastcgi_pass 引用，改了会断，
  // 属于站点层的编排（后续由站点面板统一处理）。
  const role = 'http' in ports ? 'http' : ''
  // 非 http 端口（如 FTP 的 ftp 角色）不在快捷弹窗里改——统一走设置对话框的端口字段。
  if (!role) {
    onSettings(inst)
    return
  }
  const cur = ports[role]
  let val = ''
  try {
    const r = await ElMessageBox.prompt(
      t('plugins.portPromptMsg'),
      t('plugins.changePort'),
      {
        inputValue: String(cur),
        inputPattern: /^\d{1,5}$/,
        inputErrorMessage: t('plugins.portInvalid'),
      },
    )
    val = String(r.value || '').trim()
  } catch {
    return // 用户取消
  }
  const port = Number(val)
  if (!port) return
  busy.value = inst.id
  try {
    await SetInstancePort(inst.id, role, port)
    ElMessage.success(t('plugins.portUpdated'))
    await refresh()
  } catch (e) {
    ElMessage.error(String(parseErr(e)))
  } finally {
    busy.value = ''
  }
}

async function onMore(cmd: string, inst?: InstanceView) {
  if (!inst) return
  if (cmd === 'settings') {
    onSettings(inst)
    return
  }
  if (cmd === 'logs') {
    onLogs(inst)
    return
  }
  if (cmd === 'setPort') {
    await onEditPort(inst)
    return
  }
  if (cmd === 'copyPort') {
    const txt = portsText(inst)
    try {
      await navigator.clipboard.writeText(txt)
      ElMessage.success(t('plugins.copied'))
    } catch {
      ElMessage.info(txt)
    }
    return
  }
  if (cmd === 'remove') {
    try {
      await ElMessageBox.confirm(t('plugins.removeConfirm', { id: inst.id }), { type: 'warning' })
    } catch {
      return
    }
    try {
      await RemoveInstance(inst.id)
      await refresh()
    } catch (e) {
      ElMessage.error(String(parseErr(e)))
    }
  }
}

async function onLogs(inst: InstanceView) {
  try {
    logTitle.value = `${inst.id} · ${t('plugins.logs')}`
    logText.value = (await InstanceLogs(inst.id, 200)) as unknown as string
    logDrawer.value = true
  } catch (e) {
    ElMessage.error(String(parseErr(e)))
  }
}

function parseErr(e: unknown): string {
  if (e instanceof Error) return e.message
  try { return JSON.stringify(e) } catch { return String(e) }
}

onMounted(refresh)
</script>

<style scoped>
.runtime-panel { padding: 0.75rem; overflow-y: auto; height: 100%; }
.rp-head { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; margin-bottom: 0.75rem; flex-wrap: wrap; }
.rp-head-main { display: flex; align-items: baseline; gap: 0.375rem; min-width: 0; }
.rp-head-ops { display: flex; align-items: center; justify-content: flex-end; gap: 0.375rem; min-width: 0; flex-wrap: wrap; }
.rp-title { font-size: 0.8125rem; font-weight: 600; color: var(--text-primary); }
.rp-sub { font-size: 0.6875rem; color: var(--text-muted); }

/* 软件市场入口：标题行右侧，按分类浏览可安装的软件与版本 */
.rp-market {
  display: inline-flex; align-items: center; gap: 0.25rem; flex-shrink: 0;
  height: 1.5rem; padding: 0 0.5rem;
  border: 1px solid var(--border-subtle); border-radius: var(--radius-sm);
  background: transparent; cursor: pointer;
  font-family: inherit; font-size: 0.6875rem; line-height: 1; color: var(--text-secondary);
  transition: color .12s, border-color .12s, background .12s;
}
.rp-market:hover { color: var(--accent); border-color: var(--accent); background: var(--bg-hover); }

/* 数据目录：路径常显，点击打开（用户要求运行时统一在软件自己的目录下） */
.rp-path {
  display: inline-flex; align-items: center; gap: 0.3125rem;
  max-width: 60%;
  padding: 0.125rem 0.375rem;
  border: 1px solid var(--border-subtle); border-radius: var(--radius-sm);
  background: transparent; cursor: pointer; font-family: inherit;
}
.rp-path:hover { border-color: var(--border-default); background: var(--bg-hover); }
.rp-path-label { font-size: 0.625rem; color: var(--text-muted); flex-shrink: 0; }
.rp-path code {
  font-size: 0.625rem; color: var(--text-secondary);
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap; direction: rtl; text-align: left;
}
.rp-path-tag {
  flex-shrink: 0; font-size: 0.5625rem; letter-spacing: 0.02em;
  padding: 0 0.25rem; border-radius: var(--radius-sm);
  color: var(--accent-text, var(--text-primary)); background: var(--accent-soft, transparent);
}

/* 卡片列表：一个软件一张卡，窄到 240px 也不裁按钮 */
.rp-grid { display: flex; flex-direction: column; gap: 0.625rem; container-type: inline-size; }
.rp-card {
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  padding: 0.75rem 0.875rem;
  display: flex; flex-direction: column; gap: 0.5rem;
  transition: border-color .12s ease;
}
.rp-card:hover { border-color: var(--border-default); }
/* 未安装：虚线边框，不用灰底压暗整张卡 */
.rp-card-off { border-style: dashed; background: transparent; }
.rp-card-off .rp-name { color: var(--text-secondary); font-weight: 500; }

/* 第 1 行：灯 · 名称 · 当前版本 ……… 多版本入口（名称可缩，按钮永不被挤掉） */
.rp-top { display: flex; align-items: center; gap: 0.375rem; min-width: 0; flex-wrap: nowrap; }
.rp-dot { width: 0.5rem; height: 0.5rem; border-radius: 50%; flex-shrink: 0; background: var(--text-disabled); }
.rp-dot-run { background: var(--success); box-shadow: 0 0 0 3px color-mix(in srgb, var(--success) 18%, transparent); }
.rp-dot-err { background: var(--error); }
.rp-dot-wait { background: var(--warning); }
.rp-dot-off { background: var(--text-disabled); }
.rp-name { font-size: 0.8125rem; font-weight: 600; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; flex: 0 1 auto; min-width: 0; }
.rp-ver-tag { font-size: 0.625rem; color: var(--text-muted); flex-shrink: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

/* 多版本：名称行右侧的小胶囊按钮，点开该软件的全部版本列表 */
.rp-vers {
  margin-left: auto; flex-shrink: 0;
  display: inline-flex; align-items: center; gap: 0.1875rem;
  padding: 0.0625rem 0.375rem;
  border: 1px solid var(--border-subtle); border-radius: 999px;
  background: transparent; cursor: pointer;
  font-family: inherit; font-size: 0.5625rem; line-height: 1.4;
  color: var(--text-muted);
  transition: color .12s, border-color .12s, background .12s;
}
.rp-vers:hover { color: var(--accent); border-color: var(--accent); background: var(--bg-hover); }
.rp-vers-n {
  font-family: var(--font-mono); font-size: 0.5rem;
  padding: 0 0.1875rem; border-radius: 999px;
  background: color-mix(in srgb, var(--accent) 16%, transparent);
  color: var(--accent);
}

/* 第 3 行：主按钮吃满剩余宽度，右侧固定一枚「⋯」菜单按钮。
   行内只有两个元素，任何侧栏宽度都不会换行。 */
.rp-actions { display: flex; align-items: center; gap: 0.375rem; flex-wrap: nowrap; }
.rp-actions :deep(.el-button + .el-button) { margin-left: 0; }
.rp-primary { flex: 1 1 auto; min-width: 0; }
.rp-actions :deep(.el-button > span) { overflow: hidden; text-overflow: ellipsis; }
.rp-actions :deep(.el-dropdown) { flex: 0 0 auto; height: 24px; }

/* 「管理」：固定窄宽，启动按钮吃掉剩余空间；240px 侧栏下三枚不换行 */
.rp-manage {
  flex: 0 0 auto;
  padding: 0.25rem 0.4375rem !important;
  font-size: 0.6875rem !important;
}
.rp-manage.is-disabled { opacity: 0.5; }

.rp-more {
  /* 全局 .el-button 用 !important 强制内边距，会把 28px 定宽按钮的
     内容区挤成 0 宽，三个点被 span 的 overflow:hidden 裁没 —— 这里必须
     同样用 !important 抢回来，并让 span 居中放开裁剪 */
  width: 1.75rem !important;
  padding: 0 !important;
  font-size: 0.875rem !important;
  font-weight: 600;
  line-height: 1;
  letter-spacing: 0.05em;
}
.rp-actions :deep(.rp-more > span) {
  width: 100%;
  justify-content: center;
  overflow: visible;
  text-overflow: clip;
}

/* 极窄：只收一点卡片内边距 */
@container (max-width: 20rem) {
  .rp-card { padding: 0.625rem 0.75rem; }
}

.rp-note { margin-top: 0.625rem; font-size: 0.625rem; color: var(--text-muted); line-height: 1.5; }
.rp-warnings { margin-top: 0.5rem; }
.rp-warn-title { font-size: 0.6875rem; color: var(--warning); margin-bottom: 0.25rem; }
.rp-warn-item { font-size: 0.625rem; color: var(--text-muted); }
.rp-log { font-family: var(--font-mono); font-size: 0.6875rem; white-space: pre-wrap; }

/* 「⋯」菜单里的端口信息行：popper 挂在 body 上，scoped 够不着，用 :global */
:global(.rp-more-popper .rp-port-line) {
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 0.6875rem;
  cursor: default;
  background-color: transparent !important;
}
:global(.rp-more-popper .rp-port-line:hover),
:global(.rp-more-popper .rp-port-line:focus) {
  color: var(--text-muted);
  background-color: transparent !important;
}
</style>
