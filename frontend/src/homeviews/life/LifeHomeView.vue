<template>
  <!-- 普通模式首页（M21）：面向普通用户的消费级首屏。
       结构借鉴易等 NAS 主界面 —— 存储环 / 地址 + 扫码 / 功能宫格，
       配色走双轨制的品牌玫红（html.ui-home 提供 --accent）。 -->
  <div class="life-home">
    <!-- 品牌区（logo/名称/模式徽标）在标题栏；「切换到专业模式」收进标题栏的
         ☰ 菜单（窄窗里页面顶部再放一行按钮太浪费纵向空间）。 -->
    <!-- 没有网盘实例时只留「创建」+ IT 组（IT 工具不依赖 NAS 服务，照样能进） -->
    <!-- 账号行（M49）：问候从底部搬到存储环上方 —— 补掉标题栏到环之间那段
         空白（对齐易等版式），底部只剩「扫码 + 会员中心」两行。
         整行可点 → 会员中心；未登录不隐藏（隐藏又空一块），显示「未登录 ·
         点击登录」占位，权重靠字号/描边压得比底部实心按钮低一档。 -->
    <!-- 顶部账号行（M49/M53）：胶囊容器里两个入口 —— 主体（头像+问候+箭头，
         整段可点 = 会员中心）+ 右侧齿轮（= 公网/IPv6 设置说明，M53 从地址卡
         挪到这里：地址卡 4 个元素太挤，而「设置」跟身份行并列更清爽）。
         外层必须是 div：HTML 不允许 button 嵌套 button。 -->
    <div class="life-me">
      <button class="life-me-main" :title="meTitle" @click="openMember">
        <span class="life-me-av">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round">
            <circle cx="12" cy="8.4" r="3.6" /><path d="M4.8 20c0-3.5 3.2-6.1 7.2-6.1s7.2 2.6 7.2 6.1" />
          </svg>
        </span>
        <span class="life-me-tx">{{ meText }}</span>
        <svg class="life-me-arrow" width="11" height="11" viewBox="0 0 24 24" fill="none"
          stroke="currentColor" stroke-width="2.4" stroke-linecap="round">
          <path d="M9 6l6 6-6 6" />
        </svg>
      </button>
      <!-- 设置入口 + 下拉装同一个 wrap：hover 展开、鼠标移进面板不会误关
           （M58：齿轮旁边补「设置」二字，纯图标找不到；改为 hover 即展开） -->
      <div class="life-set-wrap" @mouseenter="onSetHover(true)" @mouseleave="onSetHover(false)">
      <button class="life-me-gear" :class="{ on: setOpen }" :aria-label="t('life.setTitle')" @click.stop="onSetHover(true)">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="12" cy="12" r="3" />
          <path d="M19.1 13.7a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V20a2 2 0 1 1-4 0v-.09a1.65 1.65 0 0 0-1.08-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09a1.65 1.65 0 0 0 1.51-1.08 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33h.08a1.65 1.65 0 0 0 1-1.51V4a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82v.08a1.65 1.65 0 0 0 1.51 1H20a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
        </svg>
        <span class="life-me-gear-tx">{{ t('life.setTitle') }}</span>
        <!-- 下三角：跟地址下拉同一个暗示「点得开」，展开时翻上去 -->
        <svg class="life-me-caret" :class="{ on: setOpen }" width="8" height="8" viewBox="0 0 24 24"
          fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round">
          <path d="M6 9l6 6 6-6" />
        </svg>
      </button>

      <!-- 设置下拉（M57，参照易等的分组菜单）：三段九项，带图标+说明+右箭头。
           能真跑的接现有页面（IPv6→公网设置 / 端口绑定→NAS配置 / 改密→NAS 用户 /
           退出→LicenceLogout）；「固定内网地址」「清除缓存」后端还没有能力，
           点了给「即将上线」，不装作能用。 -->
      <div v-if="setOpen" class="life-set" @click.stop>
        <p class="life-set-hd">{{ t('set.connGroup') }}</p>
        <button class="life-set-it" @click="onSet('fixedIp')">
          <span class="life-set-ico" v-html="SET_ICONS.pin"></span>
          <span class="life-set-tx">
            <b>{{ t('set.fixedIp') }}</b>
            <i>{{ t('set.fixedIpHint') }}</i>
          </span>
          <svg class="life-set-ar" width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M9 6l6 6-6 6" /></svg>
        </button>
        <button class="life-set-it" @click="onSet('v6')">
          <span class="life-set-ico" v-html="SET_ICONS.v6"></span>
          <span class="life-set-tx">
            <b>{{ t('set.v6') }}</b>
            <i>{{ t('set.v6Hint') }}</i>
          </span>
          <svg class="life-set-ar" width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M9 6l6 6-6 6" /></svg>
        </button>

        <p class="life-set-hd">{{ t('set.expGroup') }}</p>
        <button class="life-set-it" @click="onSet('port')">
          <span class="life-set-ico" v-html="SET_ICONS.shield"></span>
          <span class="life-set-tx">
            <b>{{ t('set.portBind') }}</b>
            <i>{{ t('set.portBindHint') }}</i>
          </span>
          <svg class="life-set-ar" width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M9 6l6 6-6 6" /></svg>
        </button>

        <p class="life-set-hd">{{ t('set.acctGroup') }}</p>
        <button class="life-set-it" @click="onSet('pwd')">
          <span class="life-set-ico" v-html="SET_ICONS.lock"></span>
          <span class="life-set-tx">
            <b>{{ t('set.pwd') }}</b>
            <i>{{ t('set.pwdHint') }}</i>
          </span>
          <svg class="life-set-ar" width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M9 6l6 6-6 6" /></svg>
        </button>
        <button class="life-set-it" @click="onSet('cache')">
          <span class="life-set-ico" v-html="SET_ICONS.trash"></span>
          <span class="life-set-tx">
            <b>{{ t('set.cache') }}</b>
            <i>{{ t('set.cacheHint') }}</i>
          </span>
          <svg class="life-set-ar" width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M9 6l6 6-6 6" /></svg>
        </button>
        <!-- 未登录时没有「退出」可言，直接不渲染（比点了报错干净） -->
        <button v-if="lic.loggedIn" class="life-set-it danger" @click="onSet('logout')">
          <span class="life-set-ico" v-html="SET_ICONS.out"></span>
          <span class="life-set-tx">
            <b>{{ t('set.logout') }}</b>
            <i>{{ t('set.logoutHint') }}</i>
          </span>
          <svg class="life-set-ar" width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"><path d="M9 6l6 6-6 6" /></svg>
        </button>
      </div>
      </div>
    </div>

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
              <span class="life-url-kind">{{ curAddr ? addrTitle(curAddr) : t('life.addrLocal') }}</span>
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
              v-for="a in addrs" :key="a.id"
              class="life-url-item" :class="{ on: a.id === curID, off: !a.ok }"
              @click="chooseAddr(a)"
            >
              <span class="life-url-item-hd">
                <b>{{ addrTitle(a) }}</b>
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
          <button class="life-btn" :disabled="busy" @click="toggleRun">{{ running ? t('life.stop') : t('life.start') }}</button>
          <button class="life-btn" @click="openLocalDir">{{ t('life.nasDir') }}</button>
          <button class="life-btn" @click="openManage">{{ t('life.manage') }}</button>
          <button class="life-btn" @click="copyUrl">{{ t('life.copy') }}</button>
        </div>
      </div>
    </div>

    <!-- 宫格两组：我的 NAS（个人空间各模块 + 用户管理）/ IT 专业工具（重点几项）。
         NAS 未启动时宫格与扫码一律置灰不可点（服务没起，进去只有登录页），
         启动按钮本身当然要保持可点。 -->
    <div v-if="inst" class="life-group">
      <div class="life-group-hd">
        <span>{{ t('life.myNas') }}</span>
        <!-- 同 IT 组：组头「打开 ›」只开 NAS 根页面（文件管理器），宫格各 tile 仍是一步直达 -->
        <button class="life-grp-open" :title="t('life.myNas')" @click="openFiles">
          {{ t('common.open') }}
          <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round">
            <path d="M9 6l6 6-6 6" />
          </svg>
        </button>
      </div>
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
      <div class="life-group-hd">
        <span>{{ t('life.grpIt') }}</span>
        <!-- 打开 IT 工具窗本体（原顶栏按钮挪到这里）：宫格四项是「亮窗+直达页面」，
             这个是只亮窗不导航 —— 语义归组，顶栏腾地方 -->
        <button class="life-grp-open" :title="t('life.itTools')" @click="showIT">
          {{ t('common.open') }}
          <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round">
            <path d="M9 6l6 6-6 6" />
          </svg>
        </button>
      </div>
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

    <!-- 底部两行：上「扫码自动登进NAS」（弱化虚线）、下「会员中心」实心按钮
         （M50 恢复：顶部胶囊 hover 提示不够显性，「会员」必须常显在界面上，
         用户才找得到买/续入口）。已登录给「续费/升级」，未登录给「登录」。
         扫码必须服务在跑，保持 v-if。点击都开独立窗口，不跳页面。 -->
    <div class="life-foot">
      <button v-if="inst" class="life-scan" @click="onScan">
        <span class="life-ico" v-html="ICONS.qr"></span>
        <span>{{ t('life.scanNas') }}</span>
      </button>
      <button class="life-member" :title="memberBtnText" @click="openMember">
        <span class="life-ico" v-html="ICONS.member"></span>
        <span>{{ memberBtnText }}</span>
      </button>
    </div>

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
import { Browser, Events } from '@wailsio/runtime'
import { OpenAppWindow, OpenManageWindow, OpenITTool, OpenMembershipWindow, OpenV6Window, OpenFixIpWindow, LicenceState, LicenceLogout, OpenPathInExplorer, ShowITWindow, ClearAppCache } from '../../../bindings/github.com/duihao/duihao/app'
import { ElMessage, ElMessageBox } from 'element-plus'
import WebDavManageDialog from '../../components/WebDavManageDialog.vue'
import DavQuickDialog from '../../components/DavQuickDialog.vue'
import UrlQrDialog from '../../components/UrlQrDialog.vue'
import {
  ListInstances, CreateInstance, StartInstance, StopInstance, ListPlugins
} from '../../../bindings/github.com/duihao/duihao/backend/duihao/service/plugincenterservice'
import {
  GetWebDavConnInfo, GetStorageStats, ListWebDavShares, EnableLanAccess, NASEntryURL
} from '../../../bindings/github.com/duihao/duihao/backend/duihao/service/webdavservice'

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
interface AddrView { id: string; kind: string; label: string; url: string; ok: boolean; note?: string; fix?: string }
const ADDR_KEY = 'life.addrKind'
const addrs = computed<AddrView[]>(() => ((conn.value?.addrs || []) as AddrView[]))
const curID = ref('')
const pickOpen = ref(false)
const fixing = ref(false)
const urlBox = ref<HTMLElement | null>(null)

