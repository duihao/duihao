<template>
  <el-dialog
    v-model="visible"
    :title="titleText"
    width="62rem"
    draggable
    append-to-body
    :close-on-click-modal="false"
  >
    <!-- 工具栏：路径 + 上传 + 新建 -->
    <div class="db-bar">
      <el-button size="small" text :disabled="!rel" @click="up">{{ t('davbrowse.up') }}</el-button>
      <span class="db-crumb">
        <button class="db-crumb-item" @click="go('')">{{ t('davbrowse.root') }}</button>
        <template v-for="(seg, i) in segs" :key="i">
          <span class="db-sep">/</span>
          <button class="db-crumb-item" @click="go(segs.slice(0, i + 1).join('/'))">{{ seg }}</button>
        </template>
      </span>
      <span class="db-grow" />
      <el-button size="small" :disabled="readonly || busy" @click="pickFiles">{{ t('davbrowse.uploadFiles') }}</el-button>
      <el-button size="small" :disabled="readonly || busy" @click="pickDir">{{ t('davbrowse.uploadDir') }}</el-button>
      <el-button size="small" :disabled="readonly || busy" @click="mkdir">{{ t('davbrowse.newDir') }}</el-button>
    </div>

    <div v-if="readonly" class="db-ro">{{ t('davbrowse.readonly') }}</div>

    <div class="db-body">
      <!-- 列表 -->
      <div class="db-list" @dragover.prevent @drop.prevent="onDrop">
        <div v-if="!entries.length" class="db-empty">{{ t('davbrowse.empty') }}</div>
        <div
          v-for="e in entries" :key="e.path"
          class="db-row" :class="{ 'db-on': sel?.path === e.path }"
          @click="select(e)"
          @dblclick="e.dir ? go(e.path) : preview(e)"
        >
          <span class="db-ico">{{ e.dir ? '📁' : iconOf(e.ext) }}</span>
          <span class="db-name" :title="e.name">{{ e.name }}</span>
          <span class="db-size">{{ e.dir ? '' : humanSize(e.size) }}</span>
          <span class="db-time">{{ e.modTime }}</span>
          <span class="db-ops">
            <el-button v-if="!e.dir" size="small" text @click.stop="preview(e)">{{ t('davbrowse.preview') }}</el-button>
            <el-button size="small" text type="danger" :disabled="readonly" @click.stop="remove(e)">✕</el-button>
          </span>
        </div>
      </div>

      <!-- 预览 -->
      <div class="db-preview">
        <div v-if="!sel" class="db-empty">{{ t('davbrowse.pickHint') }}</div>
        <div v-else-if="sel.dir" class="db-empty">{{ t('davbrowse.dirHint') }}</div>
        <template v-else>
          <div class="db-pv-head">
            <span class="db-pv-name" :title="sel.name">{{ sel.name }}</span>
            <span v-if="pv" class="db-pv-size">{{ humanSize(pv.size) }}</span>
          </div>
          <div v-if="pvLoading" class="db-empty">{{ t('davbrowse.loading') }}</div>
          <pre v-else-if="pv?.kind === 'text'" class="db-pv-text">{{ pv.text }}</pre>
          <img v-else-if="pv?.kind === 'image'" class="db-pv-img" :src="`data:${pv.mime};base64,${pv.base64}`" alt="" />
          <iframe v-else-if="pv?.kind === 'pdf'" class="db-pv-pdf" :src="`data:application/pdf;base64,${pv.base64}`" />
          <div v-else class="db-empty">{{ pv?.message || t('davbrowse.noPreview') }}</div>
        </template>
      </div>
    </div>

    <template #footer>
      <span class="db-foot">{{ t('davbrowse.dropHint') }}</span>
      <el-button size="small" :disabled="!sel" @click="openInExplorer">{{ t('davbrowse.openDir') }}</el-button>
      <el-button size="small" @click="visible = false">{{ t('common.close') }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Events } from '@wailsio/runtime'
import { useI18n } from '../i18n'
import { OpenMultipleFilesDialog, OpenDirectoryDialog } from '../../bindings/github.com/ys-ll/uniterm/app'
import {
  ListWebDavDir, PreviewWebDavFile, UploadToWebDav, DeleteWebDavEntry,
  CreateWebDavDir, OpenWebDavEntryDir,
} from '../../bindings/github.com/ys-ll/uniterm/backend/duihao/service/webdavservice'

interface Entry {
  name: string; path: string; dir: boolean; size: number; modTime: string; ext?: string
}
interface Preview {
  kind: string; mime?: string; text?: string; base64?: string; size: number; message?: string
}

const props = defineProps<{
  visible: boolean
  instanceId: string
  shareId: string
  shareUser?: string
  shareName?: string
  readonly?: boolean
}>()
const emit = defineEmits<{ (e: 'update:visible', v: boolean): void }>()
const { t } = useI18n()

