<template>
  <!-- IPv6 直连独立窗（/?w=v6）
       参照易等的「激活向导」版式做，但每一步都是对号真实探测的结果：
       ①本机 GUA 地址 ②IPv6 出口 ③网盘 dav 端口 ④写入公网总开关。
       未开启 -> 一句说明 + 一个大按钮；开着 -> 左「访问地址」右「动态域名」。
       动态域名是完整的一套：域名 / 解析方式 / 密钥 / 间隔 / 立即更新 / 上一轮结果。
       只填域名不选解析方式 = 手动解析，右栏直接把该填的 AAAA 摆出来自己加。
       窗口本身由 v6_window.go 创建（默认 640×480 无边框，内容超高时内容区滚动）。 -->
  <div class="v6-root">
    <header class="v6-bar" @mousedown="onDragDown">
      <span class="v6-brand">
        <img class="brand-mark" src="/favicon.png" alt="对号" draggable="false" />
        <b>{{ t('v6.title') }}</b>
      </span>
      <span class="v6-drag"></span>
      <button class="v6-win" :title="t('window.minimize')" @click="minimise">
        <svg width="10" height="10" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path d="M5 12h14" /></svg>
      </button>
      <button class="v6-win close" :title="t('common.close')" @click="close">
        <svg width="10" height="10" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path d="M6 6l12 12M18 6L6 18" /></svg>
      </button>
    </header>

    <main class="v6-body">
      <!-- ── 检测中：只显示进度，不给任何可点的东西（避免中途误触） ── -->
      <div v-if="phase === 'checking'" class="v6-hero">
        <div class="v6-spin">
          <svg width="66" height="66" viewBox="0 0 50 50" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round">
            <path d="M25 6a19 19 0 1 1-13.4 5.6" opacity=".22" />
            <path d="M25 6a19 19 0 0 1 13.4 5.6" />
          </svg>
        </div>
        <p class="v6-step">{{ t('v6.step', { n: stepNo, total: 4 }) }}</p>
        <h2 class="v6-h2">{{ stepText }}</h2>
      </div>

      <!-- ── 已激活：单栏大布局（地址卡在上，域名设置默认闭合） ── -->
      <template v-else-if="phase === 'on'">
        <div class="v6-hero tight">
          <div class="v6-ok">
            <svg width="30" height="30" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round"><path d="M4 12.5l5.5 5.5L20 6.5" /></svg>
          </div>
          <h2 class="v6-h2">{{ t('v6.onTitle') }}</h2>
          <p class="v6-desc">{{ t('v6.onDesc') }}</p>
        </div>

        <div v-if="!out" class="v6-warn">{{ t('v6.outWarn') }}</div>

        <!-- 域名访问卡：配了域名才出现，放在地址卡之前（用域名访问才是常态） -->
        <div v-if="domainURL" class="v6-card">
          <div class="v6-card-hd">
            <span>{{ t('v6.domAccess') }}</span>
            <span class="v6-card-acts">
              <button type="button" @click="copy(domainURL)">
                <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><rect x="8.6" y="8.6" width="11" height="11" rx="2" /><path d="M5.4 15.4H4.6a1 1 0 0 1-1-1V4.6a1 1 0 0 1 1-1h9.8a1 1 0 0 1 1 1v.8" /></svg>
                {{ t('v6.copy') }}
              </button>
              <button type="button" @click="openUrl(domainURL)">
                <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M14.5 4.6H6.2v14.8h8.3" /><path d="M12.2 12h8.2M17.6 9.2l2.8 2.8-2.8 2.8" /></svg>
                {{ t('v6.open') }}
              </button>
            </span>
          </div>
          <div class="v6-url" :title="domainURL">{{ domainURL }}</div>
          <div class="v6-card-tag" :class="{ auto: autoOn }">
            {{ autoOn ? t('v6.autoOn') : t('v6.manualOn') }}
          </div>
        </div>

        <div class="v6-addrs">
          <div v-for="(u, i) in urls" :key="u" class="v6-card">
            <div class="v6-card-hd">
              <span>{{ t('v6.addrN', { n: i + 1 }) }}</span>
              <span class="v6-card-acts">
                <button type="button" @click="copy(u)">
                  <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round"><rect x="8.6" y="8.6" width="11" height="11" rx="2" /><path d="M5.4 15.4H4.6a1 1 0 0 1-1-1V4.6a1 1 0 0 1 1-1h9.8a1 1 0 0 1 1 1v.8" /></svg>
                  {{ t('v6.copy') }}
                </button>
                <button type="button" @click="openUrl(u)">
                  <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M14.5 4.6H6.2v14.8h8.3" /><path d="M12.2 12h8.2M17.6 9.2l2.8 2.8-2.8 2.8" /></svg>
                  {{ t('v6.open') }}
                </button>
              </span>
            </div>
            <div class="v6-url" :title="u">{{ u }}</div>
          </div>
        </div>

        <!-- ── 域名设置：默认闭合，点开才是完整的动态域名解析（DDNS） ── -->
        <div ref="domBox" class="v6-dom">
          <button type="button" class="v6-dom-hd" :class="{ on: domOpen }" @click="toggleDom">
            <svg class="v6-dom-car" width="10" height="10" viewBox="0 0 24 24" fill="none"
              stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round">
              <path d="M6 9l6 6 6-6" />
            </svg>
            <b>{{ t('v6.ddnsPane') }}</b>
            <span class="v6-tag" :class="{ auto: autoOn }">
              {{ autoOn ? t('v6.tagAuto') : t('v6.tagManual') }}
            </span>
            <span class="v6-dom-fd">{{ st?.full || t('v6.domEmptyShort') }}</span>
          </button>

          <div v-if="domOpen" class="v6-dom-body">
            <div class="v6-fld">
              <label>{{ t('v6.lblDomain') }}</label>
              <div class="v6-inline">
                <el-input v-model="sub" :placeholder="t('v6.subPh')" />
                <span class="v6-dot">.</span>
                <el-input v-model="domain" :placeholder="t('v6.domPh')" />
              </div>
            </div>

            <div class="v6-fld">
              <label>{{ t('v6.lblProvider') }}</label>
              <el-select v-model="provider" class="v6-sel" :placeholder="t('v6.pManual')">
                <el-option :label="t('v6.pManual')" value="" />
                <el-option :label="t('v6.pDnspod')" value="dnspod" />
                <el-option :label="t('v6.pAliyun')" value="aliyun" />
              </el-select>
            </div>

            <template v-if="provider">
              <div class="v6-fld">
                <label>{{ t('v6.lblKey') }}</label>
                <div class="v6-inline">
                  <el-input v-model="token" type="password" show-password :disabled="clearToken" :placeholder="tokenPh" />
                  <button v-if="st?.tokenSet && !clearToken" type="button" class="v6-link" @click="clearToken = true">{{ t('v6.keyClear') }}</button>
                  <button v-else-if="clearToken" type="button" class="v6-link danger" @click="clearToken = false">{{ t('v6.keyKeep') }}</button>
                </div>
              </div>
              <div class="v6-fld-note">
                <span class="v6-hint">{{ provider === 'aliyun' ? t('v6.keyPhAliyun') : t('v6.keyPhDnspod') }}</span>
              </div>

              <div class="v6-fld">
                <label>{{ t('v6.lblInterval') }}</label>
                <div class="v6-inline">
                  <el-input-number v-model="interval" :min="5" :max="1440" :step="5" controls-position="right" />
                  <span class="v6-unit">{{ t('v6.minute') }}</span>
                </div>
              </div>
            </template>

            <div class="v6-acts">
              <el-button type="primary" :loading="saving" @click="saveDdns">
                {{ provider ? t('v6.saveAndResolve') : t('v6.saveDom') }}
              </el-button>
              <button v-if="autoOn" type="button" class="v6-link" :disabled="updating" @click="updateNow">
                {{ updating ? t('v6.updating') : t('v6.updateNow') }}
              </button>
            </div>

            <!-- 自动解析：上一轮真实结果 -->
            <p v-if="provider" class="v6-status" :class="{ ok: st?.lastOK, bad: !!st?.lastAt && !st?.lastOK }">
              {{ statusText }}
            </p>

            <!-- 手动解析：把该填的 AAAA 摆出来 -->
            <div v-else-if="st?.domain" class="v6-manual">
              <p class="v6-hint">{{ t('v6.manualHint') }}</p>
              <div class="v6-aaaa-hd">{{ t('v6.manualTarget') }}</div>
              <div v-for="a in addrs" :key="a" class="v6-aaaa">
                <code>{{ a }}</code>
                <button type="button" class="v6-link" @click="copy(a)">{{ t('v6.copy') }}</button>
              </div>
            </div>
            <p v-else class="v6-hint">{{ t('v6.domEmpty') }}</p>
          </div>
        </div>

        <div class="v6-foot">
          <button type="button" class="v6-unbind" @click="unbind">{{ t('v6.unbind') }}</button>
        </div>
      </template>

      <!-- ── 未开启 / 没有可用地址（失败态） ── -->
      <div v-else class="v6-hero">
        <div class="v6-ico" :class="{ bad: phase === 'error' }">
          <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round">
            <circle cx="12" cy="12" r="8.6" />
            <path d="M3.4 12h17.2M12 3.4c2.6 2.4 4 5.5 4 8.6s-1.4 6.2-4 8.6c-2.6-2.4-4-5.5-4-8.6s1.4-6.2 4-8.6z" />
          </svg>
        </div>
        <p v-if="phase === 'error'" class="v6-step">{{ t('v6.failStep') }}</p>
        <h2 class="v6-h2">{{ phase === 'error' ? t('v6.noAddrTitle') : t('v6.offTitle') }}</h2>
        <p class="v6-desc">{{ phase === 'error' ? t('v6.noAddrDesc') : t('v6.offDesc') }}</p>
        <div class="v6-cta">
          <el-button v-if="phase === 'error'" size="large" @click="recheck">{{ t('v6.retry') }}</el-button>
          <el-button v-else type="primary" size="large" :loading="busy" @click="enable">{{ t('v6.offBtn') }}</el-button>
        </div>
      </div>
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import { Browser, Window } from '@wailsio/runtime'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from './i18n'
import { useWindowDrag } from './composables/useWindowDrag'
import { V6Probe, V6CheckOut, V6Enable, V6Disable, V6SaveDDNS, V6UpdateNow } from '../bindings/github.com/duihao/duihao/app'

