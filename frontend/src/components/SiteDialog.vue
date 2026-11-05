<template>
  <el-dialog
    v-model="visible"
    :title="isCreate ? t('sites.createTitle') : t('sites.settingsTitle', { id: site?.id || '' })"
    width="46rem"
    top="6vh"
    class="sd-dialog"
    destroy-on-close
  >
    <el-alert
      v-if="nginxInstances.length === 0"
      type="warning" :closable="false" show-icon
      :title="t('sites.needWeb')" class="sd-alert"
    />

    <el-tabs v-model="tab" class="sd-tabs">
      <!-- ============ 基本配置 ============ -->
      <el-tab-pane :label="t('sites.tabBasic')" name="basic">
        <div class="sd-form">
          <div class="sd-row">
            <span class="sd-label">{{ t('sites.domains') }}</span>
            <div class="sd-fill">
              <el-input v-model="f.mainDomain" size="small" placeholder="demo.test" />
            </div>
          </div>

          <div class="sd-row">
            <span class="sd-label">{{ t('sites.secondDomain') }}</span>
            <div class="sd-fill">
              <el-input v-model="f.secondDomain" size="small" placeholder="www.demo.test, m.demo.test" />
              <span class="sd-hint">{{ t('sites.secondDomainHint') }}</span>
            </div>
          </div>

          <div class="sd-row">
            <span class="sd-label">{{ t('sites.port') }}</span>
            <div class="sd-fill">
              <div class="sd-portline">
                <el-radio-group v-model="f.scheme" size="small" @change="onSchemeChange">
                  <el-radio value="http">http</el-radio>
                  <el-radio value="https">https</el-radio>
                </el-radio-group>
                <el-input
                  v-model="f.port" size="small" class="sd-port"
                  :placeholder="f.scheme === 'https' ? '443' : '80'"
                />
              </div>
              <span class="sd-hint">{{ f.scheme === 'https' ? t('sites.httpsHint') : t('sites.portHint') }}</span>
            </div>
          </div>

          <div class="sd-row">
            <span class="sd-label">{{ t('sites.root') }}</span>
            <div class="sd-pick">
              <el-input
                v-model="f.root" size="small" @input="rootTouched = true"
                :placeholder="rootBase ? rootBase + '/<域名>' : ''"
              />
              <el-button size="small" @click="onPickRoot">{{ t('sites.browse') }}</el-button>
            </div>
          </div>

          <!-- 创建环境 -->
          <div class="sd-row sd-row-top">
            <span class="sd-label">{{ t('sites.createEnv') }}</span>
            <div class="sd-fill">
              <div class="sd-env">
                <el-checkbox v-model="f.createFtp" :disabled="ftpLinked" size="small">
                  {{ t('sites.createFtp') }}
                </el-checkbox>
                <el-checkbox v-model="f.createDb" :disabled="dbLinked" size="small">
                  {{ t('sites.createDb') }}
                </el-checkbox>
                <el-checkbox v-model="f.syncHosts" size="small">{{ t('sites.syncHosts') }}</el-checkbox>
                <el-checkbox v-model="f.production" size="small">{{ t('sites.production') }}</el-checkbox>
              </div>
              <span class="sd-hint">{{ t('sites.envHint') }}</span>

              <!-- FTP 子表单 -->
              <div v-if="f.createFtp" class="sd-sub">
                <template v-if="ftpLinked">
                  <span class="sd-hint">{{ t('sites.envFtpLinked', { user: ftpLinkedUser }) }}</span>
                </template>
                <template v-else>
                  <div class="sd-subrow">
                    <span class="sd-sublabel">{{ t('sites.ftpInstance') }}</span>
                    <el-select v-model="f.ftpInstance" size="small" class="sd-grow" :placeholder="t('sites.ftpNoInstance')">
                      <el-option
                        v-for="i in ftpInstances" :key="i.id"
                        :label="instLabel(i, 'ftp')" :value="i.id"
                      />
                    </el-select>
                  </div>
                  <div class="sd-subrow">
                    <span class="sd-sublabel">{{ t('sites.ftpUser') }}</span>
                    <el-input v-model="f.ftpUser" size="small" class="sd-grow" />
                    <span class="sd-sublabel">{{ t('sites.ftpPassword') }}</span>
                    <el-input v-model="f.ftpPass" size="small" class="sd-grow" />
                  </div>
                </template>
              </div>

              <!-- 数据库子表单 -->
              <div v-if="f.createDb" class="sd-sub">
                <div class="sd-subrow">
                  <span class="sd-sublabel">{{ t('sites.dbInstance') }}</span>
                  <el-select v-model="f.dbInstance" size="small" class="sd-grow" :placeholder="t('sites.dbNoInstance')">
                    <el-option v-for="i in dbInstances" :key="i.id" :label="instLabel(i, 'db')" :value="i.id" />
                  </el-select>
                  <span class="sd-sublabel">{{ t('sites.dbName') }}</span>
                  <el-input v-model="f.dbName" size="small" class="sd-grow" />
                </div>
                <div class="sd-subrow">
                  <span class="sd-sublabel">{{ t('sites.dbUser') }}</span>
                  <el-input v-model="f.dbUser" size="small" class="sd-grow" :disabled="dbLinked" />
                  <span class="sd-sublabel">{{ t('sites.dbPassword') }}</span>
                  <el-input v-model="f.dbPass" size="small" class="sd-grow" :disabled="dbLinked" />
                </div>
                <span class="sd-hint">{{ t('sites.dbEnvHint') }}</span>
              </div>
            </div>
          </div>

          <!-- 程序类型 -->
          <div class="sd-row sd-switch-row">
            <span class="sd-label">{{ t('sites.programType') }}</span>
            <div class="sd-fill">
              <el-checkbox v-model="f.usePhp" size="small">PHP</el-checkbox>
            </div>
          </div>

          <div v-if="f.usePhp" class="sd-row">
            <span class="sd-label">{{ t('sites.phpVersion') }}</span>
            <div class="sd-fill">
              <div class="sd-inline">
                <el-select
                  v-model="f.php" size="small" class="sd-grow"
                  :placeholder="t('sites.phpPick')" :loading="!!provisioning"
                >
                  <el-option
                    v-for="c in phpChoices" :key="c.version"
                    :label="phpLabel(c)" :value="c.version"
                  />
                </el-select>
                <span class="sd-sublabel">{{ t('sites.expireAt') }}</span>
                <el-date-picker
                  v-model="f.expireAt" type="date" size="small" class="sd-date"
                  value-format="YYYY-MM-DD" :placeholder="t('sites.expireHint')"
                />
              </div>
              <span class="sd-hint">{{ t('sites.phpBindHint') }}</span>
            </div>
          </div>

          <div v-if="isCreate" class="sd-row">
            <span class="sd-label">{{ t('sites.webServer') }}</span>
            <div class="sd-fill">
              <el-select v-model="f.web" size="small" :placeholder="t('sites.needWeb')">
                <el-option
                  v-for="i in nginxInstances" :key="i.id"
                  :label="instLabel(i, 'http')" :value="i.id"
                />
              </el-select>
            </div>
          </div>
          <div v-else class="sd-row">
            <span class="sd-label">{{ t('sites.webServer') }}</span>
            <div class="sd-fill">
              <span class="sd-hint">{{ webLabel }}</span>
            </div>
          </div>

          <div class="sd-row">
            <span class="sd-label">{{ t('sites.remark') }}</span>
            <div class="sd-fill">
              <el-input v-model="f.remark" size="small" :placeholder="t('sites.remarkPlaceholder')" />
            </div>
          </div>
        </div>
      </el-tab-pane>

      <!-- ============ 高级配置 ============ -->
      <el-tab-pane :label="t('sites.tabAdvanced')" name="advanced">
        <div class="sd-form">
          <div class="sd-row sd-switch-row">
            <span class="sd-label">{{ t('sites.accessLog') }}</span>
            <el-switch v-model="f.accessLog" size="small" />
          </div>
          <div class="sd-row sd-switch-row">
            <span class="sd-label">{{ t('sites.errorLog') }}</span>
            <el-switch v-model="f.errorLog" size="small" />
          </div>
          <div class="sd-row sd-switch-row">
            <span class="sd-label">{{ t('sites.gzip') }}</span>
            <el-switch v-model="f.gzip" size="small" />
          </div>
          <div class="sd-row sd-switch-row">
            <span class="sd-label">{{ t('sites.cacheExpires') }}</span>
            <el-switch v-model="f.cacheExpires" size="small" />
          </div>
          <div class="sd-row sd-switch-row">
            <span class="sd-label">{{ t('sites.autoIndex') }}</span>
            <el-switch v-model="f.autoIndex" size="small" />
          </div>
          <div class="sd-row">
            <span class="sd-label">{{ t('sites.indexFiles') }}</span>
            <div class="sd-fill">
              <el-input v-model="f.index" size="small" placeholder="index.html index.htm index.php" />
              <span class="sd-hint">{{ t('sites.indexHint') }}</span>
            </div>
          </div>
        </div>
      </el-tab-pane>

      <!-- ============ 安全配置 ============ -->
      <el-tab-pane :label="t('sites.tabSecurity')" name="security">
        <div class="sd-form">
          <div class="sd-row sd-switch-row">
            <span class="sd-label">{{ t('sites.denyHidden') }}</span>
            <el-switch v-model="f.denyHidden" size="small" />
          </div>
          <div class="sd-row sd-switch-row">
            <span class="sd-label">{{ t('sites.crossIsolate') }}</span>
            <el-switch v-model="f.crossIsolate" size="small" />
          </div>
          <div class="sd-row">
            <span class="sd-label">{{ t('sites.denyFiles') }}</span>
            <div class="sd-fill">
              <el-input v-model="f.denyFiles" size="small" placeholder="sql, bak, log, env" />
              <span class="sd-hint">{{ t('sites.denyFilesHint') }}</span>
            </div>
          </div>
          <div class="sd-row">
            <span class="sd-label">{{ t('sites.denyExec') }}</span>
            <div class="sd-fill">
              <el-input v-model="f.denyExecDirs" size="small" placeholder="uploads, data, runtime" />
              <span class="sd-hint">{{ t('sites.denyExecHint') }}</span>
            </div>
          </div>
          <div class="sd-row">
            <span class="sd-label">{{ t('sites.allowIPs') }}</span>
            <div class="sd-fill">
              <el-input v-model="f.allowIPs" size="small" placeholder="192.168.1.0/24, 127.0.0.1" />
              <span class="sd-hint">{{ t('sites.allowIPsHint') }}</span>
            </div>
          </div>
          <div class="sd-row">
            <span class="sd-label">{{ t('sites.denyIPs') }}</span>
            <el-input v-model="f.denyIPs" size="small" placeholder="10.0.0.5, 203.0.113.0/24" />
          </div>
        </div>
      </el-tab-pane>

      <!-- ============ 错误页面 ============ -->
      <el-tab-pane :label="t('sites.tabErrors')" name="errors">
        <div class="sd-col">
          <span class="sd-hint">{{ t('sites.errorPagesHint') }}</span>
          <el-input v-model="f.errorPages" type="textarea" :rows="12" size="small" class="sd-mono" />
          <div class="sd-errfoot">
            <el-button size="small" :loading="creatingPages" :disabled="isCreate" @click="onCreateErrorPages">
              {{ t('sites.errorPagesCreate') }}
            </el-button>
          </div>
        </div>
      </el-tab-pane>

      <!-- ============ 伪静态 ============ -->
      <el-tab-pane :label="t('sites.tabRewrite')" name="rewrite">
        <div class="sd-col">
          <span class="sd-hint">{{ t('sites.rewriteHint') }}</span>
          <el-input v-model="f.rewrite" type="textarea" :rows="14" size="small" class="sd-mono" />
        </div>
      </el-tab-pane>

      <!-- ============ 其他 ============ -->
      <el-tab-pane :label="t('sites.tabOther')" name="other">
        <div class="sd-form">
          <div class="sd-row">
            <span class="sd-label">{{ t('sites.redirectTo') }}</span>
            <div class="sd-fill">
              <el-input v-model="f.redirectTo" size="small" placeholder="https://www.example.com" />
              <span class="sd-hint">{{ t('sites.redirectHint') }}</span>
            </div>
          </div>
        </div>
      </el-tab-pane>
    </el-tabs>

    <template #footer>
      <el-button size="small" @click="visible = false">{{ t('sites.cancel') }}</el-button>
      <el-button size="small" type="primary" :loading="saving" @click="onSave">
        {{ isCreate ? t('sites.create') : t('sites.save') }}
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from '../i18n'
import { OpenDirectoryDialog } from '../../bindings/github.com/duihao/duihao/app'
import {
  CreateSite, UpdateSite, WriteHosts, CreateSiteErrorPages, SiteDefaultRoot, SiteDefaultPorts,
} from '../../bindings/github.com/duihao/duihao/backend/duihao/service/siteservice'
import { ListInstances, ListPlugins, CreateInstance } from '../../bindings/github.com/duihao/duihao/backend/duihao/service/plugincenterservice'
import { ListFTPAccounts, CreateFTPAccount } from '../../bindings/github.com/duihao/duihao/backend/duihao/service/ftpservice'
import { CreateMySQLDatabase } from '../../bindings/github.com/duihao/duihao/backend/duihao/service/mysqlservice'

