<template>
  <el-dialog
    v-model="visible"
    :title="t('rdm.title', { name: instanceName || instanceId })"
    width="60rem"
    top="6vh"
    class="rdm-dialog"
    destroy-on-close
  >
    <!-- 概览（右侧并入实例启停，不再单占标题栏下的一整行） -->
    <div class="rdm-top">
      <div class="rdm-cards">
        <div class="rdm-card">
          <span class="rdm-k">{{ t('rdm.version') }}</span>
          <span class="rdm-v">{{ ov.version || '—' }}</span>
        </div>
        <div class="rdm-card">
          <span class="rdm-k">{{ t('rdm.uptime') }}</span>
          <span class="rdm-v">{{ fmtUptime(ov.uptimeSeconds) }}</span>
        </div>
        <div class="rdm-card">
          <span class="rdm-k">{{ t('rdm.memory') }}</span>
          <span class="rdm-v">{{ ov.usedMemoryHuman || '—' }}</span>
        </div>
        <div class="rdm-card">
          <span class="rdm-k">{{ t('rdm.clients') }}</span>
          <span class="rdm-v">{{ ov.connectedClients }}</span>
        </div>
        <div class="rdm-card">
          <span class="rdm-k">{{ t('rdm.keys') }}</span>
          <span class="rdm-v">{{ ov.totalKeys }}</span>
        </div>
      </div>
      <InstancePower class="mng-inline-end" :instance-id="instanceId" @changed="onPowerChanged" />
    </div>

    <el-tabs v-model="tab" class="rdm-tabs">
      <!-- 键空间 -->
      <el-tab-pane :label="t('rdm.tabKeys')" name="keys">
        <div class="rdm-bar">
          <div class="rdm-bar-left">
            <el-select v-model="db" size="small" class="rdm-db" @change="onSwitchDb">
              <el-option
                v-for="d in ov.dbs" :key="d.index"
                :label="`db${d.index} · ${d.keys}`" :value="d.index"
              />
            </el-select>
            <el-input
              v-model="pattern" size="small" class="rdm-pattern"
              :placeholder="t('rdm.patternHint')" @keyup.enter="reloadKeys(true)"
            />
            <el-button size="small" :loading="loading" @click="reloadKeys(true)">{{ t('rdm.search') }}</el-button>
          </div>
          <div class="rdm-bar-right">
            <el-button size="small" :loading="busy" @click="doBackup">{{ t('rdm.backup') }}</el-button>
            <el-button size="small" type="danger" plain :loading="busy" @click="doFlush">{{ t('rdm.flush') }}</el-button>
          </div>
        </div>

        <el-table :data="keys" size="small" height="40vh" :empty-text="t('rdm.noKeys')">
          <el-table-column prop="key" :label="t('rdm.colKey')" min-width="200" show-overflow-tooltip />
          <el-table-column prop="type" :label="t('rdm.colType')" width="90" />
          <el-table-column :label="t('rdm.colTtl')" width="90">
            <template #default="{ row }">{{ row.ttl < 0 ? t('rdm.noExpire') : row.ttl + 's' }}</template>
          </el-table-column>
          <el-table-column prop="size" :label="t('rdm.colSize')" width="80" />
          <el-table-column prop="preview" :label="t('rdm.colPreview')" min-width="180" show-overflow-tooltip />
          <el-table-column :label="t('rdm.colActions')" width="140" align="right">
            <template #default="{ row }">
              <el-button size="small" text @click="openDetail(row)">{{ t('rdm.view') }}</el-button>
              <el-button size="small" text type="danger" @click="doDelete(row)">{{ t('rdm.delete') }}</el-button>
            </template>
          </el-table-column>
        </el-table>
        <div class="rdm-more" v-if="cursor">
          <el-button size="small" text :loading="loading" @click="reloadKeys(false)">{{ t('rdm.loadMore') }}</el-button>
        </div>
      </el-tab-pane>

      <!-- 快照 -->
      <el-tab-pane :label="t('rdm.tabBackup')" name="backup">
        <div class="rdm-bar">
          <el-button size="small" :loading="busy" @click="doBackup">{{ t('rdm.backup') }}</el-button>
          <span class="rdm-hint">{{ t('rdm.backupHint') }}</span>
        </div>
        <el-table :data="backups" size="small" height="40vh" :empty-text="t('rdm.noBackup')">
          <el-table-column prop="name" :label="t('rdm.colFile')" min-width="240" show-overflow-tooltip />
          <el-table-column :label="t('rdm.colFileSize')" width="100">
            <template #default="{ row }">{{ fmtBytes(row.size) }}</template>
          </el-table-column>
          <el-table-column :label="t('rdm.colTime')" width="160">
            <template #default="{ row }">{{ fmtTime(row.createdAt) }}</template>
          </el-table-column>
          <el-table-column :label="t('rdm.colActions')" width="90" align="right">
            <template #default="{ row }">
              <el-button size="small" text type="danger" @click="doDeleteBackup(row)">{{ t('rdm.delete') }}</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>
    </el-tabs>

    <!-- 键详情 -->
    <el-dialog v-model="detailOpen" :title="t('rdm.detailTitle', { key: detail.key })" width="38rem" append-to-body>
      <div class="rdm-detail-head">
        <el-tag size="small" effect="plain">{{ detail.type }}</el-tag>
        <span class="rdm-hint">TTL: {{ detail.ttl < 0 ? t('rdm.noExpire') : detail.ttl + 's' }}</span>
        <span class="rdm-hint">{{ t('rdm.colSize') }}: {{ detail.size }}</span>
        <span v-if="detail.truncated" class="rdm-hint rdm-warn">{{ t('rdm.truncated') }}</span>
      </div>
      <pre class="rdm-value">{{ detail.value || (detail.members || []).join('\n') }}</pre>
      <template #footer>
        <el-button size="small" @click="detailOpen = false">{{ t('rdm.close') }}</el-button>
      </template>
    </el-dialog>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, reactive, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from '../i18n'
