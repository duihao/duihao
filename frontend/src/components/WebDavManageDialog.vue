<template>
  <el-dialog
    v-model="visible"
    :title="t('davm.title', { name: instanceName || instanceId })"
    :width="standalone ? '100%' : '56rem'"
    :top="standalone ? '0' : '7vh'"
    :modal="!standalone"
    :show-close="!standalone"
    :close-on-click-modal="!standalone"
    :lock-scroll="!standalone"
    :draggable="!standalone"
    :class="['davm-dialog', { 'davm-standalone': standalone }]"
    :style="standalone ? standaloneBoxStyle : undefined"
    :body-class="standalone ? 'davm-body-plain' : ''"
    destroy-on-close
  >
    <!-- 标题栏：弹窗模式 = 标题 + 右上「独立窗口打开」（M20 分离入口）；
         standalone = ManageTitleBar 自绘顶栏（拖动/最小化/关闭）——顶栏随弹窗
         本体走，不存在被遮罩层盖住的问题。 -->
    <template #header="{ titleId, titleClass }">
      <ManageTitleBar
        v-if="standalone"
        panel="webdav"
        :title="t('davm.title', { name: instanceName || instanceId })"
      />
      <div v-else class="davm-head">
        <span :id="titleId" :class="titleClass">{{ t('davm.title', { name: instanceName || instanceId }) }}</span>
        <el-button size="small" text class="davm-detach" @click="detach">⧉ {{ t('mw.detach') }}</el-button>
      </div>
    </template>
    <!-- 连接信息卡：往哪访问一目了然。地址本身可点 = 用系统默认浏览器打开。
         实例启停放在这一行右端（原先单占一行太旷）。 -->
    <div class="davm-conn">
      <div class="davm-conn-row">
        <span class="davm-conn-label">{{ t('davm.connUrl') }}</span>
        <a
          v-if="conn?.running && conn?.url"
          class="davm-conn-url davm-conn-link"
          :href="conn.url"
          :title="t('davm.openExternal')"
          @click.prevent="openExternal"
        >{{ conn.url }}</a>
        <code v-else class="davm-conn-url" :class="{ off: !conn?.running }">{{ conn?.url || '—' }}</code>
        <el-button size="small" text @click="copyUrl">{{ t('davm.copy') }}</el-button>
        <span v-if="conn && !conn.running" class="davm-conn-hint davm-conn-hint-inline">{{ t('davm.needStart') }}</span>
        <InstancePower class="mng-inline-end" :instance-id="instanceId" @changed="onPowerChanged" />
      </div>
      <div class="davm-conn-hint">{{ t('davm.connHint') }}</div>
    </div>

    <!-- NAS 账号：管理员 + 自动创建的默认共享。密码可查看（本机密文解出）。 -->
    <div v-if="hub" class="davm-conn davm-hub">
      <div class="davm-conn-row">
        <span class="davm-conn-label">{{ t('davm.hubTitle') }}</span>
        <code class="davm-conn-url">{{ hub.adminUid }}</code>
        <code class="davm-conn-url davm-pass" :class="{ off: !hubShow.admin }">{{ hubShow.admin ? (hub.adminPass || '—') : mask }}</code>
        <el-button size="small" text @click="hubShow.admin = !hubShow.admin">{{ hubShow.admin ? t('davm.hubHide') : t('davm.hubShow') }}</el-button>
        <el-button size="small" text @click="copyText(hub.adminPass)">{{ t('davm.copy') }}</el-button>
      </div>
      <div v-if="hub.shareUser" class="davm-conn-row">
        <span class="davm-conn-label">{{ t('davm.hubShare') }}</span>
        <code class="davm-conn-url">{{ hub.shareUser }}</code>
        <code class="davm-conn-url davm-pass" :class="{ off: !hubShow.share }">{{ hubShow.share ? (hub.sharePass || '—') : mask }}</code>
        <el-button size="small" text @click="hubShow.share = !hubShow.share">{{ hubShow.share ? t('davm.hubHide') : t('davm.hubShow') }}</el-button>
        <el-button size="small" text @click="copyText(hub.sharePass)">{{ t('davm.copy') }}</el-button>
      </div>
      <div class="davm-conn-hint">{{ t('davm.hubHint') }}</div>
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

    <el-table
      :data="filtered"
      size="small"
      :height="standalone ? '100%' : '40vh'"
      class="davm-table"
      :empty-text="t('davm.noShares')"
    >
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
                <el-dropdown-item command="quick" :disabled="!row.enabled">{{ t('davq.action') }}</el-dropdown-item>
                <el-dropdown-item command="password">{{ t('davm.changePwd') }}</el-dropdown-item>
                <el-dropdown-item command="toggle">{{ row.enabled ? t('davm.disable') : t('davm.enable') }}</el-dropdown-item>
                <el-dropdown-item command="delete" divided :disabled="row.id === firstShareId" :title="row.id === firstShareId ? t('davm.firstLocked') : ''">{{ t('davm.delete') }}</el-dropdown-item>
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
          <el-input v-model="form.user" size="small" placeholder="admin" />
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

    <!-- 扫码快传：手机扫码自动登录该共享 -->
    <DavQuickDialog
      v-if="quickUser"
      v-model:visible="quickOpen"
      :instance-id="instanceId"
      :user="quickUser"
    />
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Browser } from '@wailsio/runtime'
import { useI18n } from '../i18n'
import { OpenDirectoryDialog, OpenManageWindow } from '../../bindings/github.com/ys-ll/uniterm/app'
import {
  ListWebDavShares, CreateWebDavShare, UpdateWebDavShare, SetWebDavSharePassword,
  DeleteWebDavShare, GetWebDavConnInfo, ListWebDavSiteRoots, HubAccountInfo,
} from '../../bindings/github.com/ys-ll/uniterm/backend/duihao/service/webdavservice'
import DavBrowseDialog from './DavBrowseDialog.vue'
import DavQuickDialog from './DavQuickDialog.vue'
import ManageTitleBar from './ManageTitleBar.vue'
import InstancePower from './InstancePower.vue'

