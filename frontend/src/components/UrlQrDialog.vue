<template>
  <!-- 地址二维码（首屏「访问地址」下拉右侧的二维码按钮）：
       画的是**当前选中的那条地址**，手机扫码直接打开对号网盘（需登录）。
       与 DavQuickDialog 的区别：那个是带短时 token 的免密快传，这个是纯地址。 -->
  <el-dialog
    v-model="visible"
    :title="t('life.qrAddr')"
    width="18rem"
    top="12vh"
    append-to-body
    destroy-on-close
  >
    <div class="uqr">
      <p class="uqr-hint">{{ t('life.qrAddrHint') }}</p>
      <div class="uqr-box">
        <canvas ref="cv" class="uqr-canvas"></canvas>
      </div>
      <p class="uqr-url" :title="url">{{ url }}</p>
      <el-button size="small" @click="copy">{{ t('life.copy') }}</el-button>
    </div>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'
import QRCode from 'qrcode'
import { ElMessage } from 'element-plus'
import { useI18n } from '../i18n'

const props = defineProps<{ url: string }>()
const visible = defineModel<boolean>('visible', { default: false })
const { t } = useI18n()

const cv = ref<HTMLCanvasElement | null>(null)

// el-dialog 是懒渲染：先 nextTick 拿 canvas，拿到之前重试两次（同 M21 的坑）。
async function draw(tryLeft = 3) {
  if (!cv.value) {
    if (tryLeft > 0) setTimeout(() => draw(tryLeft - 1), 60)
    return
  }
  if (!props.url || props.url === '—') return
  try {
    await QRCode.toCanvas(cv.value, props.url, { width: 200, margin: 1 })
  } catch {
    /* 画不出来就只留下面的地址文本 */
  }
}

watch(visible, async (v) => {
  if (!v) return
  await nextTick()
  draw()
})

async function copy() {
  try {
    await navigator.clipboard.writeText(props.url)
    ElMessage.success(t('life.copied'))
  } catch {
    ElMessage.warning(props.url)
  }
}
</script>

<style scoped>
.uqr { display: flex; flex-direction: column; align-items: center; gap: 0.5rem; }
.uqr-hint { margin: 0 0 0.25rem; font-size: 0.75rem; color: var(--text-secondary); text-align: center; }
.uqr-box {
  display: flex; align-items: center; justify-content: center;
  min-height: 200px; padding: 0.5rem;
  border: 1px solid var(--border-color); border-radius: 8px; background: #fff;
}
.uqr-canvas { display: block; }
.uqr-url {
  margin: 0; max-width: 100%; font-family: var(--font-mono); font-size: 0.6875rem;
  color: var(--text-muted); word-break: break-all; text-align: center;
}
</style>
