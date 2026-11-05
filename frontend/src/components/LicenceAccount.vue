<template>
  <div class="licence-account">
    <!-- 已登录：权益卡片 -->
    <template v-if="state.loggedIn">
      <div class="lic-card">
        <div class="lic-row lic-plan-row">
          <span class="lic-plan-badge" :class="'plan-' + state.plan">{{ planLabel }}</span>
          <span v-if="state.plan !== 'free'" class="lic-expire">
            {{ expireLabel }}
          </span>
        </div>
        <div class="lic-row lic-sub">
          <span class="lic-k">{{ t('licence.account') }}</span>
          <span>{{ maskMobile(state.sub) }}</span>
        </div>
        <div class="lic-row lic-sub">
          <span class="lic-k">{{ t('licence.tokenExpire') }}</span>
          <span>{{ tokenExpLabel }}</span>
        </div>
        <div class="lic-row lic-sub">
          <span class="lic-k">{{ t('licence.device') }}</span>
          <span>{{ t('licence.deviceNote') }}</span>
        </div>
        <div v-if="state.plan === 'free' && state.reason === 'entitlement_expired'" class="lic-expired-tip">
          {{ t('licence.expiredTip') }}
        </div>
      </div>
      <div class="lic-actions">
        <el-button size="small" @click="handleLogout">{{ t('licence.logout') }}</el-button>
        <el-button size="small" type="primary" link @click="openBuy">
          {{ t('licence.buy') }}
        </el-button>
      </div>
    </template>

    <!-- 未登录：手机号 + 验证码 -->
    <template v-else>
      <p class="lic-login-hint">{{ t('licence.loginHint') }}</p>
      <el-form class="lic-form" @submit.prevent="handleLogin">
        <el-form-item>
          <el-input
            v-model="mobile"
            :placeholder="t('licence.phonePlaceholder')"
            :maxlength="11"
            size="default"
            autocomplete="tel"
            @input="mobile = mobile.replace(/\D/g, '')"
          />
        </el-form-item>
        <el-form-item>
          <div class="lic-code-row">
            <el-input
              v-model="code"
              :placeholder="t('licence.codePlaceholder')"
              :maxlength="6"
              size="default"
              autocomplete="one-time-code"
              @keyup.enter="handleLogin"
            />
            <el-button
              size="default"
              :disabled="countdown > 0 || !canSend"
              :loading="sending"
              @click="handleSendCode"
            >
              {{ countdown > 0 ? t('licence.resend', { s: countdown }) : t('licence.sendCode') }}
            </el-button>
          </div>
        </el-form-item>
        <div v-if="errorMsg" class="lic-error">{{ errorMsg }}</div>
        <el-button
          class="lic-login-btn"
          type="primary"
          size="default"
          :loading="logging"
          :disabled="!canLogin"
          native-type="submit"
        >
          {{ t('licence.login') }}
        </el-button>
      </el-form>
      <div class="lic-login-foot">
        <span>{{ t('licence.agree') }}</span>
        <el-button size="small" type="primary" link @click="openBuy">{{ t('licence.buy') }}</el-button>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from '../i18n'
import { Browser } from '@wailsio/runtime'
import { LicenceLogin, LicenceLogout, LicenceSendCode, LicenceState } from '../../bindings/github.com/duihao/duihao/app'

const { t } = useI18n()

interface LicenceStateShape {
  loggedIn: boolean
  plan: string
  planText: string
  ent: number
  tokenExp: number
  sub: string
  reason: string
}

const state = ref<LicenceStateShape>({
  loggedIn: false, plan: 'free', planText: '', ent: 0, tokenExp: 0, sub: '', reason: '',
})

const mobile = ref('')
const code = ref('')
const sending = ref(false)
const logging = ref(false)
const errorMsg = ref('')
const countdown = ref(0)
let countdownTimer: ReturnType<typeof setInterval> | null = null

const canSend = computed(() => /^1\d{10}$/.test(mobile.value))
const canLogin = computed(() => canSend.value && /^\d{4,6}$/.test(code.value))

const planLabel = computed(() => {
  switch (state.value.plan) {
    case 'trial': return t('licence.planTrial')
    case 'annual': return t('licence.planAnnual')
    case 'lifetime': return t('licence.planLifetime')
    default: return t('licence.planFree')
  }
})

const expireLabel = computed(() => {
  if (!state.value.ent) return t('licence.expireNever')
  return t('licence.expireAt', { date: fmtDate(state.value.ent) })
})

const tokenExpLabel = computed(() => {
  if (!state.value.tokenExp) return '—'
  return fmtDate(state.value.tokenExp)
})

function fmtDate(sec: number): string {
  try {
    return new Date(sec * 1000).toLocaleDateString(undefined, { year: 'numeric', month: '2-digit', day: '2-digit' })
  } catch {
    return String(sec)
  }
}