interface InstanceView {
  id: string; plugin: string; version: string; name: string
  ports: Record<string, number>; live: string
}
interface SiteView {
  id: string; name: string; domains: string[]; root: string; port: number
  web: string; php?: string; db?: string; database?: string; tlsMode?: string
  webLive: string; webPort: number; sitePort: number; url: string; hostsReady: boolean
  options?: Record<string, any>
}
interface FTPAccountView { id: string; user: string; root: string; enabled: boolean }

const props = defineProps<{ site: SiteView | null }>()
const visible = defineModel<boolean>('visible', { default: false })
const emit = defineEmits<{ (e: 'saved'): void }>()

const { t } = useI18n()
const tab = ref('basic')
const saving = ref(false)
const creatingPages = ref(false)
const rootBase = ref('')
const instances = ref<InstanceView[]>([])
const rootTouched = ref(false)
// 跟着域名自动变的默认值（用于判断用户是否手工改过）
const autoUser = ref('')
const autoDb = ref('')

const isCreate = computed(() => !props.site)
const nginxInstances = computed(() => instances.value.filter(i => i.plugin === 'nginx'))
const phpInstances = computed(() => instances.value.filter(i => i.plugin === 'php'))
const dbInstances = computed(() => instances.value.filter(i => i.plugin === 'mysql'))
const ftpInstances = computed(() => instances.value.filter(i => i.plugin === 'ftpserver'))

