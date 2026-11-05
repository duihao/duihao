<template>
  <el-dialog
    v-model="visible"
    :title="t('ftpm.title', { name: instanceName || instanceId })"
    width="54rem"
    top="7vh"
    class="ftpm-dialog"
    destroy-on-close
  >
    <div class="ftpm-bar">
      <div class="ftpm-bar-left">
        <InstancePower :instance-id="instanceId" @changed="onPowerChanged" />
        <el-button size="small" type="primary" @click="openCreate">+ {{ t('ftpm.create') }}</el-button>
        <span class="ftpm-hint">{{ t('ftpm.restartHint') }}</span>
      </div>
      <div class="ftpm-bar-right">
        <el-input v-model="filter" size="small" clearable :placeholder="t('ftpm.search')" class="ftpm-search" />
        <el-button size="small" text :loading="loading" @click="reload">{{ t('ftpm.refresh') }}</el-button>
      </div>
    </div>

    <el-table :data="filtered" size="small" height="44vh" :empty-text="t('ftpm.noAccounts')">
      <el-table-column prop="user" :label="t('ftpm.colUser')" min-width="130" show-overflow-tooltip />
      <el-table-column :label="t('ftpm.colPwd')" width="90">
        <template #default>{{ mask }}</template>
      </el-table-column>
      <el-table-column :label="t('ftpm.colStatus')" width="100">
        <template #default="{ row }">
          <span v-if="row.enabled" class="ftpm-on">{{ t('ftpm.enabled') }}</span>
          <span v-else class="ftpm-off">{{ t('ftpm.disabled') }}</span>
        </template>
      </el-table-column>
      <el-table-column prop="root" :label="t('ftpm.colRoot')" min-width="220" show-overflow-tooltip />
      <el-table-column prop="note" :label="t('ftpm.colNote')" min-width="120" show-overflow-tooltip />
      <el-table-column :label="t('ftpm.colActions')" width="160" align="right">
        <template #default="{ row }">
          <template v-if="row.id === 'legacy'">
            <span class="ftpm-hint">{{ t('ftpm.legacy') }}</span>
          </template>
          <template v-else>
            <el-button size="small" text @click="openEdit(row)">{{ t('ftpm.edit') }}</el-button>
            <el-dropdown trigger="click" @command="(c) => onAction(c, row)">
              <el-button size="small" text>⋯</el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item command="password">{{ t('ftpm.changePwd') }}</el-dropdown-item>
                  <el-dropdown-item command="toggle">{{ row.enabled ? t('ftpm.disable') : t('ftpm.enable') }}</el-dropdown-item>
                  <el-dropdown-item command="delete" divided>{{ t('ftpm.delete') }}</el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </template>
        </template>
      </el-table-column>
    </el-table>

    <!-- 创建 / 编辑 -->
    <el-dialog v-model="formOpen" :title="editing ? t('ftpm.editTitle') : t('ftpm.createTitle')" width="32rem" append-to-body>
      <div class="ftpm-form">
        <div class="ftpm-row">
          <span class="ftpm-label">{{ t('ftpm.user') }}</span>
          <el-input v-model="form.user" size="small" placeholder="duihao" />
        </div>
        <div class="ftpm-row" v-if="!editing">
          <span class="ftpm-label">{{ t('ftpm.password') }}</span>
          <div class="ftpm-pick">
            <el-input v-model="form.password" size="small" />
            <el-button size="small" @click="form.password = randomPassword()">{{ t('ftpm.generate') }}</el-button>
          </div>
        </div>
        <div class="ftpm-row">
          <span class="ftpm-label">{{ t('ftpm.root') }}</span>
          <div class="ftpm-root">
            <el-input v-model="form.root" size="small" placeholder="{dir}/www" />
            <el-button size="small" @click="onBrowse">{{ t('ftpm.browse') }}</el-button>
            <el-select
              v-model="sitePick"
              size="small"
              clearable
              :placeholder="t('ftpm.fromSite')"
              class="ftpm-site"
              @change="onSitePick"
            >
              <el-option v-for="s in siteRoots" :key="s.id" :label="`${s.name} · ${s.root}`" :value="s.root" />
            </el-select>
          </div>
        </div>
        <div class="ftpm-row">
          <span class="ftpm-label">{{ t('ftpm.readonly') }}</span>
          <el-switch v-model="form.readonly" size="small" />
        </div>
        <div class="ftpm-row" v-if="editing">
          <span class="ftpm-label">{{ t('ftpm.enabled') }}</span>
          <el-switch v-model="form.enabled" size="small" />
        </div>
        <div class="ftpm-row">
          <span class="ftpm-label">{{ t('ftpm.note') }}</span>
          <el-input v-model="form.note" size="small" maxlength="64" show-word-limit />
        </div>
        <div class="ftpm-hint">{{ t('ftpm.rootHint') }}</div>
      </div>
      <template #footer>
        <el-button size="small" @click="formOpen = false">{{ t('ftpm.cancel') }}</el-button>
        <el-button size="small" type="primary" :loading="busy" @click="submitForm">{{ t('ftpm.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- 改密码 -->
    <el-dialog v-model="pwdOpen" :title="t('ftpm.pwdTitle', { name: pwdTarget.user })" width="28rem" append-to-body>
      <div class="ftpm-form">
        <div class="ftpm-row">
          <span class="ftpm-label">{{ t('ftpm.password') }}</span>
          <div class="ftpm-pick">
            <el-input v-model="pwdTarget.password" size="small" />
            <el-button size="small" @click="pwdTarget.password = randomPassword()">{{ t('ftpm.generate') }}</el-button>
          </div>
        </div>
        <div class="ftpm-hint">{{ t('ftpm.pwdHint') }}</div>
      </div>
      <template #footer>
        <el-button size="small" @click="pwdOpen = false">{{ t('ftpm.cancel') }}</el-button>
        <el-button size="small" type="primary" :loading="busy" @click="doChangePwd">{{ t('ftpm.save') }}</el-button>
      </template>
    </el-dialog>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from '../i18n'
import { OpenDirectoryDialog } from '../../bindings/github.com/duihao/duihao/app'
import InstancePower from './InstancePower.vue'
import {
  ListFTPAccounts, CreateFTPAccount, UpdateFTPAccount, SetFTPAccountPassword,
  DeleteFTPAccount, ListFTPSiteRoots,
} from '../../bindings/github.com/duihao/duihao/backend/duihao/service/ftpservice'

const props = defineProps<{ instanceId: string; instanceName?: string }>()
const visible = defineModel<boolean>('visible', { default: false })
const { t } = useI18n()

const loading = ref(false)
const busy = ref(false)
const filter = ref('')
const mask = '••••••'
const accounts = ref<any[]>([])
const siteRoots = ref<any[]>([])
const sitePick = ref('')

const filtered = computed(() => {
  const q = filter.value.trim().toLowerCase()
  if (!q) return accounts.value
  return accounts.value.filter(a =>
    String(a.user || '').toLowerCase().includes(q) || String(a.root || '').toLowerCase().includes(q))
})

const formOpen = ref(false)
const editing = ref(false)
const form = reactive({ id: '', user: '', password: '', root: '', readonly: false, enabled: true, note: '' })

const pwdOpen = ref(false)
const pwdTarget = reactive({ id: '', user: '', password: '' })

// immediate：父组件用 v-if 挂载，挂载时 visible 已为 true，不加就永远不自动加载账号表。
watch(visible, (v) => { if (v) reload() }, { immediate: true })

// 顶部启停后：等进程真正起停完成再刷一次（否则拿到的还是旧状态）
function onPowerChanged() {
  window.setTimeout(() => { void reload() }, 700)
}

async function reload() {
  if (!props.instanceId) return
  loading.value = true
  try {
    accounts.value = (await ListFTPAccounts(props.instanceId)) as unknown as any[]
    if (!siteRoots.value.length) {
      try { siteRoots.value = (await ListFTPSiteRoots()) as unknown as any[] } catch { siteRoots.value = [] }
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

function openCreate() {
  editing.value = false
  form.id = ''
  form.user = ''
  form.password = randomPassword()
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
      await UpdateFTPAccount(props.instanceId, form.id, form.user, form.root, form.note, form.readonly, form.enabled)
    } else {
      await CreateFTPAccount(props.instanceId, form.user, form.password, form.root, form.readonly, form.note)
    }
    ElMessage.success(editing.value ? t('ftpm.saved') : t('ftpm.created'))
    formOpen.value = false
    await reload()
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

function onAction(cmd: string, row: any) {
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
    await SetFTPAccountPassword(props.instanceId, pwdTarget.id, pwdTarget.password)
    ElMessage.success(t('ftpm.saved'))
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
    await UpdateFTPAccount(props.instanceId, row.id, row.user, row.root, row.note || '', !!row.readonly, !row.enabled)
    ElMessage.success(t('ftpm.saved'))
    await reload()
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

async function doDelete(row: any) {
  try {
    await ElMessageBox.confirm(t('ftpm.deleteConfirm', { name: row.user }), t('ftpm.delete'), { type: 'warning' })
  } catch {
    return
  }
  busy.value = true
  try {
    await DeleteFTPAccount(props.instanceId, row.id)
    ElMessage.success(t('ftpm.deleted'))
    await reload()
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}
</script>

<style scoped>
.ftpm-bar { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; flex-wrap: wrap; margin-bottom: 0.5rem; }
.ftpm-bar-left, .ftpm-bar-right { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; }
.ftpm-search { width: 12rem; }
.ftpm-hint { font-size: 0.6875rem; color: var(--text-muted); line-height: 1.5; }
.ftpm-on { color: var(--success); font-size: 0.75rem; }
.ftpm-off { color: var(--text-disabled); font-size: 0.75rem; }

.ftpm-form { display: flex; flex-direction: column; gap: 0.625rem; }
.ftpm-row { display: flex; align-items: center; gap: 0.75rem; }
.ftpm-label { width: 5rem; flex-shrink: 0; font-size: 0.75rem; color: var(--text-secondary); }
.ftpm-pick { flex: 1; display: flex; gap: 0.375rem; min-width: 0; }
.ftpm-root { flex: 1; display: flex; gap: 0.375rem; min-width: 0; flex-wrap: wrap; }
.ftpm-root :deep(.el-input) { flex: 1 1 12rem; }
.ftpm-site { width: 100%; }
</style>
