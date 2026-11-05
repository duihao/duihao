<template>
  <!-- 会员中心独立窗（B8）：/?w=member
       无边框透明窗（Go 侧 member_window.go 创建），圆角/描边由全局 win-main
       画，顶栏自绘。左侧「会员权益」= 商品接口的 goods.summary（按行拆条），
       右侧问候条 + 套餐卡 + 支付卡，一屏内完成选套餐与扫码。 -->
  <div class="mb-root">
    <header class="mb-bar" @mousedown="onDragDown">
      <span class="mb-brand">
        <img class="brand-mark" src="/favicon.png" alt="对号" draggable="false" />
        <b>{{ t('licence.membershipCenter') }}</b>
      </span>
      <span class="mb-drag"></span>
      <button class="mb-win" :title="t('window.minimize')" @click="minimise">
        <svg width="10" height="10" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path d="M5 12h14" /></svg>
      </button>
      <button class="mb-win close" title="关闭" @click="close">
        <svg width="10" height="10" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path d="M6 6l12 12M18 6L6 18" /></svg>
      </button>
    </header>

    <main class="mb-body">
      <div class="mb-main">
        <!-- ── 左：会员权益（商品摘要） ── -->
        <aside class="mb-benefits">
          <h2 class="mb-benefits-title">{{ t('member.benefitsTitle') }}</h2>
          <p class="mb-benefits-sub">{{ t('member.benefitsSub') }}</p>
          <div class="mb-hr"></div>
          <div class="mb-benefits-hd">{{ t('member.benefitsHd') }}</div>
          <ul class="mb-list">
            <li v-for="(b, i) in benefits" :key="i">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><path d="M4 12.5l5.5 5.5L20 6.5" /></svg>
              <span>{{ b }}</span>
            </li>
          </ul>
        </aside>

        <!-- ── 右：账号 / 套餐 / 支付 ── -->
        <section class="mb-right">
          <!-- 未登录：三种方式 —— 验证码 / 密码 / 微信扫码（扫码待开放平台资质，先占位） -->
          <div v-if="!state.loggedIn" class="mb-login-card">
            <h2 class="mb-h2">{{ t('member.loginTitle') }}</h2>
            <p class="mb-sub">{{ t('licence.loginHint') }}</p>

            <!-- 方式切换：密码 | 验证码 | 微信扫码（顺序按用户口径） -->
            <div class="mb-tabs" role="tablist">
              <button
                v-for="m in loginTabs"
                :key="m.key"
                class="mb-tab"
                :class="{ on: loginMode === m.key }"
                role="tab"
                :aria-selected="loginMode === m.key"
                @click="switchMode(m.key)"
              >{{ m.label }}</button>
            </div>

            <!-- 验证码登录 -->
            <template v-if="loginMode === 'sms'">
              <div class="mb-field">
                <el-input
                  v-model="mobile"
                  :placeholder="t('licence.phonePlaceholder')"
                  :maxlength="11"
                  size="large"
                  @input="mobile = mobile.replace(/\D/g, '')"
                />
              </div>
              <div class="mb-field mb-code">
                <el-input
                  v-model="code"
                  :placeholder="t('licence.codePlaceholder')"
                  :maxlength="6"
                  size="large"
                  @keyup.enter="login"
                />
                <el-button size="large" :disabled="countdown > 0 || !canSend" :loading="sending" @click="sendCode">
                  {{ countdown > 0 ? t('licence.resend', { s: countdown }) : t('licence.sendCode') }}
                </el-button>
              </div>
            </template>

            <!-- 密码登录 -->
            <template v-else-if="loginMode === 'password'">
              <div class="mb-field">
                <el-input
                  v-model="username"
                  :placeholder="t('licence.usernamePlaceholder')"
                  size="large"
                  @keyup.enter="loginByPassword"
                />
              </div>
              <div class="mb-field">
                <el-input
                  v-model="password"
                  type="password"
                  show-password
                  :placeholder="t('licence.passwordPlaceholder')"
                  size="large"
                  @keyup.enter="loginByPassword"
                />
              </div>
              <p class="mb-fine mb-pwd-hint">{{ t('licence.passwordHint') }}</p>
            </template>

            <!-- 微信扫码：与官网登录页同源双轨（自有公众号/上游中转），
                 二维码图由 Go 拉取转 data URL（CSP 安全），命中后服务端直接签令牌 -->
            <template v-else>
              <div class="mb-wechat">
                <div class="mb-qr-box">
                  <img v-if="wxQr" :src="wxQr" class="mb-qr-img" alt="微信二维码" />
                  <div v-else class="mb-qr-ph mb-wechat-ph">{{ wxLoading ? t('member.wxLoading') : t('member.wxFailed') }}</div>
                  <div v-if="wxExpired" class="mb-qr-mask">
                    <el-button size="small" class="mb-qr-refresh" @click="wxRefresh">{{ t('member.wxRefresh') }}</el-button>
                  </div>
                </div>
                <p class="mb-fine mb-wx-tip">{{ wxTip }}</p>
                <!-- 动态检测行：呼吸点 + 每秒跳动的等待/检测/有效期计时（对齐官网扫码页节奏） -->
                <p v-if="wxQr && !wxExpired" class="mb-wx-live">
                  <span class="mb-wx-dot"></span>
                  <span>{{ t('member.wxChecking', { n: wxChecks }) }} · {{ t('member.wxRemain', { t: wxRemainText }) }}</span>
                </p>
              </div>
            </template>

            <p v-if="error" class="mb-error">{{ error }}</p>
            <el-button
              v-if="loginMode === 'sms'"
              class="mb-block mb-login-btn"
              type="primary"
              size="large"
              :disabled="!canLogin"
              :loading="logging"
              @click="login"
            >{{ t('member.loginNow') }}</el-button>
            <el-button
              v-else-if="loginMode === 'password'"
              class="mb-block mb-login-btn"
              type="primary"
              size="large"
              :disabled="!canPwdLogin"
              :loading="logging"
              @click="loginByPassword"
            >{{ t('member.loginNow') }}</el-button>

            <!-- 协议行：复选框 + 居中；《用户协议》点开弹窗（内容长出滚动条）。
                 用 div 不用 label —— label 包裹会把「点协议链接」也算作切换复选框。 -->
            <div class="mb-agree-row">
              <el-checkbox v-model="agreeChecked" class="mb-agree-cb" />
              <span class="mb-agree-tx" @click="agreeChecked = !agreeChecked">
                {{ t('member.agreeBefore') }}
                <a class="mb-agree-link" @click.stop.prevent="agreementOpen = true">{{ t('member.agreementLink') }}</a>
              </span>
            </div>
          </div>

          <template v-else>
            <!-- 问候条：手机号 + 档位 + 到期 -->
            <div class="mb-hello">
              <span class="mb-hello-dot"></span>
              <span class="mb-hello-tx">
                {{ t('member.hello', { mobile: state.mobile || state.nickname || state.sub || '—', plan: planLabel }) }}
                <template v-if="entLabel"> · {{ entLabel }}</template>
              </span>
              <el-button link size="small" class="mb-hello-out" @click="logout">{{ t('licence.logout') }}</el-button>
            </div>

            <!-- 套餐卡 -->
            <div class="mb-plans-hd">
              <span>{{ t('member.plans') }}</span>
              <span v-if="goodsTitle" class="mb-goods">{{ goodsTitle }}</span>
            </div>
            <div v-if="loading" class="mb-loading">{{ t('licence.loading') }}</div>
            <div v-else class="mb-plan-grid">
              <button
                v-for="s in skus"
                :key="s.code"
                class="mb-plan"
                :class="{ on: s.code === selected }"
                @click="pickPlan(s.code)"
              >
                <span class="mb-radio" :class="{ on: s.code === selected }"></span>
                <span class="mb-plan-name">{{ s.name }}</span>
                <span class="mb-plan-price">{{ s.price_text }}</span>
                <span class="mb-plan-period">{{ s.period_text || s.note }}</span>
                <span v-if="s.period_text && s.note" class="mb-plan-note">{{ s.note }}</span>
              </button>
            </div>
            <div v-if="!skus.length && !loading" class="mb-empty">{{ error || t('licence.noPlans') }}</div>

            <!-- 支付卡：下单后出现；paid 后换成功块 -->
            <div v-if="paid" class="mb-paid">
              <span class="mb-paid-ico">✓</span>
              <div>
                <p class="mb-paid-tx">{{ t('licence.paySuccess') }}</p>
                <p v-if="entText" class="mb-paid-sub">{{ entText }}</p>
              </div>
              <el-button size="small" @click="resetOrder">{{ t('licence.buyAgain') }}</el-button>
            </div>

            <div v-else-if="order" class="mb-pay">
              <div class="mb-pay-hd">{{ t('member.payMethod') }} · 微信支付</div>
              <div class="mb-pay-body">
                <div class="mb-qr">
                  <img v-if="order.pay.qr" :src="order.pay.qr" :alt="t('licence.payQr')" />
                  <span v-else class="mb-qr-empty">{{ order.pay.msg || t('licence.payNoQr') }}</span>
                </div>
                <div class="mb-pay-info">
                  <span class="mb-pay-label">{{ t('licence.payAmount') }}</span>
                  <b class="mb-pay-num">{{ fmtFen(order.amount) }}</b>
                  <div class="mb-pay-btns">
                    <el-button size="small" :loading="creating" @click="createOrder">{{ t('member.refreshQr') }}</el-button>
                    <el-button size="small" link @click="resetOrder">{{ t('licence.payBack') }}</el-button>
                  </div>
                  <p class="mb-pay-status">{{ pollText }}</p>
                  <el-button v-if="order.pay.pay_url" link size="small" @click="openPayUrl">
                    {{ t('licence.payOpenBrowser') }}
                  </el-button>
                </div>
              </div>
            </div>

            <!-- 未下单：与支付卡同结构（二维码位占位），点「立即购买」再生成 -->
            <div v-else class="mb-pay">
              <div class="mb-pay-hd">{{ t('member.payMethod') }} · 微信支付</div>
              <div class="mb-pay-body">
                <div class="mb-qr mb-qr-ph">{{ t('member.qrPlaceholder') }}</div>
                <div class="mb-pay-info">
                  <span class="mb-pay-label">{{ t('licence.payAmount') }}</span>
                  <b class="mb-pay-num">{{ selectedPrice }}</b>
                  <div class="mb-pay-btns">
                    <el-button type="primary" size="small" :disabled="!selected" :loading="creating" @click="createOrder">
                      {{ t('licence.buyNow') }}
                    </el-button>
                  </div>
                </div>
              </div>
            </div>

            <p class="mb-fine mb-agree mb-agree-open" @click="agreementOpen = true">{{ t('member.agreement') }}</p>
          </template>
        </section>
      </div>
    </main>

    <!-- 用户协议：自绘遮罩窗（不用 el-dialog —— teleport 到 body + 透明无边框窗会
         在卡片区留下一个巨大的 logo 残影）。内容默认占位，超长时 body 内部滚动。 -->
    <div v-if="agreementOpen" class="mb-mask" @click.self="agreementOpen = false">
      <div class="mb-agree-win" role="dialog" aria-modal="true">
        <header class="mb-agree-hd">
          <b>{{ t('member.agreementTitle') }}</b>
          <button class="mb-agree-x" :title="t('common.close')" @click="agreementOpen = false">
            <svg width="11" height="11" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path d="M6 6l12 12M18 6L6 18" /></svg>
          </button>
        </header>
        <div class="mb-agree-body">
          <!-- 后台商品填了「会员协议」→ 直接渲染（服务端已 sanitize_html 净化）。
               运营改商品即改协议全文，客户端不用跟着发版。 -->
          <div v-if="agreementHtml" class="mb-agree-rich" v-html="agreementHtml"></div>
          <!-- 后台没填 → 内置默认条款 -->
          <template v-else>
          <p><b>一、协议的接受</b></p>
          <p>欢迎使用对号桌面软件（以下简称「本软件」）。在您登录、注册或购买会员服务前，请务必仔细阅读本用户协议（以下简称「本协议」）。您勾选同意或实际使用本软件服务，即视为您已阅读并同意本协议的全部内容。</p>
          <p><b>二、会员服务</b></p>
          <p>本软件提供免费试用、年费会员与永久会员等多种服务形式。会员权益以商品页公示内容为准；年费会员按自然周期计算有效期，永久会员在软件生命周期内持续有效。会员权益仅限授权设备使用，具体席位以订单为准。</p>
          <p><b>三、账号与设备</b></p>
          <p>您应妥善保管账号与密码，并对账号下的行为负责。为保障权益安全，本软件会将授权与登录设备绑定；更换设备或超出席位上限时，可能需要先解绑或升级席位。</p>
          <p><b>四、用户行为规范</b></p>
          <p>您承诺不利用本软件从事任何违反法律法规或损害他人合法权益的活动；不对本软件进行反向工程、恶意篡改或批量自动化滥用。违反者，我们有权中止或终止对其提供服务。</p>
          <p><b>五、隐私与数据</b></p>
          <p>我们仅收集为提供服务所必需的信息（如手机号、设备指纹与授权状态），并按隐私政策进行保护；除法定情形外，不会向第三方出售或共享您的个人信息。</p>
          <p><b>六、免责声明</b></p>
          <p>因不可抗力、网络故障或第三方服务中断导致的服务暂停，我们不承担赔偿责任，但将尽力恢复服务并已尽合理注意义务。</p>
          <p><b>七、协议的变更与终止</b></p>
          <p>我们可能随版本更新对本协议进行修订，修订后的协议将在客户端或官网公示；若您继续使用，即视为接受修订。您可随时停止使用并注销账号。</p>
          <p><b>八、其他</b></p>
          <p>本协议适用中华人民共和国法律。如对协议内容有任何疑问，可通过官网联系方式与我们取得联系。</p>
          </template>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Browser, Events, Window } from '@wailsio/runtime'
