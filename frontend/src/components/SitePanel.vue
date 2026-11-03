<template>
  <div class="site-panel">
    <div class="sp-head">
      <!-- 标题可点 + 右侧列表图标按钮，两种方式都能打开「全部站点」总览 -->
      <button class="sp-title" :title="t('sites.allHint')" @click="allDlg = true">
        {{ t('sites.title') }}
        <span v-if="sites.length" class="sp-title-n">{{ sites.length }}</span>
      </button>
      <button class="sp-all" :title="t('sites.allHint')" @click="allDlg = true">
        <svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.4">
          <path d="M2 4h12M2 8h12M2 12h12" stroke-linecap="round" />
        </svg>
      </button>
      <el-button size="small" type="primary" @click="openCreate">{{ t('sites.new') }}</el-button>
    </div>

    <div v-if="sites.length === 0" class="sp-empty">{{ t('sites.empty') }}</div>

    <div v-for="s in sites" :key="s.id" class="sp-card">
      <!-- 行 1：灯 · 域名 · 端口 ……… 状态 -->
      <div class="sp-top">
        <span class="sp-dot" :class="dotClass(liveOf(s))" />
        <span class="sp-name" :title="s.id">{{ s.domains[0] || s.id }}</span>
        <span class="sp-port" :title="t('sites.port')">:{{ s.sitePort }}</span>
        <span class="sp-state" :class="stateClass(liveOf(s))">{{ stateText(liveOf(s)) }}</span>
      </div>

      <!-- 行 2：全部域名（含附加域名）+ 静态标记 -->
      <div class="sp-domains" :title="s.domains.join(', ')">
        <span>{{ s.domains.join(', ') }}</span>
        <span v-if="s.tlsMode === 'selfsigned'" class="sp-tag sp-tag-tls">https</span>
        <!-- https 站点同时开的明文端口：点一下用 http 打开（自签证书不便时用） -->
        <button v-if="s.plainUrl" class="sp-tag sp-tag-http" :title="s.plainUrl" @click.stop="openPlain(s)">http :{{ s.plainPort }}</button>
        <span v-if="!s.php" class="sp-tag">{{ t('sites.tagStatic') }}</span>
        <span v-else class="sp-tag sp-tag-php">{{ phpTag(s) }}</span>
        <span v-if="s.options?.expireAt" class="sp-expire" :title="t('sites.expireAt')">{{ s.options.expireAt }}</span>
      </div>

      <!-- 行 3：根目录 + 服务栈 -->
      <div class="sp-line">
        <span class="sp-val sp-mono" :title="s.root">{{ s.root }}</span>
      </div>
      <div class="sp-line">
        <span class="sp-val sp-stack">{{ stackText(s) }}</span>
      </div>

      <!-- 行 4：只留两个必要动作（启动/停止、打开）+ 「⋯」菜单。
           配置 / 日志 / hosts / 根目录 / 改端口 / 重建 / 删除全收进菜单，
           240px 窄侧栏也不会换行、不裁字。 -->
      <div class="sp-actions">
        <el-button
          size="small" class="sp-act"
          :type="liveOf(s) === 'running' ? 'warning' : 'success'"
          :loading="busy === s.id"
          @click="onToggle(s)"
        >{{ liveOf(s) === 'running' ? t('sites.stop') : t('sites.start') }}</el-button>
        <el-button
          size="small" type="primary" class="sp-act"
          :disabled="liveOf(s) !== 'running'"
          @click="onOpen(s)"
        >{{ t('sites.open') }}</el-button>
        <el-dropdown trigger="click" popper-class="sp-more-popper" @command="(cmd) => onMore(cmd, s)">
          <el-button size="small" class="sp-more" :title="t('sites.more')">⋯</el-button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item
                v-for="it in moreItems(s)" :key="it.cmd"
                :command="it.cmd" :divided="!!it.divided" :class="it.cls"
              >{{ it.label }}</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>
    </div>

    <!-- 全部站点总览：点面板标题「站点」打开，一次看全所有站点 -->
    <el-dialog
      v-model="allDlg"
      :title="t('sites.allTitle')"
      width="54rem"
      draggable
      :close-on-click-modal="false"
      append-to-body
    >
      <div class="sl-head">
        <span class="sl-count">{{ t('sites.allCount', { n: sites.length }) }}</span>
        <el-button size="small" type="primary" plain @click="openCreate">{{ t('sites.new') }}</el-button>
      </div>

      <div class="sl-list">
        <div v-for="s in sites" :key="s.id" class="sl-row">
          <span class="sl-dot" :class="dotClass(liveOf(s))" />
          <div class="sl-main">
            <div class="sl-top">
              <span class="sl-domain" :title="s.domains.join(', ')">{{ s.domains[0] || s.id }}</span>
              <span class="sp-port" :title="t('sites.port')">:{{ s.sitePort }}</span>
              <span v-if="s.tlsMode === 'selfsigned'" class="sp-tag sp-tag-tls">https</span>
              <span v-if="!s.php" class="sp-tag">{{ t('sites.tagStatic') }}</span>
        <span v-else class="sp-tag sp-tag-php">{{ phpTag(s) }}</span>
              <span v-if="s.options?.expireAt" class="sl-expire">{{ s.options.expireAt }}</span>
            </div>
            <div class="sl-sub">
              <span class="sl-mono" :title="s.root">{{ s.root }}</span>
              <span class="sl-stack">{{ stackText(s) }}</span>
            </div>
          </div>
          <span class="sl-state" :class="stateClass(liveOf(s))">{{ stateText(liveOf(s)) }}</span>
          <div class="sl-ops">
            <el-button
              size="small"
              :type="liveOf(s) === 'running' ? 'warning' : 'success'"
              :loading="busy === s.id"
              @click="onToggle(s)"
            >{{ liveOf(s) === 'running' ? t('sites.stop') : t('sites.start') }}</el-button>
            <el-button
              size="small" type="primary" plain
              :disabled="liveOf(s) !== 'running'"
              @click="onOpen(s)"
            >{{ t('sites.open') }}</el-button>
            <el-dropdown trigger="click" popper-class="sp-more-popper" @command="(cmd) => onMore(cmd, s)">
              <el-button size="small" class="sp-more" :title="t('sites.more')">⋯</el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item
                    v-for="it in moreItems(s)" :key="it.cmd"
                    :command="it.cmd" :divided="!!it.divided" :class="it.cls"
                  >{{ it.label }}</el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </div>
        </div>

        <div v-if="!sites.length" class="sp-empty">{{ t('sites.empty') }}</div>
      </div>
    </el-dialog>

    <!-- hosts -->
    <el-dialog v-model="hostsDlg" :title="t('sites.hostsTitle')" width="30rem">
      <p class="sp-hint">{{ t('sites.hostsHint') }}</p>
      <pre class="sp-hosts-box">{{ hostsLine }}</pre>
      <template #footer>
        <el-button size="small" @click="onCopyHosts">{{ t('sites.copy') }}</el-button>
        <el-button size="small" type="primary" :loading="writingHosts" @click="onWriteHosts">{{ t('sites.writeHosts') }}</el-button>
      </template>
    </el-dialog>

    <el-drawer v-model="logDrawer" :title="logTitle" size="60%">
      <pre class="sp-log">{{ logText }}</pre>
    </el-drawer>

    <!-- 新建 / 编辑共用同一个「站点」对话框：基本配置就是第一屏（对齐 phpStudy） -->
    <SiteDialog v-model:visible="siteDlg" :site="dlgSite" @saved="refresh" />

    <!-- 一键部署程序（DuihaoCMS / WordPress / Typecho / 自定义） -->
    <DeployDialog v-model:visible="deployDlg" :site="deploySite" />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Browser } from '@wailsio/runtime'
