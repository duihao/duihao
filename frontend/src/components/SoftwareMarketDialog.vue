<template>
  <el-dialog
    v-model="show"
    :title="t('market.title')"
    :width="dialogW"
    :fullscreen="full"
    draggable
    :close-on-click-modal="false"
    append-to-body
    class="mk-dialog"
  >
    <template #header="{ titleId, titleClass }">
      <div class="mk-head">
        <span :id="titleId" :class="titleClass">{{ t('market.title') }}</span>
        <button
          class="mk-zoom"
          :title="full ? t('market.restore') : t('market.maximize')"
          @click="full = !full"
        >
          <!-- 放大：四角箭头 / 还原：向内收缩 -->
          <svg v-if="!full" viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.5">
            <path d="M6 2H2v4M10 2h4v4M6 14H2v-4M10 14h4v-4" stroke-linecap="round" />
          </svg>
          <svg v-else viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.5">
            <path d="M2 6h4V2M14 6h-4V2M2 10h4v4M14 10h-4v4" stroke-linecap="round" />
          </svg>
        </button>
      </div>
    </template>

    <div class="mk-wrap" :class="{ 'mk-wrap-full': full }">
      <div class="mk-body" :class="{ 'mk-body-full': full }">
        <!-- 左：分类（由清单 category 聚合，未知分类原样显示，新增清单即可新增分类） -->
        <div class="mk-cats" :class="{ 'mk-cats-full': full }">
          <button
            v-for="c in cats"
            :key="c.id"
            class="mk-cat"
            :class="{ 'mk-cat-on': c.id === cat }"
            @click="cat = c.id"
          >
            <span class="mk-cat-name">{{ c.name }}</span>
            <span class="mk-cat-n">{{ c.count }}</span>
          </button>
        </div>

        <!-- 右：该分类下的软件 -->
        <div class="mk-list" :class="{ 'mk-list-full': full }">
          <div v-for="p in shown" :key="p.id" class="mk-item">
            <div class="mk-item-main">
              <div class="mk-item-top">
                <span class="mk-name">{{ p.name }}</span>
                <span class="mk-kind">{{ t('plugins.kind.' + p.kind) }}</span>
                <span v-if="p.installedVersions.length" class="mk-badge">
                  {{ t('market.installedN', { n: p.installedVersions.length }) }}
                </span>
              </div>
              <div class="mk-desc">{{ p.description || p.id }}</div>
            </div>

            <div class="mk-ops">
              <el-select
                v-model="sel[p.id]"
                size="small"
                class="mk-ver"
                :title="t('market.verHint')"
                @change="(v) => onPickVersion(p, v)"
              >
                <el-option
                  v-for="v in p.versions"
                  :key="v"
                  :label="verLabel(p, v)"
                  :value="v"
                />
              </el-select>
              <el-button
                v-if="!p.installedVersions.length"
                size="small"
                type="primary"
                class="mk-install"
                :loading="busy === p.id"
                @click="onInstall(p)"
              >{{ t('plugins.install') }}</el-button>
              <el-button
                v-else
                size="small"
                plain
                type="primary"
                class="mk-install"
                @click="onOpenVersions(p)"
              >{{ t('market.manage') }}</el-button>
              <el-dropdown
                trigger="click"
                popper-class="rp-more-popper"
                @command="(cmd) => onMore(cmd, p)"
              >
                <el-button size="small" class="mk-more" :title="t('plugins.more')">⋯</el-button>
                <template #dropdown>
                  <el-dropdown-menu>
                    <el-dropdown-item command="versions">{{ t('plugins.versions') }}</el-dropdown-item>
                    <el-dropdown-item v-if="instOf(p)" command="default">
                      {{ t('market.setDefault') }}
                    </el-dropdown-item>
                    <el-dropdown-item command="settings" :disabled="!instOf(p)">
                      {{ t('plugins.settings') }}
                    </el-dropdown-item>
                    <el-dropdown-item command="logs" :disabled="!instOf(p)">
                      {{ t('plugins.logs') }}
                    </el-dropdown-item>
                    <el-dropdown-item command="url" divided>{{ t('market.copyURL') }}</el-dropdown-item>
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
            </div>
          </div>

          <div v-if="!shown.length" class="mk-empty">{{ t('market.empty') }}</div>
        </div>
      </div>

      <div class="mk-note">
        {{ t('market.note', { dir: manifestDir || 'config/plugins' }) }}
      </div>

      <!-- 右下角拖拽手柄：调整弹窗宽度（关闭全屏时可用），宽度记在本地 -->
      <div v-if="!full" class="mk-grip" :title="t('market.resize')" @mousedown.prevent="onGripDown" />
    </div>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from '../i18n'
