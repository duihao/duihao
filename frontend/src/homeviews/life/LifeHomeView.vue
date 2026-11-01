<template>
  <!-- 普通模式首页（M21）：面向普通用户的消费级首屏。
       结构借鉴易等 NAS 主界面 —— 存储环 / 地址 + 扫码 / 功能宫格，
       配色走双轨制的品牌玫红（html.ui-home 提供 --accent）。 -->
  <div class="life-home">
    <!-- 品牌区（logo/名称/模式徽标）在标题栏；「切换到专业模式」收进标题栏的
         ☰ 菜单（窄窗里页面顶部再放一行按钮太浪费纵向空间）。 -->
    <!-- 没有网盘实例时只留「创建」+ IT 组（IT 工具不依赖 NAS 服务，照样能进） -->
    <div v-if="!inst" class="life-empty">
      <p>{{ t('life.noInst') }}</p>
      <button class="life-btn primary" :disabled="busy" @click="createInst">{{ t('life.create') }}</button>
    </div>

    <div v-if="inst" class="life-hero">
      <div class="life-ring">
        <svg viewBox="0 0 120 120" width="148" height="148">
          <circle cx="60" cy="60" r="50" fill="none" stroke="var(--bg-surface)" stroke-width="9" />
          <!-- 已用：**当前盘符**的已用占比（不是整块物理硬盘，也不是所有分区之和） -->
          <circle
            cx="60" cy="60" r="50" fill="none"
            :stroke="'var(--accent)'" stroke-width="9" stroke-linecap="round"
            :stroke-dasharray="ringDash" transform="rotate(-90 60 60)"
          />
          <!-- NAS：网盘目录自身占了多少 —— 画在已用弧**里面**（细一档），
               一眼能看出「已用里有多少是我们自己占的」，不会盖住已用弧。
               颜色固定中性灰（不再用 --info：那套会随主题漂成蓝/深洋红；
               也别用品红 —— 和「已用」同色系，两条弧会糊成一条） -->
          <circle
            v-if="nasDash"
            cx="60" cy="60" r="42" fill="none"
            stroke="var(--nas-arc)" stroke-width="6" stroke-linecap="round"
            :stroke-dasharray="nasDash" transform="rotate(-90 60 60)"
          />
          <text x="60" y="50" text-anchor="middle" class="life-ring-k">{{ ringLabel }}</text>
          <text x="60" y="73" text-anchor="middle" class="life-ring-v">{{ totalText }}</text>
          <text x="60" y="89" text-anchor="middle" class="life-ring-s">{{ usedPctText }}</text>
        </svg>
        <div class="life-legend">
          <span><i class="dot used"></i>{{ t('life.used') }} {{ usedText }}</span>
          <span><i class="dot free"></i>{{ t('life.free') }} {{ freeText }}</span>
          <span><i class="dot nas"></i>{{ t('life.nasUsed') }} {{ nasText }}</span>
        </div>
      </div>

      <div class="life-side">
        <!-- 访问地址：下拉切换 本机/局域网/IPv6；地球 = 外部浏览器打开当前地址，
             二维码 = 当前地址的扫码图（宫格里的「扫码」仍是免密快传）。 -->
        <div class="life-url" ref="urlBox">
          <button class="life-url-pick" :class="{ open: pickOpen }" @click="pickOpen = !pickOpen">
            <!-- 当前选中的地址：上面方式名、下面网址（窄窗自动省略号，悬停看全文） -->
            <span class="life-url-cur">
              <span class="life-url-kind">{{ curAddr ? kindLabel(curAddr.kind) : t('life.addrLocal') }}</span>
              <span class="life-url-sub" :title="curAddr?.url || ''">{{ curAddr?.url || '—' }}</span>
            </span>
            <svg class="life-caret" width="10" height="10" viewBox="0 0 24 24" fill="none"
              stroke="currentColor" stroke-width="2.6" stroke-linecap="round">
              <path d="M6 9l6 6 6-6" />
            </svg>
          </button>
          <button class="life-icon" :title="t('life.open')" :disabled="!curAddr?.ok" @click="openExternal">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
              <circle cx="12" cy="12" r="9" /><path d="M3 12h18M12 3c3 3.5 3 14.5 0 18M12 3c-3 3.5-3 14.5 0 18" />
            </svg>
          </button>
          <button class="life-icon accent" :title="t('life.qrAddr')" :disabled="!curAddr?.ok" @click="qrOpen = true">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <rect x="4" y="4" width="6" height="6" /><rect x="14" y="4" width="6" height="6" />
              <rect x="4" y="14" width="6" height="6" /><path d="M14 14h3v3h-3zM20 14v3M14 20h3M20 20h.01" />
            </svg>
          </button>

          <div v-if="pickOpen" class="life-url-pop">
            <div
              v-for="a in addrs" :key="a.kind"
              class="life-url-item" :class="{ on: a.kind === curKind, off: !a.ok }"
              @click="chooseAddr(a)"
            >
              <span class="life-url-item-hd">
                <b>{{ kindLabel(a.kind) }}</b>
                <i v-if="!a.ok" class="life-url-flag">{{ a.note || '—' }}</i>
                <button v-if="a.fix" class="life-url-fix" :disabled="fixing" @click.stop="enableLan">
                  {{ fixing ? t('life.addrEnabling') : t('life.addrEnable') }}
                </button>
              </span>
              <span class="life-url-item-url">{{ a.url }}</span>
            </div>
          </div>
        </div>
        <div class="life-acts">
          <button class="life-btn" @click="openFiles">{{ t('life.myNas') }}</button>
          <button class="life-btn" @click="copyUrl">{{ t('life.copy') }}</button>
          <button class="life-btn" @click="openManage">{{ t('life.manage') }}</button>
          <button class="life-btn" :disabled="busy" @click="toggleRun">{{ running ? t('life.stop') : t('life.start') }}</button>
        </div>
      </div>
    </div>

    <!-- 宫格两组：我的 NAS（个人空间各模块 + 用户管理）/ IT 专业工具（重点几项）。
         NAS 未启动时宫格与扫码一律置灰不可点（服务没起，进去只有登录页），
         启动按钮本身当然要保持可点。 -->
    <div v-if="inst" class="life-group">
      <div class="life-group-hd">{{ t('life.myNas') }}</div>
      <div class="life-grid">
        <button
          v-for="tile in nasTiles" :key="tile.key" class="life-tile"
          @click="onTile(tile)"
        >
          <span class="life-ico" v-html="tile.icon"></span>
          <span class="life-label">{{ t(tile.key) }}</span>
        </button>
      </div>
    </div>

    <div class="life-group">
      <div class="life-group-hd">{{ t('life.grpIt') }}</div>
      <div class="life-grid">
        <button
          v-for="tile in itTiles" :key="tile.key" class="life-tile"
          :title="t(tile.key)" @click="onIT(tile)"
        >
          <span class="life-ico" v-html="tile.icon"></span>
          <span class="life-label">{{ t(tile.key) }}</span>
        </button>
      </div>
    </div>

    <button v-if="inst" class="life-scan" @click="onScan">
      <span class="life-ico" v-html="ICONS.qr"></span>
      <span>{{ t('life.scanNas') }}</span>
    </button>

    <WebDavManageDialog
      v-if="inst"
      v-model:visible="manageOpen"
      :instance-id="inst.id"
      :instance-name="inst.name || ''"
      @saved="refresh"
    />
    <DavQuickDialog
      v-if="inst && quickUser"
      v-model:visible="quickOpen"
      :instance-id="inst.id"
      :user="quickUser"
    />
    <UrlQrDialog v-if="inst && curAddr" v-model:visible="qrOpen" :url="curAddr.url" />
  </div>