interface V6State {
  enabled: boolean
  addrs: string[]
  port: number
  domain: string
  sub: string
  instance: string
  provider: string
  tokenSet: boolean
  interval: number
  lastIP: string
  lastOK: boolean
  lastMsg: string
  lastAt: string
  full: string
  auto: boolean
}

const { t } = useI18n()
const { onDragDown } = useWindowDrag()

const phase = ref<'checking' | 'off' | 'on' | 'error'>('checking')
const stepNo = ref(1)
const busy = ref(false)
const st = ref<V6State | null>(null)
const out = ref(true)

// 动态域名表单：provider 空 = 手动解析（只存域名，自己加 AAAA）
// 域名设置默认闭合（用户定稿：弹窗主体是地址，域名是进阶功能，别一进来就摊开）
const domOpen = ref(false)
const domBox = ref<HTMLElement | null>(null)
const saving = ref(false)
const updating = ref(false)
const domain = ref('')
const sub = ref('')
const provider = ref('')
const token = ref('')
const clearToken = ref(false)
const interval = ref(10)

// 端口取实例 registry 的实际值（与首屏地址卡同源），不是插件默认值。
const addrs = computed(() => st.value?.addrs || [])
const urls = computed(() => {
  const p = st.value?.port || 9111
  return addrs.value.map((a) => `http://[${a}]:${p}/`)
})