import { useI18n } from './i18n'
import { useWindowDrag } from './composables/useWindowDrag'
import {
  LicenceFetchQR,
  LicenceLogin,
  LicenceLogout,
  LicenceOrderCreate,
  LicenceOrderStatus,
  LicencePasswordLogin,
  LicenceProducts,
  LicenceSendCode,
  LicenceState,
  LicenceWechatPoll,
  LicenceWechatQR,
} from '../bindings/github.com/duihao/duihao/app'

const { t } = useI18n()
const { onDragDown } = useWindowDrag()

interface StateShape {
  loggedIn: boolean
  plan: string
  planText: string
  ent: number
  tokenExp: number
  sub: string
  nickname?: string
  mobile?: string
  reason: string
}

const state = ref<StateShape>({
  loggedIn: false, plan: 'free', planText: '', ent: 0, tokenExp: 0, sub: '', nickname: '', mobile: '', reason: '',
})

const loading = ref(false)
const creating = ref(false)
const error = ref('')
const goodsTitle = ref('')
const goodsSummary = ref('')
// 商品后台的两段富文本（已由服务端净化）：会员权益 → 左栏特权清单，
// 会员协议 → 《用户协议》弹窗正文。两者都为空时各自回落（摘要 / 内置占位条款）。
const goodsBenefits = ref('')
const goodsAgreement = ref('')
const skus = ref<{ code: string; name: string; price: number; price_text: string; period_text: string; note: string }[]>([])
const selected = ref('')
const order = ref<{ order_no: string; amount: number; pay: { ok: boolean; type: string; qr: string; pay_url: string; msg: string } } | null>(null)
const paid = ref(false)
const entText = ref('')
const pollText = ref('')