</template>

<script setup lang="ts">
// LifeHomeView：普通模式（一般大众）首屏。
// 取舍：只做「壳 + 入口」，数据仍来自插件中心/网盘服务；导航/日记/账本是
// M22 的活，这里先占位（点击提示即将上线），避免空壳被误认为坏掉。
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from '../../i18n'
import { Browser } from '@wailsio/runtime'
import { OpenAppWindow, OpenManageWindow, OpenITTool } from '../../../bindings/github.com/ys-ll/uniterm/app'
import { ElMessage, ElMessageBox } from 'element-plus'
import WebDavManageDialog from '../../components/WebDavManageDialog.vue'
import DavQuickDialog from '../../components/DavQuickDialog.vue'
import UrlQrDialog from '../../components/UrlQrDialog.vue'
import {
  ListInstances, CreateInstance, StartInstance, StopInstance, ListPlugins
} from '../../../bindings/github.com/ys-ll/uniterm/backend/duihao/service/plugincenterservice'
import {
  GetWebDavConnInfo, GetStorageStats, ListWebDavShares, EnableLanAccess, NASEntryURL
} from '../../../bindings/github.com/ys-ll/uniterm/backend/duihao/service/webdavservice'

const { t } = useI18n()

interface InstanceView {
  id: string; plugin: string; version: string; name: string
  ports: Record<string, number>; state: string; live: string
}
const inst = ref<InstanceView | null>(null)
const conn = ref<any>(null)
const stats = ref<{ total: number; free: number; used: number; drive?: string; root?: string } | null>(null)
const quickUser = ref('')
const busy = ref(false)
const manageOpen = ref(false)
const quickOpen = ref(false)
const qrOpen = ref(false)