// —— PHP 版本（phpStudy 式：站点直接绑版本，不必先去运行时面板启动/装配）——
// 清单里所有 PHP 版本 + 已存在的实例合并成一张列表；下拉存「版本号」，
// 保存时再解析成实例（没装就按需装配，首次会下载运行时）。
interface PhpChoice { version: string; instanceId: string; port: number; live: string }
const phpVersions = ref<string[]>([])
const provisioning = ref('')
const phpChoices = computed<PhpChoice[]>(() => {
  const byVersion = new Map<string, InstanceView>()
  for (const i of phpInstances.value) {
    if (!byVersion.has(i.version)) byVersion.set(i.version, i)
  }
  const out: PhpChoice[] = []
  const seen = new Set<string>()
  for (const v of phpVersions.value) {
    const inst = byVersion.get(v)
    out.push({ version: v, instanceId: inst?.id || '', port: inst?.ports?.fcgi || 0, live: inst?.live || '' })
    seen.add(v)
  }
  // 清单外的手工实例（自定义目录装的）也别丢
  for (const [v, inst] of byVersion) {
    if (seen.has(v)) continue
    out.push({ version: v, instanceId: inst.id, port: inst.ports?.fcgi || 0, live: inst.live })
  }
  return out
})
// 实例下拉文案：名称缺失退化为插件 id、端口缺失整段省略，
// 避免早期装配的记录（没有 name 字段）在界面上渲染出 "undefined · xxx"。
function instLabel(i: InstanceView, role: string): string {
  const name = i.name || i.plugin || i.id
  const port = i.ports?.[role]
  return port ? `${name} · ${i.id} · :${port}` : `${name} · ${i.id}`
}