import { useI18n } from '../i18n'
import SiteDialog from './SiteDialog.vue'
import DeployDialog from './DeployDialog.vue'
import { ListInstances } from '../../bindings/github.com/ys-ll/uniterm/backend/duihao/service/plugincenterservice'
import {
  ListSites, DeleteSite, StartSite, StopSite,
  SiteLogs, OpenSite, HostsLine, WriteHosts, RebuildSite, SetSitePort, OpenSiteRoot,
  LocalCAStatus, InstallLocalCA, DiagnoseSitePort, AutoFixSitePort,
} from '../../bindings/github.com/ys-ll/uniterm/backend/duihao/service/siteservice'

interface InstanceView {
  id: string; plugin: string; version: string; name: string
  ports: Record<string, number>; live: string
}
interface SiteView {
  id: string; name: string; domains: string[]; root: string; port: number
  web: string; php?: string; db?: string; database?: string; tlsMode?: string
  webLive: string; phpLive?: string; phpPort?: number; dbLive?: string
  webPort: number; sitePort: number; url: string; hostsReady: boolean
  // https 站点附带的明文端口：plainUrl 空表示只有 https（M58）
  plainPort?: number; plainUrl?: string
  disabled?: boolean; live?: string
  options?: Record<string, any>
}

const { t } = useI18n()
const sites = ref<SiteView[]>([])
const instances = ref<InstanceView[]>([])
const busy = ref('')