// ---- 访问地址下拉 ----
interface AddrView { kind: string; label: string; url: string; ok: boolean; note?: string; fix?: string }
const ADDR_KEY = 'life.addrKind'
const addrs = computed<AddrView[]>(() => ((conn.value?.addrs || []) as AddrView[]))
const curKind = ref('')
const pickOpen = ref(false)
const fixing = ref(false)
const urlBox = ref<HTMLElement | null>(null)

// 当前选中项：选过的还在就用它，否则退回第一个可用（局域网优先）。
const curAddr = computed<AddrView | null>(() => {
  const list = addrs.value
  if (!list.length) return null
  return list.find(a => a.kind === curKind.value) || list.find(a => a.ok) || list[0]
})

function kindLabel(kind: string): string {
  if (kind === 'lan') return t('life.addrLan')
  if (kind === 'v6') return t('life.addrV6')
  return t('life.addrLocal')
}

// 默认落「局域网」（手机扫码最常用），它不可用才退本机；用户手选过就记住。
function pickDefaultAddr() {
  const list = addrs.value
  if (!list.length) return
  if (list.some(a => a.kind === curKind.value && a.ok)) return
  let kept = ''
  try { kept = localStorage.getItem(ADDR_KEY) || '' } catch { /* 隐私模式 */ }
  const pick =
    list.find(a => a.kind === kept && a.ok) ||
    list.find(a => a.kind === 'lan' && a.ok) ||
    list.find(a => a.ok) || list[0]
  curKind.value = pick.kind
}

function chooseAddr(a: AddrView) {
  if (!a.ok) {
    // 不可用但「改设置就能通」→ 直接问一句要不要开，省得去设置里翻。
    if (a.fix) enableLan()
    return
  }
  pickOpen.value = false
  curKind.value = a.kind
  try { localStorage.setItem(ADDR_KEY, a.kind) } catch { /* 隐私模式 */ }
}

// 一键开启局域网访问：监听地址 0.0.0.0 + 自签 HTTPS（服务层硬规则，缺 TLS 起不来）。
// 实例运行中会被重启一次，所以先问一句。
async function enableLan() {
  if (fixing.value || !inst.value) return
  try {
    await ElMessageBox.confirm(
      t('life.addrEnableAsk'),
      t('life.addrEnable'),
      { confirmButtonText: t('life.addrEnable'), cancelButtonText: t('common.cancel'), type: 'info' }
    )
  } catch { return }
  fixing.value = true
  try {
    const info = await EnableLanAccess(inst.value.id)
    if (info) conn.value = info
    pickDefaultAddr()
    ElMessage.success(t('life.addrEnabled'))
  } catch (e: any) {
    ElMessage.error(String(e?.message || e))
  } finally {
    fixing.value = false
  }
}

function onDocClick(e: MouseEvent) {
  if (!pickOpen.value) return
  if (urlBox.value && !urlBox.value.contains(e.target as Node)) pickOpen.value = false
}

// 地球：系统浏览器打开当前地址（内置窗入口是下面的「网盘」按钮）。
function openExternal() {
  const a = curAddr.value
  if (!a || !a.ok) return
  if (!running.value) {
    ElMessage.warning(t('life.startFirst'))
    return
  }
  Browser.OpenURL(a.url)
}

const running = computed(() => inst.value?.live === 'running')

const GB = 1024 ** 3
function fmt(n?: number | null): string {
  if (n == null || n < 0) return '—'
  const g = n / GB
  if (g >= 1024) return `${(g / 1024).toFixed(2)} TB`
  return g >= 10 ? `${Math.round(g * 10) / 10} GB` : `${g.toFixed(1)} GB`
}
const totalText = computed(() => fmt(stats.value?.total ?? null))
// 盘符已用 = 盘符总量 - 盘符剩余（都是当前盘符的口径，不掺别的分区）
const usedText = computed(() => fmt(stats.value ? stats.value.total - stats.value.free : null))
const freeText = computed(() => fmt(stats.value?.free ?? null))
// NAS 占用 = 网盘根目录自身的体积（后端 walk 出来的，-1 表示还在算）
const nasText = computed(() => (stats.value && stats.value.used >= 0 ? fmt(stats.value.used) : '—'))
// 环上只写「存储」，不跟盘符（用户定稿：字面更干净；算哪个盘看「已用/可用」的数值就够了）
const ringLabel = computed(() => t('life.storage'))