import { CreateInstance } from '../../bindings/github.com/ys-ll/uniterm/backend/duihao/service/plugincenterservice'

interface VersionInfo { version: string; url?: string; sha256?: string; size?: number; installed: boolean }
interface PluginView {
  id: string; name: string; kind: string; category: string; description: string
  versions: string[]; installedVersions: string[]; defaultVersion: string
  workers: number; hasInstance: boolean; multiRun: boolean
  versionInfos?: VersionInfo[]
}
interface InstanceView {
  id: string; plugin: string; version: string; name: string
  ports: Record<string, number>; workerPorts?: Record<string, number[]>
  workers: number; state: string; runtimeDir: string
  live: string; pids: number[]; runtimeSource: string
}

const props = defineProps<{
  plugins: PluginView[]
  instances: InstanceView[]
  /** 用户自定义清单目录（展示在底部说明里，便于扩展分类与软件） */
  manifestDir?: string
  /** 各软件当前选定的默认版本（插件 id → 版本） */
  defaults?: Record<string, string>
}>()
const emit = defineEmits<{
  (e: 'refresh'): void
  (e: 'installed', pluginId: string, version: string): void
  (e: 'default', pluginId: string, version: string): void
  (e: 'versions', plugin: PluginView): void
  (e: 'settings', inst: InstanceView): void
  (e: 'logs', inst: InstanceView): void
}>()

const show = defineModel<boolean>({ default: false })
const { t } = useI18n()
const cat = ref('all')
const busy = ref('')
const sel = reactive<Record<string, string>>({})

// 弹窗尺寸：全屏开关 + 拖拽宽度（宽度持久化，下次打开保持）
const W_KEY = 'duihao.marketWidth'
const full = ref(false)
const w = ref(Number(localStorage.getItem(W_KEY)) || 0)
const dialogW = computed(() => (w.value ? `${w.value}px` : '56rem'))

function onGripDown(e: MouseEvent) {
  const el = document.querySelector('.mk-dialog .el-dialog') as HTMLElement | null
  const startW = el ? el.getBoundingClientRect().width : 896
  const startX = e.clientX
  const move = (ev: MouseEvent) => {
    const max = window.innerWidth - 24
    w.value = Math.round(Math.max(544, Math.min(max, startW + (ev.clientX - startX))))
  }
  const up = () => {
    document.removeEventListener('mousemove', move)
    document.removeEventListener('mouseup', up)
    try { localStorage.setItem(W_KEY, String(w.value)) } catch { /* 忽略 */ }
  }
  document.addEventListener('mousemove', move)
  document.addEventListener('mouseup', up)
}

// 市场条目：常驻服务 + 内置服务（cli 工具不进市场，由站点按需调用）
const items = computed(() =>
  props.plugins.filter(p => p.kind === 'process' || p.kind === 'embedded'))

// 分类名：有词条用词条，没有就原样显示 category —— 自定义清单新增分类无需改前端
function catName(id: string): string {
  const k = 'market.cat.' + id
  const v = t(k)
  return v === k ? id : v
}

const cats = computed(() => {
  const seen: string[] = []
  const count: Record<string, number> = {}
  for (const p of items.value) {
    const c = p.category || 'other'
    if (!seen.includes(c)) seen.push(c)
    count[c] = (count[c] || 0) + 1
  }
  return [
    { id: 'all', name: t('market.cat.all'), count: items.value.length },
    ...seen.map(c => ({ id: c, name: catName(c), count: count[c] })),
  ]
})

const shown = computed(() =>
  cat.value === 'all' ? items.value : items.value.filter(p => (p.category || 'other') === cat.value))