// 站点对话框（新建时 site=null，编辑时传站点）
const siteDlg = ref(false)
const dlgSite = ref<SiteView | null>(null)
// 一键部署对话框
const deployDlg = ref(false)
const deploySite = ref<SiteView | null>(null)
// 全部站点总览（点面板标题打开）
const allDlg = ref(false)
function openCreate() {
  dlgSite.value = null
  allDlg.value = false
  siteDlg.value = true
}

// 「⋯」菜单项：卡片与全部站点弹窗共用一份，避免两处文案/顺序走样
interface MoreItem { cmd: string; label: string; divided?: boolean; cls?: string }
function moreItems(s: SiteView): MoreItem[] {
  return [
    { cmd: 'settings', label: t('sites.settings') },
    { cmd: 'deploy', label: t('sites.deploy'), cls: 'sp-deploy-item' },
    { cmd: 'logs', label: t('sites.logs') },
    {
      cmd: 'hosts',
      label: s.hostsReady ? t('sites.hostsReady') : t('sites.hostsMissing'),
      divided: true,
      cls: s.hostsReady ? 'sp-hosts-item ok' : 'sp-hosts-item',
    },
    { cmd: 'root', label: t('sites.openRoot') },
    { cmd: 'port', label: t('sites.changePort') },
    { cmd: 'ca', label: t('sites.localCA'), divided: true },
    { cmd: 'rebuild', label: t('sites.rebuild') },
    { cmd: 'delete', label: t('sites.delete'), divided: true },
  ]
}

const hostsDlg = ref(false)
const hostsLine = ref('')
const hostsSite = ref('')
const writingHosts = ref(false)

const logDrawer = ref(false)
const logTitle = ref('')
const logText = ref('')

// 编辑站点 → 复用同一个对话框
function onSettings(s: SiteView) {
  dlgSite.value = s
  siteDlg.value = true
}

// 站点状态文案（站点本身没有独立进程，取 web 实例的活状态）
function stateText(live: string): string {
  if (live === 'running') return t('sites.running')
  if (live === 'error') return t('sites.stateError')
  if (live === 'starting') return t('sites.stateStarting')
  if (live === 'stopping') return t('sites.stateStopping')
  return t('sites.stateStopped')
}
function stateClass(live: string): string {
  if (live === 'running') return 'sp-state-run'
  if (live === 'error') return 'sp-state-err'
  return ''
}

