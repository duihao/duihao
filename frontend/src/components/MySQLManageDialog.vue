<template>
  <el-dialog
    v-model="visible"
    :title="t('dbm.title', { name: instanceName || instanceId })"
    width="62rem"
    top="5vh"
    class="dbm-dialog"
    destroy-on-close
  >
    <!-- 工具条：连接状态 + 主操作 + 搜索（对齐 phpStudy 数据库管理页） -->
    <div class="dbm-bar">
      <div class="dbm-bar-left">
        <el-tag v-if="status.connected" type="success" size="small" effect="plain">
          MySQL {{ status.version }} · :{{ status.port }}
        </el-tag>
        <el-tag v-else type="warning" size="small" effect="plain">{{ t('dbm.disconnected') }}</el-tag>
        <el-button
          size="small" type="primary"
          :disabled="!status.connected || !status.rootPasswordSet"
          @click="openCreate"
        >
          + {{ t('dbm.createDb') }}
        </el-button>
        <el-button
          size="small"
          :disabled="!status.connected || !status.rootPasswordSet"
          @click="openRootPwd"
        >
          {{ t('dbm.changeRootPwd') }}
        </el-button>
      </div>
      <div class="dbm-bar-right">
        <el-input v-model="filter" size="small" clearable :placeholder="t('dbm.search')" class="dbm-search" />
        <el-button size="small" text :loading="loading" @click="reload">{{ t('dbm.refresh') }}</el-button>
        <el-button size="small" text @click="openConn">{{ t('dbm.connection') }}</el-button>
      </div>
    </div>

    <el-alert
      v-if="!status.connected"
      type="warning"
      :closable="false"
      show-icon
      class="dbm-alert"
      :title="status.error || t('dbm.notConnectedHint')"
      :description="t('dbm.connectFirst')"
    />

    <!-- 首次使用：root 还是空密码，必须先设置（管理面其余操作此时全部禁用） -->
    <el-alert v-if="status.connected && !status.rootPasswordSet" type="info" :closable="false" class="dbm-setup">
      <template #title>
        <span class="dbm-setup-title">{{ t('dbm.setupTitle') }}</span>
      </template>
      <div class="dbm-setup-body">
        <span class="dbm-hint">{{ t('dbm.setupHint') }}</span>
        <div class="dbm-setup-row">
          <el-input
            v-model="setupPwd" size="small" type="password" show-password
            class="dbm-setup-input" :placeholder="t('dbm.newPwd')"
          />
          <el-button size="small" @click="setupPwd = randomPassword()">{{ t('dbm.generate') }}</el-button>
          <el-button size="small" type="primary" :loading="busy || !setupPwd" @click="doSetupPwd">
            {{ t('dbm.setupBtn') }}
          </el-button>
        </div>
      </div>
    </el-alert>

    <el-tabs v-model="tab" class="dbm-tabs">
      <!-- ============ 数据库列表 ============ -->
      <el-tab-pane :label="t('dbm.tabDb')" name="db">
        <el-table :data="filteredDbs" size="small" height="46vh" :empty-text="t('dbm.noData')" class="dbm-table">
          <el-table-column type="index" label="#" width="46" />
          <el-table-column prop="name" :label="t('dbm.colName')" min-width="150" show-overflow-tooltip />
          <el-table-column :label="t('dbm.colUser')" min-width="120" show-overflow-tooltip>
            <template #default="{ row }">{{ (row.users || []).join(', ') || '—' }}</template>
          </el-table-column>
          <el-table-column :label="t('dbm.colPassword')" width="80">
            <template #default>{{ row0Mask }}</template>
          </el-table-column>
          <el-table-column prop="tables" :label="t('dbm.colTables')" width="70" />
          <el-table-column :label="t('dbm.colSize')" width="88">
            <template #default="{ row }">{{ fmtBytes(row.bytes) }}</template>
          </el-table-column>
          <el-table-column :label="t('dbm.colStatus')" width="80">
            <template #default>
              <span class="dbm-ok">{{ t('dbm.statusOk') }}</span>
            </template>
          </el-table-column>
          <el-table-column :label="t('dbm.colActions')" width="104" align="right">
            <template #default="{ row }">
              <el-dropdown trigger="click" @command="(c) => onAction(c, row)">
                <el-button size="small" :disabled="!status.connected || !status.rootPasswordSet">
                  {{ t('dbm.action') }}<span class="dbm-caret">▾</span>
                </el-button>
                <template #dropdown>
                  <el-dropdown-menu>
                    <el-dropdown-item command="connect">{{ t('dbm.connectDb') }}</el-dropdown-item>
                    <el-dropdown-item command="backup" divided>{{ t('dbm.backup') }}</el-dropdown-item>
                    <el-dropdown-item command="privileges">{{ t('dbm.privileges') }}</el-dropdown-item>
                    <el-dropdown-item command="password">{{ t('dbm.changePwd') }}</el-dropdown-item>
                    <el-dropdown-item command="import">{{ t('dbm.import') }}</el-dropdown-item>
                    <el-dropdown-item command="drop" divided>{{ t('dbm.drop') }}</el-dropdown-item>
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <!-- ============ 备份列表 ============ -->
      <el-tab-pane :label="t('dbm.tabBackup')" name="backup">
        <el-table :data="backups" size="small" height="46vh" :empty-text="t('dbm.backupEmpty')">
          <el-table-column prop="database" :label="t('dbm.colName')" width="150" show-overflow-tooltip />
          <el-table-column prop="name" :label="t('dbm.fileName')" min-width="220" show-overflow-tooltip />
          <el-table-column :label="t('dbm.colSize')" width="90">
            <template #default="{ row }">{{ fmtBytes(row.size) }}</template>
          </el-table-column>
          <el-table-column :label="t('dbm.time')" width="150">
            <template #default="{ row }">{{ fmtTime(row.createdAt) }}</template>
          </el-table-column>
          <el-table-column :label="t('dbm.colActions')" width="150" align="right">
            <template #default="{ row }">
              <el-button size="small" text @click="onRestore(row)">{{ t('dbm.restore') }}</el-button>
              <el-button size="small" text type="danger" @click="onDeleteBackup(row)">{{ t('dbm.delete') }}</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>
    </el-tabs>

    <!-- ============ 创建数据库 ============ -->
    <el-dialog v-model="createOpen" :title="t('dbm.createTitle')" width="30rem" append-to-body>
      <div class="dbm-form">
        <div class="dbm-row">
          <span class="dbm-label">{{ t('dbm.dbName') }}</span>
          <el-input v-model="cf.name" size="small" placeholder="duihao_site" @input="onNameInput" />
        </div>
        <div class="dbm-row">
          <span class="dbm-label">{{ t('dbm.userName') }}</span>
          <div class="dbm-fill">
            <el-input v-model="cf.user" size="small" placeholder="duihao_site" />
            <span class="dbm-hint">{{ t('dbm.userHint') }}</span>
          </div>
        </div>
        <div class="dbm-row">
          <span class="dbm-label">{{ t('dbm.password') }}</span>
          <div class="dbm-pick">
            <el-input v-model="cf.password" size="small" />
            <el-button size="small" @click="cf.password = randomPassword()">{{ t('dbm.generate') }}</el-button>
          </div>
        </div>
        <div class="dbm-row">
          <span class="dbm-label">{{ t('dbm.charset') }}</span>
          <el-select v-model="cf.charset" size="small" class="dbm-fill">
            <el-option v-for="c in charsets" :key="c" :label="c" :value="c" />
          </el-select>
        </div>
      </div>
      <template #footer>
        <el-button size="small" @click="createOpen = false">{{ t('dbm.cancel') }}</el-button>
        <el-button size="small" type="primary" :loading="busy" @click="doCreate">{{ t('dbm.create') }}</el-button>
      </template>
    </el-dialog>

    <!-- ============ 修改业务账号密码 ============ -->
    <el-dialog v-model="pwdOpen" :title="t('dbm.pwdTitle', { name: pwdTarget.db })" width="28rem" append-to-body>
      <div class="dbm-form">
        <div class="dbm-row">
          <span class="dbm-label">{{ t('dbm.userName') }}</span>
          <el-input v-model="pwdTarget.user" size="small" />
        </div>
        <div class="dbm-row">
          <span class="dbm-label">{{ t('dbm.newPwd') }}</span>
          <div class="dbm-pick">
            <el-input v-model="pwdTarget.password" size="small" />
            <el-button size="small" @click="pwdTarget.password = randomPassword()">{{ t('dbm.generate') }}</el-button>
          </div>
        </div>
        <div class="dbm-hint dbm-hint-block">{{ t('dbm.pwdHint') }}</div>
      </div>
      <template #footer>
        <el-button size="small" @click="pwdOpen = false">{{ t('dbm.cancel') }}</el-button>
        <el-button size="small" type="primary" :loading="busy" @click="doChangePwd">{{ t('dbm.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- ============ 连接数据库：补记该库账号密码 ============ -->
    <el-dialog v-model="credOpen" :title="t('dbm.connectCredTitle', { name: credTarget.db })" width="28rem" append-to-body>
      <div class="dbm-form">
        <div class="dbm-row">
          <span class="dbm-label">{{ t('dbm.userName') }}</span>
          <el-input v-model="credTarget.user" size="small" />
        </div>
        <div class="dbm-row">
          <span class="dbm-label">{{ t('dbm.password') }}</span>
          <el-input
            v-model="credTarget.password" size="small" type="password" show-password
            :placeholder="t('dbm.connectCredPwd')" @keyup.enter="confirmCred"
          />
        </div>
        <div class="dbm-hint dbm-hint-block">{{ t('dbm.connectCredHint') }}</div>
      </div>
      <template #footer>
        <el-button size="small" @click="connectAsRoot">{{ t('dbm.connectAsRoot') }}</el-button>
        <el-button size="small" type="primary" :loading="busy" :disabled="!credTarget.password" @click="confirmCred">
          {{ t('dbm.connectSaveCred') }}
        </el-button>
      </template>
    </el-dialog>

    <!-- ============ 修改 root 密码 ============ -->
    <el-dialog v-model="rootOpen" :title="t('dbm.rootPwdTitle')" width="28rem" append-to-body>
      <div class="dbm-form">
        <div class="dbm-row">
          <span class="dbm-label">{{ t('dbm.newPwd') }}</span>
          <div class="dbm-pick">
            <el-input v-model="rootPwd" size="small" />
            <el-button size="small" @click="rootPwd = randomPassword()">{{ t('dbm.generate') }}</el-button>
          </div>
        </div>
        <div class="dbm-hint dbm-hint-block">{{ t('dbm.rootPwdHint', { user: status.user || 'root' }) }}</div>
      </div>
      <template #footer>
        <el-button size="small" @click="rootOpen = false">{{ t('dbm.cancel') }}</el-button>
        <el-button size="small" type="primary" :loading="busy" @click="doChangeRootPwd">{{ t('dbm.save') }}</el-button>
      </template>
    </el-dialog>

    <!-- ============ 连接设置（root 凭证） ============ -->
    <el-dialog v-model="connOpen" :title="t('dbm.connTitle')" width="30rem" append-to-body>
      <div class="dbm-form">
        <div class="dbm-row">
          <span class="dbm-label">{{ t('dbm.rootUser') }}</span>
          <el-input v-model="conn.user" size="small" />
        </div>
        <div class="dbm-row">
          <span class="dbm-label">{{ t('dbm.password') }}</span>
          <el-input v-model="conn.password" size="small" type="password" show-password />
        </div>
        <div class="dbm-hint dbm-hint-block">{{ t('dbm.connHint') }}</div>
      </div>
      <template #footer>
        <el-button size="small" @click="connOpen = false">{{ t('dbm.cancel') }}</el-button>
        <el-button size="small" :loading="busy" @click="doTestConn">{{ t('dbm.testConn') }}</el-button>
        <el-button size="small" type="primary" :loading="busy" @click="doSaveConn">{{ t('dbm.saveConn') }}</el-button>
      </template>
    </el-dialog>

    <!-- ============ 权限 ============ -->
    <el-dialog v-model="privOpen" :title="t('dbm.privTitle', { name: privTarget.db })" width="34rem" append-to-body>
      <div class="dbm-priv-head">
        <el-checkbox v-model="privAll" @change="onPrivAll">{{ t('dbm.selectAll') }}</el-checkbox>
        <span class="dbm-hint">{{ t('dbm.privHint', { user: privTarget.user }) }}</span>
      </div>
      <el-checkbox-group v-model="privChecked" class="dbm-priv-grid">
        <el-checkbox v-for="p in privilegeNames" :key="p" :value="p" :label="p" />
      </el-checkbox-group>
      <template #footer>
        <el-button size="small" @click="privOpen = false">{{ t('dbm.cancel') }}</el-button>
        <el-button size="small" type="primary" :loading="busy" @click="doSavePriv">{{ t('dbm.save') }}</el-button>
      </template>
    </el-dialog>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from '../i18n'
import { OpenFileDialogFiltered } from '../../bindings/github.com/ys-ll/uniterm/app'
import {
  MySQLStatus, ListMySQLDatabases, CreateMySQLDatabase, SetMySQLDatabasePassword,
  SetMySQLRootPassword, GetMySQLPrivileges, SetMySQLPrivileges, MySQLPrivilegeNames,
  DropMySQLDatabase, BackupMySQLDatabase, ListMySQLBackups, DeleteMySQLBackup,
  RestoreMySQLBackup, ImportMySQLSQL, TestMySQLCredential, SaveMySQLCredential,
  MySQLConnectionInfo, SaveMySQLDbUser,
} from '../../bindings/github.com/ys-ll/uniterm/backend/duihao/service/mysqlservice'
import { launchConnection } from '../composables/connectionLauncher'
import type { ConnectionConfig } from '../types/session'

const props = defineProps<{ instanceId: string; instanceName?: string }>()
const visible = defineModel<boolean>('visible', { default: false })
const { t } = useI18n()

const tab = ref('db')
const filter = ref('')
const loading = ref(false)
const busy = ref(false)
const row0Mask = '••••••'

const status = reactive({ connected: false, version: '', port: 0, user: 'root', passwordSaved: false, rootPasswordSet: false, error: '' })
const setupPwd = ref('')
const dbs = ref<any[]>([])
const backups = ref<any[]>([])
const privilegeNames = ref<string[]>([])
const charsets = ['utf8mb4', 'utf8', 'gbk', 'latin1']

const filteredDbs = computed(() => {
  const q = filter.value.trim().toLowerCase()
  if (!q) return dbs.value
  return dbs.value.filter(d =>
    String(d.name).toLowerCase().includes(q) ||
    (d.users || []).some((u: string) => u.toLowerCase().includes(q)))
})

// 创建库
const createOpen = ref(false)
const cf = reactive({ name: '', user: '', password: '', charset: 'utf8mb4' })

// 改业务密码 / root 密码 / 连接设置
const pwdOpen = ref(false)
const pwdTarget = reactive({ db: '', user: '', password: '' })
const rootOpen = ref(false)
const rootPwd = ref('')
const connOpen = ref(false)
const conn = reactive({ user: 'root', password: '' })

// 权限
const privOpen = ref(false)
const privTarget = reactive({ db: '', user: '' })
const privChecked = ref<string[]>([])
const privAll = ref(false)

// 「连接数据库」时补记该库账号密码（外部建的库没有记录，填一次就记住）
const credOpen = ref(false)
const credTarget = reactive({ db: '', user: '', password: '', rootInfo: null as any })

// immediate：父组件用 v-if 挂载本对话框，挂载时 visible 已经是 true，
// 不加 immediate 就永远不会自动连（用户点「管理」后停在「未连接」）。
watch(visible, (v) => { if (v) reload() }, { immediate: true })

// 实例刚启动时 mysqld 可能还在初始化/还没监听端口：首次连不上就等 700ms 自动重试一次，
// 避免用户看到「未连接」后被迫手动点刷新。凭证错误不重试（密码错误等 700ms 也一样）。
async function reload() {
  if (!props.instanceId) return
  loading.value = true
  try {
    let st: any = await MySQLStatus(props.instanceId)
    // 刚启动时 mysqld 可能还没监听端口：连不上先等 700ms 再探一次。
    // 凭证类错误不重试（等多久都一样，直接让用户看到原因）。
    if (!st?.connected && /refused|Can't connect|10061|no such host|timeout/i.test(st?.error || '')) {
      await new Promise(r => setTimeout(r, 700))
      st = await MySQLStatus(props.instanceId)
    }
    Object.assign(status, st || {})
    if (!status.connected) {
      dbs.value = []
      backups.value = []
      return
    }
    try {
      dbs.value = (await ListMySQLDatabases(props.instanceId)) as unknown as any[]
    } catch (e) {
      dbs.value = []
      ElMessage.error(errText(e))
    }
    try { backups.value = (await ListMySQLBackups(props.instanceId)) as unknown as any[] } catch { backups.value = [] }
    if (!privilegeNames.value.length) {
      try { privilegeNames.value = (await MySQLPrivilegeNames()) as unknown as string[] } catch { /* 旧版后端 */ }
    }
  } finally {
    loading.value = false
  }
}

function errText(e: unknown): string {
  if (e instanceof Error) return e.message
  const s = String(e)
  return s.replace(/^Error:\s*/, '')
}

function fmtBytes(n: number): string {
  const b = Number(n) || 0
  if (b < 1024) return b + ' B'
  if (b < 1024 * 1024) return (b / 1024).toFixed(1) + ' KB'
  if (b < 1024 * 1024 * 1024) return (b / 1024 / 1024).toFixed(1) + ' MB'
  return (b / 1024 / 1024 / 1024).toFixed(2) + ' GB'
}

function fmtTime(s: string): string {
  if (!s) return '—'
  return String(s).replace('T', ' ').slice(0, 19)
}

function randomPassword(): string {
  const chars = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789'
  let out = ''
  const buf = new Uint32Array(16)
  crypto.getRandomValues(buf)
  for (let i = 0; i < 16; i++) out += chars[buf[i] % chars.length]
  return out
}

function onNameInput(v: string) {
  // 库名先填，用户名跟随（phpStudy 习惯：一库一用户同名），用户改过就不再覆盖
  if (!cf.user || cf.user === cf.name) cf.user = v
}

function openCreate() {
  cf.name = ''
  cf.user = ''
  cf.password = randomPassword()
  cf.charset = 'utf8mb4'
  createOpen.value = true
}

async function doCreate() {
  busy.value = true
  try {
    await CreateMySQLDatabase(props.instanceId, {
      name: cf.name.trim(), user: cf.user.trim(), password: cf.password, charset: cf.charset,
    })
    ElMessage.success(t('dbm.created'))
    createOpen.value = false
    await reload()
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

function onAction(cmd: string, row: any) {
  const user = (row.users && row.users[0]) || row.name
  if (cmd === 'connect') return openClient(row.name, row)
  if (cmd === 'backup') return doBackup(row)
  if (cmd === 'privileges') return openPriv(row, user)
  if (cmd === 'password') return openPwd(row, user)
  if (cmd === 'import') return doImport(row)
  if (cmd === 'drop') return doDrop(row)
}

// 「连接数据库」：直接调用对号内置的数据库连接能力（connectionLauncher →
// database 会话），连到该库，进入完整的 SQL 编辑 / 表结构 / 数据浏览界面。
//
// 账号优先用**该库自己的账号密码**（后端从 instances/<id>/dbusers.json 读，
// 建库/改密时记下的），连进去就是这个库的身份、权限范围也对得上。该库还没
// 记过密码（外部工具建的、老版本建的）时，先让用户填一次并记住；用户也
// 可以直接选择用 root 连。
//
// 只新建一次临时会话：persist=false，不写入连接列表、不改动任何已有连接
// 或实例配置，管理面板原有逻辑完全不受影响。
async function openClient(db: string, row?: any) {
  if (!status.connected || !status.rootPasswordSet) return
  busy.value = true
  try {
    // 连接参数由后端给（凭证不出对号进程），库名一并带过去当默认库
    const info: any = await MySQLConnectionInfo(props.instanceId, db)
    if (info?.usingRoot) {
      const dbUser = info?.staleUser || (row?.users && row.users[0]) || ''
      if (dbUser) {
        // 该库有账号，但密码簿里没有可用密码：让用户填一次（填了会记住）
        credTarget.db = db
        credTarget.user = dbUser
        credTarget.password = ''
        credTarget.rootInfo = info
        credOpen.value = true
        return
      }
    }
    await launchWith(info, db)
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

// 拿一份连接参数真正开标签页（openClient 与「保存并连接」共用）
async function launchWith(info: any, db: string) {
  const user = info?.user || status.user || 'root'
  const cfg = {
    id: `duihao-mysql-${props.instanceId}-${db}-${Date.now()}`,
    // 标题带上账号，一眼能看出是以哪个身份连的
    name: `${db} · ${user}`,
    type: 'database',
    dbType: 'mysql',
    host: info?.host || '127.0.0.1',
    port: Number(info?.port) || status.port,
    user,
    authType: 'password',
    password: info?.password || '',
    dbName: info?.database || db,
  } as ConnectionConfig
  // 回退 root 时说明原因：记过密码但失效 vs 从没记过
  if (info?.staleUser) {
    ElMessage.warning(t('dbm.connectStalePwd', { user: info.staleUser, root: user }))
  } else if (info?.usingRoot) {
    ElMessage.warning(t('dbm.connectUsingRoot', { root: user }))
  }
  // 先收起管理面板，新开的连接标签页才不会被挡在后面
  visible.value = false
  await launchConnection(cfg, { persist: false })
}

// 填一次该库账号密码：验证通过后记住，再用它连接
async function confirmCred() {
  if (!credTarget.password) return
  busy.value = true
  try {
    await SaveMySQLDbUser(props.instanceId, credTarget.db, credTarget.user, credTarget.password)
    const info: any = await MySQLConnectionInfo(props.instanceId, credTarget.db)
    credOpen.value = false
    await launchWith(info, credTarget.db)
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

// 不填密码，直接用实例的 root 连
async function connectAsRoot() {
  const info = credTarget.rootInfo
  const db = credTarget.db
  credOpen.value = false
  await launchWith(info, db)
}

async function doBackup(row: any) {
  busy.value = true
  try {
    await BackupMySQLDatabase(props.instanceId, row.name)
    ElMessage.success(t('dbm.backuped'))
    tab.value = 'backup'
    await reload()
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

function openPwd(row: any, user: string) {
  pwdTarget.db = row.name
  pwdTarget.user = user
  pwdTarget.password = randomPassword()
  pwdOpen.value = true
}

async function doChangePwd() {
  busy.value = true
  try {
    await SetMySQLDatabasePassword(props.instanceId, pwdTarget.db, pwdTarget.user, pwdTarget.password)
    ElMessage.success(t('dbm.saved'))
    pwdOpen.value = false
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

// 首次使用：设置 root 密码（后端在密码为空时拒绝一切管理操作）
async function doSetupPwd() {
  if (!setupPwd.value) return
  busy.value = true
  try {
    await SetMySQLRootPassword(props.instanceId, setupPwd.value)
    ElMessage.success(t('dbm.saved'))
    setupPwd.value = ''
    await reload()
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

function openRootPwd() {
  rootPwd.value = randomPassword()
  rootOpen.value = true
}

async function doChangeRootPwd() {
  busy.value = true
  try {
    await SetMySQLRootPassword(props.instanceId, rootPwd.value)
    ElMessage.success(t('dbm.saved'))
    rootOpen.value = false
    await reload()
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

function openConn() {
  conn.user = status.user || 'root'
  conn.password = ''
  connOpen.value = true
}

async function doTestConn() {
  busy.value = true
  try {
    await TestMySQLCredential(props.instanceId, conn.user, conn.password)
    ElMessage.success(t('dbm.connOk'))
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

async function doSaveConn() {
  busy.value = true
  try {
    await SaveMySQLCredential(props.instanceId, conn.user, conn.password)
    ElMessage.success(t('dbm.saved'))
    connOpen.value = false
    await reload()
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

async function openPriv(row: any, user: string) {
  privTarget.db = row.name
  privTarget.user = user
  privChecked.value = []
  try {
    const list = (await GetMySQLPrivileges(props.instanceId, row.name, user)) as unknown as string[]
    privChecked.value = Array.isArray(list) ? list : []
  } catch (e) {
    ElMessage.error(errText(e))
  }
  privAll.value = privilegeNames.value.length > 0 && privChecked.value.length === privilegeNames.value.length
  privOpen.value = true
}

function onPrivAll(v: any) {
  privChecked.value = v ? [...privilegeNames.value] : []
}

async function doSavePriv() {
  busy.value = true
  try {
    await SetMySQLPrivileges(props.instanceId, privTarget.db, privTarget.user, privChecked.value)
    ElMessage.success(t('dbm.saved'))
    privOpen.value = false
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

async function doDrop(row: any) {
  let dropUser = false
  try {
    await ElMessageBox.confirm(
      t('dbm.dropConfirm', { name: row.name }),
      t('dbm.drop'),
      { type: 'warning', confirmButtonText: t('dbm.drop'), cancelButtonText: t('dbm.cancel') },
    )
    const r = await ElMessageBox.confirm(t('dbm.dropUser'), t('dbm.drop'), {
      type: 'warning', distinguishCancelAndClose: true,
      confirmButtonText: t('dbm.yes'), cancelButtonText: t('dbm.no'),
    })
    dropUser = r === 'confirm'
  } catch (e) {
    return
  }
  busy.value = true
  try {
    await DropMySQLDatabase(props.instanceId, row.name, dropUser)
    ElMessage.success(t('dbm.deleted'))
    await reload()
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

async function doImport(row: any) {
  let file = ''
  try {
    file = String(await OpenFileDialogFiltered(t('dbm.importTitle'), 'SQL', '*.sql'))
  } catch {
    return
  }
  if (!file) return
  busy.value = true
  try {
    await ImportMySQLSQL(props.instanceId, row.name, file)
    ElMessage.success(t('dbm.imported'))
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

async function onRestore(row: any) {
  try {
    await ElMessageBox.confirm(
      t('dbm.restoreConfirm', { name: row.database, file: row.name }),
      t('dbm.restore'),
      { type: 'warning', confirmButtonText: t('dbm.restore'), cancelButtonText: t('dbm.cancel') },
    )
  } catch {
    return
  }
  busy.value = true
  try {
    await RestoreMySQLBackup(props.instanceId, row.database, row.path)
    ElMessage.success(t('dbm.restored'))
    await reload()
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

async function onDeleteBackup(row: any) {
  try {
    await ElMessageBox.confirm(t('dbm.deleteConfirm', { file: row.name }), t('dbm.delete'), { type: 'warning' })
  } catch {
    return
  }
  try {
    await DeleteMySQLBackup(props.instanceId, row.path)
    ElMessage.success(t('dbm.deleted'))
    await reload()
  } catch (e) {
    ElMessage.error(errText(e))
  }
}
</script>

<style scoped>
.dbm-bar { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; flex-wrap: wrap; margin-bottom: 0.5rem; }
.dbm-bar-left, .dbm-bar-right { display: flex; align-items: center; gap: 0.375rem; flex-wrap: wrap; }
.dbm-search { width: 12rem; }
.dbm-alert { margin-bottom: 0.5rem; }
.dbm-setup { margin-bottom: 0.5rem; }
.dbm-setup-title { font-weight: 600; }
.dbm-setup-body { display: flex; flex-direction: column; gap: 0.375rem; }
.dbm-setup-row { display: flex; align-items: center; gap: 0.375rem; margin-top: 0.25rem; }
.dbm-setup-input { flex: 1; max-width: 18rem; }
.dbm-tabs :deep(.el-tabs__header) { margin-bottom: 0.5rem; }
.dbm-ok { color: var(--success); font-size: 0.75rem; }
.dbm-caret { margin-left: 0.25rem; font-size: 0.625rem; opacity: 0.7; }

.dbm-form { display: flex; flex-direction: column; gap: 0.625rem; }
.dbm-row { display: flex; align-items: center; gap: 0.75rem; }
.dbm-label { width: 5.5rem; flex-shrink: 0; font-size: 0.75rem; color: var(--text-secondary); }
.dbm-fill { flex: 1; min-width: 0; }
.dbm-pick { flex: 1; display: flex; gap: 0.375rem; min-width: 0; }
.dbm-hint { font-size: 0.6875rem; color: var(--text-muted); line-height: 1.5; }
.dbm-hint-block { margin-left: 6.25rem; }

.dbm-priv-head { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; margin-bottom: 0.5rem; flex-wrap: wrap; }
.dbm-priv-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 0.25rem 0.5rem; }
</style>
