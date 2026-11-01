<template>
  <!-- 扫码快传：把共享的短时登录链接画成二维码，手机扫码自动登录后
       直接落在文件管理器页面（上传 / 下载 / 预览都是现成能力）。 -->
  <el-dialog
    v-model="visible"
    :title="t('davq.title', { user: user })"
    width="20rem"
    top="12vh"
    append-to-body
    destroy-on-close
  >
    <div class="davq-body">
      <p class="davq-hint">{{ t('davq.hint') }}</p>
      <div class="davq-qbox">
        <canvas v-show="ready" ref="cv" class="davq-canvas"></canvas>
        <span v-if="!ready" class="davq-loading">{{ busy ? t('davq.loading') : '—' }}</span>
      </div>
      <p class="davq-note">{{ t('davq.ttl', { min: ttlMin }) }}</p>
      <p class="davq-note">{{ t('davq.tls') }}</p>
      <div class="davq-acts">
        <el-button size="small" :loading="busy" @click="gen">{{ t('davq.refresh') }}</el-button>
      </div>
    </div>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import QRCode from 'qrcode'
import { ElMessage } from 'element-plus'
import { useI18n } from '../i18n'
import { CreateWebDavQuickToken } from '../../bindings/github.com/ys-ll/uniterm/backend/duihao/service/webdavservice'

const props = defineProps<{ instanceId: string; user: string }>()
const visible = defineModel<boolean>('visible', { default: false })
const { t } = useI18n()

const busy = ref(false)
const ready = ref(false)
const ttlMin = ref(5)
const cv = ref<HTMLCanvasElement | null>(null)

// immediate：父级 v-if 挂载时 visible 已为 true，不加 watch 永远不触发（工程已知坑）。
watch(visible, (v) => { if (v) gen() }, { immediate: true })

async function gen() {
  busy.value = true
  ready.value = false
  try {
    const info = (await CreateWebDavQuickToken(props.instanceId, props.user)) as unknown as {
      url: string; ttl: number
    }
    ttlMin.value = Math.max(1, Math.round((info.ttl || 300) / 60))
    await QRCode.toCanvas(cv.value, info.url, { width: 220, margin: 1 })
    ready.value = true
  } catch (e) {
    ElMessage.error(errText(e))
    visible.value = false
  } finally {
    busy.value = false
  }
}

function errText(e: unknown): string {
  if (e instanceof Error) return e.message
  return String(e).replace(/^Error:\s*/, '')
}
</script>

<style scoped>
.davq-body { display: flex; flex-direction: column; align-items: center; gap: 0.5rem; }
.davq-hint { margin: 0 0 0.25rem; font-size: 0.8rem; color: var(--text-secondary); text-align: center; }
.davq-qbox { display: flex; align-items: center; justify-content: center; min-height: 220px; padding: 0.5rem; border: 1px solid var(--border-color); border-radius: 8px; background: #fff; }
.davq-canvas { display: block; }
.davq-loading { font-size: 0.8rem; color: var(--text-muted); }
.davq-note { margin: 0; font-size: 0.6875rem; color: var(--text-muted); line-height: 1.5; text-align: center; }
.davq-acts { margin-top: 0.25rem; }
</style>