function phpLabel(c: PhpChoice): string {
  if (!c.instanceId) return `PHP ${c.version} · ${t('sites.phpAutoInstall')}`
  const live = c.live === 'running' ? ` · ${t('sites.running')}` : ''
  const port = c.port ? ` · :${c.port}` : ''
  return `PHP ${c.version} · ${c.instanceId}${port}${live}`
}

// 版本 → 实例 id：已装直接用，没装就装配一个（首次会取包，可能要等一会）。
async function resolvePhpInstance(): Promise<string> {
  if (!f.usePhp) return ''
  const v = f.php
  const hit = phpChoices.value.find(c => c.version === v && c.instanceId)
  if (hit) return hit.instanceId
  if (!phpChoices.value.some(c => c.version === v)) {
    throw new Error(t('sites.phpVersionMissing', { v }))
  }
  provisioning.value = v
  ElMessage.info(t('sites.phpProvisioning', { v }))
  try {
    const view = (await CreateInstance('php', v, '', [])) as unknown as InstanceView
    instances.value = [...instances.value, view]
    return view.id
  } finally {
    provisioning.value = ''
  }
}

// 编辑态：站点已关联的 FTP 账号 / 数据库（已存在则不允许"取消勾选"）
const ftpLinkedUser = ref('')
const ftpLinked = computed(() => !isCreate.value && !!ftpLinkedUser.value)
const dbLinked = computed(() => !isCreate.value && !!props.site?.db && !!props.site?.database)