// 当前选中项：选过的还在就用它，否则退回第一个可用（局域网优先）。
// 用 id 而不是 kind —— 一台机器常有多条公网 IPv6（宽带动态段、临时隐私地址、
// 二级路由下发的段），光靠 kind='v6' 永远只会落到第一条。
const curAddr = computed<AddrView | null>(() => {
  const list = addrs.value
  if (!list.length) return null
  return list.find(a => a.id === curID.value) || list.find(a => a.ok) || list[0]
})

function kindLabel(kind: string): string {
  if (kind === 'lan') return t('life.addrLan')
  if (kind === 'v6') return t('life.addrV6')
  return t('life.addrLocal')
}

// 下拉里多条 IPv6 要能分清：按 IPv6 出现顺序加序号（数字不带语言，不用翻译）。
function addrTitle(a: AddrView): string {
  const base = kindLabel(a.kind)
  if (a.kind !== 'v6') return base
  const v6s = addrs.value.filter(x => x.kind === 'v6')
  if (v6s.length < 2) return base
  return `${base} ${v6s.findIndex(x => x.id === a.id) + 1}`
}

// 默认落「局域网」（手机扫码最常用），它不可用才退本机；用户手选过就记住。
function pickDefaultAddr() {
  const list = addrs.value
  if (!list.length) return
  if (list.some(a => a.id === curID.value && a.ok)) return
  let kept = ''
  try { kept = localStorage.getItem(ADDR_KEY) || '' } catch { /* 隐私模式 */ }
  const pick =
    list.find(a => a.id === kept && a.ok) ||
    list.find(a => a.kind === 'lan' && a.ok) ||
    list.find(a => a.ok) || list[0]
  curID.value = pick.id
}