const props = defineProps<{ instanceId: string; instanceName?: string; standalone?: boolean }>()

// standalone（独立窗口）里的弹窗外壳：全靠内联样式撑满内容区。
// 内联样式优先级最高，不会被 Element Plus 自带的 margin-top / 圆角 / 阴影 /
// 16px 内边距压过去（之前用 class + !important 的写法在打包后没生效，导致
// 弹窗只按内容高度收尾、底下露出一条带阴影的空区）。
const standaloneBoxStyle: Record<string, string> = {
  margin: '0',
  width: '100%',
  height: '100%',
  maxHeight: 'none',
  minHeight: '0',
  padding: '0',
  borderRadius: '0',
  boxShadow: 'none',
  background: 'var(--bg-elevated)',
  display: 'flex',
  flexDirection: 'column',
  overflow: 'hidden'
}
const visible = defineModel<boolean>('visible', { default: false })
const { t } = useI18n()

const loading = ref(false)
const busy = ref(false)
const filter = ref('')
const mask = '••••••'
const shares = ref<any[]>([])
// 第一个创建的共享账号（CreatedAt 最早，空值视为最早）——后端禁止删除，
// 这里同步把删除入口置灰，用户不用点了才知道。
const firstShareId = computed(() => {
  const list = shares.value || []
  if (!list.length) return ''
  const earlier = (a: string, b: string) => (!a ? !!b : !!b && a < b)
  let best = list[0]
  for (const sh of list) {
    if (earlier(sh.createdAt, best.createdAt)) best = sh
  }
  return best.id
})
const conn = ref<any>(null)
const siteRoots = ref<any[]>([])
const sitePick = ref('')
// NAS 账号（管理员 + 自动创建的默认共享）：连接管理里可查看密码
const hub = ref<any>(null)
const hubShow = reactive({ admin: false, share: false })

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

// 扫码快传
const quickOpen = ref(false)
const quickUser = ref('')

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
    try { hub.value = await HubAccountInfo(props.instanceId) } catch { hub.value = null }
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

// 顶部启停后：等进程真正起停完成再刷连接信息（否则拿到的还是旧状态）
function onPowerChanged() {
  window.setTimeout(() => { void reload() }, 700)
}

// 分离为独立窗口：开窗成功后收起弹窗（窗口里是同一个面板，状态各自独立加载）
async function detach() {
  try {
    await OpenManageWindow('webdav', props.instanceId, props.instanceName || '')
    visible.value = false
  } catch (e) {
    ElMessage.error(errText(e))
  }
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

// NAS 账号密码复制（管理员 / 默认共享共用）
async function copyText(v?: string) {
  const s = String(v || '')
  if (!s) return
  try {
    await navigator.clipboard.writeText(s)
    ElMessage.success(t('davm.copied'))
  } catch {
    ElMessage.warning(s)
  }
}

// 系统默认浏览器打开（地址行本身是链接，点它 = 这个）
async function openExternal() {
  const url = String(conn.value?.url || '')
  if (!url || !conn.value?.running) return
  try {
    // Wails 3.0.0 起 Browser 命名空间只导出 OpenURL（旧名 BrowserOpenURL 已移除）
    await Browser.OpenURL(url)
  } catch {
    window.open(url, '_blank')
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
  if (cmd === 'quick') {
    quickUser.value = row.user
    quickOpen.value = true
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
/* standalone（M20 独立窗口）的外壳样式全部走内联（见 standaloneBoxStyle）与
   Element Plus 的 header-class / body-class 钩子（见 ManageStandalone.vue 的
   全局块）。这里不再用 :deep 改写弹窗自身盒子——scoped 深选择器在独立窗里
   命中不稳定，之前就是它没生效导致弹窗收在窗中间。 */
.davm-conn { border: 1px solid var(--border-color); border-radius: 8px; padding: 0.5rem 0.75rem; margin-bottom: 0.75rem; background: var(--bg-subtle, rgba(127, 127, 127, 0.04)); }
.davm-head { display: flex; align-items: center; justify-content: space-between; gap: 0.75rem; }
.davm-detach { flex-shrink: 0; }
.davm-conn-row { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; }
.davm-conn-label { font-size: 0.75rem; color: var(--text-secondary); flex-shrink: 0; }
.davm-conn-url { font-size: 0.8125rem; font-family: var(--font-mono, monospace); padding: 0.125rem 0.375rem; border-radius: 4px; background: rgba(127, 127, 127, 0.1); }
.davm-conn-url.off { opacity: 0.5; }
/* 可点状态的地址：链接样式，其余观感与 code 一致 */
a.davm-conn-link { color: var(--accent); text-decoration: none; cursor: pointer; }
a.davm-conn-link:hover { text-decoration: underline; }
.davm-conn-hint { font-size: 0.6875rem; color: var(--text-muted); line-height: 1.5; margin-top: 0.25rem; }
.davm-conn-hint-inline { margin-top: 0; }
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
/* NAS 账号区：账号与密码并排；密码未显示时用圆点占位 */
.davm-hub .davm-pass { font-family: var(--font-mono); }
.davm-hub .davm-pass.off { letter-spacing: 0.2em; }
</style>
