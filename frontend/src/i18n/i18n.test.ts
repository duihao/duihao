import { describe, expect, it } from 'vitest'

// 语言包完整性守卫。
//
// 背景：M21 加普通模式首屏时只给宫格补了 life.* 词条，顶部的存储环/按钮漏了，
// 界面直接显示 life.storage、life.copy 这种裸 key。这类问题编译不报错，
// 只能靠肉眼看界面，所以在这里钉死：
//   1) 代码里静态写死的 i18n key，必须在 zh-CN 与 en 里都存在（en 是兜底语言）；
//   2) 首屏（life.*）词条必须在全部 11 个语言里齐全，不能靠 en 兜底显示英文；
//   3) 语言包文件里不允许重复键（JSON 解析会静默吞掉后者，肉眼极难发现）。

type Dict = Record<string, string>

const locales = import.meta.glob('./locales/*.json', {
  eager: true,
  import: 'default'
}) as Record<string, Dict>

const rawLocales = import.meta.glob('./locales/*.json', {
  eager: true,
  query: '?raw',
  import: 'default'
}) as Record<string, string>

const sources = import.meta.glob('../**/*.{vue,ts}', {
  eager: true,
  query: '?raw',
  import: 'default'
}) as Record<string, string>

const messages: Record<string, Dict> = {}
for (const [path, mod] of Object.entries(locales)) {
  messages[path.split('/').pop()!.replace('.json', '')] = mod
}

const LANG_COUNT = 11

// 形态：t 后接括号，括号里第一个引号字符串，且字符串形如 a.b（含点号才当词条）。
const CALL = /\bt\(\s*['"`]([A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)+)['"`]/g

function usedKeys(): Map<string, string[]> {
  const map = new Map<string, string[]>()
  for (const [path, code] of Object.entries(sources)) {
    const file = path.replace(/^\.\.\//, '')
    for (const m of code.matchAll(CALL)) {
      const key = m[1]
      const list = map.get(key) || []
      if (!list.includes(file)) list.push(file)
      map.set(key, list)
    }
  }
  return map
}

describe('i18n 语言包', () => {
  it('11 个语言文件都能解析', () => {
    expect(Object.keys(messages)).toHaveLength(LANG_COUNT)
  })

  it('代码里用到的 key 在 zh-CN / en 都存在', () => {
    const used = usedKeys()
    expect(used.size).toBeGreaterThan(500) // glob 失效时空跑会漏检
    for (const lang of ['zh-CN', 'en']) {
      const missing = [...used.keys()]
        .filter((k) => !(k in messages[lang]))
        .map((k) => `${k}  (${used.get(k)!.join(', ')})`)
      expect({ lang, missing }).toEqual({ lang, missing: [] })
    }
  })

  it('life.* 首屏词条在全部语言里齐全', () => {
    // 基准取 zh-CN 与 en 的并集：只拿 zh-CN 当基准的话，zh-CN 自己删词条会被漏检。
    const need = [
      ...new Set(
        [...Object.keys(messages['zh-CN']), ...Object.keys(messages['en'])].filter((k) =>
          k.startsWith('life.')
        )
      )
    ]
    expect(need.length).toBeGreaterThan(10)
    for (const lang of Object.keys(messages)) {
      const missing = need.filter((k) => !(k in messages[lang]))
      expect({ lang, missing }).toEqual({ lang, missing: [] })
    }
  })

  it('语言包文件没有重复键', () => {
    for (const [path, raw] of Object.entries(rawLocales)) {
      const keys = [...raw.matchAll(/^\s*"([^"]+)"\s*:/gm)].map((m) => m[1])
      const dup = keys.filter((k, i) => keys.indexOf(k) !== i)
      expect({ file: path, dup }).toEqual({ file: path, dup: [] })
    }
  })
})