function chooseAddr(a: AddrView) {
  if (!a.ok) {
    // 不可用但「改设置就能通」→ 直接问一句要不要开，省得去设置里翻。
    if (a.fix) enableLan()
    return
  }
  pickOpen.value = false
  curID.value = a.id
  try { localStorage.setItem(ADDR_KEY, a.id) } catch { /* 隐私模式 */ }
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
  if (pickOpen.value) {
    if (!urlBox.value || !urlBox.value.contains(e.target as Node)) pickOpen.value = false
  }
  // 设置下拉：点面板以外任何地方收起。面板内部自带 @click.stop
  if (setOpen.value && !(e.target as HTMLElement)?.closest?.('.life-me')) setOpen.value = false
}

// ---- 设置下拉（齿轮）----
const setOpen = ref(false)

// hover 展开（M58）：移入即开，移出延 180ms 关 —— 按钮和面板之间留着 0.3rem 空隙，
// 立刻关会让鼠标斜着划进面板时闪一下。
let setLeaveTimer: number | undefined
function onSetHover(v: boolean) {
  if (setLeaveTimer) { clearTimeout(setLeaveTimer); setLeaveTimer = undefined }
  if (v) setOpen.value = true
  else setLeaveTimer = window.setTimeout(() => { setOpen.value = false }, 180)
}
const SET_ICONS = {
  pin: '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="4" width="7" height="7" rx="1.4"/><rect x="13.5" y="4" width="6.5" height="6.5" rx="1.4"/><rect x="4" y="14" width="6.5" height="6.5" rx="1.4"/><path d="M14 14h3v3h-3zM18.5 18.5h2"/></svg>',
  v6: '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><circle cx="12" cy="12" r="8.6"/><path d="M3.4 12h17.2M12 3.4c2.6 2.4 4 5.3 4 8.6s-1.4 6.2-4 8.6c-2.6-2.4-4-5.3-4-8.6s1.4-6.2 4-8.6z"/></svg>',
  shield: '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linejoin="round"><path d="M12 3.4 19 6v6c0 4-3 7.1-7 8.6C8 19.1 5 16 5 12V6z"/><path d="M9.2 12.1l2 2 3.6-4"/></svg>',
  lock: '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"><rect x="4.6" y="10.4" width="14.8" height="9.6" rx="2.2"/><path d="M8.2 10.4V8a3.8 3.8 0 0 1 7.6 0v2.4"/></svg>',
  trash: '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"><path d="M4.5 7h15M9.5 7V5.2h5V7M6.6 7l.9 12.2h9l.9-12.2"/><path d="M10.4 11v5M13.6 11v5"/></svg>',
  out: '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M14.5 4.6H6.2v14.8h8.3"/><path d="M12.2 12h8.2M17.6 9.2l2.8 2.8-2.8 2.8"/></svg>'
}

