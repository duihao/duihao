<template>
  <el-dialog
    v-model="visible"
    :title="t('deploy.title')"
    width="46rem"
    draggable
    append-to-body
    :close-on-click-modal="false"
    @open="load"
  >
    <!-- 站点上下文：装到哪儿、什么栈 -->
    <div class="dp-head">
      <div class="dp-site">
        <span class="dp-dom">{{ site?.domains?.[0] || site?.id }}</span>
        <span class="dp-port">:{{ site?.sitePort }}</span>
        <span v-if="site?.php" class="dp-tag dp-tag-php">PHP</span>
        <span v-if="site?.database" class="dp-tag">{{ site.database }}</span>
      </div>
      <button class="dp-link" :title="site?.root" @click="openPkgDir">{{ t('deploy.pkgDir') }}</button>
    </div>

    <!-- 部署成功 -->
    <div v-if="result" class="dp-done">
      <div class="dp-done-title">
        <span class="dp-done-ico">✓</span>
        {{ t('deploy.done', { name: result.appName, n: result.files }) }}
      </div>
      <div class="dp-done-meta">
        <span>{{ t('deploy.source') }}: {{ result.source }}</span>
        <span>{{ (result.bytes / 1024 / 1024).toFixed(1) }} MB</span>
      </div>
      <p v-if="result.note" class="dp-note">{{ result.note }}</p>
      <div v-if="result.dbHint" class="dp-db">
        <span class="dp-db-label">{{ t('deploy.dbHint') }}</span>
        <code class="dp-db-val">{{ result.dbHint }}</code>
        <el-button size="small" text @click="copyDb">{{ t('sites.copy') }}</el-button>
      </div>
      <p v-if="result.warning" class="dp-warn">{{ result.warning }}</p>
      <div class="dp-done-acts">
        <el-button size="small" type="primary" @click="openSetup">{{ t('deploy.openSetup') }}</el-button>
        <el-button size="small" @click="result = null; load()">{{ t('deploy.backList') }}</el-button>
      </div>
    </div>

    <!-- 程序列表 -->
    <div v-else class="dp-list">
      <p class="dp-hint">{{ t('deploy.hint') }}</p>
      <div v-for="a in apps" :key="a.id" class="dp-card" :class="{ 'dp-on': a.deployed }">
        <div class="dp-row">
          <span class="dp-name">{{ a.name }}</span>
          <span class="dp-ver">{{ a.version }}</span>
          <span v-if="a.deployed" class="dp-badge">{{ t('deploy.installed') }}</span>
          <span class="dp-grow" />
          <el-button
            v-if="a.id === 'custom' || hasLocal(a.id)"
            size="small" text class="dp-pick"
            :disabled="deploying"
            @click="pickZip(a)"
          >{{ zipOf(a.id) ? t('deploy.repick') : t('deploy.pickZip') }}</el-button>
          <el-button
            size="small" type="primary"
            :disabled="!!a.blocked || deploying"
            :loading="deploying && curApp === a.id"
            @click="run(a)"
          >{{ a.deployed ? t('deploy.redeploy') : t('deploy.deploy') }}</el-button>
        </div>

        <div class="dp-sum">{{ a.summary }}</div>

        <div class="dp-meta">
          <span v-if="a.requiresPhp" class="dp-tag">PHP {{ a.requiresPhp }}</span>
          <span v-if="a.requiresDb" class="dp-tag">DB {{ a.requiresDb }}</span>
          <span v-for="e in a.requiresExt || []" :key="e" class="dp-tag">{{ e }}</span>
          <span v-if="zipOf(a.id)" class="dp-file">📦 {{ baseName(zipOf(a.id)) }}</span>
          <span v-if="a.blocked" class="dp-block">{{ a.blocked }}</span>
        </div>

        <div v-if="a.id === 'custom'" class="dp-custom">
          <el-input v-model="customEntry" size="small" :placeholder="t('deploy.customEntry')" />
        </div>

        <div v-if="deploying && curApp === a.id" class="dp-prog">
          <el-progress :percentage="progress.percent" :stroke-width="6" :show-text="false" />
          <span class="dp-phase">{{ progress.message }}</span>
        </div>
      </div>
    </div>

    <template #footer>
      <el-button size="small" @click="visible = false">{{ t('common.close') }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, watch, onUnmounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Events } from '@wailsio/runtime'
import { useI18n } from '../i18n'
import { OpenFileDialogFiltered } from '../../bindings/github.com/duihao/duihao/app'
import {
  ListDeployApps, DeployApp, OpenDeployPkgDir, OpenDeploySetup,
} from '../../bindings/github.com/duihao/duihao/backend/duihao/service/deployservice'