// 登录表单
const mobile = ref('')
const code = ref('')
const sending = ref(false)
const logging = ref(false)
const countdown = ref(0)
let countdownTimer: ReturnType<typeof setInterval> | null = null

// 登录方式：密码 | 验证码 | 微信扫码（与官网登录页同源双轨：自有公众号/上游中转）
type LoginMode = 'sms' | 'password' | 'wechat'
const loginMode = ref<LoginMode>('password')
const loginTabs = computed(() => [
  { key: 'password' as LoginMode, label: t('licence.loginPassword') },
  { key: 'sms' as LoginMode, label: t('licence.loginSms') },
  { key: 'wechat' as LoginMode, label: t('licence.loginWechat') },
])

// ── 微信扫码：取码（Go 拉图转 data URL）→ 1.5s 轮询 → 命中即登录 ──
const wxQr = ref('')            // 二维码 data URL
const wxScene = ref('')
const wxKey = ref('')
const wxLoading = ref(false)
const wxPolling = ref(false)
const wxExpired = ref(false)
const wxStatus = ref('')        // waiting 阶段的提示（服务端 msg，如「等待扫码」）
let wxTimer: ReturnType<typeof setInterval> | null = null
// 1s 心跳：等待秒数 / 检测次数 / 二维码剩余时间（服务端 scene 600s 过期，本地同步倒计时）
const wxElapsed = ref(0)
const wxChecks = ref(0)
const wxRemain = ref(300)
let wxSecTimer: ReturnType<typeof setInterval> | null = null

const wxRemainText = computed(() => {
  const s = Math.max(0, wxRemain.value)
  return `${String(Math.floor(s / 60)).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`
})

const wxTip = computed(() => {
  if (wxExpired.value) return t('member.wxExpired')
  if (!wxQr.value) return agreeChecked.value ? '' : t('member.agreeRequired')
  return wxStatus.value || t('member.wxWaiting')
})

// 微信 Tab 下勾上协议后自动取码（此前因未勾选被拦）
watch(() => agreeChecked.value, (on) => {
  if (on && loginMode.value === 'wechat' && !wxQr.value && !wxLoading.value) wxLoad()
})

function wxStop() {
  if (wxTimer) { clearInterval(wxTimer); wxTimer = null }
  if (wxSecTimer) { clearInterval(wxSecTimer); wxSecTimer = null }
  wxPolling.value = false
}

