<template>
  <el-dialog
    v-model="show"
    :title="title"
    width="560px"
    :close-on-click-modal="false"
    append-to-body
    @open="onOpen"
  >
    <div v-if="loading" class="sd-loading">{{ t('settings.loading') }}</div>
    <template v-else-if="schema">
      <el-tabs v-model="tab">
        <el-tab-pane
          v-for="g in schema.groups"
          :key="g.title"
          :label="g.title"
          :name="g.title"
        >
          <div class="sd-grid">
            <div v-for="f in g.fields" :key="f.key" class="sd-field">
              <label class="sd-label">{{ f.label }}</label>
              <div class="sd-control">
                <!-- number：数字输入 + 单位 -->
                <template v-if="f.type === 'number'">
                  <el-input-number
                    v-model="numModel[f.key]"
                    :min="f.min || 0"
                    :max="f.max || 2147483647"
                    :step="1"
                    step-strictly
                    size="small"
                    controls-position="right"
                    class="sd-num"
                  />
                  <span v-if="f.unit" class="sd-unit">{{ f.unit }}</span>
                </template>
                <!-- switch：开关 -->
                <template v-else-if="f.type === 'switch'">
                  <el-switch v-model="boolModel[f.key]" size="small" />
                </template>
                <!-- select：下拉 -->
                <template v-else-if="f.type === 'select'">
                  <el-select v-model="vals[f.key]" size="small" class="sd-full">
                    <el-option v-for="o in f.options" :key="o" :label="o" :value="o" />
                  </el-select>
                </template>
                <!-- password：密码 -->
                <template v-else-if="f.type === 'password'">
                  <el-input
                    v-model="vals[f.key]"
                    size="small"
                    type="password"
                    show-password
                    :placeholder="f.placeholder || ''"
                  />
                </template>
                <!-- text / 其他：文本 -->
                <template v-else>
                  <el-input v-model="vals[f.key]" size="small" :placeholder="f.placeholder || ''" />
                </template>
              </div>
              <div v-if="f.hint" class="sd-hint">{{ f.hint }}</div>
            </div>
          </div>
        </el-tab-pane>
        <!-- 配置文件原文（phpStudy 的「配置文件」Tab） -->
        <el-tab-pane v-if="schema.configFile" :label="t('settings.configFile')" name="__file">
          <el-input
            v-model="fileText"
            type="textarea"
            :rows="16"
            class="sd-file"
            spellcheck="false"
          />
          <div class="sd-hint">{{ t('settings.fileHint') }}</div>
        </el-tab-pane>
      </el-tabs>
    </template>
    <div v-else class="sd-loading">{{ t('settings.none') }}</div>

    <template #footer>
      <div class="sd-footer">
        <el-button size="small" text @click="onResetDefaults">{{ t('settings.setDefaults') }}</el-button>
        <span class="sd-spacer" />
        <el-button size="small" @click="show = false">{{ t('settings.cancel') }}</el-button>
        <el-button
          v-if="tab === '__file' && schema && schema.configFile"
          size="small"
          :loading="savingFile"
          @click="onSaveFile"
        >{{ t('settings.saveFile') }}</el-button>
        <el-button size="small" type="primary" :loading="saving" @click="onConfirm">
          {{ t('settings.confirm') }}
        </el-button>
      </div>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useI18n } from '../i18n'
import {
  GetSettingsSchema, ApplySettings, ResetSettings,
  ReadInstanceFile, SaveInstanceFile,
} from '../../bindings/github.com/ys-ll/uniterm/backend/duihao/service/plugincenterservice'

// schema 视图（与后端 instance.SettingsView 对应）
interface SettingField {
  key: string; label: string; type: string; default?: string
  unit?: string; options?: string[]; min?: number; max?: number
  placeholder?: string; portRole?: string; hint?: string; current: string
}
interface SettingGroup { title: string; fields: SettingField[] }
interface SettingsView {
  instanceId: string; plugin: string; groups: SettingGroup[]
  configFile?: string; restart: boolean
}

const props = defineProps<{ instanceId: string; instanceName: string; running: boolean }>()
const emit = defineEmits<{ (e: 'saved'): void }>()

const show = defineModel<boolean>({ default: false })
const { t } = useI18n()

const loading = ref(false)
const saving = ref(false)
const savingFile = ref(false)
const schema = ref<SettingsView | null>(null)
const tab = ref('')
const fileText = ref('')