const webLabel = computed(() => {
  const i = instances.value.find(x => x.id === props.site?.web)
  if (!i) return props.site?.web || '—'
  const name = i.name || i.plugin || i.id
  const port = i.ports?.http ? ` · :${i.ports.http}` : ''
  return `${name}@${i.version} · ${i.id}${port}`
})

const f = reactive({
  mainDomain: '', secondDomain: '', scheme: 'http', port: '', root: '',
  createFtp: false, ftpInstance: '', ftpUser: '', ftpPass: '',
  createDb: false, dbInstance: '', dbName: '', dbUser: '', dbPass: '',
  syncHosts: false, production: false,
  usePhp: false, php: '', web: '', expireAt: '', remark: '',
  accessLog: false, errorLog: false, gzip: false, cacheExpires: false, autoIndex: false,
  index: '', denyHidden: false, denyFiles: '', denyExecDirs: '', crossIsolate: false,
  allowIPs: '', denyIPs: '', errorPages: '', rewrite: '', redirectTo: '',
})

// —— 随机口令：FTP / 数据库默认给一个可用的强密码，用户可改 ——
function randPass(n = 14): string {
  const cs = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789'
  const buf = new Uint32Array(n)
  try {
    crypto.getRandomValues(buf)
  } catch {
    // 极老/受限 webview 兜底：和时间戳混一点熵，够本机 FTP 用
    for (let i = 0; i < n; i++) buf[i] = (Date.now() + i * 7919) % 4294967296
  }
  let s = ''
  for (let i = 0; i < n; i++) s += cs[buf[i] % cs.length]
  return s
}
// 域名 → 可用的库名/用户名（MySQL 标识符只留字母数字下划线）
function ident(domain: string, maxLen: number): string {
  const s = (domain || '').toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '')
  return (s || 'site').slice(0, maxLen)
}

function resetForm() {
  Object.assign(f, {
    mainDomain: '', secondDomain: '', scheme: 'http', port: '', root: '',
    createFtp: false, ftpInstance: '', ftpUser: '', ftpPass: '',
    createDb: false, dbInstance: '', dbName: '', dbUser: '', dbPass: '',
    syncHosts: false, production: false,
    usePhp: false, php: '', web: '', expireAt: '', remark: '',
    accessLog: false, errorLog: false, gzip: false, cacheExpires: false, autoIndex: false,
    index: '', denyHidden: false, denyFiles: '', denyExecDirs: '', crossIsolate: false,
    allowIPs: '', denyIPs: '', errorPages: '', rewrite: '', redirectTo: '',
  })
  rootTouched.value = false
  autoUser.value = ''
  autoDb.value = ''
  ftpLinkedUser.value = ''
}

