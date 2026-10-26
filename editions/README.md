# 发行版（editions）目录

M19 插件化产品线：**同一份源码内核，发行版差异只来自 editions 文件 + 三个打包参数**。
M6 把「品牌差异」从源码硬编码抽成了构建参数，专有版出包不再改任何源文件。

## 文件

| 文件 | 说明 |
| --- | --- |
| `editions.drive.json` | 「对号网盘」运行时配置：`home.plugin = webdav`，首屏即网盘首页，侧栏与 Tab 条隐藏 |
| `drive.nsh` | 「对号网盘」NSIS 品牌定义（产品名 / 安装目录名 / exe 名 / editions 文件名），UTF-8 **with BOM** |
| `build-drive.ps1` | 一键打包脚本：构建 `duihao-drive.exe` + NSIS 安装包 |
| `../build/windows/info.drive.json` | 「对号网盘」exe 版本信息（属性里的产品名 / 描述） |

## 运行时怎么生效

`editionpkg` 读 **exe 同级**（安装包根，与 `payload\` 同层）的 `editions.json`：

```
bin/
  duihao-drive.exe
  editions.json      ← 安装时由 editions.drive.json 改名而来
  payload/
```

- 没有 `editions.json` = core 构建，行为与以前完全一致（StartTabContent 首屏）
- 解析失败 / `home.plugin` 为空 → 按无发行版处理并记日志，回落 connections 首屏
- 启动日志会打一行：`M19: edition=drive name=对号网盘 home=webdav fallback=connections`

## 一键打包（M6）

```powershell
# 默认 v0.1.0、machine 安装范围
powershell -ExecutionPolicy Bypass -File editions\build-drive.ps1

# 指定版本 / 用户级安装（免 UAC） / 只出 exe
powershell -ExecutionPolicy Bypass -File editions\build-drive.ps1 -Version 0.2.0 -InstallScope user
powershell -ExecutionPolicy Bypass -File editions\build-drive.ps1 -SkipInstaller
```

产物：

| 文件 | 说明 |
| --- | --- |
| `bin\duihao-drive.exe` | 专有版主程序（与 core 的 `duihao.exe` 并存） |
| `bin\DuihaoDrive-amd64-installer.exe` | 安装包，品牌/安装目录/快捷方式均为「对号网盘」 |
| `build\windows\info.drive.build.json` | 版本信息中间产物（脚本按 `-Version` 生成） |

脚本内部做的事（等价于手工流程）：

1. 清掉 `frontend\dist`（vite 空目录会触发安全删除守卫）
2. 用 `-Version` 生成 `info.drive.build.json`（exe 属性里的产品名）
3. 复制 `drive.nsh` → `build\windows\installer\edition.nsh`（`project.nsi` 用 `-DEDITION=1` !include；打完自动删除，避免 core 误用）
4. `wails3 build APP_NAME=duihao-drive VERSION=… INFO_JSON=…`
5. `wails3 package APP_NAME=duihao-drive EDITION=true EDITIONS_BUNDLE=true …`

窗口标题与更新检查也随之分开：标题在 Go 侧按 `editions.json` 的 `name` 创建窗口时设定（任务栏/Alt+Tab 立刻正确）；
专有版不做主线仓库的版本检查。

## 参数是怎么流进去的

| 参数 | 作用点 | 说明 |
| --- | --- | --- |
| `APP_NAME` | `build:native` 的 `-o bin/<APP_NAME>.exe`、NSIS 的 `ARG_WAILS_*_BINARY` | 决定产物 exe 名；CLI 变量覆盖 Taskfile 全局变量 |
| `INFO_JSON` | `generate:syso -info <file>` | 决定 exe 属性里的 ProductName / FileDescription |
| `EDITION=1` | `project.nsi` `!include "edition.nsh"` | 品牌字符串走文件，**不走命令行**，避免中文被控制台代码页破坏 |
| `EDITIONS_BUNDLE=1` | 安装时把 `editions\<EDITION_JSON>` 装成 exe 同级的 `editions.json` | 源路径相对 `app\editions\`，所以 `bin\` 不会残留 editions.json 把 core 也带偏 |

`edition.nsh` 可定义的键（都有 core 默认值）：

```nsis
!define PRODUCT_NAME        "对号网盘"      ; 向导标题 / 快捷方式 / 卸载列表（中文 OK）
!define PRODUCT_NAME_EN     "DuihaoDrive"   ; 安装目录 + 卸载注册表键（ASCII）
!define PUBLISHER           "Duihao"
!define BINARY              "duihao-drive.exe"
!define INSTALLER_BASENAME  "DuihaoDrive"   ; 安装包文件名前缀（core 默认 duihao）
!define EDITION_JSON        "editions.drive.json"
```

## 本地试跑（不开安装包）

```powershell
Copy-Item editions\editions.drive.json bin\editions.json
bin\duihao-drive.exe            # 主窗口默认 Hidden，托盘/Alt+Space →「打开主窗口」
Remove-Item bin\editions.json   # 还原：否则 core 的 duihao.exe 也会被切到网盘首屏
```

调试想看主窗口直接显示：设环境变量 `DUIHAO_SHOW_MAIN=1` 再启动。

## 新增一个专有版

1. 写 `editions.<name>.json`（`home.plugin` 指向带 `ui.home` 的插件）
2. 前端 `src/homeviews/registry.ts` 加一行视图映射（若无现成视图）
3. 复制 `drive.nsh` → `<name>.nsh` 改 5 个 define
4. 复制 `../build/windows/info.drive.json` → `info.<name>.json`
5. 复制 `build-drive.ps1` 改顶部 4 个变量（`AppName` / `ProductNameEn` / nsh 名 / editions 名）

**不需要改任何源码**——内核、插件、前端行为都不动。

## 已知边界

- **打包参数已就绪，但 `build/config.yml` 的 `info.productName` 仍是「对号」**：它只用于生成 macOS/iOS 资源与默认 syso，Windows 专有版已由 `INFO_JSON` 覆盖，所以不影响 Windows 出包；做 macOS 发行版时才需要参数化。
- **core 与 drive 共用同一数据目录**（`%APPDATA%\Duihao`）与同一单实例锁：两者不能同时运行（后启动者只会唤起已运行实例）。这是刻意选择——同一引擎，不需要两套运行时缓存。
- 图标目前两者相同；专有版换图标等 M6 之后的品牌物料阶段（放 `build/appicon_<name>.png` 再走 `generate:syso -icon`）。