const fullDomain = computed(() => st.value?.full || '')
const domainURL = computed(() => {
  const f = fullDomain.value
  if (!f) return ''
  return `http://${f}:${st.value?.port || 9111}/`
})
const autoOn = computed(() => !!st.value?.auto)
const tokenPh = computed(() => {
  if (clearToken.value) return t('v6.keyWillClear')
  if (st.value?.tokenSet) return t('v6.keySaved')
  return provider.value === 'aliyun' ? t('v6.keyPhAliyun') : t('v6.keyPhDnspod')
})

const statusText = computed(() => {
  const s = st.value
  if (!s) return ''
  if (!s.lastAt) return t('v6.autoWait')
  return t('v6.autoStatus', { msg: s.lastMsg || t('v6.autoNone'), at: s.lastAt })
})

const stepText = computed(() => {
  switch (stepNo.value) {
    case 1: return t('v6.stepAddr')
    case 2: return t('v6.stepOut')
    case 3: return t('v6.stepPort')
    default: return t('v6.stepEnable')
  }
})

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

function apply(s: V6State) {
  st.value = s
  sub.value = s.sub || ''
  domain.value = s.domain || ''
  provider.value = s.provider || ''
  interval.value = s.interval || 10
}

// 打开窗口先只读一次（不拨 IPv6，避免一开窗就卡 6 秒）。
async function load() {
  phase.value = 'checking'
  stepNo.value = 1
  try {
    const s = (await V6Probe()) as unknown as V6State
    apply(s)
    if (!s.enabled) {
      phase.value = 'off'
      return
    }
    // 已经开着：补一次出口探测，用来决定顶部要不要出警告条
    stepNo.value = 2
    out.value = (await V6CheckOut()) as boolean
    phase.value = 'on'
  } catch (e) {
    ElMessage.error(String(e))
    phase.value = 'error'
  }
}

