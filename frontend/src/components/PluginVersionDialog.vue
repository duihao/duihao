<template>
  <el-dialog
    v-model="show"
    :title="title"
    width="40rem"
    :close-on-click-modal="false"
    append-to-body
    class="pv-dialog"
  >
    <div class="pv-body">
      <div class="pv-hint">{{ t('plugins.versionsHint') }}</div>

      <div class="pv-list">
        <template v-for="row in rows" :key="row.version">
        <div
          class="pv-row"
          :class="{ 'pv-row-off': !row.inst }"
        >
          <span class="pv-dot" :class="dotClass(row.inst)" />
          <div class="pv-id">
            <span class="pv-ver">{{ row.version === 'builtin' ? t('plugins.builtin') : 'v' + row.version }}</span>
            <span v-if="row.isDefault" class="pv-tag pv-tag-on">{{ t('plugins.defaultVersion') }}</span>
            <button
              v-else-if="row.inst"
              class="pv-setdef"
              :title="t('market.setDefaultHint')"
              @click="onSetDefault(row)"
            >{{ t('market.setDefault') }}</button>
          </div>
          <span class="pv-ports">{{ row.inst ? portsText(row.inst) : '' }}</span>
          <span class="pv-state" :class="stateClass(row.inst)">{{ stateText(row.inst) }}</span>
          <div class="pv-ops">
            <template v-if="row.inst">
              <el-button
                size="small"
                plain
                class="pv-op-main"
                :type="row.inst.live === 'running' ? 'warning' : 'success'"
                :loading="busy === row.inst.id"
                @click="onToggle(row)"
              >{{ row.inst.live === 'running' ? t('plugins.stop') : t('plugins.start') }}</el-button>
              <el-button size="small" text class="pv-op" @click="emitSettings(row)">
                {{ t('plugins.settings') }}
              </el-button>
              <el-button size="small" text class="pv-op" @click="emitLogs(row)">
                {{ t('plugins.logs') }}
              </el-button>
              <el-button size="small" text type="danger" class="pv-op" @click="onRemove(row)">
                {{ t('plugins.removeInstance') }}
              </el-button>
            </template>
            <el-button
              v-else
              size="small"
              type="primary"
              plain
              class="pv-op-main"
              :loading="busy === 'new:' + row.version"
              @click="onInstall(row.version)"
            >{{ t('plugins.install') }}</el-button>
          </div>
        </div>
        <!-- 安装中：行下方显示取包进度（阶段/百分比/速度/重试），与市场卡片同款 -->
        <div v-if="row.version === installingVer && progFrame" class="pv-prog">
          <div class="pv-prog-head">
            <span class="pv-prog-phase">{{ phaseLabel(progFrame) }}</span>
            <span class="pv-prog-pct">{{ barPct(progFrame) }}</span>
          </div>
          <div class="pv-prog-bar"><i :class="{ 'is-indet': isIndet(progFrame) }" :style="{ width: barWidth(progFrame) }" /></div>
          <div class="pv-prog-meta">{{ sizeText(progFrame) }}</div>
        </div>
        </template>

        <div v-if="!rows.length" class="pv-empty">{{ t('plugins.noVersions') }}</div>
      </div>
    </div>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from '../i18n'
import { useRuntimeProgress } from '../composables/useRuntimeProgress'
import {
  CreateInstance, StartInstance, StopInstance, RemoveInstance,
} from '../../bindings/github.com/duihao/duihao/backend/duihao/service/plugincenterservice'

interface PluginView {
  id: string; name: string; kind: string; category: string; description: string
  versions: string[]; installedVersions: string[]; defaultVersion: string
  workers: number; hasInstance: boolean; multiRun: boolean
}
interface InstanceView {
  id: string; plugin: string; version: string; name: string
  ports: Record<string, number>; workerPorts?: Record<string, number[]>
  workers: number; state: string; runtimeDir: string
  live: string; pids: number[]; runtimeSource: string
}
interface Row { version: string; inst?: InstanceView; isDefault: boolean }

const props = defineProps<{
  plugin: PluginView | null
  instances: InstanceView[]
  /** 各软件当前选定的默认版本（插件 id → 版本）；缺省时退回清单 defaultVersion */
  defaults?: Record<string, string>
}>()
const emit = defineEmits<{
  (e: 'refresh'): void
  (e: 'installed', pluginId: string, version: string): void
  (e: 'default', pluginId: string, version: string): void
  (e: 'settings', inst: InstanceView): void
  (e: 'logs', inst: InstanceView): void
}>()

const show = defineModel<boolean>({ default: false })
const { t } = useI18n()
const busy = ref('')