const RING_C = 2 * Math.PI * 50
// NAS 弧半径比已用弧小一档（42 vs 50），才真的落在已用弧**内侧**：
// 半径相同的话两条弧同心叠在一起，灰弧会把玫红的已用弧整段盖住。
const NAS_C = 2 * Math.PI * 42
// 注意：入参是 0..100 的百分比，画弧要的是周长比例 —— 先 /100 再乘周长，
// 否则 dash 长度大于周长，整圈都会被画满（曾经踩过）。
function arc(pct: number, c = RING_C): string {
  const p = Math.min(100, Math.max(0, pct)) / 100
  return `${(c * p).toFixed(1)} ${c.toFixed(1)}`
}
const usedPct = computed(() => {
  const s = stats.value
  if (!s || !s.total) return 0
  return ((s.total - s.free) / s.total) * 100
})
const usedPctText = computed(() => (stats.value?.total ? `${t('life.used')} ${Math.round(usedPct.value)}%` : ''))
const ringDash = computed(() => arc(usedPct.value))
// NAS 占用弧：不足 0.4% 时不画（0.1% 的线在 9px 环宽上根本看不见，反而像脏点）
const nasDash = computed(() => {
  const s = stats.value
  if (!s || !s.total || s.used <= 0) return ''
  const pct = (s.used / s.total) * 100
  return pct < 0.4 ? '' : arc(pct, NAS_C)
})

const ICONS = {
  drive: '<svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>',
  nav: '<svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c3 3.5 3 14.5 0 18M12 3c-3 3.5-3 14.5 0 18"/></svg>',
  diary: '<svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><path d="M5 4h11l3 3v13H5z"/><path d="M9 10h6M9 14h6"/></svg>',
  ledger: '<svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><circle cx="12" cy="12" r="9"/><path d="M12 7v10M9.5 9.5c0-1 1-1.7 2.5-1.7s2.5.7 2.5 1.8c0 2.4-5 1.6-5 4 0 1.1 1 1.9 2.5 1.9s2.5-.7 2.5-1.7"/></svg>',
  video: '<svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><rect x="3" y="5" width="18" height="14" rx="2"/><path d="M10 9.5l5 2.5-5 2.5z"/></svg>',
  music: '<svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><path d="M9 18V6l10-2v11"/><circle cx="6.5" cy="18" r="2.5"/><circle cx="16.5" cy="15" r="2.5"/></svg>',
  health: '<svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"><path d="M20.5 8.6a4.6 4.6 0 0 0-8.5-2.4A4.6 4.6 0 0 0 3.5 8.6c0 4.6 8.5 10 8.5 10s8.5-5.4 8.5-10z"/></svg>',
  camera: '<svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect x="2.5" y="6.5" width="12.5" height="11" rx="2.5"/><path d="M15 10.5l6-3.2v9.4l-6-3.2"/></svg>',
  voice: '<svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"><rect x="9" y="3" width="6" height="11" rx="3"/><path d="M5.5 11.5v.5a6.5 6.5 0 0 0 13 0v-.5M12 18.5V21"/></svg>',
  download: '<svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M12 4v10"/><path d="M8 10.5l4 4 4-4"/><path d="M4.5 18.5h15"/></svg>',
  remote: '<svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="8.5"/><path d="M3.5 12h17"/><path d="M12 3.5c2.6 2.3 4 5.2 4 8.5s-1.4 6.2-4 8.5c-2.6-2.3-4-5.2-4-8.5s1.4-6.2 4-8.5z"/></svg>',
  // 我的 NAS 组：用户管理（管理员才看得到）
  users: '<svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"><circle cx="9" cy="8" r="3.2"/><path d="M3.5 19.5c0-3 2.4-5.2 5.5-5.2s5.5 2.2 5.5 5.2"/><path d="M16 5.6a3 3 0 0 1 0 5.5M17.6 14.5c1.9.7 3.2 2.4 3.2 4.6"/></svg>',
  // IT 专业工具组：环境软件 / 站点管理 / 创建连接 / 连接管理
  plug: '<svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"><path d="M9 3v5M15 3v5M7 8h10v4a5 5 0 0 1-10 0z"/><path d="M12 17v4"/></svg>',
  site: '<svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c2.8 2.6 3 15.4 0 18M12 3c-2.8 2.6-3 15.4 0 18"/></svg>',
  connNew: '<svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"><circle cx="10" cy="12" r="7.5"/><path d="M10 8.5v7M6.5 12h7"/><path d="M17.5 16.5l3 3M20.5 16.5l-3 3"/></svg>',
  connList: '<svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"><rect x="3" y="4.5" width="18" height="6" rx="2"/><rect x="3" y="13.5" width="18" height="6" rx="2"/><path d="M6.5 7.5h.01M6.5 16.5h.01"/></svg>',
  qr: '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><rect x="3.5" y="3.5" width="6.5" height="6.5" rx="1"/><rect x="14" y="3.5" width="6.5" height="6.5" rx="1"/><rect x="3.5" y="14" width="6.5" height="6.5" rx="1"/><path d="M14 14h2.5v2.5H14zM19 14h1.5M14 19v1.5M18.5 19H21M21 16.5V19"/></svg>',
}