// 每秒心跳：推进等待/检测/剩余时间；剩余归零直接判过期（不依赖轮询报错）
function wxBeat() {
  wxElapsed.value += 1
  wxRemain.value = Math.max(0, 300 - wxElapsed.value)
  if (wxRemain.value <= 0) {
    wxStop()
    wxExpired.value = true
  }
}

async function wxLoad() {
  if (wxLoading.value) return
  // 扫码即登录，同样要求先勾选《用户协议》
  if (!requireAgree()) return
  wxStop()
  wxQr.value = ''
  wxScene.value = ''
  wxKey.value = ''
  wxExpired.value = false
  wxStatus.value = ''
  wxElapsed.value = 0
  wxChecks.value = 0
  wxRemain.value = 300
  wxLoading.value = true
  try {
    const r = await LicenceWechatQR()
    const d = r as unknown as { dataURL?: string; scene?: string; key?: string }
    wxQr.value = d?.dataURL || ''
    wxScene.value = d?.scene || ''
    wxKey.value = d?.key || ''
    if (wxQr.value && wxScene.value && wxKey.value) {
      wxTimer = setInterval(wxTick, 1000)
      wxSecTimer = setInterval(wxBeat, 1000)
    }
  } catch (e) {
    wxQr.value = ''
    error.value = apiErrMsg(e)
  } finally {
    wxLoading.value = false
  }
}

async function wxRefresh() {
  await wxLoad()
}

async function wxTick() {
  if (wxPolling.value || !wxScene.value || !wxKey.value) return
  wxPolling.value = true
  wxChecks.value += 1
  try {
    const r = await LicenceWechatPoll(wxScene.value, wxKey.value)
    const d = r as unknown as { waiting?: boolean; state?: StateShape }
    if (d?.waiting) {
      wxStatus.value = ''
      return
    }
    // 命中：服务端已签好令牌并绑定设备，state 广播也会跟上
    wxStop()
    wxQr.value = ''
    if (d?.state) applyState(d.state)
    code.value = ''
    await loadProducts()
  } catch (e) {
    // 过期/失效 → 蒙层「刷新二维码」；其余错误（如 E_WX_NOBIND 未绑定）
    // 原样透出，不能一律当过期吞掉
    const msg = apiErrMsg(e)
    wxStop()
    if (/失效|过期/.test(msg)) {
      wxExpired.value = true
    } else {
      wxQr.value = ''
      error.value = msg
    }
  } finally {
    wxPolling.value = false
  }
}

// 密码登录表单（官网注册的账号：用户名/邮箱 + 密码）
const username = ref('')
const password = ref('')
const canPwdLogin = computed(() => username.value.trim().length > 0 && password.value.length > 0)

// 用户协议：默认勾选；未勾选点登录给提示；点《用户协议》开弹窗
const agreeChecked = ref(true)
const agreementOpen = ref(false)

function requireAgree(): boolean {
  if (agreeChecked.value) return true
  error.value = t('member.agreeRequired')
  return false
}

function switchMode(m: LoginMode) {
  if (loginMode.value === m) return
  loginMode.value = m
  error.value = ''
  // 进入微信 Tab 才取码并开始轮询；离开即停（避免无谓的在线轮询与限流）
  if (m === 'wechat') {
    wxLoad()
  } else {
    wxStop()
    wxQr.value = ''
    wxExpired.value = false
  }
}

const canSend = computed(() => /^1\d{10}$/.test(mobile.value))
const canLogin = computed(() => canSend.value && /^\d{4,6}$/.test(code.value))

// 未下单时支付卡里的应付金额 = 当前选中套餐价（与下单后金额一致，避免跳变）
const selectedPrice = computed(() => {
  const s = skus.value.find((x) => x.code === selected.value)
  return s ? s.price_text : '—'
})

const planLabel = computed(() => {
  switch (state.value.plan) {
    case 'trial': return t('licence.planTrial')
    case 'annual': return t('licence.planAnnual')
    case 'lifetime': return t('licence.planLifetime')
    default: return t('licence.planFree')
  }
})

// 到期文案：永久不给「到期」；免费/无权益不显示
const entLabel = computed(() => {
  if (state.value.plan === 'free') return ''
  if (state.value.plan === 'lifetime' || !state.value.ent) return t('licence.expireNever')
  try {
    const d = new Date(state.value.ent * 1000)
      .toLocaleDateString(undefined, { year: 'numeric', month: '2-digit', day: '2-digit' })
    return t('licence.expireAt', { date: d })
  } catch {
    return String(state.value.ent)
  }
})

// 富文本 → 条目文本数组。后台「会员权益」是富文本（richtext），运营可能写成
// <ul><li> 列表、也可能一段段 <p>；这里按「有用什么就取什么」的优先级拆：
// li → 块级元素（p/div/标题）→ 整体纯文本按换行拆。只取文本，不带标签，
// 左栏是一条条带勾的小条目，不需要富文本排版。
function htmlToLines(html: string): string[] {
  const src = (html || '').trim()
  if (!src) return []
  const clean = (s: string) => s.replace(/^\s*[-*·•]\s*/, '').replace(/\s+/g, ' ').trim()
  // 纯文本（没有标签）：按行拆
  if (!/<[a-z][\s\S]*>/i.test(src)) {
    return src.split(/\r?\n/).map(clean).filter(Boolean)
  }
  try {
    const doc = new DOMParser().parseFromString(src, 'text/html')
    const lis = Array.from(doc.querySelectorAll('li'))
      .map((el) => clean(el.textContent || ''))
      .filter(Boolean)
    if (lis.length) return lis
    const blocks = Array.from(doc.body.querySelectorAll('p,div,h1,h2,h3,h4,h5,h6'))
      .map((el) => clean(el.textContent || ''))
      .filter(Boolean)
    if (blocks.length) return blocks
    return (doc.body.textContent || '').split(/\r?\n/).map(clean).filter(Boolean)
  } catch {
    return src.split(/\r?\n/).map(clean).filter(Boolean)
  }
}