async function fillFromSite(s: SiteView | null) {
  const o = s?.options || {}
  f.mainDomain = s?.domains?.[0] || ''
  f.secondDomain = (s?.domains || []).slice(1).join(', ')
  f.scheme = s?.tlsMode === 'selfsigned' ? 'https' : 'http'
  // 新建时保留 watch 里预填的默认端口（80），编辑时用站点当前端口
  if (s) f.port = String(s.sitePort || s.port || '')
  f.root = s?.root || ''
  rootTouched.value = !!s
  f.usePhp = !!s?.php
  // 回填的是「版本号」：站点记录存实例 id，界面展示版本（下拉按版本工作）
  {
    const phpInst = instances.value.find(i => i.id === s?.php)
    f.php = s?.php ? (phpInst?.version || '') : (phpChoices.value[0]?.version || '')
  }
  f.web = s?.web || nginxInstances.value[0]?.id || ''
  f.expireAt = o.expireAt || ''
  f.remark = o.remark || ''
  f.production = !!o.production
  f.accessLog = !!o.accessLog
  f.errorLog = !!o.errorLog
  f.gzip = !!o.gzip
  f.cacheExpires = !!o.cacheExpires
  f.autoIndex = !!o.autoIndex
  f.index = o.index || ''
  f.denyHidden = !!o.denyHidden
  f.denyFiles = o.denyFiles || ''
  f.denyExecDirs = o.denyExecDirs || ''
  f.crossIsolate = !!o.crossIsolate
  f.allowIPs = o.allowIPs || ''
  f.denyIPs = o.denyIPs || ''
  f.errorPages = o.errorPages || ''
  f.rewrite = o.rewrite || ''
  f.redirectTo = o.redirectTo || ''
  f.syncHosts = !!s?.hostsReady

  // 数据库：编辑态有值即视为已关联
  f.createDb = !!s?.db && !!s?.database
  f.dbInstance = s?.db || dbInstances.value[0]?.id || ''
  f.dbName = s?.database || ident(f.mainDomain, 20)
  f.dbUser = f.dbName
  f.dbPass = randPass()

  // FTP：编辑态按"站点根目录"匹配已有账号
  f.createFtp = false
  f.ftpInstance = ftpInstances.value[0]?.id || ''
  f.ftpUser = ident(f.mainDomain, 20)
  f.ftpPass = randPass()
  if (!s) {
    if (f.mainDomain) f.root = defaultRootFor(f.mainDomain)
    return
  }
  for (const ins of ftpInstances.value) {
    try {
      const accs = (await ListFTPAccounts(ins.id)) as unknown as FTPAccountView[]
      const hit = (accs || []).find(a => samePath(a.root, s.root))
      if (hit) {
        ftpLinkedUser.value = hit.user
        f.createFtp = true
        f.ftpInstance = ins.id
        break
      }
    } catch { /* 单个实例读失败不影响其它 */ }
  }
}

function samePath(a: string, b: string): boolean {
  const n = (p: string) => String(p || '').replace(/[\\/]+/g, '/').replace(/\/+$/, '').toLowerCase()
  return !!a && n(a) === n(b)
}

function defaultRootFor(domain: string): string {
  const base = rootBase.value.replace(/[\\/]+$/, '')
  return `${base}/${ident(domain, 32)}`
}

watch(visible, async (open) => {
  if (!open) return
  tab.value = 'basic'
  resetForm()
  try {
    instances.value = (await ListInstances()) as unknown as InstanceView[]
  } catch { /* 实例列表失败不影响保存 */ }
  // PHP 可绑定的版本：清单里的全部版本（未装的也能选，保存时按需装配）
  try {
    const plugins = (await ListPlugins()) as unknown as { id: string; versions: string[] }[]
    phpVersions.value = plugins.find(p => p.id === 'php')?.versions || []
  } catch { phpVersions.value = [] }
  try {
    rootBase.value = String(await SiteDefaultRoot())
    const ports = (await SiteDefaultPorts()) as unknown as Record<string, number>
    // 新建时预填 http 默认端口（留空也行，后端会自动挑 80/443 起顺延）
    if (isCreate.value) f.port = String(ports.http)
  } catch { rootBase.value = '' }
  await fillFromSite(props.site)
})