// 我的 NAS 组：个人空间各模块（与网盘顶栏同名同序）+ 用户管理。
const nasTiles = [
  { key: 'life.files', icon: ICONS.drive, act: 'drive' },
  { key: 'life.video', icon: ICONS.video, act: 'video' },
  { key: 'life.music', icon: ICONS.music, act: 'music' },
  { key: 'life.download', icon: ICONS.download, act: 'download' },
  { key: 'life.notes', icon: ICONS.diary, act: 'notes' },
  { key: 'life.ledger', icon: ICONS.ledger, act: 'ledger' },
  { key: 'life.health', icon: ICONS.health, act: 'health' },
  { key: 'life.nav', icon: ICONS.nav, act: 'nav' },
  { key: 'life.voice', icon: ICONS.voice, act: 'voice' },
  { key: 'life.camera', icon: ICONS.camera, act: 'camera' },
  { key: 'life.remote', icon: ICONS.remote, act: 'remote' },
  { key: 'life.users', icon: ICONS.users, act: 'users' }
]

// IT 专业工具组：只放最常用的四个入口（环境软件 / 站点管理 / 创建连接 / 连接管理），
// 点击后由后端 OpenITTool 亮起 IT 工具窗并广播 {view, action} —— 两个窗口是两个
// 独立 webview，DOM 事件传不过去，只能走 Go 的事件广播。
// 「创建连接」落在 connections 面板后再补一个 connection 动作（直接开新建表单）。
const itTiles = [
  { key: 'life.plugins', icon: ICONS.plug, view: 'plugins', action: '' },
  { key: 'life.sitesMgr', icon: ICONS.site, view: 'sites', action: '' },
  { key: 'life.newConn', icon: ICONS.connNew, view: 'connections', action: 'connection' },
  { key: 'life.conns', icon: ICONS.connList, view: 'connections', action: '' }
]

function onIT(tile: { view: string; action: string }) {
  OpenITTool(tile.view, tile.action).catch(() => { /* 老版本 exe 没有这个方法：静默 */ })
}

// 网盘就是实例根地址（"/"），不加 /-files 别名。内置 webview 新窗打开。
// 根路径是文件管理器（共享账号语义），走普通地址 —— 不需要 NAS 直登票。
// 宫格各 NAS 模块统一入口：127.0.0.1（恒 http）+ 管理员 60 秒直登票
// （?_k=，仅本机回环有效），桌面点进来免登录；扫码/浏览器直接访问不带票，
// 照常输账号密码。换票失败（服务没起/老版本 exe）退回本机 http 地址，
// 登录页兜底，功能不断。
async function nasURL(path: string): Promise<string> {
  if (inst.value) {
    try {
      return await NASEntryURL(inst.value.id, path)
    } catch { /* 直登票拿不到就算了，登录页兜底 */ }
  }
  const port = conn.value?.port
  return port ? `http://127.0.0.1:${port}${path === '/' ? '/' : path}` : ''
}
// NAS 未启动时的统一提示：宫格 / 「我的NAS」/ 扫码都先过这里。
// 按钮不置灰（各状态下都可点），点了给一句「请先启动」，比灰按钮更好懂。
function ensureRunning(): boolean {
  if (running.value) return true
  ElMessage.warning(t('life.startFirst'))
  return false
}
function openFiles() {
  if (!ensureRunning()) return
  if (!conn.value?.url) {
    ElMessage.warning(t('life.startFirst'))
    return
  }
  nasURL('/').then((url) => {
    if (!url) return
    OpenAppWindow(t('life.files'), url).catch(() => Browser.OpenURL(url))
  })
}
// 网盘配置：直接开独立管理窗（M20 管理窗，panel=webdav），不再是弹窗。
// 开窗失败（老版本 exe 等）退回弹窗，功能不断。
async function openManage() {
  if (!inst.value) return
  try {
    await OpenManageWindow('webdav', inst.value.id, inst.value.name || '')
  } catch {
    manageOpen.value = true
  }
}
async function openApp(path: string, title = '') {
  if (!ensureRunning()) return
  if (!conn.value?.url) {
    ElMessage.warning(t('life.startFirst'))
    return
  }
  const url = await nasURL(path)
  if (!url) return
  OpenAppWindow(title, url).catch(() => Browser.OpenURL(url))
}
async function copyUrl() {
  const url = curAddr.value?.url || conn.value?.url
  if (!url || url === '—') return
  try {
    await navigator.clipboard.writeText(url)
    ElMessage.success(t('life.copied'))
  } catch {
    ElMessage.warning(url)
  }
}
function onTile(tile: { act: string }) {
  switch (tile.act) {
    case 'drive': openFiles(); break
    case 'nav': openApp('/-home', t('life.nav')); break
    case 'notes': openApp('/-notes', t('life.notes')); break
    case 'ledger': openApp('/-ledger', t('life.ledger')); break
    case 'video': openApp('/-video', t('life.video')); break
    case 'music': openApp('/-music', t('life.music')); break
    case 'health': openApp('/-health', t('life.health')); break
    case 'voice': openApp('/-voice', t('life.voice')); break
    case 'download': openApp('/-download', t('life.download')); break
    case 'camera': openApp('/-camera', t('life.camera')); break
    case 'remote': openApp('/-remote', t('life.remote')); break
    case 'users': openApp('/-users', t('life.users')); break
  }
}