// ---- 取包进度（与市场卡片共用 composable）----
const {
  progFor, phaseLabel, barPct, isIndet, barWidth, sizeText,
  startProgressPoll, stopProgressPoll,
} = useRuntimeProgress()
// 正在安装的版本（busy = 'new:<ver>'）→ 该行下方显示进度条
const installingVer = computed(() => (busy.value.startsWith('new:') ? busy.value.slice(4) : ''))
const progFrame = computed(() => (props.plugin ? progFor(props.plugin.id) : null))
// 弹窗打开期间才轮询；若打开时安装已在进行（从市场点开），第一拍就能拿到帧
watch(show, (v) => {
  if (v) startProgressPoll()
  else stopProgressPoll()
})

const title = computed(() =>
  `${props.plugin?.name || ''} · ${t('plugins.versions')}`)

// 行的来源：清单声明的全部版本 + 已装但清单里没有的版本（如内置 builtin 实例）
const rows = computed<Row[]>(() => {
  const p = props.plugin
  if (!p) return []
  const list = p.versions.slice()
  for (const i of props.instances) {
    if (!list.includes(i.version)) list.push(i.version)
  }
  return list.map(v => {
    const inst = props.instances.find(i => i.version === v)
    // 「默认」标签只挂在已安装的行上：未安装的版本不能当默认（左侧卡片也只会显示已装实例）
    return { version: v, inst, isDefault: !!inst && v === defaultOf(p) }
  })
})

// 默认版本：优先用用户在市场/本弹窗里选定的，其次清单声明的默认版本
function defaultOf(p: PluginView): string {
  return props.defaults?.[p.id] || p.defaultVersion
}

function onSetDefault(row: Row) {
  const p = props.plugin
  if (!p || !row.inst) return
  emit('default', p.id, row.version)
  ElMessage.success(t('market.defaultSet', { ver: row.version }))
}

function dotClass(inst?: InstanceView): string {
  if (!inst) return 'pv-dot-off'
  const map: Record<string, string> = {
    running: 'pv-dot-run', starting: 'pv-dot-wait', stopping: 'pv-dot-wait', error: 'pv-dot-err',
  }
  return map[inst.live] || 'pv-dot-off'
}
function stateText(inst?: InstanceView): string {
  if (!inst) return t('plugins.notInstalled')
  const map: Record<string, string> = {
    running: 'plugins.running',
    starting: 'plugins.stateStarting',
    stopping: 'plugins.stateStopping',
    error: 'plugins.stateError',
  }
  return t(map[inst.live] || 'plugins.stopped')
}
function stateClass(inst?: InstanceView): string {
  if (!inst) return 'pv-state-off'
  if (inst.live === 'running') return 'pv-state-run'
  if (inst.live === 'error') return 'pv-state-err'
  return ''
}
function portsText(inst: InstanceView): string {
  return Object.entries(inst.ports || {}).map(([k, v]) => `${k}:${v}`).join(' · ')
}

function emitSettings(row: Row) {
  if (row.inst) emit('settings', row.inst)
}
function emitLogs(row: Row) {
  if (row.inst) emit('logs', row.inst)
}

async function onInstall(ver: string) {
  const p = props.plugin
  if (!p) return
  busy.value = 'new:' + ver
  startProgressPoll()
  try {
    await CreateInstance(p.id, ver, '', [])
    ElMessage.success(t('plugins.instanceCreated'))
    emit('installed', p.id, ver)
    emit('refresh')
  } catch (e) {
    ElMessage.error(String(parseErr(e)))
  } finally {
    busy.value = ''
  }
}

async function onToggle(row: Row) {
  const inst = row.inst
  if (!inst) return
  busy.value = inst.id
  try {
    if (inst.live === 'running') await StopInstance(inst.id)
    else await StartInstance(inst.id)
    emit('refresh')
  } catch (e) {
    ElMessage.error(String(parseErr(e)))
    emit('refresh')
  } finally {
    busy.value = ''
  }
}

async function onRemove(row: Row) {
  const inst = row.inst
  if (!inst) return
  try {
    await ElMessageBox.confirm(t('plugins.removeConfirm', { id: inst.id }), { type: 'warning' })
  } catch {
    return
  }
  busy.value = inst.id
  try {
    await RemoveInstance(inst.id)
    ElMessage.success(t('plugins.instanceRemoved'))
    emit('refresh')
  } catch (e) {
    ElMessage.error(String(parseErr(e)))
  } finally {
    busy.value = ''
  }
}

function parseErr(e: unknown): string {
  if (e instanceof Error) return e.message
  try { return JSON.stringify(e) } catch { return String(e) }
}
</script>