// 域名变化时，只要用户没手动改过根目录就跟着走（phpStudy 的默认习惯）
watch(() => f.mainDomain, (d) => {
  if (!rootTouched.value && d) f.root = defaultRootFor(d)
  if (!d) return
  // FTP 账号 / 库名 / 库用户默认跟着域名走（没被用户改过时才动）
  if (isCreate.value) {
    if (!f.ftpUser || f.ftpUser === 'site' || f.ftpUser === autoUser.value) {
      autoUser.value = ident(d, 20)
      f.ftpUser = autoUser.value
    }
    if (!f.dbName || f.dbName === 'site' || f.dbName === autoDb.value) {
      autoDb.value = ident(d, 20)
      f.dbName = autoDb.value
      f.dbUser = autoDb.value
    }
  }
})

async function onSchemeChange(v: string | number | boolean | undefined) {
  const scheme = String(v || 'http')
  let ports: Record<string, number> = { http: 80, https: 443 }
  try { ports = (await SiteDefaultPorts()) as unknown as Record<string, number> } catch { /* 用默认 */ }
  const cur = Number(String(f.port).trim())
  // 只在端口还是"上一个协议默认值"时跟着换，避免覆盖用户手填的端口
  if (!cur || cur === ports.http || cur === ports.https) {
    f.port = String(scheme === 'https' ? ports.https : ports.http)
  }
}

async function onPickRoot() {
  try {
    const dir = await OpenDirectoryDialog()
    if (dir) {
      f.root = String(dir)
      rootTouched.value = true
    }
  } catch { /* 用户取消 */ }
}

function collectDomains(): string[] {
  return [f.mainDomain, ...f.secondDomain.split(/[,\s]+/)]
    .map(d => d.trim()).filter(Boolean)
}

function buildOptions() {
  return {
    accessLog: f.accessLog, errorLog: f.errorLog, gzip: f.gzip,
    cacheExpires: f.cacheExpires, autoIndex: f.autoIndex, index: f.index.trim(),
    denyHidden: f.denyHidden, denyFiles: f.denyFiles.trim(), denyExecDirs: f.denyExecDirs.trim(),
    crossIsolate: f.crossIsolate, allowIPs: f.allowIPs.trim(), denyIPs: f.denyIPs.trim(),
    errorPages: f.errorPages, rewrite: f.rewrite, redirectTo: f.redirectTo.trim(),
    production: f.production, expireAt: f.expireAt.trim(), remark: f.remark.trim(),
  } as any
}

// 「创建环境」是一串可以部分失败的副作用：站点本身建成了就该保留，
// 失败的项汇总成一条提示，而不是把整次创建判负。
async function applyEnv(siteID: string, root: string, warn: string[]) {
  const needFtp = f.createFtp && !ftpLinked.value
  const needDb = f.createDb && !dbLinked.value
  const needHosts = f.syncHosts && (isCreate.value || !props.site?.hostsReady)

  if (needFtp) {
    if (!f.ftpInstance) {
      warn.push(t('sites.envFtpNoInstance'))
    } else {
      try {
        const user = f.ftpUser.trim() || ident(f.mainDomain, 20)
        await CreateFTPAccount(f.ftpInstance, user, f.ftpPass, root, false, siteID)
      } catch (e) {
        warn.push(`${t('sites.createFtp')}: ${errText(e)}`)
      }
    }
  }
  if (needDb) {
    if (!f.dbInstance) {
      warn.push(t('sites.envDbNoInstance'))
    } else {
      try {
        await CreateMySQLDatabase(f.dbInstance, {
          name: f.dbName.trim(), user: f.dbUser.trim(), password: f.dbPass, charset: 'utf8mb4',
        } as any)
      } catch (e) {
        warn.push(`${t('sites.createDb')}: ${errText(e)}`)
      }
    }
  }
  if (needHosts) {
    try {
      await WriteHosts(siteID)
    } catch (e) {
      warn.push(`${t('sites.syncHosts')}: ${errText(e)}`)
    }
  }
}