// 扫码：单独一行（不在宫格里占位）。弹快传二维码，手机扫码进 NAS。
function onScan() {
  if (!ensureRunning()) return
  if (!quickUser.value) {
    ElMessage.warning(t('life.startFirst'))
    return
  }
  quickOpen.value = true
}

// 存储统计：盘容量秒回，NAS 占用是后端后台 walk 出来的，第一次拿到 -1（还在算）。
// 这里补几轮轻量轮询把「NAS 占用」填上，最多 8 次（≈12 秒），别一直打后端。
let statsTimer: ReturnType<typeof setTimeout> | null = null
async function refreshStats(tries = 0) {
  if (!inst.value) return
  try {
    stats.value = await GetStorageStats(inst.value.id)
  } catch {
    if (tries === 0) stats.value = null
    return
  }
  if ((stats.value?.used ?? -1) < 0 && tries < 8) {
    statsTimer = setTimeout(() => { void refreshStats(tries + 1) }, 1500)
  }
}

async function refresh() {
  try {
    const list = await ListInstances()
    const mine = (list as any[]).filter((i: any) => i.plugin === 'webdav')
    inst.value = mine[0] || null
    if (!inst.value) return
    try { conn.value = await GetWebDavConnInfo(inst.value.id) } catch { conn.value = null }
    pickDefaultAddr()
    void refreshStats()
    try {
      const shares = await ListWebDavShares(inst.value.id)
      const first = (shares as any[]).find((s: any) => s.enabled)
      quickUser.value = first?.user || ''
    } catch { quickUser.value = '' }
  } catch { /* 启动早期服务未就绪：保持空态 */ }
}

async function createInst() {
  if (busy.value) return
  busy.value = true
  try {
    const plugins = await ListPlugins()
    const p = (plugins as any[]).find((x: any) => x.id === 'webdav')
    await CreateInstance('webdav', p?.defaultVersion || '', '', [])
    await refresh()
  } catch (e: any) {
    ElMessage.error(String(e?.message || e))
  } finally {
    busy.value = false
  }
}

async function toggleRun() {
  if (!inst.value || busy.value) return
  busy.value = true
  try {
    if (running.value) await StopInstance(inst.value.id)
    else await StartInstance(inst.value.id)
    await refresh()
  } catch (e: any) {
    ElMessage.error(String(e?.message || e))
  } finally {
    busy.value = false
  }
}

onMounted(() => {
  document.addEventListener('click', onDocClick)
  refresh()
})
onBeforeUnmount(() => {
  document.removeEventListener('click', onDocClick)
  if (statsTimer) clearTimeout(statsTimer)
})
</script>

<style scoped>
/* 普通模式窗口是竖版窄窗（宽 330 锁死，见 app.go 的 LifeWindowWidth），
   所以这里按「窄窗优先」排布：存储环居中打头、操作两列、宫格四列。 */
