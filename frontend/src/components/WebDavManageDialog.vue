<template>
  <el-dialog
    v-model="visible"
    :title="t('davm.title', { name: instanceName || instanceId })"
    width="56rem"
    top="7vh"
    class="davm-dialog"
    destroy-on-close
  >
    <!-- 连接信息卡：往哪挂载一目了然 -->
    <div class="davm-conn">
      <div class="davm-conn-row">
        <span class="davm-conn-label">{{ t('davm.connUrl') }}</span>
        <code class="davm-conn-url" :class="{ off: !conn?.running }">{{ conn?.url || '—' }}</code>
        <el-button size="small" text @click="copyUrl">{{ t('davm.copy') }}</el-button>
        <span v-if="conn && !conn.running" class="davm-conn-hint">{{ t('davm.needStart') }}</span>
      </div>
      <div class="davm-conn-hint">{{ t('davm.connHint') }}</div>
    </div>

    <div class="davm-bar">
      <div class="davm-bar-left">
        <el-button size="small" type="primary" @click="openCreate">+ {{ t('davm.create') }}</el-button>
        <span class="davm-hint">{{ t('davm.restartHint') }}</span>
      </div>
      <div class="davm-bar-right">
        <el-input v-model="filter" size="small" clearable :placeholder="t('davm.search')" class="davm-search" />
        <el-button size="small" text :loading="loading" @click="reload">{{ t('davm.refresh') }}</el-button>
      </div>
    </div>

    <el-table :data="filtered" size="small" height="40vh" :empty-text="t('davm.noShares')">
      <el-table-column prop="user" :label="t('davm.colUser')" min-width="120" show-overflow-tooltip />
      <el-table-column prop="name" :label="t('davm.colName')" min-width="100" show-overflow-tooltip>
        <template #default="{ row }">{{ row.name || '—' }}</template>
      </el-table-column>
      <el-table-column :label="t('davm.colPwd')" width="90">
        <template #default>{{ mask }}</template>
      </el-table-column>
      <el-table-column :label="t('davm.colStatus')" width="90">
        <template #default="{ row }">
          <span v-if="row.enabled" class="davm-on">{{ t('davm.enabled') }}</span>
          <span v-else class="davm-off">{{ t('davm.disabled') }}</span>
        </template>
      </el-table-column>
      <el-table-column prop="root" :label="t('davm.colRoot')" min-width="200" show-overflow-tooltip />
      <el-table-column :label="t('davm.colReadonly')" width="80" align="center">
        <template #default="{ row }">
          <span v-if="row.readonly" class="davm-ro">{{ t('davm.yes') }}</span>
          <span v-else class="davm-hint">{{ t('davm.no') }}</span>
        </template>
      </el-table-column>
      <el-table-column :label="t('davm.colActions')" width="160" align="right">
        <template #default="{ row }">
          <el-button size="small" text @click="openEdit(row)">{{ t('davm.edit') }}</el-button>
          <el-dropdown trigger="click" @command="(c: string) => onAction(c, row)">
            <el-button size="small" text>⋯</el-button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="browse">{{ t('davbrowse.browse') }}</el-dropdown-item>
                <el-dropdown-item command="password">{{ t('davm.changePwd') }}</el-dropdown-item>
                <el-dropdown-item command="toggle">{{ row.enabled ? t('davm.disable') : t('davm.enable') }}</el-dropdown-item>
                <el-dropdown-item command="delete" divided>{{ t('davm.delete') }}</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </template>
      </el-table-column>
    </el-table>

    <!-- 创建 / 编辑 -->
    <el-dialog v-model="formOpen" :title="editing ? t('davm.editTitle') : t('davm.createTitle')" width="32rem" append-to-body>
      <div class="davm-form">
        <div class="davm-row">
          <span class="davm-label">{{ t('davm.user') }}</span>
          <el-input v-model="form.user" size="small" placeholder="duihao" />
        </div>
        <div class="davm-row" v-if="!editing">
          <span class="davm-label">{{ t('davm.password') }}</span>
          <div class="davm-pick">
            <el-input v-model="form.password" size="small" />
            <el-button size="small" @click="form.password = randomPassword()">{{ t('davm.generate') }}</el-button>
          </div>
        </div>
        <div class="davm-row">
          <span class="davm-label">{{ t('davm.name') }}</span>
          <el-input v-model="form.name" size="small" :placeholder="t('davm.namePh')" maxlength="32" />
        </div>
        <div class="davm-row">
          <span class="davm-label">{{ t('davm.root') }}</span>
          <div class="davm-root">
            <el-input v-model="form.root" size="small" placeholder="D:/www/dav" />
            <el-button size="small" @click="onBrowse">{{ t('davm.browse') }}</el-button>
            <el-select
              v-model="sitePick"
              size="small"
              clearable
              :placeholder="t('davm.fromSite')"
              class="davm-site"
              @change="onSitePick"
            >
              <el-option v-for="s in siteRoots" :key="s.id" :label="`${s.name} · ${s.root}`" :value="s.root" />
            </el-select>
          </div>
        </div>
        <div class="davm-row">
          <span class="davm-label">{{ t('davm.readonly') }}</span>
          <el-switch v-model="form.readonly" size="small" />
        </div>
        <div class="davm-row" v-if="editing">
          <span class="davm-label">{{ t('davm.enabled') }}</span>
          <el-switch v-model="form.enabled" size="small" />
        </div>
        <div class="davm-row">
          <span class="davm-label">{{ t('davm.note') }}</span>
          <el-input v-model="form.note" size="small" maxlength="64" show-word-limit />
        </div>
        <div class="davm-hint">{{ t('davm.rootHint') }}</div>
      </div>
      <template #footer>
        <el-button size="small" @click="formOpen = false">{{ t('davm.cancel') }}</el-button>
        <el-button size="small" type="primary" :loading="busy" @click="submitForm">{{ t('davm.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- 改密码 -->
    <el-dialog v-model="pwdOpen" :title="t('davm.pwdTitle', { name: pwdTarget.user })" width="28rem" append-to-body>
      <div class="davm-form">
        <div class="davm-row">
          <span class="davm-label">{{ t('davm.password') }}</span>
          <div class="davm-pick">
            <el-input v-model="pwdTarget.password" size="small" />
            <el-button size="small" @click="pwdTarget.password = randomPassword()">{{ t('davm.generate') }}</el-button>
          </div>
        </div>
        <div class="davm-hint">{{ t('davm.pwdHint') }}</div>
      </div>
      <template #footer>
        <el-button size="small" @click="pwdOpen = false">{{ t('davm.cancel') }}</el-button>
        <el-button size="small" type="primary" :loading="busy" @click="doChangePwd">{{ t('davm.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- 共享内容浏览：不挂载也能看/传/删（预览文本、图片、PDF） -->
    <DavBrowseDialog
      v-if="browseShare"
      v-model:visible="browseOpen"
      :instance-id="instanceId"
      :share-id="browseShare.id"
      :share-user="browseShare.user"
      :share-name="browseShare.name"
      :readonly="!!browseShare.readonly"
    />
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from '../i18n'
import { OpenDirectoryDialog } from '../../bindings/github.com/ys-ll/uniterm/app'
import {
  ListWebDavShares, CreateWebDavShare, UpdateWebDavShare, SetWebDavSharePassword,
  DeleteWebDavShare, GetWebDavConnInfo, ListWebDavSiteRoots,
} from '../../bindings/github.com/ys-ll/uniterm/backend/duihao/service/webdavservice'
import DavBrowseDialog from './DavBrowseDialog.vue'

const props = defineProps<{ instanceId: string; instanceName?: string }>()
const visible = defineModel<boolean>('visible', { default: false })
const { t } = useI18n()

const loading = ref(false)
const busy = ref(false)
const filter = ref('')
const mask = '••••••'
const shares = ref<any[]>([])
const conn = ref<any>(null)
const siteRoots = ref<any[]>([])
const sitePick = ref('')

const filtered = computed(() => {
  const q = filter.value.trim().toLowerCase()
  if (!q) return shares.value
  return shares.value.filter((s: any) =>
    String(s.user || '').toLowerCase().includes(q) ||
    String(s.root || '').toLowerCase().includes(q) ||
    String(s.name || '').toLowerCase().includes(q))
})

const formOpen = ref(false)
const editing = ref(false)
const form = reactive({ id: '', user: '', password: '', name: '', root: '', readonly: false, enabled: true, note: '' })

// 共享内容浏览（预览 / 上传 / 删除）
const browseOpen = ref(false)
const browseShare = ref<any>(null)

const pwdOpen = ref(false)
const pwdTarget = reactive({ id: '', user: '', password: '' })

// immediate：父组件用 v-if 挂载，挂载时 visible 已为 true，不加就永远不自动加载。
watch(visible, (v) => { if (v) reload() }, { immediate: true })

async function reload() {
  if (!props.instanceId) return
  loading.value = true
  try {
    shares.value = (await ListWebDavShares(props.instanceId)) as unknown as any[]
    try { conn.value = await GetWebDavConnInfo(props.instanceId) } catch { conn.value = null }
    if (!siteRoots.value.length) {
      try { siteRoots.value = (await ListWebDavSiteRoots()) as unknown as any[] } catch { siteRoots.value = [] }
    }
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    loading.value = false
  }
}

function errText(e: unknown): string {
  if (e instanceof Error) return e.message
  return String(e).replace(/^Error:\s*/, '')
}

function randomPassword(): string {
  const chars = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789'
  let out = ''
  const buf = new Uint32Array(14)
  crypto.getRandomValues(buf)
  for (let i = 0; i < 14; i++) out += chars[buf[i] % chars.length]
  return out
}

async function copyUrl() {
  const url = String(conn.value?.url || '')
  if (!url) return
  try {
    await navigator.clipboard.writeText(url)
    ElMessage.success(t('davm.copied'))
  } catch {
    ElMessage.warning(url)
  }
}

function openCreate() {
  editing.value = false
  form.id = ''
  form.user = ''
  form.password = randomPassword()
  form.name = ''
  form.root = '{dir}/www'
  form.readonly = false
  form.enabled = true
  form.note = ''
  sitePick.value = ''
  formOpen.value = true
}

function openEdit(row: any) {
  editing.value = true
  form.id = row.id
  form.user = row.user
  form.password = ''
  form.name = row.name || ''
  form.root = row.root
  form.readonly = !!row.readonly
  form.enabled = !!row.enabled
  form.note = row.note || ''
  sitePick.value = ''
  formOpen.value = true
}

async function onBrowse() {
  try {
    const dir = String(await OpenDirectoryDialog(form.root && !form.root.includes('{') ? form.root : ''))
    if (dir) form.root = dir
  } catch {
    /* 用户取消 */
  }
}

function onSitePick(v: string) {
  if (v) form.root = v
}

async function submitForm() {
  busy.value = true
  try {
    if (editing.value) {
      await UpdateWebDavShare(props.instanceId, form.id, form.user, form.root, form.readonly, form.enabled, form.name, form.note)
    } else {
      await CreateWebDavShare(props.instanceId, form.user, form.password, form.root, form.readonly, form.name, form.note)
    }
    ElMessage.success(editing.value ? t('davm.saved') : t('davm.created'))
    formOpen.value = false
    await reload()
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

function onAction(cmd: string, row: any) {
  if (cmd === 'browse') {
    browseShare.value = row
    browseOpen.value = true
    return
  }
  if (cmd === 'password') {
    pwdTarget.id = row.id
    pwdTarget.user = row.user
    pwdTarget.password = randomPassword()
    pwdOpen.value = true
    return
  }
  if (cmd === 'toggle') return doToggle(row)
  if (cmd === 'delete') return doDelete(row)
}

async function doChangePwd() {
  busy.value = true
  try {
    await SetWebDavSharePassword(props.instanceId, pwdTarget.id, pwdTarget.password)
    ElMessage.success(t('davm.saved'))
    pwdOpen.value = false
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

async function doToggle(row: any) {
  busy.value = true
  try {
    await UpdateWebDavShare(props.instanceId, row.id, row.user, row.root, !!row.readonly, !row.enabled, row.name || '', row.note || '')
    ElMessage.success(t('davm.saved'))
    await reload()
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

async function doDelete(row: any) {
  try {
    await ElMessageBox.confirm(t('davm.deleteConfirm', { name: row.user }), t('davm.delete'), { type: 'warning' })
  } catch {
    return
  }
  busy.value = true
  try {
    await DeleteWebDavShare(props.instanceId, row.id)
    ElMessage.success(t('davm.deleted'))
    await reload()
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}
</script>

<style scoped>
.davm-conn { border: 1px solid var(--border-color); border-radius: 8px; padding: 0.5rem 0.75rem; margin-bottom: 0.75rem; background: var(--bg-subtle, rgba(127, 127, 127, 0.04)); }
.davm-conn-row { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; }
.davm-conn-label { font-size: 0.75rem; color: var(--text-secondary); flex-shrink: 0; }
.davm-conn-url { font-size: 0.8125rem; font-family: var(--font-mono, monospace); padding: 0.125rem 0.375rem; border-radius: 4px; background: rgba(127, 127, 127, 0.1); }
.davm-conn-url.off { opacity: 0.5; }
.davm-conn-hint { font-size: 0.6875rem; color: var(--text-muted); line-height: 1.5; margin-top: 0.25rem; }
.davm-bar { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; flex-wrap: wrap; margin-bottom: 0.5rem; }
.davm-bar-left, .davm-bar-right { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; }
.davm-search { width: 12rem; }
.davm-hint { font-size: 0.6875rem; color: var(--text-muted); line-height: 1.5; }
.davm-on { color: var(--success); font-size: 0.75rem; }
.davm-off { color: var(--text-disabled); font-size: 0.75rem; }
.davm-ro { color: var(--warning, #e6a23c); font-size: 0.75rem; }

.davm-form { display: flex; flex-direction: column; gap: 0.625rem; }
.davm-row { display: flex; align-items: center; gap: 0.75rem; }
.davm-label { width: 5rem; flex-shrink: 0; font-size: 0.75rem; color: var(--text-secondary); }
.davm-pick { flex: 1; display: flex; gap: 0.375rem; min-width: 0; }
.davm-root { flex: 1; display: flex; gap: 0.375rem; min-width: 0; flex-wrap: wrap; }
.davm-root :deep(.el-input) { flex: 1 1 12rem; }
.davm-site { width: 100%; }
</style>
