<template>
  <!-- 固定内网地址（/?w=fixip）
       参照易等的版式：配置卡 → 两段说明 → 三步兜底 → 底部按钮。
       差别在于我们不要求用户退出应用再右键「以管理员身份运行」——
       点「固定地址」时后端直接对 netsh 弹一次 UAC，点「是」就落盘。
       窗口本身由 fixip_window.go 创建（520×640 无边框，内容超高时内容区滚动）。 -->
  <div class="fx-root">
    <header class="fx-bar" @mousedown="onDragDown">
      <span class="fx-brand">
        <img class="brand-mark" src="/favicon.png" alt="对号" draggable="false" />
        <b>{{ t('fixip.title') }}</b>
      </span>
      <span class="fx-drag"></span>
      <button class="fx-win" :title="t('fixip.refresh')" :disabled="busy" @click="load(true)">
        <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
          <path d="M20 11a8 8 0 1 0-2.3 5.7" /><path d="M20 5v6h-6" />
        </svg>
      </button>
      <button class="fx-win close" :title="t('common.close')" @click="close">
        <svg width="10" height="10" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path d="M6 6l12 12M18 6L6 18" /></svg>
      </button>
    </header>

    <main class="fx-body">
      <h1 class="fx-h1">{{ t('fixip.h2') }}</h1>

      <!-- ── 配置卡：当前网卡的实况（读注册表，不受系统语言影响） ── -->
      <div v-if="st?.found" class="fx-card">
        <div class="fx-row"><span>{{ t('fixip.nic') }}</span><b>{{ st.name }}</b></div>
        <div class="fx-row"><span>{{ t('fixip.ip') }}</span><b>{{ st.ip }}</b></div>
        <div class="fx-row"><span>{{ t('fixip.mask') }}</span><b>{{ st.mask || '—' }}</b></div>
        <div class="fx-row"><span>{{ t('fixip.gw') }}</span><b>{{ st.gateway || '—' }}</b></div>
        <div class="fx-row"><span>{{ t('fixip.dns') }}</span><b>{{ dnsText }}</b></div>
        <div class="fx-row"><span>{{ t('fixip.mode') }}</span>
          <b :class="st.dhcp ? 'warn' : 'good'">{{ st.dhcp ? t('fixip.dhcp') : t('fixip.static') }}</b>
        </div>
      </div>
      <div v-else class="fx-card empty">{{ t('fixip.empty') }}</div>

      <!-- ── 已生效的回执（绿条）/ 刚发起提权、还在等落盘（黄条） ── -->
      <p v-if="banner === 'fixed'" class="fx-banner good">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round"><path d="M4 12.5l5.5 5.5L20 6.5" /></svg>
        {{ t('fixip.okFixed') }}
      </p>
      <p v-else-if="banner === 'restored'" class="fx-banner good">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round"><path d="M4 12.5l5.5 5.5L20 6.5" /></svg>
        {{ t('fixip.okRestored') }}
      </p>
      <p v-else-if="st?.pending" class="fx-banner wait">{{ t('fixip.pending') }}</p>

      <p class="fx-p">{{ t('fixip.keep') }}</p>
      <p class="fx-p">{{ t('fixip.notReserve') }}</p>
      <p class="fx-p">{{ t('fixip.needAdmin') }}</p>

      <div class="fx-steps">
        <p class="fx-steps-hd">{{ t('fixip.stepsTitle') }}</p>
        <p>1. {{ t('fixip.step1') }}</p>
        <p>2. {{ t('fixip.step2') }}</p>
        <p>3. {{ t('fixip.step3') }}</p>
      </div>

      <button type="button" class="fx-link" @click="openNet">{{ t('fixip.openNet') }}</button>
    </main>

    <footer class="fx-foot">
      <button class="fx-btn ghost" :disabled="busy" @click="close">{{ t('common.cancel') }}</button>
      <button
        v-if="st?.found"
        class="fx-btn primary"
        :class="{ ok: !st.dhcp }"
        :disabled="busy || st.pending"
        @click="apply"
      >
        <span v-if="busy">{{ st.dhcp ? t('fixip.fixing') : t('fixip.restoring') }}</span>
        <span v-else>{{ st.dhcp ? t('fixip.fix') : t('fixip.restore') }}</span>
      </button>
      <button v-else class="fx-btn primary" disabled>{{ t('fixip.fix') }}</button>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Window } from '@wailsio/runtime'