.life-home {
  flex: 1;
  min-width: 0;
  /* 纵向 flex：宫格之后的空间由扫码行吃掉（margin-top:auto），窗口拉高时
     不会留一大块空白；高度不够时仍是整列可滚（极端小屏的兜底）。 */
  display: flex;
  flex-direction: column;
  overflow-y: auto;
  /* 不露滚动条（用户定稿）：780 高度按实测排得下，溢出只是兜底 ——
     仍可用滚轮滚，但看不见条，免得右侧多一根竖线破坏竖窗观感 */
  scrollbar-width: none;
  padding: 0.75rem 1rem 1rem;
}
.life-home::-webkit-scrollbar { width: 0; height: 0; }
.life-empty {
  margin-top: 3rem;
  text-align: center;
  color: var(--text-muted);
  font-size: 0.8125rem;
}
.life-empty .life-btn { margin-top: 0.75rem; }
.life-hero {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 0.875rem;
  margin-top: 0.25rem;
}
.life-ring {
  /* NAS 弧 / 色点专用色：中性灰。刻意不用品红系 —— 已用弧是品牌玫红，
     两条同色系的弧贴在一起会糊成一条；灰在深浅两套主题上都稳（浅色底
     是暖白 --bg-surface，深色底是近黑），不需要按主题换。 */
  --nas-arc: #8a8f96;
  display: flex;
  flex-direction: column;
  align-items: center;
}
.life-ring svg {
  width: 148px;
  height: 148px;
}
.life-ring-k { fill: var(--text-muted); font-size: 8px; }
.life-ring-v { fill: var(--text-primary); font-size: 16.5px; font-weight: 600; }
.life-ring-s { fill: var(--text-muted); font-size: 7.5px; }
/* 三行图例：已用 / 可用 / NAS 占用。窄窗放不下就换行，不缩字号（11px 已是最小舒适值） */
.life-legend {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 0.2rem 0.7rem;
  margin-top: 0.55rem;
  font-size: 0.6875rem;
  color: var(--text-muted);
  white-space: nowrap;
}
.life-legend .dot {
  display: inline-block;
  width: 0.4375rem;
  height: 0.4375rem;
  border-radius: 0.125rem;
  margin-right: 0.25rem;
}
.life-legend .used { background: var(--accent); }
.life-legend .free {
  background: var(--bg-surface);
  box-shadow: inset 0 0 0 1px var(--border-subtle);
}
.life-legend .nas { background: var(--nas-arc); }
.life-side {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.life-url {
  position: relative;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.375rem;
  background: var(--bg-elevated);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  padding: 0.45rem 0.5rem;
}
/* 下拉触发器：方式名 + 当前网址两行（用户要求「默认选中的把网址也显示出来」），
   网址过长一律省略号，完整值在 title 里。整行比原来高一档，窄窗也不挤。 */
.life-url-pick {
  flex: 1;
  min-width: 0;
  min-height: 2.05rem;
  display: inline-flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.25rem;
  padding: 0.15rem 0.375rem;
  border: none;
  border-radius: 0.3125rem;
  background: transparent;
  color: var(--text-primary);
  font-size: 0.6875rem;
  text-align: left;
  cursor: pointer;
}
/* 两行文字块：本身要吃满剩余宽度，才能把省略号算对（flex 子项默认 min-width:auto） */
.life-url-cur {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 0.05rem;
}
.life-url-pick:hover { background: var(--bg-surface); }
.life-url-pick.open { background: var(--bg-surface); }
.life-url-kind { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.life-url-sub {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-muted);
  /* 与展开项、与 IPv6 地址同口径：等宽更好读，冒号和方括号不会挤在一堆 */
  font-family: var(--font-mono);
  font-size: 0.625rem;
  line-height: 1.3;
}
.life-caret { flex: 0 0 10px; color: var(--text-muted); transition: transform 0.15s; }
.life-url-pick.open .life-caret { transform: rotate(180deg); }
/* 面板**在流内**展开（不浮层遮挡）：窄窗 330 里浮层会盖住下面的地址行和四个
   按钮，用户反而看不到自己选的地址。展开时把下方内容往下推，谁也不挡谁。 */
/* 浮层：绝对定位压在地址行下面，不参与流布局——展开时首屏高度不变，
   下面的地址行/按钮被盖住一点无所谓，点空白处即收起（见 onDocClick）。 */
.life-url-pop {
  position: absolute;
  top: calc(100% + 0.25rem);
  left: 0;
  right: 0;
  z-index: 40;
  display: flex;
  flex-direction: column;
  gap: 0.125rem;
  padding: 0.25rem;
  background: var(--bg-elevated);
  border: 1px solid var(--border-color);
  border-radius: var(--radius-sm);
  box-shadow: 0 10px 28px rgb(0 0 0 / 24%);
}
.life-url-item {
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
  padding: 0.3rem 0.375rem;
  border: none;
  border-radius: 0.3125rem;
  background: transparent;
  color: var(--text-primary);
  text-align: left;
  cursor: pointer;
}
.life-url-item:hover { background: var(--bg-hover); }
.life-url-item.on { background: color-mix(in srgb, var(--accent) 14%, transparent); }
.life-url-item.off { cursor: default; }
/* 置灰做在子元素上，别用整块 opacity —— 否则里面的「开启」按钮也跟着变淡 */
.life-url-item.off > .life-url-item-hd > b,
.life-url-item.off > .life-url-flag,
.life-url-item.off > .life-url-item-url { opacity: 0.55; }
.life-url-item-hd {
  display: flex;
  align-items: center;
  gap: 0.375rem;
  min-width: 0;
}
.life-url-item-hd b { font-size: 0.6875rem; font-weight: 600; flex: 0 0 auto; }
.life-url-flag {
  flex: 1 1 auto;
  font-size: 0.5625rem;
  font-style: normal;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}
/* 不可用但改设置就能通时的小按钮（监听地址改 0.0.0.0 + 自签 HTTPS） */
.life-url-fix {
  flex: 0 0 auto;
  margin-left: auto;
  padding: 0.0625rem 0.4375rem;
  border: 1px solid var(--accent);
  border-radius: 999px;
  background: transparent;
  color: var(--accent);
  font-size: 0.5625rem;
  line-height: 1.5;
  cursor: pointer;
}
.life-url-fix:hover { background: color-mix(in srgb, var(--accent) 14%, transparent); }
.life-url-fix:disabled { opacity: 0.5; cursor: default; }
.life-url-item-url {
  font-family: var(--font-mono);
  font-size: 0.625rem;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.life-icon {
  width: 1.75rem;
  height: 1.75rem;
  flex: 0 0 1.75rem;
  border-radius: 0.375rem;
  border: none;
  background: var(--bg-surface);
  color: var(--text-secondary);
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}
.life-icon:hover { color: var(--text-primary); }
.life-icon.accent { background: var(--accent); color: var(--on-accent); }
.life-icon:disabled { opacity: 0.4; cursor: not-allowed; }
/* 四个操作两列排，窄窗里不会把按钮压成「复制…」这种省略号 */
.life-acts {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0.375rem;
}
.life-btn {
  height: 1.75rem;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border-subtle);
  background: var(--bg-elevated);
  color: var(--text-primary);
  font-size: 0.6875rem;
  cursor: pointer;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  padding: 0 0.25rem;
}
/* 四个操作按钮：hover 要有明确反馈（全局光标规则见文件末尾那段）。
   原来只有 border-color 从 subtle→hover 一点点色差，浅色主题下几乎看不出，
   用户反馈「鼠标移上去没变化」——这里补上底色、描边、文字三层变化。 */
.life-btn:hover:not(:disabled) {
  border-color: var(--accent);
  background: color-mix(in srgb, var(--accent) 12%, var(--bg-elevated));
  color: var(--accent);
}
.life-btn:active:not(:disabled) {
  background: color-mix(in srgb, var(--accent) 20%, var(--bg-elevated));
}
.life-btn:focus-visible {
  outline: 2px solid color-mix(in srgb, var(--accent) 55%, transparent);
  outline-offset: 1px;
}
.life-btn.primary {
  background: var(--accent);
  border-color: var(--accent);
  color: var(--on-accent);
  padding: 0 1rem;
}
.life-btn:disabled { opacity: 0.5; cursor: not-allowed; }
/* 宫格分组：我的 NAS / IT 专业工具 —— 小标题 + 四列宫格 */
.life-group { margin-top: 1.05rem; }
.life-group + .life-group { margin-top: 0.9rem; }
.life-group-hd {
  font-size: 0.625rem;
  color: var(--text-muted);
  letter-spacing: 0.06em;
  margin: 0 0 0.4rem 0.15rem;
}
.life-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 0.375rem;
}
/* 宫格与扫码行之间至少留一点缝（扫码行的 margin-top:auto 在底部有富余时
   会吃掉全部剩余空间，没富余时这里兜住间距） */