// 会员特权 = 商品后台的「会员权益」（富文本）；没填则回落到「摘要」纯文本
// （官网后台一行一条）；都没有时给一条兜底文案，避免左栏只剩标题。
const benefits = computed<string[]>(() => {
  const fromRich = htmlToLines(goodsBenefits.value)
  if (fromRich.length) return fromRich
  const lines = (goodsSummary.value || '')
    .split(/\r?\n/)
    .map((s) => s.replace(/^\s*[-*·•]\s*/, '').trim())
    .filter(Boolean)
  return lines.length ? lines : [t('member.benefitsFallback')]
})

// 《用户协议》正文 = 商品后台的「会员协议」（富文本，服务端已净化）。
// 后台留空时回落到模板内置的默认条款（见模板里的 v-else 分支）。
const agreementHtml = computed<string>(() => (goodsAgreement.value || '').trim())

function applyState(s: StateShape) {
  state.value = s
}

async function refreshState() {
  try {
    applyState((await LicenceState()) as unknown as StateShape)
  } catch {
    /* 事件广播兜底 */
  }
}

async function loadProducts() {
  loading.value = true
  error.value = ''
  try {
    const r = await LicenceProducts()
    const resp = r as {
      goods?: { title?: string; summary?: string; benefits?: string; agreement?: string }
      skus?: typeof skus.value
    }
    goodsTitle.value = resp?.goods?.title || ''
    goodsSummary.value = resp?.goods?.summary || ''
    goodsBenefits.value = resp?.goods?.benefits || ''
    goodsAgreement.value = resp?.goods?.agreement || ''
    skus.value = resp?.skus || []
    if (skus.value.length && !selected.value) selected.value = skus.value[0].code
  } catch (e) {
    error.value = apiErrMsg(e)
  } finally {
    loading.value = false
  }
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

async function sendCode() {
  if (!canSend.value || sending.value) return
  error.value = ''
  sending.value = true
  try {
    await LicenceSendCode(mobile.value)
    startCountdown()
  } catch (e) {
    error.value = apiErrMsg(e) || t('licence.sendCodeFailed')
  } finally {
    sending.value = false
  }
}

async function login() {
  if (!canLogin.value || logging.value || !requireAgree()) return
  error.value = ''
  logging.value = true
  try {
    applyState((await LicenceLogin(mobile.value, code.value)) as unknown as StateShape)
    code.value = ''
    await loadProducts()
  } catch (e) {
    error.value = apiErrMsg(e) || t('licence.loginFailed')
  } finally {
    logging.value = false
  }
}

// 密码登录：POST /api/auth/login {mode:'password', username, password, ...}
async function loginByPassword() {
  if (!canPwdLogin.value || logging.value || !requireAgree()) return
  error.value = ''
  logging.value = true
  try {
    applyState((await LicencePasswordLogin(username.value.trim(), password.value)) as unknown as StateShape)
    password.value = ''
    await loadProducts()
  } catch (e) {
    error.value = apiErrMsg(e) || t('licence.loginFailed')
  } finally {
    logging.value = false
  }
}

async function logout() {
  try {
    await LicenceLogout()
  } catch { /* 状态广播会跟上 */ }
  resetOrder()
  // 套餐/权益保持展示（左栏未登录也渲染），只清支付相关状态
  await refreshState()
}

function pickPlan(code: string) {
  if (code === selected.value) return
  selected.value = code
  // 换套餐后旧支付码作废，回到「立即购买」
  if (order.value) resetOrder()
}

async function createOrder() {
  if (!selected.value || creating.value) return
  creating.value = true
  error.value = ''
  stopPoll()
  try {
    const r = await LicenceOrderCreate(selected.value, 1)
    const o = r as typeof order.value
    // 二维码不直连外链：pay.qr 是网关图 URL，前端 <img> 直连会受 CSP 拦截、
    // 断网白框。统一由 Go 侧拉取转本地 data URL。
    if (o?.pay?.type === 'qrcode' && o.pay.qr) {
      try {
        o.pay.qr = await LicenceFetchQR(o.pay.qr)
      } catch (e) {
        o.pay.qr = ''
        o.pay.msg = apiErrMsg(e)
      }
    }
    order.value = o
    paid.value = false
    pollFails = 0
    pollText.value = ''
    if (o?.pay?.type === 'redirect' && o.pay.pay_url) Browser.OpenURL(o.pay.pay_url)
    startPoll()
  } catch (e) {
    error.value = apiErrMsg(e) || t('licence.orderFailed')
  } finally {
    creating.value = false
  }
}

let pollTimer: ReturnType<typeof setInterval> | null = null
let pollFails = 0

function startPoll() {
  stopPoll()
  pollTimer = setInterval(() => void pollOnce(), 2500)
}

function stopPoll() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

async function pollOnce() {
  if (!order.value?.order_no) return
  try {
    const r = await LicenceOrderStatus(order.value.order_no)
    const s = r as { paid: boolean; status: string; entitlement?: { plan_text?: string; ent_text?: string } | null }
    pollFails = 0
    if (s.paid) {
      paid.value = true
      entText.value = [s.entitlement?.plan_text, s.entitlement?.ent_text].filter(Boolean).join(' · ')
      // 后端到账即刷已换过 token，这里再拉一次状态把问候条与档位更新掉
      await refreshState()
      stopPoll()
      return
    }
    if (s.status === 'closed') {
      resultClosed()
      return
    }
    pollText.value = t('licence.payWaiting')
  } catch {
    pollFails++
    if (pollFails >= 5) {
      stopPoll()
      pollText.value = t('licence.payPollFail')
    }
  }
}

function resultClosed() {
  stopPoll()
  order.value = null
  error.value = t('licence.payClosed')
}

function resetOrder() {
  stopPoll()
  order.value = null
  paid.value = false
  entText.value = ''
  pollText.value = ''
}

function openPayUrl() {
  const u = order.value?.pay?.pay_url
  if (u) Browser.OpenURL(u)
}

function fmtFen(fen: number): string {
  return '¥' + (fen / 100).toFixed(2)
}

function apiErrMsg(e: unknown): string {
  const s = e instanceof Error ? e.message : String(e ?? '')
  return s
}

function minimise() {
  void Window.Minimise()
}
function close() {
  void Window.Close()
}

onMounted(async () => {
  await refreshState()
  // 套餐与权益是公开数据（GET /api/order/product 已开放匿名），
  // 未登录也要把左栏「会员权益」和右栏套餐价目渲染出来。
  await loadProducts()
  Events.On('licence:state', (ev: unknown) => {
    const d = ev as StateShape | { data?: StateShape }
    const s = (d && typeof d === 'object' && 'loggedIn' in d) ? (d as StateShape) : (d as { data?: StateShape })?.data
    if (s) applyState(s)
  })
})

onBeforeUnmount(() => {
  stopPoll()
  wxStop()
  if (countdownTimer) clearInterval(countdownTimer)
})
</script>

<style scoped>
.mb-root {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  border-radius: var(--win-radius);
  overflow: hidden;
  background: var(--bg-base);
  /* Element Plus 的 primary 默认蓝，与 NAS 主窗的玫红 accent 不搭：
     在这里把主色指过去（按钮 / link / 选中态一次改完）。 */
  --el-color-primary: var(--accent);
  --el-color-primary-light-3: color-mix(in srgb, var(--accent) 72%, var(--bg-base));
  --el-color-primary-light-5: color-mix(in srgb, var(--accent) 52%, var(--bg-base));
  --el-color-primary-light-7: color-mix(in srgb, var(--accent) 32%, var(--bg-base));
  --el-color-primary-light-8: color-mix(in srgb, var(--accent) 22%, var(--bg-base));
  --el-color-primary-light-9: color-mix(in srgb, var(--accent) 12%, var(--bg-base));
  --el-color-primary-dark-2: color-mix(in srgb, var(--accent) 82%, #000);
}
.mb-bar {
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
.mb-brand {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  font-size: 0.8125rem;
  color: var(--text-primary);
  white-space: nowrap;
}
.brand-mark {
  width: 1rem;
  height: 1rem;
  border-radius: 0.25rem;
  -webkit-user-drag: none;
}
.mb-brand b { font-weight: 700; letter-spacing: 0.02em; }
.mb-drag { flex: 1; height: 100%; }
.mb-win {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.325rem;
  height: 1.325rem;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  flex-shrink: 0;
}
.mb-win svg { stroke: currentColor; fill: none; }
.mb-win:hover { background: var(--bg-surface); color: var(--text-primary); }
.mb-win.close:hover { background: #e81123; color: #fff; }

.mb-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 1rem 1.125rem 1.125rem;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
}
/* 两栏撑满可用高度：左权益卡随窗高到底，右栏协议行 margin-top:auto 贴底，
   未下单时也不会出现「上边一块内容 + 下边一大片黑」 */
.mb-main {
  display: grid;
  /* 左栏 14.5rem（M51 从 15.5rem 收窄）：权益列表文字 0.75rem 用不了那么宽，
     收出来给右栏的登录卡/套餐卡 —— 880 窗里右栏内容更密 */
  grid-template-columns: 14.5rem minmax(0, 1fr);
  gap: 1.125rem;
  align-items: stretch;
  flex: 1;
  min-height: 0;
}

/* ── 左：会员权益 ── */
.mb-benefits {
  background: var(--bg-elevated);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  padding: 1rem 1rem 1.125rem;
  align-self: stretch;
  /* 后台把「会员权益」写成几十条时左栏不能把窗撑破：内部滚动，标题固定 */
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow-y: auto;
}
.mb-benefits-title {
  margin: 0;
  font-size: 1.0625rem;
  font-weight: 800;
  letter-spacing: 0.04em;
  color: var(--accent);
}
.mb-benefits-sub {
  margin: 0.2rem 0 0;
  font-size: 0.6875rem;
  color: var(--text-muted);
}
.mb-hr {
  height: 1px;
  background: var(--border-subtle);
  margin: 0.75rem 0 0.625rem;
}
.mb-benefits-hd {
  font-size: 0.75rem;
  font-weight: 600;
  color: var(--text-secondary);
  margin-bottom: 0.5rem;
}
.mb-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  /* 行间距：0.4rem → 0.8rem（后台权益写 8 条时原来挤成一片，翻倍后每行可辨） */
  gap: 0.8rem;
}
.mb-list li {
  display: flex;
  align-items: flex-start;
  gap: 0.375rem;
  font-size: 0.75rem;
  line-height: 1.45;
  color: var(--text-primary);
}
.mb-list svg { flex: 0 0 auto; margin-top: 0.18rem; color: var(--accent); }

/* ── 右：账号 / 套餐 / 支付 ── */
.mb-right {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  min-width: 0;
}
.mb-hello {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.625rem 0.875rem;
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  border: 1px solid var(--border-subtle);
  font-size: 0.75rem;
  color: var(--text-primary);
}
.mb-hello-dot {
  width: 1.25rem;
  height: 1.25rem;
  flex: 0 0 1.25rem;
  border-radius: 50%;
  background: color-mix(in srgb, var(--accent) 18%, transparent);
  position: relative;
}
.mb-hello-dot::after {
  content: '';
  position: absolute;
  inset: 0.34rem;
  border-radius: 50%;
  background: var(--accent);
}
.mb-hello-tx { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.mb-hello-out { flex: 0 0 auto; }

.mb-plans-hd {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 0.75rem;
  font-size: 0.8125rem;
  font-weight: 600;
  color: var(--text-primary);
}
.mb-goods { font-size: 0.6875rem; font-weight: 400; color: var(--text-muted); }

.mb-plan-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.75rem;
}
.mb-plan {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 0.25rem;
  padding: 0.875rem 1rem;
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  color: var(--text-primary);
  font-family: inherit;
  text-align: left;
  cursor: pointer;
  transition: border-color 0.15s ease, background 0.15s ease;
}
.mb-plan:hover { border-color: color-mix(in srgb, var(--accent) 45%, var(--border-subtle)); }
.mb-plan.on {
  border-color: var(--accent);
  background: color-mix(in srgb, var(--accent) 9%, var(--bg-elevated));
  box-shadow: 0 0 0 1px var(--accent) inset;
}
.mb-radio {
  position: absolute;
  top: 0.75rem;
  right: 0.75rem;
  width: 0.625rem;
  height: 0.625rem;
  border-radius: 50%;
  border: 1.5px solid var(--text-muted);
}
.mb-radio.on { border-color: var(--accent); box-shadow: inset 0 0 0 2px var(--bg-elevated); background: var(--accent); }
.mb-plan-name { font-size: 0.8125rem; font-weight: 600; }
.mb-plan-price { font-size: 1.375rem; font-weight: 800; color: var(--accent); letter-spacing: 0.01em; }
.mb-plan-period { font-size: 0.6875rem; color: var(--text-secondary); }
.mb-plan-note { font-size: 0.6875rem; color: var(--text-muted); line-height: 1.4; }

.mb-buy-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
}
.mb-buy-hint { font-size: 0.6875rem; color: var(--text-muted); }
.mb-buy { min-width: 8rem; }

/* 支付卡 */
.mb-pay {
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  padding: 0.875rem 1rem 1rem;
}
.mb-pay-hd { font-size: 0.8125rem; font-weight: 600; color: var(--text-primary); margin-bottom: 0.75rem; }
.mb-pay-body { display: flex; gap: 1.125rem; align-items: flex-start; }
.mb-qr {
  width: 132px;
  height: 132px;
  flex: 0 0 132px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-md);
  background: #fff;
  overflow: hidden;
}
.mb-qr img { width: 100%; height: 100%; object-fit: contain; }
/* 未下单时的二维码位：虚线占位，说明点了「立即购买」才生成 */
.mb-qr-ph {
  background: var(--bg-surface);
  border: 1px dashed var(--border-subtle);
  color: var(--text-muted);
  font-size: 0.6875rem;
  line-height: 1.5;
  text-align: center;
  padding: 0 0.75rem;
  box-sizing: border-box;
}
.mb-qr-empty { font-size: 0.6875rem; color: #666; padding: 0 0.75rem; text-align: center; line-height: 1.5; }
.mb-pay-info { display: flex; flex-direction: column; gap: 0.25rem; min-width: 0; }
.mb-pay-label { font-size: 0.6875rem; color: var(--text-muted); }
.mb-pay-num { font-size: 1.5rem; font-weight: 800; color: var(--accent); letter-spacing: 0.01em; }
.mb-pay-btns { display: flex; align-items: center; gap: 0.5rem; margin-top: 0.25rem; }
.mb-pay-status { margin: 0.125rem 0 0; font-size: 0.6875rem; color: var(--accent); min-height: 1rem; }

/* 支付成功条 */
.mb-paid {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  border: 1px solid color-mix(in srgb, var(--el-color-success) 55%, var(--border-subtle));
  background: color-mix(in srgb, var(--el-color-success) 10%, var(--bg-elevated));
  border-radius: var(--radius-md);
  padding: 0.75rem 1rem;
}
.mb-paid-ico {
  width: 1.75rem;
  height: 1.75rem;
  flex: 0 0 1.75rem;
  border-radius: 50%;
  background: var(--el-color-success);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 1rem;
}
.mb-paid-tx { margin: 0; font-size: 0.8125rem; font-weight: 600; color: var(--text-primary); }
.mb-paid-sub { margin: 0.1rem 0 0; font-size: 0.6875rem; color: var(--text-muted); }
.mb-paid > :last-child { margin-left: auto; }

/* 未登录卡：与左栏等高（mb-right 被 grid 拉伸，卡片吃满），内容顶置、协议贴底 */
.mb-login-card {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: var(--bg-elevated);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  padding: 1.25rem 1.5rem 1.125rem;
}
/* 协议行：与上方内容拉开距离（吃掉剩余空间），横向居中 */
.mb-agree-row {
  margin-top: auto;
  padding-top: 0.875rem;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.4rem;
  font-size: 0.6875rem;
  color: var(--text-muted);
  cursor: pointer;
  user-select: none;
}
.mb-agree-cb { height: auto; }
.mb-agree-cb .el-checkbox__input.is-focus .el-checkbox__inner { border-color: var(--accent); }
.mb-agree-cb .el-checkbox__inner { border-radius: 50%; }
.mb-agree-cb .el-checkbox__input.is-checked .el-checkbox__inner {
  background: var(--accent);
  border-color: var(--accent);
}
.mb-agree-tx { white-space: nowrap; }
.mb-agree-link { color: var(--accent); cursor: pointer; }
.mb-agree-link:hover { text-decoration: underline; }
/* 已登录态的《会员协议》行：整行可点 */
.mb-agree-open { cursor: pointer; }
.mb-agree-open:hover { color: var(--accent); }

/* ── 用户协议遮罩窗（自绘，不用 el-dialog） ── */
.mb-mask {
  position: fixed;
  inset: 0;
  z-index: 2500;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 1.25rem;
  background: rgba(0, 0, 0, 0.5);
  border-radius: var(--win-radius);
}
.mb-agree-win {
  width: 100%;
  max-width: 560px;
  max-height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
  background: var(--bg-base);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  box-shadow: 0 18px 48px rgba(0, 0, 0, 0.45);
  overflow: hidden;
}
.mb-agree-hd {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  padding: 0.75rem 1rem;
  border-bottom: 1px solid var(--border-subtle);
  background: var(--bg-elevated);
  font-size: 0.875rem;
  color: var(--text-primary);
}
.mb-agree-x {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.375rem;
  height: 1.375rem;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
}
.mb-agree-x svg { stroke: currentColor; fill: none; }
.mb-agree-x:hover { background: var(--bg-surface); color: var(--text-primary); }
/* 正文：内容多时内部滚动（上下滚动条），标题栏固定 */
.mb-agree-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 0.875rem 1.125rem 1rem;
  font-size: 0.8125rem;
  line-height: 1.75;
  color: var(--text-primary);
  user-select: text;
}
.mb-agree-body p { margin: 0.35rem 0; }
.mb-agree-body p b { display: inline-block; margin-top: 0.4rem; color: var(--text-primary); }
/* 后台「会员协议」是富文本，带 h1-h4 / ul / 表格 / 图片，补齐这些标签的观感，
   否则 v-html 出来是一坨无间距的纯文本 */