async function onSave() {
  const domains = collectDomains()
  if (domains.length === 0) {
    ElMessage.error(t('sites.domainsHint'))
    return
  }
  if (isCreate.value && !f.web) {
    ElMessage.error(t('sites.needWeb'))
    return
  }
  if (isCreate.value && !f.root.trim()) {
    ElMessage.error(t('sites.rootHint'))
    return
  }
  if (f.usePhp && !f.php) {
    ElMessage.error(t('sites.phpPick'))
    return
  }
  const tls = f.scheme === 'https'
  const port = Number(String(f.port).trim()) || 0

  saving.value = true
  const warn: string[] = []
  try {
    // PHP 版本 → 实例（未装的版本在这里按需装配；失败就整单不落，避免存出半截站点）
    const php = await resolvePhpInstance()
    const db = f.createDb ? f.dbInstance : ''
    const database = f.createDb ? f.dbName.trim() : ''
    let siteID = props.site?.id || ''
    if (isCreate.value) {
      const view = (await CreateSite(
        '', domains, port, f.root.trim(), f.web, php, db, database, tls, buildOptions(),
      )) as unknown as SiteView
      siteID = view.id
    } else {
      await UpdateSite(
        siteID, '', domains, port, f.root.trim(), php, db, database, tls, buildOptions(),
      )
    }
    await applyEnv(siteID, f.root.trim(), warn)

    if (warn.length > 0) {
      ElMessage.warning(t('sites.envPartial', { list: warn.join('；') }))
    } else {
      ElMessage.success(t(isCreate.value ? 'sites.created' : 'sites.saved'))
    }
    visible.value = false
    emit('saved')
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    saving.value = false
  }
}

async function onCreateErrorPages() {
  if (!props.site) return
  creatingPages.value = true
  try {
    const made = (await CreateSiteErrorPages(props.site.id)) as unknown as string[]
    if (!made || made.length === 0) ElMessage.info(t('sites.errorPagesNone'))
    else ElMessage.success(t('sites.errorPagesCreated', { n: made.length }))
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    creatingPages.value = false
  }
}

function errText(e: unknown): string {
  if (e instanceof Error) return e.message
  try { return JSON.stringify(e) } catch { return String(e) }
}
</script>

<style scoped>
.sd-dialog :deep(.el-dialog__body) { padding-top: 0.25rem; }
.sd-tabs :deep(.el-tabs__header) { margin-bottom: 0.75rem; }
.sd-tabs :deep(.el-tabs__item) { font-size: 0.8125rem; height: 2.25rem; line-height: 2.25rem; }

.sd-alert { margin-bottom: 0.5rem; }
.sd-form { display: flex; flex-direction: column; gap: 0.75rem; padding: 0.25rem 0.125rem; }
.sd-col { display: flex; flex-direction: column; gap: 0.5rem; }
.sd-row { display: flex; align-items: flex-start; gap: 0.75rem; }
.sd-row-top { align-items: flex-start; }
.sd-switch-row { align-items: center; }
.sd-label {
  flex: 0 0 7.5rem; text-align: right; padding-top: 0.3125rem;
  font-size: 0.8125rem; color: var(--text-secondary);
}
.sd-switch-row .sd-label { padding-top: 0; }
.sd-fill { flex: 1; display: flex; flex-direction: column; gap: 0.25rem; min-width: 0; }
.sd-pick { flex: 1; display: flex; gap: 0.375rem; min-width: 0; }
.sd-pick :deep(.el-input) { flex: 1; }
.sd-portline { display: flex; align-items: center; gap: 0.75rem; }
.sd-port { max-width: 7rem; }
.sd-inline { display: flex; align-items: center; gap: 0.5rem; min-width: 0; }
.sd-grow { flex: 1; min-width: 0; }
.sd-date { flex: 0 0 10rem; }
.sd-hint { font-size: 0.6875rem; line-height: 1.4; color: var(--text-muted); }
.sd-mono :deep(textarea) { font-family: var(--font-mono); font-size: 0.75rem; line-height: 1.55; }
.sd-errfoot { display: flex; justify-content: flex-end; }

/* 创建环境：四个勾选一行 + 子表单缩进块 */
.sd-env { display: flex; flex-wrap: wrap; align-items: center; gap: 0.25rem 1rem; }
.sd-env :deep(.el-checkbox) { margin-right: 0; }
.sd-sub {
  margin-top: 0.375rem; padding: 0.4375rem 0.625rem;
  border: 1px solid var(--border-subtle); border-radius: var(--radius-sm);
  background: var(--bg-base);
  display: flex; flex-direction: column; gap: 0.375rem;
}
.sd-subrow { display: flex; align-items: center; gap: 0.5rem; min-width: 0; }
.sd-sublabel { flex: 0 0 auto; font-size: 0.6875rem; color: var(--text-muted); white-space: nowrap; }
</style>
