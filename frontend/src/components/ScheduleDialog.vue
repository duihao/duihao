<template>
  <el-dialog
    v-model="visible"
    :title="t('sched.title')"
    width="52rem"
    draggable
    append-to-body
    :close-on-click-modal="false"
    @open="load"
  >
    <p class="sc-hint">{{ t('sched.hint') }}</p>

    <div v-if="!rows.length" class="sc-empty">{{ t('sched.empty') }}</div>

    <div v-for="(r, i) in rows" :key="i" class="sc-row">
      <div class="sc-main">
        <el-select v-model="r.kind" size="small" class="sc-kind" :disabled="busy">
          <el-option :label="t('sched.kindMysql')" value="backup-mysql" />
          <el-option :label="t('sched.kindRedis')" value="backup-redis" />
          <el-option :label="t('sched.kindLogs')" value="rotate-logs" />
        </el-select>

        <el-select
          v-model="r.instance" size="small" class="sc-inst"
          :disabled="busy || r.kind === 'rotate-logs'" clearable
          :placeholder="t('sched.all')"
        >
          <el-option v-for="iv in instanceOptions(r.kind)" :key="iv.id" :label="iv.label" :value="iv.id" />
        </el-select>

        <el-input v-model="r.schedule" size="small" class="sc-sched" :placeholder="t('sched.scheduleHint')" :disabled="busy" />

        <el-input-number v-model="r.keep" size="small" :min="0" :max="365" controls-position="right" class="sc-keep" :disabled="busy" />
        <span class="sc-unit">{{ t('sched.keepUnit') }}</span>

        <el-switch v-model="r.enabled" :disabled="busy" />
      </div>

      <div class="sc-meta">
        <span>{{ t('sched.lastRun') }}: {{ r.lastRun ? fmt(r.lastRun) : t('sched.never') }}</span>
        <span v-if="r.lastResult" class="sc-result" :class="{ 'sc-bad': !r.lastResult.startsWith('ok') }">{{ shortResult(r.lastResult) }}</span>
        <span v-if="r.nextRun">{{ t('sched.nextRun') }}: {{ r.nextRun }}</span>
        <span class="sc-grow" />
        <el-button size="small" text :disabled="busy" @click="runNow(r)">{{ t('sched.runNow') }}</el-button>
        <el-button size="small" text type="danger" :disabled="busy" @click="rows.splice(i, 1)">{{ t('sched.remove') }}</el-button>
      </div>
    </div>

    <template #footer>
      <el-button size="small" :disabled="busy" @click="addRow">{{ t('sched.add') }}</el-button>
      <span class="sc-flex" />
      <el-button size="small" @click="visible = false">{{ t('common.close') }}</el-button>
      <el-button size="small" type="primary" :loading="busy" @click="save">{{ t('sched.save') }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from '../i18n'
import { ListInstances } from '../../bindings/github.com/ys-ll/uniterm/backend/duihao/service/plugincenterservice'
import {
  ListScheduleTasks, SaveScheduleTasks, RunScheduleTask,
} from '../../bindings/github.com/ys-ll/uniterm/backend/duihao/service/scheduleservice'

interface TaskRow {
  id: string; kind: string; instance: string; schedule: string
  keep: number; enabled: boolean
  lastRun?: string; lastResult?: string; nextRun?: string
}
interface InstView { id: string; plugin: string; name: string; version: string }

const props = defineProps<{ visible: boolean }>()
const emit = defineEmits<{ (e: 'update:visible', v: boolean): void }>()
const { t } = useI18n()

const visible = ref(props.visible)
watch(() => props.visible, (v) => { visible.value = v })
watch(visible, (v) => emit('update:visible', v))

const rows = ref<TaskRow[]>([])
const instances = ref<InstView[]>([])
const busy = ref(false)

function instanceOptions(kind: string) {
  const plugin = kind === 'backup-mysql' ? 'mysql' : kind === 'backup-redis' ? 'redis' : ''
  if (!plugin) return []
  return instances.value
    .filter((x) => x.plugin === plugin)
    .map((x) => ({ id: x.id, label: `${x.name} ${x.version}` }))
}

function fmt(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
function shortResult(s: string): string {
  return s.length > 46 ? s.slice(0, 46) + '…' : s
}

async function load() {
  try {
    instances.value = (await ListInstances()) as unknown as InstView[]
    const list = (await ListScheduleTasks()) as unknown as TaskRow[]
    rows.value = list.map((x) => ({
      ...x,
      instance: x.instance || '',
      keep: x.keep || 0,
    }))
  } catch (e) {
    ElMessage.error(parseErr(e))
  }
}

function addRow() {
  rows.value.push({
    id: '', kind: 'backup-mysql', instance: '', schedule: 'daily:03:30', keep: 7, enabled: true,
  })
}

async function save() {
  busy.value = true
  try {
    await SaveScheduleTasks(rows.value as unknown as never)
    ElMessage.success(t('sched.saved'))
    await load()
  } catch (e) {
    ElMessage.error(parseErr(e))
  } finally {
    busy.value = false
  }
}

async function runNow(r: TaskRow) {
  if (!r.id) {
    ElMessage.warning(t('sched.saveFirst'))
    return
  }
  busy.value = true
  try {
    const msg = (await RunScheduleTask(r.id)) as unknown as string
    ElMessage.success(msg || 'ok')
    await load()
  } catch (e) {
    ElMessage.error(parseErr(e))
  } finally {
    busy.value = false
  }
}

function parseErr(e: unknown): string {
  if (e instanceof Error) return e.message
  try { return JSON.stringify(e) } catch { return String(e) }
}
</script>

<style scoped>
.sc-hint { margin: 0 0 0.625rem; font-size: 0.6875rem; color: var(--text-muted); line-height: 1.6; }
.sc-empty { padding: 1.5rem 0; text-align: center; font-size: 0.75rem; color: var(--text-muted); }
.sc-row {
  border: 1px solid var(--border-subtle); border-radius: var(--radius-md);
  background: var(--bg-elevated); padding: 0.5rem 0.625rem; margin-bottom: 0.5rem;
}
.sc-main { display: flex; align-items: center; gap: 0.375rem; flex-wrap: wrap; }
.sc-kind { width: 8.5rem; }
.sc-inst { width: 10rem; }
.sc-sched { width: 9rem; font-family: var(--font-mono); }
.sc-keep { width: 5.5rem; }
.sc-unit { font-size: 0.625rem; color: var(--text-muted); }
.sc-meta { margin-top: 0.375rem; display: flex; align-items: center; gap: 0.625rem; font-size: 0.625rem; color: var(--text-muted); flex-wrap: wrap; }
.sc-result { color: var(--success); }
.sc-bad { color: var(--error); }
.sc-grow { flex: 1 1 auto; }
.sc-flex { flex: 1 1 auto; }
</style>