// 服务器只回 sub（用户 ID）不回手机号；这里没有可打码的手机号时原样显示。
function maskMobile(s: string): string {
  if (/^1\d{10}$/.test(s)) return s.slice(0, 3) + '****' + s.slice(7)
  return s || '—'
}

function startCountdown() {
  countdown.value = 60
  if (countdownTimer) clearInterval(countdownTimer)
  countdownTimer = setInterval(() => {
    countdown.value--
    if (countdown.value <= 0 && countdownTimer) {
      clearInterval(countdownTimer)
      countdownTimer = null
    }
  }, 1000)
}

async function handleSendCode() {
  if (!canSend.value || sending.value) return
  errorMsg.value = ''
  sending.value = true
  try {
    await LicenceSendCode(mobile.value)
    startCountdown()
  } catch (e) {
    errorMsg.value = apiErrMsg(e, t('licence.sendCodeFailed'))
  } finally {
    sending.value = false
  }
}

async function handleLogin() {
  if (!canLogin.value || logging.value) return
  errorMsg.value = ''
  logging.value = true
  try {
    await LicenceLogin(mobile.value, code.value)
    code.value = ''
  } catch (e) {
    errorMsg.value = apiErrMsg(e, t('licence.loginFailed'))
  } finally {
    logging.value = false
  }
}

async function handleLogout() {
  try {
    await LicenceLogout()
  } catch { /* 清理失败不提示：下次 Verify 会重新折叠 */ }
}

function openBuy() {
  Browser.OpenURL('https://duihao.cn/pricing')
}

// Go 侧错误形如 "发送太频繁 (HTTP 429)"；前端只展示文本。
function apiErrMsg(e: unknown, fallback: string): string {
  const s = e instanceof Error ? e.message : String(e ?? '')
  return s || fallback
}

async function refresh() {
  try {
    const st = await LicenceState()
    state.value = st as unknown as LicenceStateShape
  } catch { /* 后端未就绪（dev 浏览器预览）保持 free */ }
}

let unsub: (() => void) | null = null

onMounted(async () => {
  await refresh()
  try {
    const { Events } = await import('@wailsio/runtime')
    // 后端 Verify / 续签 / 降级都会广播 licence:state，前端只跟随不判权。
    const off = Events.On('licence:state', (data: unknown) => {
      const d = data as LicenceStateShape | { data?: LicenceStateShape }
      state.value = (d && typeof d === 'object' && 'loggedIn' in d ? d : (d as { data?: LicenceStateShape })?.data) as LicenceStateShape
    })
    unsub = () => off()
  } catch { /* 事件不可用时靠手动 refresh */ }
})

onBeforeUnmount(() => {
  if (unsub) unsub()
  if (countdownTimer) clearInterval(countdownTimer)
})

// 供父组件（settingsTab）在打开面板时强制刷新；也可被测试直接调用。
defineExpose({ refresh })
</script>

<style scoped>
.licence-account {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  max-width: 30rem;
}
.lic-card {
  border: 1px solid var(--border-color, var(--el-border-color));
  border-radius: 8px;
  padding: 0.875rem 1rem;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  background: var(--panel-bg, transparent);
}
.lic-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 0.8125rem;
  gap: 1rem;
}
.lic-plan-row { justify-content: flex-start; gap: 0.75rem; }
.lic-plan-badge {
  font-size: 0.875rem;
  font-weight: 600;
  padding: 0.125rem 0.625rem;
  border-radius: 999px;
  color: var(--text-secondary, var(--el-text-color-secondary));
  background: var(--hover-bg, var(--el-fill-color));
}
.lic-plan-badge.plan-annual { color: var(--primary-color, var(--el-color-primary)); }
.lic-plan-badge.plan-lifetime { color: #d48806; }
.lic-expire { color: var(--text-secondary, var(--el-text-color-secondary)); }
.lic-k { color: var(--text-secondary, var(--el-text-color-secondary)); }
.lic-expired-tip {
  font-size: 0.75rem;
  color: var(--el-color-danger);
}
.lic-actions {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.lic-login-hint {
  font-size: 0.8125rem;
  color: var(--text-secondary, var(--el-text-color-secondary));
  margin: 0;
}
.lic-form { display: flex; flex-direction: column; }
.lic-form :deep(.el-form-item) { margin-bottom: 0.75rem; }
.lic-code-row {
  display: flex;
  gap: 0.5rem;
  width: 100%;
}
.lic-code-row .el-input { flex: 1; }
.lic-code-row .el-button { white-space: nowrap; min-width: 7rem; }
.lic-error {
  font-size: 0.75rem;
  color: var(--el-color-danger);
  margin-bottom: 0.5rem;
}
.lic-login-btn { width: 100%; }
.lic-login-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 0.75rem;
  color: var(--text-secondary, var(--el-text-color-secondary));
}
</style>
