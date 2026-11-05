// 取包进度轮询（runtimepkg/progress.go 的帧数据）。
//
// 后端按「插件 id」维护最新一帧进度（phase/字节/速度/重试），前端 600ms
// 轮询一次。不用 WebSocket 事件是刻意的：轮询丢了下一拍就补上，
// 断线/重开面板也不会停在 0%。
// 使用方：PluginCenterPanel（市场卡片）与 PluginVersionDialog（多版本弹窗）。
import { onUnmounted, ref } from 'vue'
import { useI18n } from '../i18n'
import { RuntimeProgress } from '../../bindings/github.com/duihao/duihao/backend/duihao/service/plugincenterservice'

export interface RuntimeProgressView {
  pluginId: string; version: string; phase: string; source?: string; url?: string
  received: number; total: number; percent: number; speed: number
  attempt?: number; message?: string; active: boolean; start: number; ts: number
}

export function useRuntimeProgress() {
  const { t } = useI18n()

  const prog = ref<Record<string, RuntimeProgressView>>({})
  let progTimer: number | undefined
  // 结束态多留 2 秒再收起，「完成」两个字肉眼得看得见
  const progKeepUntil: Record<string, number> = {}

  function applyProgress(list: RuntimeProgressView[]) {
    const now = Date.now()
    const next: Record<string, RuntimeProgressView> = {}
    for (const p of list) {
      if (!p.active) {
        if (!progKeepUntil[p.pluginId]) progKeepUntil[p.pluginId] = now + 2000
        if (now > progKeepUntil[p.pluginId]) continue
      } else {
        delete progKeepUntil[p.pluginId]
      }
      next[p.pluginId] = p
    }
    prog.value = next
  }

  async function pollProgress() {
    try {
      applyProgress((await RuntimeProgress()) as unknown as RuntimeProgressView[])
    } catch {
      /* 轮询失败不打断安装，下一拍再试 */
    }
  }

  function stopProgressPoll() {
    if (progTimer) { clearInterval(progTimer); progTimer = undefined }
  }

  function startProgressPoll() {
    stopProgressPoll()
    void pollProgress()
    progTimer = window.setInterval(pollProgress, 600)
  }

  function progFor(id: string) {
    const p = prog.value[id]
    return p && p.phase !== 'done' && p.phase !== 'fail' ? p : null
  }

  function phaseLabel(p: RuntimeProgressView) {
    const map: Record<string, string> = {
      resolve: t('plugins.dlResolve'),
      connect: t('plugins.dlConnect'),
      download: t('plugins.dlDownload'),
      verify: t('plugins.dlVerify'),
      extract: t('plugins.dlExtract'),
    }
    const base = map[p.phase] || t('plugins.dlDownload')
    // 重试时把次数带上，用户才知道「刚才断过，正在续」
    return p.attempt && p.attempt > 1 ? `${base} · ${t('plugins.dlRetry', { n: p.attempt })}` : base
  }

  function barPct(p: RuntimeProgressView) {
    if (p.phase === 'verify' || p.phase === 'extract') return '100%'
    if (!p.total || p.total <= 0) return ''
    return `${Math.min(100, Math.round(p.percent))}%`
  }

  // 服务端没报长度 → 只能给「还在动」的观感，不能假装有百分比
  function isIndet(p: RuntimeProgressView) {
    return !(p.total > 0) && p.phase === 'download'
  }

  function barWidth(p: RuntimeProgressView) {
    if (p.phase === 'verify' || p.phase === 'extract') return '100%'
    if (!p.total || p.total <= 0) return '35%' // 服务端没给长度：走不确定态（CSS 动画）
    return `${Math.min(100, p.percent)}%`
  }

  function fmtBytes(n: number) {
    if (!n || n < 0) return '0 B'
    const u = ['B', 'KB', 'MB', 'GB']
    let i = 0
    let v = n
    while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
    return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${u[i]}`
  }

  function sizeText(p: RuntimeProgressView) {
    if (p.phase === 'verify' || p.phase === 'extract') return t('plugins.dlWorking')
    const got = fmtBytes(p.received)
    const total = p.total > 0 ? ` / ${fmtBytes(p.total)}` : ''
    const speed = p.speed > 0 && p.phase === 'download' ? ` · ${fmtBytes(p.speed)}/s` : ''
    return `${got}${total}${speed}`
  }

  onUnmounted(stopProgressPoll)

  return {
    prog, startProgressPoll, stopProgressPoll, pollProgress, progFor,
    phaseLabel, barPct, isIndet, barWidth, fmtBytes, sizeText,
  }
}