// 走完四步：每个 stepNo 都对应一次真实调用，sleep 只是让进度看得见。
async function enable() {
  busy.value = true
  phase.value = 'checking'
  stepNo.value = 1
  try {
    const s = (await V6Probe()) as unknown as V6State
    apply(s)
    if (!s.addrs?.length) {
      phase.value = 'error'
      return
    }
    await sleep(420)
    stepNo.value = 2
    out.value = (await V6CheckOut()) as boolean
    stepNo.value = 3
    await sleep(420)
    stepNo.value = 4
    const next = (await V6Enable()) as unknown as V6State
    apply(next)
    await sleep(320)
    phase.value = 'on'
  } catch (e) {
    ElMessage.error(String(e))
    phase.value = 'error'
  } finally {
    busy.value = false
  }
}

function recheck() {
  void load()
}

// 展开/收起域名设置。展开后表单通常在折叠栏下方、窗口矮时一截落在可视区外，
// 顺手把它滚进来（block:'nearest' = 刚好够看，不多滚，地址卡不会一下被顶走）。
async function toggleDom() {
  domOpen.value = !domOpen.value
  if (!domOpen.value) return
  await nextTick()
  const body = domBox.value?.querySelector('.v6-dom-body') as HTMLElement | null
  body?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
}

async function copy(u: string) {
  try {
    await navigator.clipboard.writeText(u)
    ElMessage.success(t('v6.copied'))
  } catch {
    ElMessage.warning(u)
  }
}

function openUrl(u: string) {
  void Browser.OpenURL(u)
}

// 保存动态域名：自动解析模式下后端会当场真跑一轮，结果就在 lastOK/lastMsg 里。
async function saveDdns() {
  saving.value = true
  try {
    const next = (await V6SaveDDNS({
      domain: domain.value.trim(),
      sub: sub.value.trim(),
      provider: provider.value,
      token: token.value.trim(),
      clearToken: clearToken.value,
      interval: interval.value,
    })) as unknown as V6State
    apply(next)
    token.value = ''
    clearToken.value = false
    if (provider.value && !next.lastOK) {
      ElMessage.warning(next.lastMsg || t('v6.resolvedFail'))
    } else {
      ElMessage.success(provider.value ? t('v6.resolvedOk') : t('v6.domSaved'))
    }
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    saving.value = false
  }
}

async function updateNow() {
  updating.value = true
  try {
    const r = (await V6UpdateNow()) as unknown as { ok: boolean; msg: string; state: V6State }
    if (r.state) apply(r.state)
    if (r.ok) ElMessage.success(r.msg || t('v6.resolvedOk'))
    else ElMessage.warning(r.msg || t('v6.resolvedFail'))
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    updating.value = false
  }
}