<style scoped>
.pv-body { display: flex; flex-direction: column; gap: 0.625rem; }
.pv-hint { font-size: 0.6875rem; color: var(--text-muted); }
.pv-list { display: flex; flex-direction: column; gap: 0.375rem; }

/* 一行一个版本：灯 · 版本 · 端口 · 状态 · 操作 */
.pv-row {
  display: flex; align-items: center; gap: 0.625rem;
  padding: 0.5rem 0.625rem;
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
}
.pv-row-off { border-style: dashed; background: transparent; }
.pv-row:hover { border-color: var(--border-default); }

.pv-dot { width: 0.5rem; height: 0.5rem; border-radius: 50%; flex-shrink: 0; background: var(--text-disabled); }
.pv-dot-run { background: var(--success); box-shadow: 0 0 0 3px color-mix(in srgb, var(--success) 18%, transparent); }
.pv-dot-err { background: var(--error); }
.pv-dot-wait { background: var(--warning); }
.pv-dot-off { background: var(--text-disabled); }

.pv-id { display: flex; align-items: center; gap: 0.375rem; min-width: 6.25rem; }
.pv-ver { font-family: var(--font-mono); font-size: 0.75rem; color: var(--text-primary); white-space: nowrap; }
.pv-tag {
  font-size: 0.5625rem; padding: 0 0.25rem; border-radius: var(--radius-sm);
  color: var(--text-muted); border: 1px solid var(--border-subtle); white-space: nowrap;
}
.pv-tag-on { color: var(--accent); border-color: color-mix(in srgb, var(--accent) 42%, transparent); }
.pv-setdef {
  font: inherit; font-size: 0.5625rem; padding: 0 0.25rem; white-space: nowrap;
  border: 1px dashed var(--border-subtle); border-radius: var(--radius-sm);
  background: transparent; color: var(--text-muted); cursor: pointer;
  transition: color .12s, border-color .12s;
}
.pv-setdef:hover { color: var(--accent); border-color: var(--accent); }

.pv-ports { flex: 1 1 auto; min-width: 0; font-family: var(--font-mono); font-size: 0.6875rem; color: var(--text-muted);
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pv-state { flex-shrink: 0; width: 3.5rem; font-size: 0.6875rem; color: var(--text-secondary); }
.pv-state-run { color: var(--success); }
.pv-state-err { color: var(--error); }
.pv-state-off { color: var(--text-muted); }

.pv-ops { flex-shrink: 0; display: flex; align-items: center; gap: 0.125rem; }
.pv-op-main { min-width: 4.25rem; }
.pv-ops :deep(.el-button + .el-button) { margin-left: 0; }
.pv-op { padding: 0 0.375rem; }

.pv-empty { padding: 1rem 0; text-align: center; font-size: 0.6875rem; color: var(--text-muted); }

/* 安装中进度条（与市场卡片 .rp-prog 同款） */
.pv-prog {
  margin: -0.25rem 0 0.125rem; padding: 0.5rem 0.625rem 0.625rem;
  border: 1px dashed var(--border-subtle); border-top: none;
  border-radius: 0 0 var(--radius-md) var(--radius-md);
  background: var(--bg-elevated);
}
.pv-prog-head {
  display: flex; align-items: baseline; justify-content: space-between;
  font-size: 0.625rem; color: var(--text-muted); line-height: 1.4;
}
.pv-prog-phase { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pv-prog-pct { flex: 0 0 auto; margin-left: 0.5rem; color: var(--accent); font-variant-numeric: tabular-nums; }
.pv-prog-bar {
  margin-top: 0.25rem; height: 4px; border-radius: 2px; overflow: hidden;
  background: var(--bg-hover);
}
.pv-prog-bar > i {
  display: block; height: 100%; width: 0; border-radius: 2px;
  background: linear-gradient(90deg, var(--accent), #ff8ab5);
  transition: width 0.35s ease;
}
/* 服务端没给 Content-Length（CDN 流式响应）时走不定态：一块扫光代替百分比 */
.pv-prog-bar > i.is-indet { animation: pv-prog-indet 1.1s linear infinite; }
.pv-prog-meta {
  margin-top: 0.25rem; font-size: 0.5625rem; color: var(--text-muted);
  font-variant-numeric: tabular-nums; letter-spacing: 0.01em;
}
@keyframes pv-prog-indet {
  0% { transform: translateX(-100%); }
  100% { transform: translateX(285%); }
}

@media (max-width: 720px) {
  .pv-row { flex-wrap: wrap; }
  .pv-ports { flex-basis: 100%; order: 5; }
}
</style>