function dotClass(live: string): string {
  const map: Record<string, string> = {
    running: 'sp-dot-run', starting: 'sp-dot-wait', stopping: 'sp-dot-wait', error: 'sp-dot-err',
  }
  return map[live] || 'sp-dot-off'
}
// 站点级状态：停用的站点显示 stopped；启用的跟随 nginx 实例
//（nginx/php 是多站点共享的，光看 webLive 分不出这个站点是开是关）
function liveOf(s: SiteView): string {
  if (s.disabled) return 'stopped'
  return s.live || s.webLive
}
function stackText(s: SiteView): string {
  const name = (id?: string) => {
    if (!id) return ''
    const i = instances.value.find(x => x.id === id)
    if (!i) return id
    // 实例的 name 是「展示名」，早期装配的实例没写这个字段（omitempty）——
    // 直接用 i.name 会渲染成 "undefined@1.15.11"。退回插件 id（nginx/mysql）
    // 这种一眼能认的写法。
    return `${i.name || i.plugin || i.id}@${i.version || '—'}`
  }
  // PHP 版本单独用标签展示（见 phpTag），这里只留 Web + 数据库
  return [name(s.web), name(s.db)].filter(Boolean).join(' + ')
}
// 站点绑定的 PHP 版本（未装实例时退回实例 id）
function phpTag(s: SiteView): string {
  const i = instances.value.find(x => x.id === s.php)
  return i ? `PHP ${i.version}` : 'PHP'
}

async function refresh() {
  try {
    instances.value = (await ListInstances()) as unknown as InstanceView[]
    sites.value = (await ListSites()) as unknown as SiteView[]
  } catch (e) {
    console.error('refresh sites', e)
  }
}

async function onToggle(s: SiteView) {
  busy.value = s.id
  try {
    if (liveOf(s) === 'running') await StopSite(s.id)
    else await StartSite(s.id)
    await refresh()
  } catch (e) {
    // 端口被别的软件占了：直接给「换到空闲端口」的一键修复，而不是让用户自己猜
    const msg = String(parseErr(e))
    if (/占用|不可用|权限/.test(msg)) {
      await offerPortFix(s, msg)
    } else {
      ElMessage.error(msg)
    }
    await refresh()
  } finally {
    busy.value = ''
  }
}