async function unbind() {
  try {
    await ElMessageBox.confirm(t('v6.unbindAsk'), t('v6.unbind'), {
      confirmButtonText: t('v6.unbind'),
      cancelButtonText: t('common.cancel'),
      type: 'warning',
    })
  } catch {
    return // 用户点了取消
  }
  try {
    const next = (await V6Disable()) as unknown as V6State
    apply(next)
    phase.value = 'off'
    ElMessage.success(t('v6.unbound'))
  } catch (e) {
    ElMessage.error(String(e))
  }
}

function minimise() {
  void Window.Minimise()
}
function close() {
  void Window.Close()
}

onMounted(() => {
  void load()
})
</script>

<style scoped>
.v6-root {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  border-radius: var(--win-radius);
  overflow: hidden;
  background: var(--bg-base);
  /* 与 NAS 主窗/会员窗一致：Element Plus 主色指到品牌玫红 */
  --el-color-primary: var(--accent);
  --el-color-primary-light-3: color-mix(in srgb, var(--accent) 72%, var(--bg-base));
  --el-color-primary-light-5: color-mix(in srgb, var(--accent) 52%, var(--bg-base));
  --el-color-primary-light-8: color-mix(in srgb, var(--accent) 22%, var(--bg-base));
  --el-color-primary-light-9: color-mix(in srgb, var(--accent) 12%, var(--bg-base));
  --el-color-primary-dark-2: color-mix(in srgb, var(--accent) 82%, #000);
}

.v6-bar {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  flex-shrink: 0;
  height: 2.25rem;
  padding: 0 0.3rem 0 0.75rem;
  background: var(--bg-elevated);
  user-select: none;
  --wails-draggable: no-drag;
}
.v6-brand {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  font-size: 0.8125rem;
  color: var(--text-primary);
  white-space: nowrap;
}
.brand-mark { width: 1rem; height: 1rem; border-radius: 0.25rem; -webkit-user-drag: none; }
.v6-brand b { font-weight: 700; letter-spacing: 0.02em; }
.v6-drag { flex: 1; height: 100%; }
.v6-win {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.325rem;
  height: 1.325rem;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer !important;
  flex-shrink: 0;
}
.v6-win svg { stroke: currentColor; fill: none; }
.v6-win:hover { background: var(--bg-surface); color: var(--text-primary); }
.v6-win.close:hover { background: #e81123; color: #fff; }

.v6-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  padding: 1.1rem 1.35rem 1.1rem;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
}
/* 可滚区里的每一块都不许被压缩。flex column 默认 flex-shrink:1，内容超高时
   浏览器优先压扁卡片/表单而不是溢出，结果就是「滚不动、也没显示全」——
   把收缩关掉，超出部分才真正落到滚动区里。 */
.v6-body > .v6-card,
.v6-body > .v6-addrs,
.v6-body > .v6-dom,
.v6-body > .v6-warn,
.v6-body > .v6-foot,
.v6-body > .v6-hero.tight,
.v6-body > .v6-cta { flex: 0 0 auto; }

/* ── 顶部图标区：三种状态共用一套排版 ── */
.v6-hero {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  text-align: center;
  /* 关闭/失败态没有地址卡，剩下一整屏：让内容在垂直方向居中，别贴着上半截。
     shrink 0：内容真的放不下时溢出到滚动区，而不是把图标压成一条线。 */
  flex: 1 0 auto;
  padding: 1.4rem 1rem;
}
.v6-hero.tight { flex: 0 0 auto; justify-content: flex-start; padding: 0.5rem 1rem 0.85rem; }
.v6-hero.tight .v6-desc { max-width: 40rem; }

.v6-spin { color: var(--accent); animation: v6spin 1.1s linear infinite; }
@keyframes v6spin { to { transform: rotate(360deg); } }

.v6-ok {
  width: 2.9rem;
  height: 2.9rem;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  border: 2px solid var(--accent);
  color: var(--accent);
  margin-bottom: 0.7rem;
}
.v6-ico {
  width: 4.2rem;
  height: 4.2rem;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  background: var(--bg-elevated);
  color: var(--text-muted);
  margin-bottom: 1rem;
}
.v6-ico.bad { color: var(--el-color-warning, #e6a23c); }

.v6-step {
  margin: 0.9rem 0 0.55rem;
  font-size: 0.6875rem;
  color: var(--text-muted);
  letter-spacing: 0.02em;
}
.v6-h2 {
  margin: 0;
  font-size: 1.0625rem;
  font-weight: 700;
  color: var(--text-primary);
  letter-spacing: 0.01em;
}
.v6-desc {
  margin: 0.6rem 0 0;
  max-width: 34rem;
  font-size: 0.75rem;
  line-height: 1.7;
  color: var(--text-secondary);
}
.v6-cta { margin-top: 1.5rem; }
.v6-cta :deep(.el-button) { min-width: 15rem; height: 2.5rem !important; font-size: 0.8125rem; }

/* ── 动态域名折叠栏（默认闭合，点开才是完整 DDNS 表单） ── */
.v6-dom {
  margin-top: 0.85rem;
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  overflow: hidden;
}
.v6-dom-hd {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  width: 100%;
  padding: 0.5rem 0.7rem;
  border: 0;
  background: transparent;
  color: var(--text-secondary);
  font: inherit;
  font-size: 0.75rem;
  text-align: left;
  cursor: pointer !important;
}
.v6-dom-hd:hover { background: var(--bg-hover); }
.v6-dom-hd b { font-weight: 600; color: var(--text-primary); }
.v6-dom-car { flex: 0 0 auto; color: var(--text-muted); transition: transform 0.15s; }
.v6-dom-hd.on .v6-dom-car { transform: rotate(180deg); color: var(--accent); }
.v6-dom-fd {
  margin-left: auto;
  max-width: 55%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.625rem;
  color: var(--text-muted);
}
.v6-dom-body {
  padding: 0.25rem 0.7rem 0.75rem;
  border-top: 1px solid var(--border-subtle);
}
.v6-tag {
  padding: 0 0.32rem;
  border-radius: 999px;
  border: 1px solid var(--border-subtle);
  font-size: 0.5625rem;
  font-weight: 500;
  letter-spacing: 0;
  text-transform: none;
  color: var(--text-muted);
}
.v6-tag.auto {
  border-color: color-mix(in srgb, var(--accent) 50%, transparent);
  color: var(--accent);
}

/* ── 地址卡 ── */
.v6-addrs { display: flex; flex-direction: column; gap: 0.5rem; }
.v6-card {
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  padding: 0.45rem 0.7rem 0.5rem;
  margin-bottom: 0.5rem;
}
/* 列表内的卡靠 gap 分隔，别再叠一层外边距（否则末尾多出一段空） */
.v6-addrs .v6-card { margin-bottom: 0; }
.v6-card-hd {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  font-size: 0.6875rem;
  color: var(--text-muted);
}
.v6-card-acts { display: inline-flex; align-items: center; gap: 0.75rem; }
.v6-card-acts button {
  display: inline-flex;
  align-items: center;
  gap: 0.28rem;
  border: 0;
  background: transparent;
  padding: 0;
  color: var(--accent);
  font: inherit;
  font-size: 0.6875rem;
  cursor: pointer !important;
}
.v6-card-acts button:hover { opacity: 0.75; }
.v6-card-acts svg { stroke: currentColor; fill: none; }
.v6-url {
  margin-top: 0.28rem;
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.75rem;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.v6-card-tag {
  margin-top: 0.3rem;
  font-size: 0.625rem;
  color: var(--text-muted);
}
.v6-card-tag.auto { color: var(--accent); }

.v6-warn {
  margin-bottom: 0.6rem;
  padding: 0.5rem 0.7rem;
  border-radius: var(--radius-md);
  border: 1px solid color-mix(in srgb, var(--el-color-warning, #e6a23c) 45%, transparent);
  background: color-mix(in srgb, var(--el-color-warning, #e6a23c) 10%, transparent);
  color: var(--el-color-warning, #e6a23c);
  font-size: 0.6875rem;
  line-height: 1.6;
}

/* ── 动态域名字段 ── */
.v6-fld {
  display: grid;
  grid-template-columns: 3.6rem minmax(0, 1fr);
  align-items: center;
  gap: 0.4rem;
  margin-bottom: 0.45rem;
}
.v6-fld > label {
  font-size: 0.6875rem;
  color: var(--text-muted);
  text-align: right;
  white-space: nowrap;
}
.v6-inline { display: flex; align-items: center; gap: 0.3rem; min-width: 0; }
.v6-inline :deep(.el-input) { flex: 1; min-width: 0; }
.v6-inline :deep(.el-input__wrapper) { height: 1.95rem; }
.v6-inline :deep(.el-input-number) { width: 6.2rem; }
.v6-inline :deep(.el-input-number .el-input__wrapper) { height: 1.95rem; }
.v6-sel { width: 100%; }
.v6-sel :deep(.el-select__wrapper) { height: 1.95rem; min-height: 1.95rem; }
.v6-dot { color: var(--text-muted); }
.v6-unit { font-size: 0.6875rem; color: var(--text-muted); white-space: nowrap; }
.v6-fld-note {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 0.5rem;
  margin: -0.1rem 0 0.55rem 4rem;
}
.v6-acts { display: flex; align-items: center; gap: 0.7rem; margin: 0.15rem 0 0 4rem; }
.v6-acts :deep(.el-button) { height: 1.95rem !important; font-size: 0.75rem; }

.v6-link {
  border: 0;
  background: transparent;
  padding: 0;
  color: var(--accent);
  font: inherit;
  font-size: 0.6875rem;
  cursor: pointer !important;
  white-space: nowrap;
}
.v6-link:hover { opacity: 0.75; }
.v6-link.danger { color: #d93a32; }
.v6-link:disabled { opacity: 0.5; cursor: default !important; }

.v6-hint {
  margin: 0;
  font-size: 0.625rem;
  line-height: 1.6;
  color: var(--text-muted);
}
.v6-status {
  margin: 0.55rem 0 0;
  font-size: 0.625rem;
  line-height: 1.6;
  color: var(--text-muted);
  word-break: break-all;
}
.v6-status.ok { color: var(--accent); }
.v6-status.bad { color: var(--el-color-warning, #e6a23c); }

.v6-manual {
  margin-top: 0.55rem;
  padding: 0.5rem 0.6rem;
  border-radius: var(--radius-md);
  border: 1px dashed var(--border-subtle);
}
.v6-aaaa-hd {
  margin: 0.45rem 0 0.2rem;
  font-size: 0.625rem;
  color: var(--text-muted);
}
.v6-aaaa {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}
.v6-aaaa code {
  font-family: 'JetBrains Mono Variable', ui-monospace, monospace;
  font-size: 0.6875rem;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 解绑按钮跟着内容走，不再「永远贴底」：折叠态内容只占 ~600 时，贴底会在
   折叠栏与按钮之间拉出一大块死白；跟着内容排，留白落在窗口最底部，像窗口
   比内容高一点，比中间断一截自然。展开态内容本来就撑满，位置不变。 */
.v6-foot { margin-top: 1.1rem; }
.v6-unbind {
  width: 100%;
  height: 2.1rem;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-md);
  border: 1px solid #e0483f;
  background: #d93a32;
  color: #fff;
  font: inherit;
  font-size: 0.75rem;
  font-weight: 600;
  cursor: pointer !important;
}
.v6-unbind:hover { filter: brightness(1.08); }

/* ── 窄窗（默认 640×480）：把纵向空间匀出来给内容 ──
   折叠态要刚好放下三张地址卡，IPv6 地址一行放不下就缩等宽字号
   （截断还有 title 悬浮和「复制」兜底）。
   放在样式表末尾：媒体查询不提升特指度，写前面会被同名规则盖掉。 */
@media (max-width: 780px) {
  .v6-body { padding: 0.8rem 0.95rem 0.9rem; }
  .v6-hero.tight { padding: 0.1rem 0.4rem 0.4rem; }
  .v6-hero.tight .v6-desc { margin-top: 0.35rem; }
  .v6-ok { width: 2.2rem; height: 2.2rem; margin-bottom: 0.4rem; }
  .v6-card { padding: 0.36rem 0.6rem 0.4rem; margin-bottom: 0.4rem; }
  .v6-addrs { gap: 0.38rem; }
  .v6-url { font-size: 0.6875rem; }
  .v6-dom { margin-top: 0.55rem; }
  .v6-foot { margin-top: 0.8rem; }
}
</style>