.mb-agree-rich > :first-child { margin-top: 0; }
.mb-agree-rich :is(h1, h2, h3, h4, h5, h6) {
  margin: 0.85rem 0 0.35rem;
  font-size: 0.875rem;
  font-weight: 700;
  line-height: 1.5;
  color: var(--text-primary);
}
.mb-agree-rich :is(ul, ol) { margin: 0.35rem 0 0.5rem; padding-left: 1.15rem; }
.mb-agree-rich li { margin: 0.15rem 0; }
.mb-agree-rich :is(strong, b) { font-weight: 700; color: var(--text-primary); }
.mb-agree-rich :is(img, video) { max-width: 100%; height: auto; border-radius: var(--radius-sm, 6px); }
.mb-agree-rich a { color: var(--accent); text-decoration: none; }
.mb-agree-rich a:hover { text-decoration: underline; }
.mb-agree-rich blockquote {
  margin: 0.5rem 0;
  padding: 0.4rem 0.75rem;
  border-left: 2px solid var(--border-subtle);
  color: var(--text-secondary);
}
.mb-agree-rich table { width: 100%; border-collapse: collapse; margin: 0.5rem 0; font-size: 0.75rem; }
.mb-agree-rich :is(th, td) { border: 1px solid var(--border-subtle); padding: 0.3rem 0.45rem; text-align: left; }
.mb-agree-rich hr { border: 0; border-top: 1px solid var(--border-subtle); margin: 0.75rem 0; }
.mb-h2 { margin: 0 0 0.375rem; font-size: 1rem; font-weight: 700; color: var(--text-primary); }
.mb-sub { margin: 0 0 1rem; font-size: 0.75rem; line-height: 1.5; color: var(--text-muted); }
/* 登录方式切换：三段式小 Tab，选中态描边 + 品牌色 */
.mb-tabs {
  display: flex;
  gap: 0.375rem;
  margin-bottom: 1.375rem;
}
.mb-tab {
  flex: 1;
  padding: 0.4rem 0.25rem;
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-sm, 6px);
  background: var(--bg-surface);
  color: var(--text-secondary);
  font-family: inherit;
  font-size: 0.71875rem;
  cursor: pointer;
  transition: border-color 0.15s ease, color 0.15s ease, background 0.15s ease;
}
.mb-tab:hover { color: var(--text-primary); }
.mb-tab.on {
  border-color: var(--accent);
  color: var(--accent);
  background: color-mix(in srgb, var(--accent) 9%, var(--bg-surface));
  font-weight: 600;
}
.mb-pwd-hint { margin: -0.25rem 0 0.75rem; }
/* 微信扫码占位：等开放平台资质下发后换成真二维码 */
.mb-wechat-ph { width: 100%; height: 132px; flex: none; margin-bottom: 0.875rem; }
.mb-field { margin-bottom: 0.75rem; }
.mb-code { display: flex; gap: 0.5rem; align-items: stretch; }
.mb-code :deep(.el-input) { flex: 1; }
/* 「获取验证码」与输入框严格等高（el-input large = 40px） */
.mb-code :deep(.el-button) { white-space: nowrap; min-width: 7.5rem; height: 40px !important; }
.mb-block { width: 100%; }
/* 登录按钮与上方表单拉开间隙；高度与输入框一致（40px） */
.mb-login-btn { margin-top: 1.375rem; height: 40px !important; }
.mb-error { margin: 0 0 0.625rem; font-size: 0.75rem; color: var(--el-color-danger); }
.mb-fine { margin: 0; font-size: 0.6875rem; line-height: 1.5; color: var(--text-muted); }