.life-home .life-group:last-of-type { margin-bottom: 0.9rem; }
/* 扫码：单独一行，不占宫格位；margin-top:auto 让它吃掉底部多余高度（窗口
   拉高时贴底，不留一块空白 —— 同时保证正常高度下不会把内容顶出滚动条） */
.life-scan {
  margin-top: auto;
  padding-top: 0;
  width: 100%;
  height: 2.1rem;
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.4rem;
  border-radius: var(--radius-md);
  border: 1px dashed var(--border-subtle);
  background: var(--bg-elevated);
  color: var(--text-secondary);
  font-size: 0.6875rem;
  cursor: pointer;
}
.life-scan:hover { border-color: var(--accent); color: var(--accent); }
.life-scan .life-ico { display: inline-flex; }
.life-tile {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.3rem;
  padding: 0.625rem 0.125rem 0.5rem;
  border-radius: var(--radius-md);
  border: 1px solid var(--border-subtle);
  background: var(--bg-elevated);
  color: var(--text-primary);
  cursor: pointer;
  font-size: 0.625rem;
  line-height: 1.2;
  text-align: center;
}
.life-tile:hover { border-color: var(--accent); }
.life-tile .life-ico { color: var(--text-secondary); display: inline-flex; }
.life-tile .life-ico :deep(svg) { width: 18px; height: 18px; }
.life-group:first-of-type .life-tile:first-child .life-ico { color: var(--accent); }

/* 小手光标：style.css 里那条「原生风格光标」用
   `button:not(:disabled){cursor:default!important}` 把所有按钮的 pointer
   都压成了箭头（含这里的 .life-btn / .life-tile 自带的那行 cursor:pointer）。
   首屏这些是真·可点控件，用户明确要小手 —— 这里用更特指的选择器 + !important
   压回去；禁用态仍由上面各条 :disabled 给 not-allowed。 */
:is(
  button.life-btn,
  button.life-tile,
  button.life-scan,
  button.life-icon,
  button.life-url-pick,
  button.life-url-item,
  button.life-url-fix
):not(:disabled) {
  cursor: pointer !important;
}
</style>