import { ElMessage } from 'element-plus'
import { useI18n } from './i18n'
import { useWindowDrag } from './composables/useWindowDrag'
import { FixIpLoad, FixIpApply, FixIpRestoreDHCP, FixIpOpenNetworkSettings } from '../bindings/github.com/duihao/duihao/app'

interface FixIPState {
  found: boolean
  name: string
  ip: string
  mask: string
  gateway: string
  dns: string[]
  dhcp: boolean
  elevated: boolean
  pending: boolean
  hint: string
}

const { t } = useI18n()
const { onDragDown } = useWindowDrag()

const st = ref<FixIPState | null>(null)
const busy = ref(false)
const banner = ref<'' | 'fixed' | 'restored'>('')

const dnsText = computed(() => {
  const d = st.value?.dns || []
  return d.length ? d.join(' / ') : '—'
})

// NAS 的访问地址存在 localStorage（主窗首屏会写）：多网卡时用它定位
// 「NAS 走的是哪块网卡」，比按默认路由猜更准。
function hint(): string {
  try { return localStorage.getItem('dh.fixip.hint') || '' } catch { return '' }
}

async function load(manual = false) {
  if (busy.value) return
  try {
    const r = await FixIpLoad(hint())
    st.value = r as unknown as FixIPState
    if (r && !(r as any).dhcp && banner.value === '') banner.value = 'fixed'
  } catch (e: any) {
    if (manual) ElMessage.error(String(e?.message || e))
  }
}

async function apply() {
  if (!st.value || busy.value) return
  busy.value = true
  banner.value = ''
  const wasDHCP = st.value.dhcp
  try {
    const r = wasDHCP ? await FixIpApply() : await FixIpRestoreDHCP()
    st.value = r as unknown as FixIPState
    if (r && r.dhcp !== wasDHCP) {
      banner.value = wasDHCP ? 'fixed' : 'restored'
      ElMessage.success(wasDHCP ? t('fixip.okFixed') : t('fixip.okRestored'))
    } else if (r && r.pending) {
      ElMessage.info(t('fixip.pending'))
    }
  } catch (e: any) {
    const msg = String(e?.message || e || '')
    if (/取消/.test(msg)) ElMessage.warning(t('fixip.uacDenied'))
    else ElMessage.error(t('fixip.fail', { msg }))
  } finally {
    busy.value = false
  }
}

async function openNet() {
  try { await FixIpOpenNetworkSettings() } catch (e: any) { ElMessage.error(String(e?.message || e)) }
}

function close() { void Window.Close() }

onMounted(() => { void load() })
</script>

<style scoped>
.fx-root {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  border-radius: var(--win-radius);
  overflow: hidden;
  background: var(--bg-base);
  --el-color-primary: var(--accent);
}