// 设置项落地：现在九项里有八项能真跑（只有「清除缓存」在不同平台行为不同，
// Windows 下清 WebView 缓存 + 临时目录，不动网盘数据与登录态）。
// 「固定内网地址」开独立窗（520×640）：网卡配置六行 + 三段说明，330 宽的
// NAS 主窗塞不下；顺手把当前访问地址写进 localStorage，多网卡时后端靠它
// 认出「NAS 走的是哪块网卡」。
function onSet(k: string) {
  setOpen.value = false
  switch (k) {
    // IPv6：独立弹窗（检测 → 激活 → 地址/域名/解绑），不再跳网盘网页的 /-remote
    case 'v6': void OpenV6Window(); break
    case 'fixedIp': {
      try { localStorage.setItem('dh.fixip.hint', curAddr.value?.url || '') } catch { /* 隐私模式 */ }
      void OpenFixIpWindow()
      break
    }
    // 端口绑定：实例端口/RUL 都在 NAS配置窗（连接信息卡），先开到那儿
    case 'port': void openManage(); break
    case 'pwd': openApp('/-users', t('life.users')); break
    case 'cache': void doClearCache(); break
    case 'logout': void doLogout(); break
    default: ElMessage.info(t('common.soon'))
  }
}

// 清除缓存：只清网页缓存与临时文件，网盘文件 / 站点 / 登录状态一概不动。
function mb(n: number): string {
  if (!n) return '0 MB'
  const m = n / (1024 * 1024)
  return m >= 1024 ? `${(m / 1024).toFixed(2)} GB` : m >= 10 ? `${Math.round(m)} MB` : `${m.toFixed(1)} MB`
}

async function doClearCache() {
  try {
    await ElMessageBox.confirm(t('set.cacheAsk'), t('set.cache'), {
      confirmButtonText: t('set.cache'),
      cancelButtonText: t('common.cancel')
    })
  } catch { return }
  try {
    const r = await ClearAppCache()
    const freed = Number(r?.freed || 0)
    const locked = Number(r?.locked || 0)
    if (freed > 0) ElMessage.success(t('set.cacheDone', { size: mb(freed) }))
    else ElMessage.info(t('set.cacheNone'))
    if (locked > 0) ElMessage.warning(t('set.cacheLocked', { size: mb(locked) }))
  } catch (e: any) {
    ElMessage.error(t('set.cacheFail', { msg: String(e?.message || e) }))
  }
}