import {
  RedisOverview, RedisScanKeys, RedisGetKey, RedisDeleteKey, RedisFlushDB,
  RedisBackup, ListRedisBackups, DeleteRedisBackup,
} from '../../bindings/github.com/ys-ll/uniterm/backend/duihao/service/redisservice'
import InstancePower from './InstancePower.vue'

const props = defineProps<{ instanceId: string; instanceName?: string }>()
const visible = defineModel<boolean>('visible', { default: false })
const { t } = useI18n()

const tab = ref('keys')
const loading = ref(false)
const busy = ref(false)
const db = ref(0)
const pattern = ref('*')
const keys = ref<any[]>([])
const cursor = ref(0)
const backups = ref<any[]>([])

const ov = reactive({
  version: '', uptimeSeconds: 0, usedMemoryHuman: '', connectedClients: 0, totalKeys: 0, databases: 16, dbs: [] as any[],
})

const detailOpen = ref(false)
const detail = reactive({ key: '', type: '', ttl: 0, size: 0, value: '', members: [] as string[], truncated: false })

// immediate：父组件用 v-if 挂载，挂载时 visible 已为 true，不加就永远不自动连。
watch(visible, (v) => { if (v) reloadAll() }, { immediate: true })

// 顶部启停后：等进程真正起停完成再刷一次（否则拿到的还是旧状态）
function onPowerChanged() {
  window.setTimeout(() => { void reloadAll() }, 700)
}

async function reloadAll() {
  if (!props.instanceId) return
  try {
    const o: any = await RedisOverview(props.instanceId)
    Object.assign(ov, o || {})
  } catch (e) {
    ElMessage.error(errText(e))
    return
  }
  await reloadKeys(true)
  try { backups.value = (await ListRedisBackups(props.instanceId)) as unknown as any[] } catch { backups.value = [] }
}

function errText(e: unknown): string {
  if (e instanceof Error) return e.message
  return String(e).replace(/^Error:\s*/, '')
}

async function reloadKeys(reset: boolean) {
  if (!props.instanceId) return
  loading.value = true
  try {
    const start = reset ? 0 : cursor.value
    const r: any = await RedisScanKeys(props.instanceId, db.value, start, pattern.value || '*', 100)
    const list = (r?.keys || []) as any[]
    keys.value = reset ? list : keys.value.concat(list)
    cursor.value = Number(r?.cursor || 0)
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    loading.value = false
  }
}

function onSwitchDb() {
  keys.value = []
  cursor.value = 0
  reloadKeys(true)
}

async function openDetail(row: any) {
  try {
    const d: any = await RedisGetKey(props.instanceId, db.value, row.key)
    Object.assign(detail, d || {}, { members: d?.members || [] })
    detailOpen.value = true
  } catch (e) {
    ElMessage.error(errText(e))
  }
}

