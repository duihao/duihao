<template>
  <!-- 到点提醒卡片（语音提醒 V2）：屏幕右上角一张小卡，替代系统 toast ——
       系统 toast 不能带「完成 / 延迟」这种回调按钮，也不能保证在 Windows
       各版本上样式一致（Win10 与 Win11 差很多），所以自己画。
       窗口本体是 Frameless + 透明（见 notify_window.go），圆角/阴影在这里。 -->
  <div class="nt-card" :class="{ leaving }">
    <div class="nt-avatar" aria-hidden="true">
      <svg viewBox="0 0 24 24" width="20" height="20" fill="none"
           stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round">
        <path d="M4.5 12.5 9.5 17.5 19.5 6.5" />
      </svg>
    </div>

    <div class="nt-main">
      <p class="nt-title">{{ title || t('notify.title') }}</p>
      <p class="nt-body">{{ body }}</p>
      <p v-if="err" class="nt-err">{{ err }}</p>
    </div>

    <div class="nt-acts">
      <button class="nt-btn done" :disabled="busy" @click="act('done')">{{ t('notify.done') }}</button>
      <button class="nt-btn snooze" :disabled="busy" :title="snoozeTip" @click="act('snooze')">
        {{ t('notify.snooze') }}
      </button>
    </div>

    <!-- 自动收起倒计时：让用户知道这张卡会自己走，不是弹窗卡死 -->
    <span class="nt-bar" :style="{ animationDuration: autoClose + 's' }" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NotifyAction } from '../bindings/github.com/ys-ll/uniterm/app'
import { useI18n } from './i18n'

const { t } = useI18n()

const q = new URLSearchParams(window.location.search)
const title = q.get('t') || ''
const body = q.get('c') || ''
const snoozeMin = Number(q.get('sn') || 5) || 5
const autoClose = Number(q.get('ac') || 45) || 45

const busy = ref(false)
const leaving = ref(false)
const err = ref('')

const snoozeTip = computed(() => t('notify.snoozeTip', { min: String(snoozeMin) }))

async function act(kind: 'done' | 'snooze' | 'close') {
  if (busy.value) return
  busy.value = true
  try {
    const r = await NotifyAction(kind)
    // Go 侧会关窗；关窗前先播一个退场动画，不然卡片是「啪」地消失。
    if (r === 'ok' || r === 'closed' || r === '') {
      leaving.value = true
    }
  } catch (e) {
    err.value = e instanceof Error ? e.message : String(e)
    busy.value = false
  }
}

onMounted(() => {
  document.documentElement.classList.add('win-notify')
  // X / Esc 等同「收起」：不改变提醒本身，只是把卡片关掉。
  window.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') void act('close')
  })
})
</script>

<style scoped>
.nt-card {
  position: relative;
  box-sizing: border-box;
  display: flex;
  align-items: center;
  gap: 0.625rem;
  height: 100vh;
  padding: 0.75rem 0.75rem 0.75rem 0.875rem;
  border: 1px solid var(--border-color);
  border-radius: 14px;
  background: var(--bg-elevated);
  box-shadow: 0 16px 40px rgba(0, 0, 0, 0.32), 0 2px 6px rgba(0, 0, 0, 0.18);
  overflow: hidden;
  /* 整张卡可拖（无边框窗口没有标题栏），按钮单独设 no-drag。 */
  --wails-draggable: drag;
  animation: nt-in 220ms cubic-bezier(0.22, 0.61, 0.36, 1);
}

.nt-card.leaving {
  animation: nt-out 160ms ease-in forwards;
}

@keyframes nt-in {
  from { opacity: 0; transform: translateX(18px) scale(0.98); }
  to { opacity: 1; transform: translateX(0) scale(1); }
}
@keyframes nt-out {
  to { opacity: 0; transform: translateX(12px) scale(0.98); }
}

.nt-avatar {
  flex: none;
  width: 2.75rem;
  height: 2.75rem;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #fff;
  background: linear-gradient(140deg, #ff5fa2, #f0247e 55%, #c8155f);
  box-shadow: 0 2px 8px rgba(240, 36, 126, 0.35);
}

.nt-main {
  flex: 1;
  min-width: 0;
}

.nt-title {
  margin: 0;
  font-size: 0.875rem;
  font-weight: 650;
  line-height: 1.35;
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.nt-body {
  margin: 0.15rem 0 0;
  font-size: 0.75rem;
  line-height: 1.45;
  color: var(--text-secondary);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  word-break: break-word;
}

.nt-err {
  margin: 0.2rem 0 0;
  font-size: 0.6875rem;
  color: #ff6b6b;
}

.nt-acts {
  flex: none;
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
  --wails-draggable: no-drag;
}

.nt-btn {
  width: 3.75rem;
  height: 1.6875rem;
  border-radius: 8px;
  font-size: 0.75rem;
  font-weight: 550;
  cursor: pointer;
  transition: background 120ms ease, border-color 120ms ease;
}

/* 「完成」走品牌玫红：提醒是「品牌时刻」，不是专业模式的功能蓝交互 */
.nt-btn.done {
  border: 1px solid transparent;
  background: var(--brand, #f0247e);
  color: var(--on-accent, #fff);
}
.nt-btn.done:hover { filter: brightness(1.08); }

.nt-btn.snooze {
  border: 1px solid color-mix(in srgb, var(--brand, #f0247e) 40%, transparent);
  background: transparent;
  color: var(--text-secondary);
}
.nt-btn.snooze:hover {
  border-color: var(--brand, #f0247e);
  color: var(--brand, #f0247e);
}

.nt-btn:disabled { opacity: 0.55; cursor: default; }

/* 底部倒计时条：宽度 45s 走完，视觉上告诉用户「它会自己消失」 */
.nt-bar {
  position: absolute;
  left: 0.75rem;
  right: 0.75rem;
  bottom: 0.3rem;
  height: 2px;
  border-radius: 2px;
  background: var(--brand, #f0247e);
  opacity: 0.75;
  transform-origin: left center;
  animation-name: nt-count;
  animation-timing-function: linear;
  animation-fill-mode: forwards;
}
@keyframes nt-count {
  from { transform: scaleX(1); }
  to { transform: scaleX(0); }
}
</style>
