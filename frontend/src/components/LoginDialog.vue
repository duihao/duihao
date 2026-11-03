<template>
  <el-dialog
    v-model="visible"
    :title="t('licence.login')"
    width="400px"
    :close-on-click-modal="false"
    append-to-body
    @closed="onClosed"
  >
    <el-tabs v-model="tab">
      <!-- 方式一：用户名 / 邮箱 + 密码 -->
      <el-tab-pane :label="t('licence.loginPassword')" name="password">
        <el-form class="login-form" @submit.prevent="loginPassword">
          <el-form-item>
            <el-input
              v-model="username"
              :placeholder="t('licence.usernamePlaceholder')"
              size="default"
              autocomplete="username"
              clearable
            />
          </el-form-item>
          <el-form-item>
            <el-input
              v-model="password"
              type="password"
              :placeholder="t('licence.passwordPlaceholder')"
              size="default"
              autocomplete="current-password"
              show-password
              @keyup.enter="loginPassword"
            />
          </el-form-item>
          <div v-if="errorMsg" class="login-error">{{ errorMsg }}</div>
          <el-button
            class="login-btn"
            type="primary"
            size="default"
            :loading="logging"
            :disabled="!canPassword"
            native-type="submit"
          >
            {{ t('licence.login') }}
          </el-button>
          <p class="login-foot">{{ t('licence.passwordHint') }}</p>
        </el-form>
      </el-tab-pane>

      <!-- 方式二：手机号 + 验证码（无账号自动注册） -->
      <el-tab-pane :label="t('licence.loginSms')" name="sms">
        <el-form class="login-form" @submit.prevent="loginSms">
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
            <div class="login-code-row">
              <el-input
                v-model="code"
                :placeholder="t('licence.codePlaceholder')"
                :maxlength="6"
                size="default"
                autocomplete="one-time-code"
                @keyup.enter="loginSms"
              />
              <el-button
                size="default"
                :disabled="countdown > 0 || !canSend"
                :loading="sending"
                @click="sendCode"
              >
                {{ countdown > 0 ? t('licence.resend', { s: countdown }) : t('licence.sendCode') }}
              </el-button>
            </div>
          </el-form-item>
          <div v-if="errorMsg" class="login-error">{{ errorMsg }}</div>
          <el-button
            class="login-btn"
            type="primary"
            size="default"
            :loading="logging"
            :disabled="!canSms"
            native-type="submit"
          >
            {{ t('licence.login') }}
          </el-button>
          <p class="login-foot">{{ t('licence.agree') }}</p>
        </el-form>
      </el-tab-pane>

      <!-- 方式三：微信扫码（待微信开放平台对接，先占位） -->
      <el-tab-pane :label="t('licence.loginWechat')" name="wechat">
        <div class="login-wechat">
          <div class="login-wechat-qr">
            <span class="login-wechat-hint">{{ t('licence.wechatUnavailable') }}</span>
          </div>
          <el-button size="default" @click="tab = 'password'">
            {{ t('licence.loginUseOther') }}
          </el-button>
        </div>
      </el-tab-pane>
    </el-tabs>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from '../i18n'
import { LicenceLogin, LicencePasswordLogin, LicenceSendCode } from '../../bindings/github.com/ys-ll/uniterm/app'

const props = defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{
  (e: 'update:modelValue', v: boolean): void
  (e: 'success'): void
}>()

const { t } = useI18n()

const visible = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit('update:modelValue', v),
})

const tab = ref<'password' | 'sms' | 'wechat'>('password')
const username = ref('')
const password = ref('')
const mobile = ref('')
const code = ref('')
const sending = ref(false)
const logging = ref(false)
const errorMsg = ref('')
const countdown = ref(0)
let countdownTimer: ReturnType<typeof setInterval> | null = null

const canSend = computed(() => /^1\d{10}$/.test(mobile.value))
const canPassword = computed(() => username.value.trim() !== '' && password.value !== '')
const canSms = computed(() => canSend.value && /^\d{4,6}$/.test(code.value))

// 每次打开清上次的错误与敏感输入
watch(() => props.modelValue, (open) => {
  if (open) {
    errorMsg.value = ''
    password.value = ''
  }
})

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

async function sendCode() {
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

async function loginPassword() {
  if (!canPassword.value || logging.value) return
  errorMsg.value = ''
  logging.value = true
  try {
    await LicencePasswordLogin(username.value.trim(), password.value)
    password.value = ''
    visible.value = false
    emit('success')
  } catch (e) {
    errorMsg.value = apiErrMsg(e, t('licence.loginFailed'))
  } finally {
    logging.value = false
  }
}

async function loginSms() {
  if (!canSms.value || logging.value) return
  errorMsg.value = ''
  logging.value = true
  try {
    await LicenceLogin(mobile.value, code.value)
    code.value = ''
    visible.value = false
    emit('success')
  } catch (e) {
    errorMsg.value = apiErrMsg(e, t('licence.loginFailed'))
  } finally {
    logging.value = false
  }
}

function onClosed() {
  errorMsg.value = ''
}

// Go 侧错误形如 "用户名或密码错误 (HTTP 401)"；前端只展示文本。
function apiErrMsg(e: unknown, fallback: string): string {
  const s = e instanceof Error ? e.message : String(e ?? '')
  return s || fallback
}

onBeforeUnmount(() => {
  if (countdownTimer) clearInterval(countdownTimer)
})
</script>

<style scoped>
.login-form { display: flex; flex-direction: column; padding-top: 0.25rem; }
.login-form :deep(.el-form-item) { margin-bottom: 0.875rem; }
.login-code-row {
  display: flex;
  gap: 0.5rem;
  width: 100%;
}
.login-code-row .el-input { flex: 1; }
.login-code-row .el-button { white-space: nowrap; min-width: 7rem; }
.login-error {
  font-size: 0.75rem;
  color: var(--el-color-danger);
  margin-bottom: 0.5rem;
}
.login-btn { width: 100%; }
.login-foot {
  font-size: 0.75rem;
  color: var(--text-secondary, var(--el-text-color-secondary));
  margin: 0.75rem 0 0;
  text-align: center;
}
.login-wechat {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 1rem;
  padding: 1rem 0;
}
.login-wechat-qr {
  width: 180px;
  height: 180px;
  border: 1px dashed var(--border-color, var(--el-border-color));
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 1rem;
}
.login-wechat-hint {
  font-size: 0.8125rem;
  color: var(--text-secondary, var(--el-text-color-secondary));
  text-align: center;
  line-height: 1.6;
}
</style>