async function doLogout() {
  try {
    await ElMessageBox.confirm(t('set.logoutAsk'), t('set.logout'), {
      confirmButtonText: t('set.logout'),
      cancelButtonText: t('common.cancel'),
      type: 'warning'
    })
  } catch { return }
  try {
    await LicenceLogout()
    await refreshLic()
    ElMessage.success(t('set.loggedOut'))
  } catch (e: any) {
    ElMessage.error(String(e?.message || e))
  }
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
  // 「新建连接」= 圆 + 加号。原来圆里加号、右下角再挂一个叉，21px 下两道
  // 描边挤成一团，看着像「一堆小叉」——去掉那个叉，只留最清楚的加号。
  connNew: '<svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"><circle cx="12" cy="12" r="8.5"/><path d="M12 8.2v7.6M8.2 12h7.6"/></svg>',
  connList: '<svg width="21" height="21" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"><rect x="3" y="4.5" width="18" height="6" rx="2"/><rect x="3" y="13.5" width="18" height="6" rx="2"/><path d="M6.5 7.5h.01M6.5 16.5h.01"/></svg>',
  qr: '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><rect x="3.5" y="3.5" width="6.5" height="6.5" rx="1"/><rect x="14" y="3.5" width="6.5" height="6.5" rx="1"/><rect x="3.5" y="14" width="6.5" height="6.5" rx="1"/><path d="M14 14h2.5v2.5H14zM19 14h1.5M14 19v1.5M18.5 19H21M21 16.5V19"/></svg>',
  // 底部「会员中心」：皇冠（权益/等级语义，比星形更贴会员）
  member: '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M4 18.5h16"/><path d="M4.2 15.6 2.8 7.2l4.6 3.1L12 5l4.6 5.3 4.6-3.1-1.4 8.4z"/></svg>',
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

// 只亮起 IT 工具窗、不导航（组标题行的「打开 ›」，原顶栏「IT工具」按钮）。
function showIT() {
  ShowITWindow().catch(() => {})
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
// NAS目录：直接在资源管理器里打开网盘的**实际本地目录**（用户定稿）——
// 不是网页文件管理器（那是组头「打开 ›」/宫格「网盘」的事）。
// 路径取 GetStorageStats 的 root（= 当前启用共享账号的根目录，随共享配置变），
// 拿不到才提示；不需要服务在跑，看磁盘本来就跟 NAS 进程无关。
async function openLocalDir() {
  let dir = stats.value?.root || ''
  if (!dir && inst.value) {
    try {
      const s = await GetStorageStats(inst.value.id)
      stats.value = s
      dir = s?.root || ''
    } catch { /* 下面统一提示 */ }
  }
  if (!dir) {
    ElMessage.warning(t('life.noDir'))
    return
  }
  try {
    await OpenPathInExplorer(dir)
  } catch (e: any) {
    ElMessage.error(String(e?.message || e))
  }
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

// 会员中心：开独立窗口（member_window.go，880×640）。不在这里套 el-dialog
// —— NAS 主窗只有 330 宽，套餐卡 + 二维码在竖窗里塞成一列很难看。
async function openMember() {
  try {
    await OpenMembershipWindow()
  } catch (e: any) {
    ElMessage.error(String(e?.message || e))
  }
}

// 会员问候（顶部账号行）：登录后显示「您好！手机号（档位）· 到期」，
// 未登录退回「未登录 · 点击登录」占位（行不能消失，否则顶部又空一块）。
// 数据源与会员中心窗一致（LicenceState + licence:state 广播），
// 会员中心窗里登录/购买后本窗问候行同步刷新。
type LicShape = { loggedIn: boolean; plan: string; ent: number; mobile?: string; nickname?: string; sub?: string }
const lic = ref<LicShape>({ loggedIn: false, plan: 'free', ent: 0 })

async function refreshLic() {
  try {
    const s = (await LicenceState()) as unknown as LicShape
    if (s && typeof s === 'object') lic.value = s
  } catch {
    /* 未登录/接口异常都不打扰主界面 */
  }
}

// 回到前台补一次授权状态（最小化/隐藏期间漏掉的事件靠这个追平）
let licTimer: ReturnType<typeof setInterval> | null = null
function onLicVisible() {
  if (document.visibilityState === 'visible') void refreshLic()
}

// 到期文案单独算：顶部胶囊只放「您好！手机号（档位）」，到期时间收进悬浮提示
// （用户定稿：问候行别塞到期，「到…」被省略号切半截更难看；想看全文悬停即可）。
const licEnt = computed(() => {
  if (!lic.value.loggedIn) return ''
  if (lic.value.plan === 'free') {
    return '' // 与会员中心窗 entLabel 同口径：免费不给到期文案
  }
  if (lic.value.plan === 'lifetime' || !lic.value.ent) {
    return t('licence.expireNever')
  }
  try {
    const dt = new Date(lic.value.ent * 1000)
    // 悬浮里给完整日期（年月日），不再按同年缩成 MM/DD —— 空间不受限
    const d = dt.toLocaleDateString(undefined, { year: 'numeric', month: '2-digit', day: '2-digit' })
    return t('licence.expireAt', { date: d })
  } catch {
    return String(lic.value.ent)
  }
})

const licGreet = computed(() => {
  if (!lic.value.loggedIn) return ''
  const who = lic.value.mobile || lic.value.nickname || lic.value.sub || '—'
  const plan = lic.value.plan === 'trial' ? t('licence.planTrial')
    : lic.value.plan === 'annual' ? t('licence.planAnnual')
    : lic.value.plan === 'lifetime' ? t('licence.planLifetime')
    : t('licence.planFree')
  return t('member.hello', { mobile: who, plan })
})

// 顶部账号行文案：已登录给问候，未登录给「未登录 · 点击登录」占位
const meText = computed(() => licGreet.value || t('member.notLoggedIn'))

// 悬浮提示 = 问候 + 到期/永久 + 「点击进入会员中心」（与底部按钮双入口，
// 悬停处也说明这行点得进去）。空段自动丢掉，免费用户不会留空行。
const meTitle = computed(() =>
  [meText.value, licEnt.value, t('member.openCenterHint')].filter(Boolean).join('\n'),
)

// 会员中心按钮：已登录点明「要续费/升级」，未登录点明「要登录」
const memberBtnText = computed(() =>
  lic.value.loggedIn ? t('member.centerRenew') : t('member.centerLogin'),
)

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
  void refreshLic()
  // 登录/购买/续签都由后端广播 licence:state（App.vue、会员中心窗同款跟随模式）
  Events.On('licence:state', (ev: unknown) => {
    const d = ev as LicShape | { data?: LicShape }
    const s = (d && typeof d === 'object' && 'loggedIn' in d) ? (d as LicShape) : (d as { data?: LicShape })?.data
    if (s && typeof s === 'object') lic.value = s
  })
  // NAS配置窗里改端口后由后端广播：首屏地址卡/扫码链接立即跟着换端口。
  Events.On('webdav:conn:changed', () => { void refresh() })
  // 兜底同步（M57）：事件只覆盖本进程内发起的登录/退出/到账。窗口被隐藏过、
  // webview 还没挂上监听、或状态在别处（网页端、会员中心窗的老版本路径）变了，
  // 都会漏事件 —— 所以回到前台立刻拉一次，平时 60s 自愈一次。LicenceState 是
  // 内存快照，不打后端，成本可以忽略。
  document.addEventListener('visibilitychange', onLicVisible)
  licTimer = setInterval(() => { void refreshLic() }, 60000)
})
onBeforeUnmount(() => {
  document.removeEventListener('click', onDocClick)
  document.removeEventListener('visibilitychange', onLicVisible)
  if (statsTimer) clearTimeout(statsTimer)
  if (licTimer) clearInterval(licTimer)
  if (setLeaveTimer) clearTimeout(setLeaveTimer)
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
  padding: 0.625rem 1rem 0.875rem;
}
.life-home::-webkit-scrollbar { width: 0; height: 0; }
.life-empty {
  /* 顶部已有账号行，空状态不用再顶出 3rem 的大留白 */
  margin-top: 1.75rem;
  text-align: center;
  color: var(--text-muted);
  font-size: 0.8125rem;
}
.life-empty .life-btn { margin-top: 0.75rem; }
.life-hero {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 0.75rem;
  /* 上方已有 .life-me（自带 0.75rem 下边距），这里不再叠一层顶部间距 */
  margin-top: 0;
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
  margin-top: 0.45rem;
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
  /* 一行四个（用户定稿）：我的NAS / 复制链接 / NAS配置 / 启动NAS —— 文案都是
     3~4 字，298px 内容宽里每个约 70px 放得下；溢出兜底仍是省略号 */
  grid-template-columns: repeat(4, minmax(0, 1fr));
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
/* 宫格分组：我的 NAS / IT 专业工具 —— 小标题 + 四列宫格。
   纵向节奏在 M51 收紧过一档：顶部多了账号胶囊、底部两行间距也加大了，
   780 高的窗口必须从宫格这里省回来，否则底部行会被挤出可视区。 */
.life-group { margin-top: 0.9rem; }
.life-group + .life-group { margin-top: 0.8rem; }
.life-group-hd {
  display: flex;
  align-items: center;
  font-size: 0.625rem;
  color: var(--text-muted);
  letter-spacing: 0.06em;
  margin: 0 0 0.35rem 0.15rem;
}
/* 组头右侧「打开 ›」：IT 工具窗本体入口（原顶栏按钮挪来），品牌色常显强调、hover 减淡 */
.life-grp-open {
  display: inline-flex;
  align-items: center;
  gap: 0.15rem;
  margin-left: auto;
  margin-right: 0.15rem;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--accent);
  font-weight: 600;
  font-size: 0.625rem;
  letter-spacing: 0;
  /* 显式小手：全局 `button:not(:disabled){cursor:default!important}` 是原生
     风格约定（见 style.css），这里要压过它 —— 同样 !important 时比特指度：
     本规则编译后是 .life-grp-open[data-v-x]（0,2,0），胜过 button:not(:disabled)
     的（0,1,1）。别改成裸 `button` 选择器，那样会输。 */
  cursor: pointer !important;
}
.life-grp-open:hover {
  opacity: 0.78;
}
.life-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 0.375rem;
}
/* 宫格与扫码行之间至少留一点缝（扫码行的 margin-top:auto 在底部有富余时
   会吃掉全部剩余空间，没富余时这里兜住间距） */
.life-home .life-group:last-of-type { margin-bottom: 0.7rem; }
/* 底部两行：上扫码（虚线弱化）、下会员中心（品牌实心）。
   margin-top:auto 挂在容器上（原来挂在扫码按钮上）：窗口拉高时整块贴底，
   正常高度下不额外占高。 */
.life-foot {
  margin-top: auto;
  display: flex;
  flex-direction: column;
  /* 两行按钮间距（M51 定 0.875rem；M60 用户要求扫码按钮再往上提一点 →
     1.35rem）。会员按钮由 margin-top:auto 压底，所以加大 gap 只会把上面那行
     往上推，底部留白保持不变。 */
  gap: 1.35rem;
  flex: 0 0 auto;
}
.life-foot > button {
  width: 100%;
  height: 2.1rem;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.3rem;
  padding: 0 0.4rem;
  border-radius: var(--radius-md);
  font-family: inherit;
  font-size: 0.6875rem;
  white-space: nowrap;
  cursor: pointer;
}
.life-scan {
  border: 1px dashed var(--border-subtle);
  background: var(--bg-elevated);
  color: var(--text-secondary);
}
.life-scan:hover { border-color: var(--accent); color: var(--accent); }
/* 会员中心：品牌实心，作为销售入口比扫码更「亮」一档（M50 恢复）。
   顶部胶囊与它双入口分工：胶囊 = 身份/悬浮说明，这里 = 常显主行动。 */
.life-member {
  border: 1px solid var(--accent);
  background: var(--accent);
  color: var(--on-accent, #fff);
}
.life-member:hover { filter: brightness(1.08); }
/* 顶部账号行（M49/M53）：胶囊容器 = 主体按钮（头像+问候+箭头，点会员中心）
   + 齿轮按钮（点公网设置）。容器只管外观，点击都在内部两个 button 上 ——
   HTML 不允许 button 套 button，所以外层从 <button> 改成了 <div>。
   溢出走省略号，全文在 title 悬浮里。 */
.life-me {
  position: relative;
  display: flex;
  align-items: stretch;
  gap: 0.15rem;
  width: 100%;
  min-width: 0;
  flex: 0 0 auto;
  margin: 0 0 0.625rem;
  padding: 0.25rem 0.4rem 0.25rem 0.25rem;
  border: 1px solid var(--border-subtle);
  border-radius: 999px;
  background: var(--bg-elevated);
  color: var(--text-secondary);
  font-size: 0.6875rem;
  line-height: 1.3;
}
.life-me:hover {
  border-color: var(--accent);
  color: var(--text-primary);
}
/* 主体入口：吃掉齿轮以外的全部宽度，透明无边框（外观由容器提供） */
.life-me-main {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 0.45rem;
  padding: 0;
  border: 0;
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
}
/* 齿轮 + 下拉的定位锚（M58）：面板贴着按钮往左下展开，鼠标从按钮划到面板
   全程都在 wrap 内，不会触发外层的 mouseleave */
.life-set-wrap {
  position: relative;
  flex: 0 0 auto;
  display: flex;
  align-items: center;
}
/* 齿轮：胶囊右侧的「⚙ 设置」小标签，hover 展开下拉 */
.life-me-gear {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  gap: 0.2rem;
  align-self: center;
  height: 1.5rem;
  padding: 0 0.4rem;
  border: 0;
  border-radius: 999px;
  background: transparent;
  color: var(--text-muted);
  font: inherit;
  font-size: 0.5625rem;
  letter-spacing: 0.02em;
  white-space: nowrap;
  cursor: pointer;
}
.life-me-gear:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}
/* 齿轮打开状态：跟 hover 同款，再加品牌色图标，一眼看出菜单是这个键开的 */
.life-me-gear.on {
  background: var(--bg-hover);
  color: var(--accent);
}
/* 「设置」二字：跟图标同色系，hover 时随按钮一起变亮 */
.life-me-gear-tx {
  line-height: 1;
  cursor: pointer;
}
/* 下三角：暗示这里点得开下拉，展开时翻成上三角（跟地址行那颗同一个手势） */
.life-me-caret {
  flex: 0 0 auto;
  color: var(--text-muted);
  transition: transform 0.15s;
}
.life-me-gear.on .life-me-caret { transform: rotate(180deg); color: var(--accent); }
/* 设置下拉面板（M57）：贴胶囊下方右对齐，三段分组 + 图标/说明/箭头。
   胶囊在 330 宽窗口里几乎占满，所以面板宽度单独定，不受父级 flex 影响。 */
.life-set {
  position: absolute;
  /* 锚点改成 .life-set-wrap（= 按钮本身），所以多让 0.15rem 才落回胶囊下沿外侧 */
  top: calc(100% + 0.45rem);
  right: -0.1rem;
  z-index: 60;
  width: 15.5rem;
  max-width: calc(100vw - 1.5rem);
  padding: 0.35rem;
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  box-shadow: 0 14px 34px rgba(0, 0, 0, 0.34);
  color: var(--text-primary);
  cursor: default;
}
.life-set-hd {
  margin: 0.4rem 0.25rem 0.2rem;
  font-size: 0.5625rem;
  letter-spacing: 0.08em;
  color: var(--text-muted);
}
.life-set-hd:first-child { margin-top: 0.15rem; }
.life-set-it {
  display: flex;
  align-items: center;
  gap: 0.45rem;
  width: 100%;
  padding: 0.3rem;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: inherit;
  text-align: left;
  cursor: pointer !important;
}
.life-set-it:hover { background: var(--bg-hover); }
.life-set-ico { flex: 0 0 auto; display: inline-flex; color: var(--accent); }
.life-set-tx {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 0.05rem;
}
.life-set-tx b {
  font-size: 0.6875rem;
  font-weight: 500;
  color: var(--text-primary);
}
.life-set-tx i {
  font-style: normal;
  font-size: 0.5625rem;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.life-set-ar { flex: 0 0 auto; color: var(--text-muted); }
/* 退出用危险色（红）：唯一不可逆的一项，不能跟上面几项长得一样 */
.life-set-it.danger,
.life-set-it.danger .life-set-ico { color: var(--error); }
.life-me-av {
  flex: 0 0 1.375rem;
  width: 1.375rem;
  height: 1.375rem;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  /* 头像底走玫红 16% 混色：深色主题里是暗玫红，浅色主题里是淡粉，都不用另配 */
  background: color-mix(in srgb, var(--accent) 16%, var(--bg-surface));
  color: var(--accent);
}
.life-me-tx {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.life-me-arrow {
  flex: 0 0 auto;
  opacity: 0.45;
}
.life-foot .life-ico { display: inline-flex; }
.life-tile {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.3rem;
  padding: 0.55rem 0.125rem 0.45rem;
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
  button.life-member,
  button.life-me,
  button.life-icon,
  button.life-url-pick,
  button.life-url-item,
  button.life-url-fix
):not(:disabled) {
  cursor: pointer !important;
}
</style>