// 值模型：字符串（与后端对齐）；number/switch 派生双向模型
const vals = reactive<Record<string, string>>({})
const numModel = reactive<Record<string, number>>({})
const boolModel = reactive<Record<string, boolean>>({})

const title = computed(() =>
  `${props.instanceName || props.instanceId} · ${t('settings.title')}`)

watch(numModel, (m) => {
  for (const k in m) vals[k] = String(m[k] ?? '')
}, { deep: true })
watch(boolModel, (m) => {
  for (const k in m) vals[k] = m[k] ? '1' : '0'
}, { deep: true })

async function onOpen() {
  loading.value = true
  schema.value = null
  tab.value = ''
  try {
    const s = (await GetSettingsSchema(props.instanceId)) as unknown as SettingsView
    schema.value = s
    if (!s || !s.groups || s.groups.length === 0) {
      if (!s || !s.configFile) return
    }
    tab.value = s.groups && s.groups.length > 0 ? s.groups[0].title : '__file'
    // 填值：number/switch 派生模型，其余直接字符串
    for (const k of Object.keys(vals)) delete vals[k]
    for (const k of Object.keys(numModel)) delete numModel[k]
    for (const k of Object.keys(boolModel)) delete boolModel[k]
    for (const g of s.groups || []) {
      for (const f of g.fields) {
        vals[f.key] = f.current || ''
        if (f.type === 'number') {
          const n = Number(f.current)
          numModel[f.key] = Number.isFinite(n) ? n : 0
        } else if (f.type === 'switch') {
          boolModel[f.key] = f.current === '1' || f.current === 'true'
        }
      }
    }
    // 配置文件原文懒加载（切到该 Tab 时拉取）
    fileText.value = ''
  } catch (e) {
    ElMessage.error(String(parseErr(e)))
  } finally {
    loading.value = false
  }
}

// 切到配置文件 Tab 时读取原文
watch(tab, async (name) => {
  if (name === '__file' && schema.value?.configFile && !fileText.value) {
    try {
      fileText.value = String(await ReadInstanceFile(props.instanceId, schema.value.configFile))
    } catch (e) {
      ElMessage.error(String(parseErr(e)))
    }
  }
})

async function onConfirm() {
  if (!schema.value) return
  saving.value = true
  try {
    await ApplySettings(props.instanceId, { ...vals })
    if (schema.value.restart && props.running) {
      ElMessage.success(t('settings.savedRestarted'))
    } else {
      ElMessage.success(t('settings.saved'))
    }
    show.value = false
    emit('saved')
  } catch (e) {
    ElMessage.error(String(parseErr(e)))
  } finally {
    saving.value = false
  }
}

async function onResetDefaults() {
  try {
    await ElMessageBox.confirm(t('settings.resetConfirm'), { type: 'warning' })
  } catch {
    return
  }
  try {
    await ResetSettings(props.instanceId)
    ElMessage.success(t('settings.resetDone'))
    show.value = false
    emit('saved')
  } catch (e) {
    ElMessage.error(String(parseErr(e)))
  }
}

async function onSaveFile() {
  if (!schema.value?.configFile) return
  savingFile.value = true
  try {
    await SaveInstanceFile(props.instanceId, schema.value.configFile, fileText.value)
    ElMessage.success(t('settings.fileSaved'))
  } catch (e) {
    ElMessage.error(String(parseErr(e)))
  } finally {
    savingFile.value = false
  }
}

function parseErr(e: unknown): string {
  if (e instanceof Error) return e.message
  try { return JSON.stringify(e) } catch { return String(e) }
}
</script>

<style scoped>
.sd-loading { padding: 1.5rem; text-align: center; font-size: 0.75rem; color: var(--text-muted); }
.sd-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 0.625rem 1rem; padding: 0.25rem 0.125rem; }
.sd-field { display: flex; flex-direction: column; gap: 0.125rem; min-width: 0; }
.sd-label { font-size: 0.6875rem; color: var(--text-secondary); }
.sd-control { display: flex; align-items: center; gap: 0.3125rem; }
.sd-num { width: 100%; }
.sd-unit { font-size: 0.6875rem; color: var(--text-muted); flex-shrink: 0; }
.sd-full { width: 100%; }
.sd-hint { font-size: 0.625rem; color: var(--text-muted); line-height: 1.4; }
.sd-file :deep(textarea) { font-family: var(--font-mono); font-size: 0.6875rem; }
.sd-footer { display: flex; align-items: center; gap: 0.375rem; }
.sd-spacer { flex: 1; }
</style>
