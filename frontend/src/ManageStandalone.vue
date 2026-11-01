<template>
  <!-- 管理面板独立窗口（M20）：/?w=manage&k=<panel>&i=<instanceId>&n=<name>
       无边框透明窗，圆角/描边由 body 的全局样式画（main.ts 打 win-main 标）。
       试点面板（webdav）：弹窗 standalone 模式 = 整个窗口，自绘顶栏在弹窗的
       #header 插槽里（ManageTitleBar），不存在「顶栏被遮罩盖住」的问题。
       其余面板尚未接入 standalone，显示占位提示。 -->
  <div class="mw-root">
    <WebDavManageDialog
      v-if="panel === 'webdav'"
      v-model:visible="shown"
      :instance-id="instanceId"
      :instance-name="instanceName"
      standalone
    />
    <template v-else>
      <ManageTitleBar :panel="panel" :title="title" />
      <main class="mw-body">
        <div class="mw-unknown">{{ t('mw.unsupported') }}</div>
      </main>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from './i18n'
import ManageTitleBar from './components/ManageTitleBar.vue'
import WebDavManageDialog from './components/WebDavManageDialog.vue'

const { t } = useI18n()
const q = new URLSearchParams(window.location.search)
const panel = q.get('k') || ''
const instanceId = q.get('i') || ''
const instanceName = q.get('n') || ''

// 窗口内的面板常驻显示：关闭走窗口本身，不靠 dialog 的 visible 收尾。
const shown = ref(true)

const title = computed(() => {
  const key = panel === 'webdav' ? 'davm.title'
    : panel === 'mysql' ? 'dbm.title'
    : panel === 'redis' ? 'redism.title'
    : panel === 'ftp' ? 'ftpm.title'
    : ''
  if (!key) return instanceName || instanceId
  return t(key, { name: instanceName || instanceId })
})
</script>

<style scoped>
.mw-root {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  border-radius: var(--win-radius);
  overflow: hidden;
}
.mw-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.mw-unknown {
  padding: 2rem;
  font-size: 0.85rem;
  color: var(--text-muted);
}
</style>