// 端口冲突 → 诊断 + 一键换端
async function offerPortFix(s: SiteView, reason: string) {
  try {
    const d = (await DiagnoseSitePort(s.id)) as unknown as {
      port: number; free: boolean; occupier?: string; suggest?: number; message?: string
    }
    if (d.free) {
      ElMessage.error(reason)
      return
    }
    const detail = d.message || `端口 ${d.port} 被 ${d.occupier || '其他程序'} 占用`
    if (!d.suggest) {
      ElMessage.error(`${detail}；没有可替换的空闲端口，请手动释放端口`)
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
    const next = (await AutoFixSitePort(s.id)) as unknown as number
    ElMessage.success(t('sites.portFixDone', { port: next }))
    // 按钮写的是「换端口并启动」——换完就把站点拉起来，别再让用户点一次。
    // 这里再失败就只在准备阶段（端口明明是刚诊断出来的空闲端口，一般是
    // 后端实例起不来），照实报错即可，不再递归走一遍修复。
    try {
      await StartSite(s.id)
    } catch (e3) {
      ElMessage.error(String(parseErr(e3)))
    }
    await refresh()
  } catch (e2) {
    ElMessage.error(String(parseErr(e2)))
  }
}

// 本地根证书：装一次，所有 https 站点从「黄锁」变「绿锁」
async function onLocalCA() {
  try {
    const st = (await LocalCAStatus()) as unknown as { installed: boolean; certPath: string }
    if (st.installed) {
      ElMessage.success(t('sites.caInstalled'))
      return
    }
    try {
      await ElMessageBox.confirm(t('sites.caMsg'), t('sites.caTitle'), {
        type: 'info',
        confirmButtonText: t('sites.caOk'),
        cancelButtonText: t('common.cancel'),
      })
    } catch {
      return
    }
    const n = (await InstallLocalCA()) as unknown as number
    ElMessage.success(t('sites.caDone', { n }))
    await refresh()
  } catch (e) {
    ElMessage.error(String(parseErr(e)))
  }
}

async function onOpen(s: SiteView) {
  try {
    await OpenSite(s.id)
  } catch (e) {
    ElMessage.error(String(parseErr(e)))
  }
}

// https 站点的明文入口：直接开浏览器，不走 OpenSite（那边开的是 https）
function openPlain(s: SiteView) {
  if (s.plainUrl) Browser.OpenURL(s.plainUrl)
}

async function onLogs(s: SiteView) {
  try {
    logTitle.value = `${s.id} · ${t('sites.logs')}`
    logText.value = (await SiteLogs(s.id, 200)) as unknown as string
    logDrawer.value = true
  } catch (e) {
    ElMessage.error(String(parseErr(e)))
  }
}

async function onHosts(s: SiteView) {
  try {
    hostsSite.value = s.id
    hostsLine.value = (await HostsLine(s.id)) as unknown as string
    hostsDlg.value = true
  } catch (e) {
    ElMessage.error(String(parseErr(e)))
  }
}
async function onCopyHosts() {
  try {
    await navigator.clipboard.writeText(hostsLine.value)
    ElMessage.success(t('sites.copied'))
  } catch {
    ElMessage.info(hostsLine.value)
  }
}
async function onWriteHosts() {
  writingHosts.value = true
  try {
    await WriteHosts(hostsSite.value)
    ElMessage.success(t('sites.hostsDone'))
    hostsDlg.value = false
    await refresh()
  } catch (e) {
    ElMessage.error(String(parseErr(e)))
  } finally {
    writingHosts.value = false
  }
}

async function onMore(cmd: string, s: SiteView) {
  if (cmd === 'settings') {
    onSettings(s)
    return
  }
  if (cmd === 'deploy') {
    deploySite.value = s
    deployDlg.value = true
    return
  }
  if (cmd === 'logs') {
    await onLogs(s)
    return
  }
  if (cmd === 'hosts') {
    await onHosts(s)
    return
  }
  if (cmd === 'root') {
    try {
      await OpenSiteRoot(s.id)
    } catch (e) {
      ElMessage.error(String(parseErr(e)))
    }
    return
  }
  if (cmd === 'port') {
    let value = ''
    try {
      const r = await ElMessageBox.prompt(t('sites.portPrompt'), t('sites.changePort'), {
        inputValue: String(s.sitePort),
        inputPattern: /^\d+$/,
        inputErrorMessage: t('sites.portInvalid'),
      })
      value = String(r.value || '')
    } catch {
      return
    }
    const port = Number(value)
    if (!port || port === s.sitePort) return
    try {
      await SetSitePort(s.id, port)
      ElMessage.success(t('sites.portUpdated', { port }))
      await refresh()
    } catch (e) {
      ElMessage.error(String(parseErr(e)))
    }
    return
  }
  if (cmd === 'ca') {
    await onLocalCA()
    return
  }
  if (cmd === 'rebuild') {
    try {
      await RebuildSite(s.id)
      ElMessage.success(t('sites.rebuilt'))
    } catch (e) {
      ElMessage.error(String(parseErr(e)))
    }
    return
  }
  if (cmd === 'delete') {
    try {
      await ElMessageBox.confirm(t('sites.deleteConfirm', { id: s.id }), { type: 'warning' })
    } catch {
      return
    }
    try {
      await DeleteSite(s.id)
      await refresh()
    } catch (e) {
      ElMessage.error(String(parseErr(e)))
    }
  }
}

function parseErr(e: unknown): string {
  if (e instanceof Error) return e.message
  try { return JSON.stringify(e) } catch { return String(e) }
}

onMounted(refresh)

// 命令面板「新建站点」动作（M18）：App.vue 切到 sites 视图后广播 site:create
window.addEventListener('site:create', openCreate)
onUnmounted(() => window.removeEventListener('site:create', openCreate))
</script>

<style scoped>
.site-panel { padding: 0.5rem; overflow-y: auto; height: 100%; display: flex; flex-direction: column; }
.sp-head { display: flex; align-items: center; gap: 0.5rem; margin-bottom: 0.5rem; }
/* 标题即入口：点一下弹出全部站点总览 */
.sp-title {
  display: inline-flex; align-items: center; gap: 0.3125rem;
  font: inherit; font-size: 0.8125rem; font-weight: 600; color: var(--text-primary);
  background: transparent; border: none; padding: 0; cursor: pointer; min-width: 0;
}
.sp-title:hover { color: var(--accent); }
.sp-title-n {
  flex-shrink: 0; font-family: var(--font-mono); font-size: 0.5625rem; font-weight: 500;
  color: var(--text-muted); border: 1px solid var(--border-subtle);
  border-radius: 0.5rem; padding: 0 0.25rem; line-height: 1.35;
}
.sp-title:hover .sp-title-n { color: var(--accent); border-color: var(--accent); }
/* 标题旁的「全部站点」入口按钮（列表图标） */
.sp-all {
  flex-shrink: 0; display: inline-flex; align-items: center; justify-content: center;
  width: 1.25rem; height: 1.25rem; padding: 0;
  border: 1px solid transparent; border-radius: var(--radius-sm);
  background: transparent; color: var(--text-muted); cursor: pointer;
  transition: color .12s, background .12s, border-color .12s;
}
.sp-all:hover { color: var(--accent); background: var(--bg-hover); border-color: var(--border-subtle); }
.sp-head :deep(.el-button) { margin-left: auto; }
.sp-empty { color: var(--text-muted); font-size: 0.75rem; padding: 1.25rem 0; text-align: center; }

.sp-card {
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  padding: 0.5rem 0.625rem;
  margin-bottom: 0.5rem;
}
.sp-top { display: flex; align-items: center; gap: 0.375rem; margin-bottom: 0.25rem; min-width: 0; }
.sp-dot { width: 0.5rem; height: 0.5rem; border-radius: 50%; background: var(--text-disabled); flex-shrink: 0; }
.sp-dot-run { background: var(--success); box-shadow: 0 0 0 3px color-mix(in srgb, var(--success) 18%, transparent); }
.sp-dot-err { background: var(--error); }
.sp-dot-wait { background: var(--warning); }
.sp-dot-off { background: var(--text-disabled); }
.sp-name {
  font-size: 0.8125rem; font-weight: 600; color: var(--text-primary);
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0;
}
/* 端口：站点核心标识（每站点独立端口），比 id 更该被看见 */
.sp-port {
  font-size: 0.625rem; font-variant-numeric: tabular-nums; flex-shrink: 0;
  padding: 0 0.25rem; border-radius: var(--radius-sm);
  color: var(--accent-text, var(--text-primary));
  background: var(--accent-soft, var(--bg-hover));
}
.sp-state { margin-left: auto; font-size: 0.625rem; color: var(--text-muted); flex-shrink: 0; }
.sp-state-run { color: var(--success); }
.sp-state-err { color: var(--error); }
.sp-domains {
  display: flex; align-items: center; gap: 0.25rem; min-width: 0;
  font-size: 0.6875rem; color: var(--text-secondary); margin-bottom: 0.1875rem;
}
.sp-domains > span:first-child { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sp-tag {
  flex-shrink: 0; font-size: 0.5625rem; padding: 0 0.25rem; border-radius: 3px;
  color: var(--text-muted); border: 1px solid var(--border-subtle);
}
.sp-tag-tls { color: var(--success); border-color: color-mix(in srgb, var(--success) 45%, transparent); }
/* 明文端口标签：看着像 tag，实际可点（用 http 打开同一站点） */
.sp-tag-http {
  border: 1px solid color-mix(in srgb, var(--accent) 40%, transparent);
  background: transparent;
  color: var(--accent);
  /* button 自带字体（不是 tag 字号），这里显式对齐 .sp-tag */
  font-family: inherit;
  font-size: 0.5625rem;
  line-height: 1.4;
  cursor: pointer !important;
}
.sp-tag-http:hover { background: color-mix(in srgb, var(--accent) 14%, transparent); }
/* 站点绑定的 PHP 版本：一眼看出这个站跑的是哪个版本（多版本并存时尤其有用） */
.sp-tag-php { font-family: var(--font-mono); color: var(--text-secondary); }
.sp-expire { flex-shrink: 0; margin-left: auto; font-size: 0.625rem; color: var(--warning); }
.sp-line { display: flex; gap: 0.375rem; font-size: 0.6875rem; line-height: 1.6; }
.sp-label { color: var(--text-muted); flex: 0 0 3.25rem; }
.sp-val { color: var(--text-secondary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
.sp-mono { font-family: var(--font-mono); font-size: 0.625rem; }
.sp-stack { color: var(--text-muted); }
/* 操作行：启动/停止 + 打开 平分宽度，「⋯」固定 28px；窄侧栏（240px）也不换行不裁字 */
.sp-actions { margin-top: 0.5rem; display: flex; align-items: center; gap: 0.375rem; flex-wrap: nowrap; }
.sp-actions :deep(.el-button + .el-button) { margin-left: 0; }
.sp-act { flex: 1 1 0; min-width: 0; }
.sp-actions :deep(.el-button > span) { overflow: hidden; text-overflow: ellipsis; }
/* 全局 .el-button 的 padding/width 带 !important，这里必须同样带才压得住 */
.sp-more {
  flex: 0 0 auto;
  width: 1.75rem !important; padding: 0 !important;
  font-size: 0.875rem !important; font-weight: 600; line-height: 1; letter-spacing: 0.05em;
}
.sp-actions :deep(.sp-more > span) { width: 100%; justify-content: center; overflow: visible; text-overflow: clip; }

.sp-hint { font-size: 0.75rem; color: var(--text-secondary); margin: 0 0 0.5rem; }
.sp-hosts-box {
  background: var(--bg-base); border: 1px solid var(--border-subtle);
  border-radius: var(--radius-sm); padding: 0.5rem;
  font-family: var(--font-mono); font-size: 0.6875rem; color: var(--text-primary);
  white-space: pre-wrap; word-break: break-all;
}
.sp-log { font-family: var(--font-mono); font-size: 0.6875rem; white-space: pre-wrap; }

/* 「⋯」菜单里的 hosts 行：未绑定=警示色（teleport 到 body，需 :global） */
:global(.sp-more-popper .el-dropdown-menu__item.sp-hosts-item) { color: var(--warning); }
:global(.sp-more-popper .el-dropdown-menu__item.sp-hosts-item.ok) { color: var(--success); }

/* 全部站点总览弹窗 */
.sl-head { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; margin-bottom: 0.5rem; }
.sl-count { font-size: 0.6875rem; color: var(--text-muted); }
.sl-list { display: flex; flex-direction: column; gap: 0.375rem; max-height: 60vh; overflow-y: auto; padding-right: 0.125rem; }
.sl-row {
  display: flex; align-items: center; gap: 0.625rem;
  padding: 0.5rem 0.625rem;
  border: 1px solid var(--border-subtle); border-radius: var(--radius-md);
  background: var(--bg-elevated);
}
.sl-row:hover { border-color: var(--border-default); }
.sl-dot { width: 0.5rem; height: 0.5rem; border-radius: 50%; background: var(--text-disabled); flex-shrink: 0; }
.sl-main { flex: 1 1 auto; min-width: 0; display: flex; flex-direction: column; gap: 0.125rem; }
.sl-top { display: flex; align-items: center; gap: 0.375rem; min-width: 0; }
.sl-domain {
  font-size: 0.8125rem; font-weight: 600; color: var(--text-primary);
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0;
}
.sl-expire { flex-shrink: 0; margin-left: auto; font-size: 0.625rem; color: var(--warning); }
.sl-sub { display: flex; align-items: center; gap: 0.5rem; min-width: 0; font-size: 0.625rem; color: var(--text-muted); }
.sl-mono { font-family: var(--font-mono); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sl-stack { flex-shrink: 0; }
.sl-state { flex-shrink: 0; width: 3.5rem; text-align: right; font-size: 0.6875rem; color: var(--text-secondary); }
.sl-ops { flex-shrink: 0; display: flex; align-items: center; gap: 0.25rem; }
.sl-ops :deep(.el-button + .el-button) { margin-left: 0; }
.sl-ops :deep(.sp-more > span) { width: 100%; justify-content: center; overflow: visible; }
</style>