// 下拉初值 = 当前默认版本（且必须已安装）→ 已装第一个 → 清单默认 → 第一个版本。
// 只有「已安装」的版本才能当默认；未安装的选中值仅作为本次安装目标。
watch(() => props.plugins, (list) => {
  for (const p of list) {
    if (sel[p.id]) continue
    const d = props.defaults?.[p.id] || ''
    sel[p.id] =
      (d && p.installedVersions.includes(d) ? d : '') ||
      p.installedVersions[0] ||
      p.defaultVersion ||
      p.versions[0] ||
      ''
  }
}, { immediate: true })

function isDefault(p: PluginView, v: string): boolean {
  return (props.defaults?.[p.id] || '') === v
}

function verLabel(p: PluginView, v: string): string {
  let s = `v${v}`
  if (p.installedVersions.includes(v)) s += ` · ${t('plugins.installed')}`
  if (isDefault(p, v)) s += ` · ${t('plugins.defaultVersion')}`
  return s
}

// 选中已安装版本 = 设为默认版本（左侧卡片立即切到该版本）
function onPickVersion(p: PluginView, v: string) {
  if (!p.installedVersions.includes(v)) {
    // 未安装的版本不能当默认版本，只是「安装目标」；已装用户请到多版本里装
    if (p.installedVersions.length) ElMessage.info(t('market.needInstall'))
    return
  }
  if (isDefault(p, v)) return
  emit('default', p.id, v)
  ElMessage.success(t('market.defaultSet', { ver: v }))
}

function instOf(p: PluginView): InstanceView | undefined {
  const list = props.instances.filter(i => i.plugin === p.id)
  return list.find(i => i.live === 'running') || list[0]
}

// 「设为默认」菜单项：把当前下拉选中的已装版本设为默认
function setDefaultFromMenu(p: PluginView) {
  const inst = instOf(p)
  const ver = sel[p.id] || inst?.version || ''
  if (!ver) return
  if (!p.installedVersions.includes(ver)) {
    ElMessage.info(t('market.needInstall'))
    return
  }
  if (isDefault(p, ver)) {
    ElMessage.info(t('market.alreadyDefault', { ver }))
    return
  }
  emit('default', p.id, ver)
  ElMessage.success(t('market.defaultSet', { ver }))
}

function urlOf(p: PluginView, v: string): string {
  const info = (p.versionInfos || []).find(x => x.version === v)
  return info?.url || ''
}

async function onInstall(p: PluginView) {
  const ver = sel[p.id] || p.defaultVersion
  if (!ver) return
  busy.value = p.id
  try {
    await CreateInstance(p.id, ver, '', [])
    ElMessage.success(t('market.installedOk', { name: p.name, ver }))
    emit('installed', p.id, ver)
    emit('refresh')
  } catch (e) {
    ElMessage.error(String(parseErr(e)))
  } finally {
    busy.value = ''
  }
}

function onOpenVersions(p: PluginView) {
  emit('versions', p)
}

async function onMore(cmd: string, p: PluginView) {
  if (cmd === 'versions') {
    onOpenVersions(p)
    return
  }
  if (cmd === 'default') {
    setDefaultFromMenu(p)
    return
  }
  if (cmd === 'settings') {
    const inst = instOf(p)
    if (inst) emit('settings', inst)
    return
  }
  if (cmd === 'logs') {
    const inst = instOf(p)
    if (inst) emit('logs', inst)
    return
  }
  if (cmd === 'url') {
    const ver = sel[p.id] || p.defaultVersion
    const url = urlOf(p, ver)
    if (!url) {
      // 清单未声明下载地址（如内置服务、只能离线导入的版本）
      ElMessage.info(t('market.noURL'))
      return
    }
    try {
      await navigator.clipboard.writeText(url)
      ElMessage.success(t('plugins.copied'))
    } catch {
      ElMessage.info(url)
    }
  }
}

function parseErr(e: unknown): string {
  if (e instanceof Error) return e.message
  try { return JSON.stringify(e) } catch { return String(e) }
}
</script>

<style scoped>
.mk-head { display: flex; align-items: center; gap: 0.5rem; }
.mk-head > span { flex: 1 1 auto; min-width: 0; }
.mk-zoom {
  flex-shrink: 0; display: inline-flex; align-items: center; justify-content: center;
  width: 1.5rem; height: 1.5rem; padding: 0;
  border: 1px solid transparent; border-radius: var(--radius-sm);
  background: transparent; color: var(--text-secondary); cursor: pointer;
  transition: color .12s, background .12s, border-color .12s;
  -webkit-app-region: no-drag;
}
.mk-zoom:hover { color: var(--accent); background: var(--bg-hover); border-color: var(--border-subtle); }

