<template>
  <!-- 实例启停控件（M20 补）：管理界面头部直接用，不必回到卡片上启动/停止。
       状态从 ListInstances() 拉当前实例的 live；启停后回调 changed，让面板
       自行刷新（数据库/FTP/网盘 的连接信息都依赖实例运行态）。 -->
  <span class="ipow">
    <span class="ipow-dot" :class="dotClass" :title="liveText"></span>
    <el-button
      size="small"
      :type="running ? 'warning' : 'primary'"
      plain
      :loading="busy || transitional"
      :disabled="busy || transitional"
      @click="toggle"
    >{{ running ? t('plugins.stop') : t('plugins.start') }}</el-button>
  </span>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from '../i18n'
import { ListInstances, StartInstance, StopInstance } from '../../bindings/github.com/duihao/duihao/backend/duihao/service/plugincenterservice'

const props = defineProps<{ instanceId: string }>()
const emit = defineEmits<{ (e: 'changed', running: boolean): void }>()
const { t } = useI18n()

const live = ref('')
const busy = ref(false)

const running = computed(() => live.value === 'running')
const transitional = computed(() => live.value === 'starting' || live.value === 'stopping')
const dotClass = computed(() => {
  if (running.value) return 'on'
  if (transitional.value) return 'wait'
  return 'off'
})
const liveText = computed(() => (running.value ? t('plugins.running') : t('plugins.stopped')))

function errText(e: unknown): string {
  if (e instanceof Error) return e.message
  return String(e).replace(/^Error:\s*/, '')
}

async function refresh() {
  try {
    const list: any[] = (await ListInstances()) || []
    const inst = list.find(x => x && x.id === props.instanceId)
    if (inst) live.value = String(inst.live || inst.state || '')
  } catch {
    // 状态灯失败不影响面板本身，静默
  }
}

async function toggle() {
  busy.value = true
  const isStart = !running.value
  try {
    if (isStart) await StartInstance(props.instanceId)
    else await StopInstance(props.instanceId)
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
    await refresh()
    emit('changed', running.value)
  }
}

onMounted(refresh)
defineExpose({ refresh })
</script>

<style scoped>
.ipow {
  display: inline-flex;
  align-items: center;
  gap: 0.375rem;
  flex-shrink: 0;
  /* 管理窗顶栏里是拖动区，按钮必须不吃拖动 */
  --wails-draggable: no-drag;
}
.ipow-dot {
  width: 0.4375rem;
  height: 0.4375rem;
  border-radius: 50%;
  flex-shrink: 0;
}
.ipow-dot.on { background: var(--success, #67c23a); }
.ipow-dot.off { background: var(--text-disabled, #c0c4cc); }
.ipow-dot.wait { background: var(--warning, #e6a23c); }
</style>