interface SiteLite {
  id: string; domains: string[]; sitePort: number; root: string
  php?: string; database?: string
}
interface DeployAppView {
  id: string; name: string; version: string; category: string; summary: string
  requiresPhp?: string; requiresDb?: string; requiresExt?: string[]
  entry: string; note?: string
  deployed: boolean; deployedVersion?: string; deployedAt?: string
  blocked?: string
}
interface DeployResult {
  appId: string; appName: string; version: string; root: string; source: string
  files: number; bytes: number; entry: string; setupUrl: string
  note?: string; warning?: string
  prefillActive?: boolean; dbHint?: string
}

const props = defineProps<{ visible: boolean; site: SiteLite | null }>()
const emit = defineEmits<{ (e: 'update:visible', v: boolean): void }>()
const { t } = useI18n()

const visible = ref(props.visible)
watch(() => props.visible, (v) => { visible.value = v })
watch(visible, (v) => emit('update:visible', v))

const apps = ref<DeployAppView[]>([])
const deploying = ref(false)
const curApp = ref('')
const progress = ref({ percent: 0, message: '' })
const result = ref<DeployResult | null>(null)
const customEntry = ref('install.php')
// 每个程序选过的本地包：appId → 绝对路径
const zips = ref<Record<string, string>>({})

function zipOf(id: string): string { return zips.value[id] || '' }
function baseName(p: string): string { return p ? p.split(/[\\/]/).pop() || p : '' }
function hasLocal(id: string): boolean {
  // 只有声明了 local 源的模板才显示「选择本地包」
  return id !== 'custom'
}

async function load() {
  if (!props.site) return
  result.value = null
  try {
    apps.value = (await ListDeployApps(props.site.id)) as unknown as DeployAppView[]
  } catch (e) {
    ElMessage.error(parseErr(e))
  }
}

async function pickZip(a: DeployAppView) {
  try {
    const p = await OpenFileDialogFiltered(t('deploy.pickZip'), 'ZIP', '*.zip', '')
    if (p) zips.value = { ...zips.value, [a.id]: p }
  } catch (e) {
    ElMessage.error(parseErr(e))
  }
}

async function run(a: DeployAppView) {
  if (!props.site) return
  const entry = a.id === 'custom' ? customEntry.value.trim() : a.entry
  const zip = zipOf(a.id)
  if (a.id === 'custom' && !zip) {
    ElMessage.warning(t('deploy.needZip'))
    return
  }
  result.value = null
  deploying.value = true
  curApp.value = a.id
  progress.value = { percent: 0, message: '' }
  try {
    result.value = (await DeployApp(props.site.id, a.id, zip, entry, false)) as unknown as DeployResult
  } catch (e) {
    const msg = parseErr(e)
    if (msg.includes('已有')) {
      // 目录非空 → 让用户明确确认覆盖同名文件
      try {
        await ElMessageBox.confirm(msg, t('deploy.overwriteTitle'), {
          type: 'warning', confirmButtonText: t('deploy.overwriteOk'), cancelButtonText: t('common.cancel'),
        })
        result.value = (await DeployApp(props.site.id, a.id, zip, entry, true)) as unknown as DeployResult
      } catch (e2) {
        if (e2 !== 'cancel' && e2 !== 'close') ElMessage.error(parseErr(e2))
      }
    } else if (msg.includes('拿不到')) {
      // 没有内置包也没有可用下载源 → 直接把文件选择器弹出来，别让用户卡在这儿
      ElMessage.warning(t('deploy.needLocal'))
      await pickZip(a)
      if (zipOf(a.id)) await run(a)
    } else {
      ElMessage.error(msg)
    }
  } finally {
    deploying.value = false
    curApp.value = ''
  }
}

async function openSetup() {
  if (!props.site || !result.value) return
  try {
    await OpenDeploySetup(props.site.id, result.value.entry)
  } catch (e) {
    ElMessage.error(parseErr(e))
  }
}

async function openPkgDir() {
  try {
    await OpenDeployPkgDir()
  } catch (e) {
    ElMessage.error(parseErr(e))
  }
}

async function copyDb() {
  if (!result.value?.dbHint) return
  try {
    await navigator.clipboard.writeText(result.value.dbHint)
    ElMessage.success(t('sites.copied'))
  } catch {
    ElMessage.info(result.value.dbHint)
  }
}

function parseErr(e: unknown): string {
  if (e instanceof Error) return e.message
  try { return JSON.stringify(e) } catch { return String(e) }
}