.mk-wrap { position: relative; display: flex; flex-direction: column; gap: 0.625rem; }
.mk-body { display: flex; gap: 0.75rem; min-height: 18rem; }
.mk-wrap-full { height: calc(100vh - 8.5rem); min-height: 0; }
.mk-body-full { flex: 1 1 auto; min-height: 0; }

/* 左侧分类栏 */
.mk-cats { flex: 0 0 6.5rem; display: flex; flex-direction: column; gap: 0.125rem; }
.mk-cats-full { overflow-y: auto; }
.mk-cat {
  display: flex; align-items: center; justify-content: space-between; gap: 0.25rem;
  padding: 0.3125rem 0.5rem;
  border: 1px solid transparent; border-radius: var(--radius-sm);
  background: transparent; cursor: pointer; font-family: inherit;
  font-size: 0.75rem; color: var(--text-secondary); text-align: left;
  transition: background .12s, color .12s;
}
.mk-cat:hover { background: var(--bg-hover); color: var(--text-primary); }
.mk-cat-on { background: var(--bg-hover); color: var(--accent); border-color: var(--border-subtle); }
.mk-cat-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.mk-cat-n { flex-shrink: 0; font-family: var(--font-mono); font-size: 0.5625rem; color: var(--text-muted); }

/* 右侧软件列表 */
.mk-list { flex: 1 1 auto; min-width: 0; display: flex; flex-direction: column; gap: 0.375rem;
  max-height: 24rem; overflow-y: auto; padding-right: 0.125rem; }
.mk-list-full { max-height: none; min-height: 0; }
.mk-item {
  display: flex; align-items: center; gap: 0.625rem;
  padding: 0.5rem 0.625rem;
  border: 1px solid var(--border-subtle); border-radius: var(--radius-md);
  background: var(--bg-elevated);
}
.mk-item:hover { border-color: var(--border-default); }
.mk-item-main { flex: 1 1 auto; min-width: 0; display: flex; flex-direction: column; gap: 0.1875rem; }
.mk-item-top { display: flex; align-items: center; gap: 0.375rem; min-width: 0; }
.mk-name { font-size: 0.8125rem; font-weight: 600; color: var(--text-primary); white-space: nowrap; }
.mk-kind { font-size: 0.5625rem; color: var(--text-muted); border: 1px solid var(--border-subtle);
  border-radius: var(--radius-sm); padding: 0 0.25rem; flex-shrink: 0; }
.mk-badge { font-size: 0.5625rem; color: var(--success); flex-shrink: 0; }
.mk-desc { font-size: 0.625rem; color: var(--text-muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

.mk-ops { flex-shrink: 0; display: flex; align-items: center; gap: 0.25rem; }
.mk-ops :deep(.el-button + .el-button) { margin-left: 0; }
.mk-ver { width: 10.5rem; }
.mk-install { min-width: 4rem; }
.mk-more {
  width: 1.75rem !important; padding: 0 !important; font-size: 0.875rem !important;
  font-weight: 600; line-height: 1; letter-spacing: 0.05em;
}
.mk-ops :deep(.mk-more > span) { width: 100%; justify-content: center; overflow: visible; text-overflow: clip; }

.mk-empty { padding: 2rem 0; text-align: center; font-size: 0.6875rem; color: var(--text-muted); }
.mk-note { font-size: 0.625rem; color: var(--text-muted); line-height: 1.5; }

/* 右下角缩放手柄 */
.mk-grip {
  position: absolute; right: -0.25rem; bottom: -0.25rem;
  width: 0.875rem; height: 0.875rem; cursor: nwse-resize;
}
.mk-grip::after {
  content: ''; position: absolute; right: 0.1875rem; bottom: 0.1875rem;
  width: 0.5rem; height: 0.5rem;
  border-right: 2px solid var(--border-default);
  border-bottom: 2px solid var(--border-default);
  border-radius: 0 0 2px 0;
}
.mk-grip:hover::after { border-color: var(--accent); }

@media (max-width: 720px) {
  .mk-item { flex-wrap: wrap; }
  .mk-ops { flex-basis: 100%; }
  .mk-ver { flex: 1 1 auto; width: auto; }
}
</style>
