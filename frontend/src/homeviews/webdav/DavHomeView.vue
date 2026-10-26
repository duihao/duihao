<template>
  <div class="dh-home">
    <div class="dh-brand">
      <div class="dh-title">{{ t('davhome.title') }}</div>
      <div class="dh-slogan">{{ t('davhome.sub') }}</div>
    </div>

    <!-- 未创建实例：空状态 -->
    <div v-if="!inst" class="dh-empty">
      <p class="dh-empty-title">{{ t('davhome.notInstalled') }}</p>
      <p class="dh-empty-desc">{{ t('davhome.mountTip') }}</p>
      <button class="dh-btn primary" :disabled="busy" @click="createInst">{{ t('davhome.create') }}</button>
    </div>

    <!-- 实例状态卡 -->
    <div v-else class="dh-card">
      <div class="dh-status-row">
        <span class="dh-dot" :class="running ? 'on' : 'off'"></span>
        <span class="dh-status">{{ running ? t('davhome.running') : t('davhome.stopped') }}</span>
        <span v-if="conn?.url" class="dh-url" :title="conn.url">{{ conn.url }}</span>
      </div>
      <div class="dh-actions">
        <button class="dh-btn primary" :disabled="busy" @click="openDrive" v-if="running">{{ t('davhome.open') }}</button>
        <button class="dh-btn" :disabled="busy" @click="running ? stop() : start()">{{ running ? t('davhome.stop') : t('davhome.start') }}</button>
        <button class="dh-btn" :disabled="busy" @click="manageOpen = true">{{ t('davhome.manage') }}</button>
        <button v-if="conn?.url" class="dh-btn ghost" :disabled="busy" @click="copyUrl">{{ t('davhome.copyUrl') }}</button>
      </div>
      <p class="dh-tip">{{ t('davhome.mountTip') }}</p>
    </div>

    <WebDavManageDialog
      v-if="inst"
      v-model:visible="manageOpen"
      :instance-id="inst.id"
      :instance-name="inst.name || ''"
      @saved="refresh"
    />
  </div>
</template>

<script setup lang="ts">
// DavHomeView：「对号网盘」专有版首页（M19）。
// 网盘 = 唯一主角：状态 + 打开/启停/管理。样式与主应用一致（黑白灰）。
import { ref, computed, onMounted } from 'vue'
import { useI18n } from '../../i18n'
import { Browser } from '@wailsio/runtime'
import { ElMessage } from 'element-plus'
import WebDavManageDialog from '../../components/WebDavManageDialog.vue'
import { ListPlugins, ListInstances, CreateInstance, StartInstance, StopInstance } from '../../../bindings/github.com/ys-ll/uniterm/backend/duihao/service/plugincenterservice'
import { GetWebDavConnInfo } from '../../../bindings/github.com/ys-ll/uniterm/backend/duihao/service/webdavservice'

const { t } = useI18n()

interface InstanceView {
  id: string; plugin: string; version: string; name: string
  ports: Record<string, number>; state: string; live: string
}
const inst = ref<InstanceView | null>(null)
const conn = ref<any>(null)
const busy = ref(false)
const manageOpen = ref(false)

const running = computed(() => inst.value?.live === 'running')

async function refresh() {
  try {
    const list = await ListInstances()
    const mine = (list as any[]).filter((i: any) => i.plugin === 'webdav')
    inst.value = mine[0] || null
    if (inst.value) {
      try {
        conn.value = await GetWebDavConnInfo(inst.value.id)
      } catch { conn.value = null }
    }
  } catch { /* 启动早期 store 未就绪：静默，保持空态 */ }
}

async function createInst() {
  if (busy.value) return
  busy.value = true
  try {
    const plugins = await ListPlugins()
    const p = (plugins as any[]).find((x: any) => x.id === 'webdav')
    await CreateInstance('webdav', p?.defaultVersion || '', '', [])
    await refresh()
  } catch (e: any) {
    ElMessage.error(String(e?.message || e))
  } finally {
    busy.value = false
  }
}

async function start() {
  if (!inst.value || busy.value) return
  busy.value = true
  try {
    await StartInstance(inst.value.id)
    await refresh()
  } catch (e: any) {
    ElMessage.error(String(e?.message || e))
  } finally {
    busy.value = false
  }
}

async function stop() {
  if (!inst.value || busy.value) return
  busy.value = true
  try {
    await StopInstance(inst.value.id)
    await refresh()
  } catch (e: any) {
    ElMessage.error(String(e?.message || e))
  } finally {
    busy.value = false
  }
}

function openDrive() {
  if (conn.value?.url) Browser.OpenURL(conn.value.url)
}

async function copyUrl() {
  if (!conn.value?.url) return
  try {
    await navigator.clipboard.writeText(conn.value.url)
    ElMessage.success(t('davhome.copied'))
  } catch {
    ElMessage.warning(conn.value.url)
  }
}

onMounted(refresh)
</script>

<style scoped>
.dh-home {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  padding-top: 4rem;
  overflow-y: auto;
}
.dh-brand {
  text-align: center;
  margin-bottom: 2.5rem;
  user-select: none;
}
.dh-title {
  font-size: 1.75rem;
  font-weight: 700;
  color: var(--text-primary);
  letter-spacing: 0.125rem;
}
.dh-slogan {
  margin-top: 0.5rem;
  font-size: 0.8125rem;
  color: var(--text-muted);
}
.dh-empty,
.dh-card {
  width: min(34rem, 90%);
  background: var(--bg-surface);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  padding: 1.5rem 1.75rem;
}
.dh-empty {
  text-align: center;
}
.dh-empty-title {
  font-size: 0.9375rem;
  color: var(--text-primary);
  margin-bottom: 0.375rem;
}
.dh-empty-desc {
  font-size: 0.8125rem;
  color: var(--text-muted);
  margin-bottom: 1.25rem;
}
.dh-status-row {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.dh-dot {
  width: 0.5rem;
  height: 0.5rem;
  border-radius: 50%;
  flex-shrink: 0;
}
.dh-dot.on { background: #34c77b; }
.dh-dot.off { background: var(--text-disabled); }
.dh-status {
  font-size: 0.875rem;
  font-weight: 600;
  color: var(--text-primary);
}
.dh-url {
  margin-left: auto;
  font-size: 0.75rem;
  color: var(--text-muted);
  font-family: var(--font-mono, monospace);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 55%;
}
.dh-actions {
  display: flex;
  gap: 0.625rem;
  margin-top: 1.25rem;
  flex-wrap: wrap;
}
.dh-btn {
  height: 2rem;
  padding: 0 0.9375rem;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border-subtle);
  background: var(--bg-elevated);
  color: var(--text-primary);
  font-size: 0.8125rem;
  cursor: pointer;
  user-select: none;
}
.dh-btn:hover { border-color: var(--border-hover); }
.dh-btn.primary {
  background: var(--text-primary);
  border-color: var(--text-primary);
  color: var(--bg-surface);
}
.dh-btn.primary:hover { opacity: 0.88; }
.dh-btn.ghost {
  background: transparent;
}
.dh-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.dh-tip {
  margin-top: 1rem;
  font-size: 0.75rem;
  color: var(--text-disabled);
}
</style>