// 进度事件：只认当前站点
const unsub = Events.On('duihao:deploy-progress', (ev) => {
  const d = (ev as { data?: Record<string, unknown> }).data || {}
  if (!props.site || d.siteId !== props.site.id) return
  progress.value = {
    percent: Number(d.percent) || 0,
    message: String(d.message || ''),
  }
})
onUnmounted(() => { if (typeof unsub === 'function') unsub() })
</script>

<style scoped>
.dp-head { display: flex; align-items: center; gap: 0.5rem; margin-bottom: 0.625rem; min-width: 0; }
.dp-site { display: flex; align-items: center; gap: 0.25rem; min-width: 0; }
.dp-dom { font-size: 0.8125rem; font-weight: 600; color: var(--text-primary); }
.dp-port { font-size: 0.6875rem; color: var(--text-muted); font-variant-numeric: tabular-nums; }
.dp-link {
  margin-left: auto; flex-shrink: 0; background: transparent; border: none; padding: 0;
  font: inherit; font-size: 0.6875rem; color: var(--accent); cursor: pointer;
}
.dp-link:hover { text-decoration: underline; }

.dp-list { display: flex; flex-direction: column; gap: 0.5rem; max-height: 62vh; overflow-y: auto; padding-right: 0.125rem; }
.dp-hint { margin: 0 0 0.25rem; font-size: 0.6875rem; color: var(--text-muted); }
.dp-card {
  border: 1px solid var(--border-subtle); border-radius: var(--radius-md);
  background: var(--bg-elevated); padding: 0.625rem 0.75rem;
}
.dp-card.dp-on { border-color: color-mix(in srgb, var(--success) 40%, transparent); }
.dp-row { display: flex; align-items: center; gap: 0.375rem; min-width: 0; }
.dp-name { font-size: 0.8125rem; font-weight: 600; color: var(--text-primary); }
.dp-ver { font-size: 0.625rem; color: var(--text-muted); font-variant-numeric: tabular-nums; }
.dp-badge {
  font-size: 0.5625rem; padding: 0 0.3125rem; border-radius: 3px;
  color: var(--success); border: 1px solid color-mix(in srgb, var(--success) 45%, transparent);
}
.dp-grow { flex: 1 1 auto; }
.dp-row :deep(.el-button + .el-button) { margin-left: 0.25rem; }
.dp-sum { margin-top: 0.25rem; font-size: 0.6875rem; line-height: 1.5; color: var(--text-secondary); }
.dp-meta { display: flex; align-items: center; gap: 0.25rem; flex-wrap: wrap; margin-top: 0.375rem; }
.dp-tag {
  font-size: 0.5625rem; padding: 0 0.25rem; border-radius: 3px;
  color: var(--text-muted); border: 1px solid var(--border-subtle);
}
.dp-tag-php { font-family: var(--font-mono); color: var(--text-secondary); }
.dp-file { font-size: 0.5625rem; color: var(--accent); max-width: 18rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.dp-block { font-size: 0.625rem; color: var(--warning); }
.dp-custom { margin-top: 0.375rem; }
.dp-prog { margin-top: 0.5rem; display: flex; align-items: center; gap: 0.5rem; }
.dp-prog :deep(.el-progress) { flex: 1 1 auto; }
.dp-phase { font-size: 0.625rem; color: var(--text-muted); flex: 0 0 auto; }

.dp-done { padding: 0.5rem 0.25rem; }
.dp-done-title { font-size: 0.875rem; font-weight: 600; color: var(--text-primary); display: flex; align-items: center; gap: 0.375rem; }
.dp-done-ico {
  display: inline-flex; align-items: center; justify-content: center;
  width: 1.25rem; height: 1.25rem; border-radius: 50%;
  background: color-mix(in srgb, var(--success) 18%, transparent); color: var(--success); font-size: 0.75rem;
}
.dp-done-meta { margin-top: 0.375rem; display: flex; gap: 0.75rem; font-size: 0.6875rem; color: var(--text-muted); }
.dp-note { margin: 0.625rem 0 0; font-size: 0.6875rem; color: var(--text-secondary); }
.dp-warn { margin: 0.375rem 0 0; font-size: 0.6875rem; color: var(--warning); }
.dp-db { margin-top: 0.5rem; display: flex; align-items: center; gap: 0.375rem; flex-wrap: wrap; }
.dp-db-label { font-size: 0.625rem; color: var(--text-muted); flex: 0 0 auto; }
.dp-db-val {
  font-family: var(--font-mono); font-size: 0.625rem; color: var(--text-primary);
  background: var(--bg-base); border: 1px solid var(--border-subtle);
  border-radius: var(--radius-sm); padding: 0.125rem 0.375rem; word-break: break-all;
}
.dp-done-acts { margin-top: 0.875rem; display: flex; gap: 0.375rem; }
</style>