/* ── 自绘标题栏 ── */
.fx-bar {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  flex-shrink: 0;
  height: 2.25rem;
  padding: 0 0.3rem 0 0.75rem;
  background: var(--bg-elevated);
  border-bottom: 1px solid var(--border-subtle);
  user-select: none;
  --wails-draggable: no-drag;
}
.fx-brand {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  font-size: 0.8125rem;
  color: var(--text-primary);
  white-space: nowrap;
}
.brand-mark { width: 1rem; height: 1rem; border-radius: 0.25rem; -webkit-user-drag: none; }
.fx-brand b { font-weight: 700; letter-spacing: 0.02em; }
.fx-drag { flex: 1; height: 100%; }
.fx-win {
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
.fx-win svg { stroke: currentColor; fill: none; }
.fx-win:hover:not(:disabled) { background: var(--bg-surface); color: var(--text-primary); }
.fx-win.close:hover { background: #e81123; color: #fff; }
.fx-win:disabled { opacity: 0.4; }

/* ── 内容区 ── */
.fx-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  padding: 1.05rem 1.15rem 0.6rem;
  box-sizing: border-box;
}
.fx-h1 {
  margin: 0 0 0.75rem;
  font-size: 1.125rem;
  font-weight: 800;
  letter-spacing: 0.02em;
  color: var(--accent);
}

.fx-card {
  background: var(--bg-elevated);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  padding: 0.6rem 0.85rem;
}
.fx-card.empty {
  color: var(--text-muted);
  font-size: 0.75rem;
  line-height: 1.6;
}
.fx-row {
  display: flex;
  align-items: baseline;
  gap: 0.6rem;
  padding: 0.18rem 0;
  font-size: 0.75rem;
  line-height: 1.5;
}
.fx-row > span { flex: 0 0 4.6rem; color: var(--text-muted); }
.fx-row > b {
  flex: 1;
  min-width: 0;
  font-weight: 600;
  color: var(--text-primary);
  word-break: break-all;
}
.fx-row > b.good { color: #34b36a; }
.fx-row > b.warn { color: var(--text-primary); }

.fx-banner {
  display: flex;
  align-items: center;
  gap: 0.35rem;
  margin: 0.65rem 0 0;
  padding: 0.4rem 0.7rem;
  border-radius: var(--radius-sm);
  font-size: 0.75rem;
  line-height: 1.4;
}
.fx-banner svg { flex: 0 0 auto; }
.fx-banner.good { background: color-mix(in srgb, #34b36a 14%, transparent); color: #2f9e5e; }
.fx-banner.wait { background: color-mix(in srgb, var(--accent) 12%, transparent); color: var(--accent); }

.fx-p {
  margin: 0.7rem 0 0;
  font-size: 0.75rem;
  line-height: 1.7;
  color: var(--text-secondary);
}

.fx-steps {
  margin-top: 0.8rem;
  padding: 0.55rem 0.7rem 0.6rem;
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  border: 1px dashed var(--border-subtle);
}
.fx-steps p {
  margin: 0.22rem 0 0;
  font-size: 0.72rem;
  line-height: 1.65;
  color: var(--text-secondary);
}
.fx-steps-hd { margin: 0 !important; font-weight: 600; color: var(--text-primary) !important; }

.fx-link {
  margin: 0.75rem 0 0.25rem;
  padding: 0;
  border: 0;
  background: none;
  color: var(--accent);
  font: inherit;
  font-size: 0.75rem;
  text-decoration: underline;
  text-underline-offset: 2px;
  cursor: pointer !important;
}
.fx-link:hover { filter: brightness(1.12); }

/* ── 底部按钮 ── */
.fx-foot {
  flex-shrink: 0;
  display: flex;
  gap: 0.6rem;
  padding: 0.7rem 1.15rem 0.85rem;
  background: var(--bg-base);
  box-sizing: border-box;
}
.fx-btn {
  flex: 1;
  height: 2.15rem;
  border-radius: var(--radius-md);
  font: inherit;
  font-size: 0.8125rem;
  font-weight: 600;
  cursor: pointer !important;
  border: 1px solid transparent;
}
.fx-btn.ghost {
  background: transparent;
  border-color: var(--border-subtle);
  color: var(--text-secondary);
}
.fx-btn.ghost:hover:not(:disabled) { background: var(--bg-hover); color: var(--text-primary); }
.fx-btn.primary {
  background: var(--accent);
  color: #fff;
  box-shadow: 0 4px 14px color-mix(in srgb, var(--accent) 28%, transparent);
}
.fx-btn.primary:hover:not(:disabled) { filter: brightness(1.06); }
.fx-btn.primary.ok { background: transparent; border-color: var(--border-subtle); color: var(--text-secondary); box-shadow: none; }
.fx-btn.primary.ok:hover:not(:disabled) { background: var(--bg-hover); color: var(--text-primary); }
.fx-btn:disabled { opacity: 0.55; cursor: default !important; }
</style>