/* ── 微信扫码登录 ── */
.mb-wechat { display: flex; flex-direction: column; align-items: center; gap: 0.625rem; }
.mb-qr-box {
  position: relative;
  width: 168px;
  height: 168px;
  border-radius: var(--radius-md);
  background: #fff;
  border: 1px solid var(--border-subtle);
  overflow: hidden;
  display: flex;
  align-items: center;
  justify-content: center;
}
.mb-qr-img { width: 100%; height: 100%; object-fit: contain; display: block; }
.mb-qr-mask {
  position: absolute;
  inset: 0;
  background: rgba(255, 255, 255, 0.94);
  display: flex;
  align-items: center;
  justify-content: center;
}
.mb-qr-refresh { --el-color-primary: var(--accent); }
.mb-wx-tip { text-align: center; }
/* 动态检测行：呼吸点 + 每秒跳动的计时（等待/检测次数/二维码剩余时间） */
.mb-wx-live {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.375rem;
  margin: 0.25rem 0 0;
  font-size: 0.6875rem;
  font-variant-numeric: tabular-nums;
  color: var(--text-muted);
}
.mb-wx-dot {
  width: 0.375rem;
  height: 0.375rem;
  border-radius: 50%;
  background: var(--accent);
  animation: mb-wx-pulse 1.2s ease-in-out infinite;
  flex: none;
}
@keyframes mb-wx-pulse {
  0%, 100% { opacity: 0.25; transform: scale(0.8); }
  50% { opacity: 1; transform: scale(1.15); }
}
/* 协议行推到右栏底部（右栏是 flex column 且被 grid 拉伸到整高） */
.mb-agree { margin-top: auto; padding-top: 0.5rem; }
.mb-loading,
.mb-empty {
  padding: 1.25rem 0;
  text-align: center;
  font-size: 0.8125rem;
  color: var(--text-muted);
}
.mb-empty { color: var(--el-color-danger); }

@media (max-width: 46rem) {
  .mb-main { grid-template-columns: minmax(0, 1fr); }
  .mb-benefits { align-self: auto; }
  .mb-hello-tx { white-space: normal; }
}
</style>