const visible = ref(props.visible)
watch(() => props.visible, (v) => { visible.value = v })
watch(visible, (v) => emit('update:visible', v))

const rel = ref('')
const entries = ref<Entry[]>([])
const sel = ref<Entry | null>(null)
const pv = ref<Preview | null>(null)
const pvLoading = ref(false)
const busy = ref(false)

const segs = computed(() => rel.value.split('/').filter(Boolean))
const titleText = computed(() => {
  const who = props.shareName || props.shareUser || props.shareId
  return `${t('davbrowse.title')} · ${who}`
})

function iconOf(ext?: string): string {
  if (!ext) return '📄'
  if (['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'svg', 'ico'].includes(ext)) return '🖼️'
  if (['zip', 'rar', '7z', 'gz', 'tar'].includes(ext)) return '🗜️'
  if (['mp4', 'webm', 'mov', 'mkv'].includes(ext)) return '🎬'
  if (['mp3', 'wav', 'flac'].includes(ext)) return '🎵'
  if (['php', 'js', 'ts', 'go', 'py', 'json', 'html', 'css', 'sql'].includes(ext)) return '⌨️'
  if (ext === 'pdf') return '📕'
  return '📄'
}

function humanSize(n: number): string {
  if (!n) return '0'
  if (n < 1024) return `${n} B`
  if (n < 1048576) return `${(n / 1024).toFixed(1)} KB`
  if (n < 1073741824) return `${(n / 1048576).toFixed(1)} MB`
  return `${(n / 1073741824).toFixed(2)} GB`
}

// 首次挂载时父级是 `v-if="browseShare"` + 同时把 visible 置 true —— 组件挂载
// 那一刻 visible 已经是 true，el-dialog 的 @open 不会「变为 true」而不再触发，
// 结果第一次打开是空列表（点一下面包屑才出内容）。改成直接 watch visible。
watch(
  () => [props.visible, props.shareId] as const,
  ([vis]) => {
    if (!vis) return
    rel.value = ''
    sel.value = null
    pv.value = null
    void reload()
  },
  { immediate: true },
)

async function reload() {
  if (!props.instanceId || !props.shareId) return
  try {
    entries.value = (await ListWebDavDir(props.instanceId, props.shareId, rel.value)) as unknown as Entry[]
    sel.value = null
    pv.value = null
  } catch (e) {
    ElMessage.error(errText(e))
  }
}

function go(path: string) {
  rel.value = path
  reload()
}
function up() {
  const s = segs.value.slice(0, -1)
  go(s.join('/'))
}

function select(e: Entry) {
  sel.value = e
  if (!e.dir) preview(e)
  else pv.value = null
}

async function preview(e: Entry) {
  sel.value = e
  pvLoading.value = true
  pv.value = null
  try {
    pv.value = (await PreviewWebDavFile(props.instanceId, props.shareId, e.path, 512)) as unknown as Preview
  } catch (err) {
    ElMessage.error(errText(err))
  } finally {
    pvLoading.value = false
  }
}

async function pickFiles() {
  try {
    const list = (await OpenMultipleFilesDialog()) as unknown as string[]
    if (list && list.length) await upload(list)
  } catch (e) {
    ElMessage.error(errText(e))
  }
}
async function pickDir() {
  try {
    const dir = (await OpenDirectoryDialog()) as unknown as string
    if (dir) await upload([dir])
  } catch (e) {
    ElMessage.error(errText(e))
  }
}

async function upload(paths: string[]) {
  busy.value = true
  try {
    const r = (await UploadToWebDav(props.instanceId, props.shareId, rel.value, paths)) as unknown as {
      files: number; bytes: number; overwritten: number; skipped?: string[]
    }
    ElMessage.success(t('davbrowse.uploaded', { n: r.files, size: humanSize(r.bytes) }))
    if (r.skipped && r.skipped.length) ElMessage.warning(r.skipped.join('；'))
    await reload()
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

// 拖拽：Wails 的原生文件拖放事件给的是**真实路径**，网页 File 对象没有路径，所以走这条
function onDrop() { /* dragover 占位，真正的数据来自下面的原生事件 */ }

async function mkdir() {
  let name = ''
  try {
    const r = await ElMessageBox.prompt(t('davbrowse.newDirPrompt'), t('davbrowse.newDir'), {
      inputPattern: /^[^\\/:*?"<>|]+$/,
      inputErrorMessage: t('davbrowse.newDirBad'),
    })
    name = String(r.value || '').trim()
  } catch {
    return
  }
  if (!name) return
  try {
    await CreateWebDavDir(props.instanceId, props.shareId, rel.value, name)
    await reload()
  } catch (e) {
    ElMessage.error(errText(e))
  }
}

async function remove(e: Entry) {
  try {
    await ElMessageBox.confirm(t('davbrowse.deleteConfirm', { name: e.name }), t('davbrowse.delete'), { type: 'warning' })
  } catch {
    return
  }
  try {
    await DeleteWebDavEntry(props.instanceId, props.shareId, e.path)
    if (sel.value?.path === e.path) { sel.value = null; pv.value = null }
    await reload()
  } catch (err) {
    ElMessage.error(errText(err))
  }
}

async function openInExplorer() {
  const target = sel.value ? sel.value.path : rel.value
  try {
    await OpenWebDavEntryDir(props.instanceId, props.shareId, target)
  } catch (e) {
    ElMessage.error(errText(e))
  }
}

function errText(e: unknown): string {
  if (e instanceof Error) return e.message
  try { return JSON.stringify(e) } catch { return String(e) }
}

// 原生拖放：仅在对话框打开时接收（避免和标签页的拖放处理打架）
const unsubDrop = Events.On('common:WindowFilesDropped', (ev) => {
  if (!visible.value || props.readonly) return
  const d = (ev as { data?: { filenames?: string[] } }).data || {}
  const files = d.filenames || []
  if (files.length) upload(files)
})
onUnmounted(() => { if (typeof unsubDrop === 'function') unsubDrop() })
</script>

<style scoped>
.db-bar { display: flex; align-items: center; gap: 0.375rem; margin-bottom: 0.5rem; flex-wrap: wrap; }
.db-crumb { display: flex; align-items: center; gap: 0.125rem; min-width: 0; flex-wrap: wrap; }
.db-crumb-item {
  background: transparent; border: none; padding: 0 0.125rem; cursor: pointer;
  font: inherit; font-size: 0.6875rem; color: var(--accent);
}
.db-crumb-item:hover { text-decoration: underline; }
.db-sep { font-size: 0.625rem; color: var(--text-disabled); }
.db-grow { flex: 1 1 auto; }
.db-ro { margin-bottom: 0.375rem; font-size: 0.625rem; color: var(--warning); }
.db-body { display: flex; gap: 0.625rem; height: 56vh; }
.db-list {
  flex: 1 1 58%; min-width: 0; overflow-y: auto;
  border: 1px solid var(--border-subtle); border-radius: var(--radius-md); background: var(--bg-base);
}
.db-row {
  display: flex; align-items: center; gap: 0.375rem;
  padding: 0.25rem 0.5rem; cursor: pointer; border-bottom: 1px solid var(--border-subtle);
}
.db-row:hover { background: var(--bg-hover); }
.db-row.db-on { background: var(--bg-hover); box-shadow: inset 2px 0 0 var(--accent); }
.db-ico { flex: 0 0 auto; font-size: 0.75rem; width: 1rem; text-align: center; }
.db-name { flex: 1 1 auto; min-width: 0; font-size: 0.75rem; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.db-size { flex: 0 0 4.5rem; text-align: right; font-size: 0.625rem; color: var(--text-muted); font-variant-numeric: tabular-nums; }
.db-time { flex: 0 0 6.5rem; text-align: right; font-size: 0.625rem; color: var(--text-disabled); }
.db-ops { flex: 0 0 auto; display: flex; align-items: center; gap: 0.125rem; }
.db-ops :deep(.el-button + .el-button) { margin-left: 0; }
.db-empty { padding: 1.25rem; text-align: center; font-size: 0.6875rem; color: var(--text-muted); }
.db-preview {
  flex: 1 1 42%; min-width: 0; display: flex; flex-direction: column;
  border: 1px solid var(--border-subtle); border-radius: var(--radius-md); background: var(--bg-elevated); overflow: hidden;
}
.db-pv-head { display: flex; align-items: center; gap: 0.375rem; padding: 0.375rem 0.5rem; border-bottom: 1px solid var(--border-subtle); }
.db-pv-name { font-size: 0.6875rem; font-weight: 600; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.db-pv-size { margin-left: auto; font-size: 0.625rem; color: var(--text-muted); flex: 0 0 auto; }
.db-pv-text {
  margin: 0; padding: 0.5rem; overflow: auto; flex: 1 1 auto;
  font-family: var(--font-mono); font-size: 0.6875rem; line-height: 1.55; color: var(--text-primary);
  white-space: pre-wrap; word-break: break-word;
}
.db-pv-img { max-width: 100%; max-height: 100%; object-fit: contain; margin: auto; }
.db-pv-pdf { flex: 1 1 auto; border: none; width: 100%; }
.db-foot { float: left; font-size: 0.625rem; color: var(--text-disabled); line-height: 2; }
</style>