async function doDelete(row: any) {
  try {
    await ElMessageBox.confirm(t('rdm.deleteConfirm', { key: row.key }), t('rdm.delete'), { type: 'warning' })
  } catch {
    return
  }
  try {
    await RedisDeleteKey(props.instanceId, db.value, row.key)
    ElMessage.success(t('rdm.deleted'))
    keys.value = keys.value.filter(k => k.key !== row.key)
    const o: any = await RedisOverview(props.instanceId)
    Object.assign(ov, o || {})
  } catch (e) {
    ElMessage.error(errText(e))
  }
}

async function doFlush() {
  try {
    await ElMessageBox.confirm(t('rdm.flushConfirm', { db: db.value }), t('rdm.flush'), {
      type: 'warning', confirmButtonText: t('rdm.flush'), cancelButtonText: t('rdm.cancel'),
    })
  } catch {
    return
  }
  busy.value = true
  try {
    await RedisFlushDB(props.instanceId, db.value)
    ElMessage.success(t('rdm.flushed'))
    keys.value = []
    cursor.value = 0
    const o: any = await RedisOverview(props.instanceId)
    Object.assign(ov, o || {})
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

async function doBackup() {
  busy.value = true
  try {
    await RedisBackup(props.instanceId)
    ElMessage.success(t('rdm.backuped'))
    backups.value = (await ListRedisBackups(props.instanceId)) as unknown as any[]
    tab.value = 'backup'
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

async function doDeleteBackup(row: any) {
  try {
    await ElMessageBox.confirm(t('rdm.deleteConfirmFile', { file: row.name }), t('rdm.delete'), { type: 'warning' })
  } catch {
    return
  }
  try {
    await DeleteRedisBackup(props.instanceId, row.path)
    ElMessage.success(t('rdm.deleted'))
    backups.value = (await ListRedisBackups(props.instanceId)) as unknown as any[]
  } catch (e) {
    ElMessage.error(errText(e))
  }
}

function fmtBytes(n: number): string {
  const b = Number(n) || 0
  if (b < 1024) return b + ' B'
  if (b < 1024 * 1024) return (b / 1024).toFixed(1) + ' KB'
  if (b < 1024 * 1024 * 1024) return (b / 1024 / 1024).toFixed(1) + ' MB'
  return (b / 1024 / 1024 / 1024).toFixed(2) + ' GB'
}

function fmtUptime(sec: number): string {
  const s = Number(sec) || 0
  if (!s) return '—'
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (d) return `${d}d ${h}h`
  if (h) return `${h}h ${m}m`
  return `${m}m`
}

function fmtTime(v: string): string {
  if (!v) return '—'
  return String(v).replace('T', ' ').slice(0, 19)
}
</script>

<style scoped>
.rdm-top { display: flex; align-items: center; gap: 0.5rem; margin-bottom: 0.75rem; }
.rdm-cards { flex: 1 1 auto; display: grid; grid-template-columns: repeat(5, minmax(0, 1fr)); gap: 0.5rem; }
.rdm-card {
  border: 1px solid var(--border-subtle); border-radius: var(--radius-md);
  background: var(--bg-elevated); padding: 0.5rem 0.625rem;
  display: flex; flex-direction: column; gap: 0.125rem; min-width: 0;
}
.rdm-k { font-size: 0.625rem; color: var(--text-muted); }
.rdm-v { font-size: 0.8125rem; font-weight: 600; color: var(--text-primary); font-family: var(--font-mono);
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

.rdm-bar { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; flex-wrap: wrap; margin-bottom: 0.5rem; }
.rdm-bar-left, .rdm-bar-right { display: flex; align-items: center; gap: 0.375rem; flex-wrap: wrap; }
.rdm-db { width: 8.5rem; }
.rdm-pattern { width: 13rem; }
.rdm-hint { font-size: 0.6875rem; color: var(--text-muted); }
.rdm-warn { color: var(--warning); }
.rdm-more { display: flex; justify-content: center; margin-top: 0.375rem; }
.rdm-tabs :deep(.el-tabs__header) { margin-bottom: 0.5rem; }

.rdm-detail-head { display: flex; align-items: center; gap: 0.625rem; margin-bottom: 0.5rem; flex-wrap: wrap; }
.rdm-value {
  margin: 0; padding: 0.625rem; max-height: 46vh; overflow: auto;
  background: var(--bg-surface); border: 1px solid var(--border-subtle); border-radius: var(--radius-sm);
  font-family: var(--font-mono); font-size: 0.6875rem; color: var(--text-secondary);
  white-space: pre-wrap; word-break: break-all;
}
</style>
