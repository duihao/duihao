Unicode true
ManifestDPIAware true

; ─────────────────────────────────────────────────────────────────────────────
; 对号 (Duihao) — NSIS installer
; Brand: PRODUCT_NAME shows in wizard/shortcuts/uninstall list (Chinese OK,
; file is UTF-8 with BOM + Unicode true). Install dir keeps ASCII "Duihao".
; ─────────────────────────────────────────────────────────────────────────────
; 界面：整窗品牌渐变大背景 + 居中白色对号 + 自绘位图按钮（无标题栏）。
; 素材由 make_modern.py 按 DPI 生成到 modern\，运行时按系统缩放挑一套。
; ─────────────────────────────────────────────────────────────────────────────
; M6: dedicated editions inject their brand through `-DEDITION`, which pulls in
; edition.nsh (UTF-8, written next to this script by editions/build-*.ps1).
; Keeping the strings in a file — never on the command line — avoids Chinese
; text being mangled by the console code page.
!ifdef EDITION
  !include "edition.nsh"
!endif

; M6: every brand string is overridable, so editions (对号 / 对号网盘 / ...)
; share this one script. Defaults = core.
!ifndef PRODUCT_NAME
  !define PRODUCT_NAME "对号"
!endif
!ifndef PRODUCT_NAME_EN
  !define PRODUCT_NAME_EN "Duihao"
!endif
!ifndef PUBLISHER
  !define PUBLISHER "Duihao"
!endif
!ifndef BINARY
  !define BINARY "duihao.exe"
!endif
!define WEBVIEW2_BOOTSTRAPPER "MicrosoftEdgeWebview2Setup.exe"
; Edge WebView2 Evergreen Runtime per-machine registration GUID
!define WEBVIEW2_REGKEY "SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"
; 生成物：以下版本号由 sync_version.py 维护（真源 build/windows/VERSION），勿手改
!ifndef VERSION
  !define VERSION "1.0.0"
!endif
; Optional bundled plugins: build with `makensis -DINCLUDE_VCXSRV` only when
; plugins\vcxsrv exists (it is .gitignored upstream, so OFF by default).
!ifdef INCLUDE_VCXSRV
  !ifndef VCXSRV_DIR
    !define VCXSRV_DIR "plugins\vcxsrv"
  !endif
!endif

Name "${PRODUCT_NAME}"
; Installer file name derives from a dedicated ASCII base name, so a drive
; build and a core build can sit side by side in bin\ without clobbering each
; other — while core keeps its historical "duihao-amd64-installer.exe".
!ifndef INSTALLER_BASENAME
  !define INSTALLER_BASENAME "duihao"
!endif
!ifdef ARG_WAILS_AMD64_BINARY
  !define ARCH_SUFFIX "amd64"
!else
  !define ARCH_SUFFIX "arm64"
!endif
!ifndef INSTALLER_OUT
  !define INSTALLER_OUT "${INSTALLER_BASENAME}-${ARCH_SUFFIX}-installer.exe"
!endif
; makensis resolves OutFile relative to this script's directory (installer\),
; so three levels up is the app root — installers land next to the exe in bin\.
OutFile "..\..\..\bin\${INSTALLER_OUT}"

; ── Install scope ────────────────────────────────────────────────────────────
; machine (default): Program Files + HKLM + admin elevation.
; user: LOCALAPPDATA\Programs + HKCU + no UAC prompt (used by `INSTALL_SCOPE=user`).
!ifdef WAILS_INSTALL_SCOPE
  !if "${WAILS_INSTALL_SCOPE}" == "user"
    !define SHELL_CONTEXT HKCU
    InstallDir "$LOCALAPPDATA\Programs\${PRODUCT_NAME_EN}"
  !endif
!endif
!ifndef SHELL_CONTEXT
  !define SHELL_CONTEXT HKLM
  ; 默认装 D:\Duihao：数据目录（便携 data\）就在安装目录下，数据放数据盘；
  ; .onInit 里发现 D 盘不存在时回落到 Program Files。
  InstallDir "D:\${PRODUCT_NAME_EN}"
!endif
!ifndef REQUEST_EXECUTION_LEVEL
  !define REQUEST_EXECUTION_LEVEL admin
!endif
RequestExecutionLevel ${REQUEST_EXECUTION_LEVEL}
SetCompressor /SOLID lzma
; 128MB 字典下 32 位 makensis 会在压缩流初始化阶段失败
; （Internal compiler error #12345: deflateInit() failed [-2]）。
; 64MB 可稳定出包，体积约 24.2%（128 时约 22%，差 ~1.5MB）。
SetCompressorDictSize 64

; ── 品牌化 ───────────────────────────────────────────────────────────────────
; 安装器/卸载器 exe 图标（此前一直是 NSIS 默认图标）。
!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
; 本安装器不再使用 MUI 的安装页面（界面全自绘），MUI_INIT 不会被触发，
; MUI_ICON 也就不会落到 exe 上 —— 显式设一次。
Icon "..\icon.ico"
UninstallIcon "..\icon.ico"

; 安装器文件属性（右键 > 属性 > 详细信息）。VIProductVersion 必须是
; 四段数字；正式发版可用 -DINSTALLER_FILEVERSION=1.2.0.0 覆盖。
!ifndef INSTALLER_FILEVERSION
  !define INSTALLER_FILEVERSION "1.0.0.0"
!endif
VIProductVersion "${INSTALLER_FILEVERSION}"
VIAddVersionKey "ProductName" "${PRODUCT_NAME}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "CompanyName" "对号科技有限公司"
VIAddVersionKey "FileDescription" "${PRODUCT_NAME} 安装程序"
VIAddVersionKey "FileVersion" "${INSTALLER_FILEVERSION}"
VIAddVersionKey "LegalCopyright" "Copyright (C) 对号科技有限公司"

!include "MUI2.nsh"
!include "nsDialogs.nsh"
!include "LogicLib.nsh"
!include "WinMessages.nsh"

; ── 版式基准（100% 缩放，单位 px）─────────────────────────────────────────────
; 窗口与所有坐标按 $kPct 等比放大；位图素材同比例生成 —— DPI aware 进程不缩放
; 位图，只有 1:1 才不发虚。改这里的数字，必须同步改 make_modern.py。
!define BW 740
!define BH 512

; 向导自带按钮 ID：1=下一步/安装/关闭、2=取消、3=上一步
!define ID_NEXT   1
!define ID_CANCEL 2
!define ID_BACK   3

Var kPct
Var X
Var Y
Var W
Var H
Var F

Var hPage
Var hEdit
Var hBgProg
Var hProg
Var bRun
Var bShaped          ; 窗口是否已成型（只在首页定尺寸+居中，之后保住用户拖动后的位置）
Var hDrag            ; 顶部拖动热区

; 首页 → 安装 → 完成页
Page custom PageMainCreate PageMainLeave
Page instfiles
Page custom PageDoneCreate PageDoneLeave

!macro POS x y w h
  IntOp $X ${x} * $kPct
  IntOp $X $X / 100
  IntOp $Y ${y} * $kPct
  IntOp $Y $Y / 100
  IntOp $W ${w} * $kPct
  IntOp $W $W / 100
  IntOp $H ${h} * $kPct
  IntOp $H $H / 100
!macroend

; CreateFont 用负值表示字符高度（像素）
!macro FONT v
  IntOp $F ${v} * $kPct
  IntOp $F $F / 100
  IntOp $F $F * -1
!macroend

; 整窗背景位图
!macro BG name parent
  System::Call 'user32::LoadImageW(p 0, w "$PLUGINSDIR\modern\modern_${name}_$kPct.bmp", i 0, i 0, i 0, i 0x10) p .R1'
  !insertmacro POS 0 0 ${BW} ${BH}
  ; WS_CLIPSIBLINGS(0x04000000) 必须有：否则背景重绘时会把盖在上面的兄弟按钮一起刷掉，
  ; 表现就是按钮「存在但看不见」。
  System::Call 'user32::CreateWindowExW(i 0, w "Static", w "", i 0x5400000E, i 0, i 0, i $W, i $H, p ${parent}, p 0, p 0, p 0) p .R9'
  SendMessage $R9 ${STM_SETIMAGE} 0 $R1
!macroend

; 顶部拖动热区：无边框窗口没有标题栏，在顶部铺一层 Static 接住点击，点它就让系统
; 进入「移动窗口」模态循环。Static 没有 alpha 也不支持真透明（WS_EX_TRANSPARENT 仍会
; 被父窗口背景刷填成灰块），所以这里贴一张**从同页底图同坐标裁下来的条带**，像素逐点
; 相同，视觉上等于透明。
!macro DRAG name out
  !insertmacro POS 0 0 ${BW} 108
  nsDialogs::CreateControl "Static" "0x5400010E" "0" "$X" "$Y" "$W" "$H" ""
  Pop ${out}
  System::Call 'user32::LoadImageW(p 0, w "$PLUGINSDIR\modern\modern_drag_${name}_$kPct.bmp", i 0, i 0, i 0, i 0x10) p .R1'
  SendMessage ${out} ${STM_SETIMAGE} 0 $R1
  System::Call 'user32::InvalidateRect(p ${out}, p 0, i 1)'
!macroend

; 页面内的位图按钮。用 Static + SS_BITMAP（SS_NOTIFY 收点击）而不是 BS_BITMAP
; 按钮：按钮会给自己画一圈 1px 边框，圆角会被切掉；Static 只老老实实贴图。
; 圆角靠「从背景图同坐标裁一块底」实现（Static 不支持 alpha）。
!macro PB name x y w h out
  !insertmacro POS ${x} ${y} ${w} ${h}
  nsDialogs::CreateControl "Static" "0x5000010E" "0" "$X" "$Y" "$W" "$H" ""
  Pop ${out}
  System::Call 'user32::LoadImageW(p 0, w "$PLUGINSDIR\modern\modern_b_${name}_$kPct.bmp", i 0, i 0, i 0, i 0x10) p .R1'
  SendMessage ${out} ${STM_SETIMAGE} 0 $R1
  ; 必须显式提到最前：整窗背景 Static 比按钮晚进 z 序顶（否则点击全被背景吃掉，
  ; 表现就是「三个按钮都点不动」）。HWND_TOP=0，SWP_NOMOVE|NOSIZE|NOACTIVATE=0x0013
  System::Call 'user32::SetWindowPos(p ${out}, p 0, i 0, i 0, i 0, i 0, i 0x0013)'
  System::Call 'user32::InvalidateRect(p ${out}, p 0, i 1)'
!macroend

Function .onInit
  Call DetectDpi
  Call CheckAndCloseProcess
  Call PickDefaultDir
  InitPluginsDir
  SetOutPath $PLUGINSDIR\modern
  File /r "modern\*.bmp"
FunctionEnd

; 按系统 DPI 选素材档位（96 DPI = 100%）
Function DetectDpi
  System::Call 'user32::GetDC(p 0) p .R1'
  System::Call 'gdi32::GetDeviceCaps(p R1, i 88) i .R2'   ; LOGPIXELSX
  System::Call 'user32::ReleaseDC(p 0, p R1)'
  ${If} $R2 >= 216
    StrCpy $kPct 200
  ${ElseIf} $R2 >= 156
    StrCpy $kPct 175
  ${ElseIf} $R2 >= 132
    StrCpy $kPct 150
  ${ElseIf} $R2 >= 108
    StrCpy $kPct 125
  ${Else}
    StrCpy $kPct 100
  ${EndIf}
FunctionEnd

; 去掉标题栏，窗口定成 BW x BH 并居中
Function ShapeWindow
  System::Call 'user32::GetWindowLongW(p $HWNDPARENT, i -16) i .R1'
  IntOp $R1 $R1 & 0xFF30FFFF      ; 清 WS_CAPTION/THICKFRAME/SYSMENU/MIN/MAX
  IntOp $R1 $R1 | 0x80000000      ; WS_POPUP
  System::Call 'user32::SetWindowLongW(p $HWNDPARENT, i -16, i R1)'

  ; 无边框 popup 窗口系统不给投影。给窗口类补 CS_DROPSHADOW(0x00020000)，
  ; 再用 SWP_FRAMECHANGED 让改动落到已创建的窗口上，才有一圈柔和的外阴影。
  ; GCL_STYLE = -26；R4 这里只做临时用（调用方此时还没占用它）。
  System::Call 'user32::GetClassLongW(p $HWNDPARENT, i -26) i .R4'
  IntOp $R4 $R4 | 0x00020000
  System::Call 'user32::SetClassLongW(p $HWNDPARENT, i -26, i R4)'
  System::Call 'user32::RedrawWindow(p $HWNDPARENT, p 0, p 0, i 0x0001)'

  ; Win11 22H2+：DWMWA_WINDOW_CORNER_PREFERENCE(33)=DWMWCP_ROUND(2) 圆角，
  ; 顺便拿到 DWM 的柔和投影。Win10 上返回 E_INVALIDARG，无副作用。
  System::Call 'dwmapi::DwmSetWindowAttribute(p $HWNDPARENT, i 33, *i 2, i 4)'
  ; DWMWA_BORDER_COLOR(34)=0xFFFFFFFE（DWMWA_COLOR_NONE），去掉系统那条 1px 描边
  System::Call 'dwmapi::DwmSetWindowAttribute(p $HWNDPARENT, i 34, *i 0xFFFFFFFE, i 4)'

  ; 只在首页成型一次。用户把窗口拖到别处后，换页若再居中会把窗口拽回去。
  ${If} $bShaped != 1
    !insertmacro POS 0 0 ${BW} ${BH}
    System::Call 'user32::GetSystemMetrics(i 0) i .R2'
    System::Call 'user32::GetSystemMetrics(i 1) i .R3'
    IntOp $R2 $R2 - $W
    IntOp $R2 $R2 / 2
    IntOp $R3 $R3 - $H
    IntOp $R3 $R3 / 2
    System::Call 'user32::SetWindowPos(p $HWNDPARENT, p 0, i R2, i R3, i $W, i $H, i 0x0034)'
    StrCpy $bShaped 1
  ${EndIf}
FunctionEnd

; 隐藏向导自带的底部品牌字/分隔线
Function HideChrome
  GetDlgItem $R0 $HWNDPARENT 1024
  ShowWindow $R0 0
  GetDlgItem $R0 $HWNDPARENT 1025
  ShowWindow $R0 0
  GetDlgItem $R0 $HWNDPARENT 1026
  ShowWindow $R0 0
  GetDlgItem $R0 $HWNDPARENT 1028     ; "Nullsoft Install System" 版本字
  ShowWindow $R0 0
FunctionEnd

; 自定义页里原生按钮一定会被页面窗口盖住（页面是它的兄弟且 Show 时置顶），
; 所以自定义页一律改用页面内的位图按钮，原生按钮只隐藏着当「被点击」的目标。
Function HideNativeButtons
  GetDlgItem $R0 $HWNDPARENT ${ID_NEXT}
  ShowWindow $R0 0
  GetDlgItem $R0 $HWNDPARENT ${ID_CANCEL}
  ShowWindow $R0 0
  GetDlgItem $R0 $HWNDPARENT ${ID_BACK}
  ShowWindow $R0 0
FunctionEnd

; ── 首页（选目录 + 开始）─────────────────────────────────────────────────────
Function PageMainCreate
  nsDialogs::Create 1018
  Pop $hPage
  ${If} $hPage == error
    Abort
  ${EndIf}

  Call ShapeWindow

  !insertmacro POS 0 0 ${BW} ${BH}
  System::Call 'user32::SetWindowPos(p $hPage, p 0, i 0, i 0, i $W, i $H, i 0x0014)'

  !insertmacro BG main $hPage
  !insertmacro DRAG main $hDrag
  ${NSD_OnClick} $hDrag DragClick

  ; 路径输入框：叠在白色胶囊上，y 使其文字垂直居中（胶囊中心 340）
  !insertmacro FONT 15
  !insertmacro POS 126 349 438 22
  System::Call 'user32::CreateWindowExW(i 0, w "Edit", w "", i 0x50000080, i $X, i $Y, i $W, i $H, p $hPage, p 0, p 0, p 0) p .R4'
  System::Call 'gdi32::CreateFontW(i $F, i 0, i 0, i 0, i 400, i 0, i 0, i 0, i 1, i 0, i 0, i 0, i 0, w "Microsoft YaHei") p .R3'
  SendMessage $R4 ${WM_SETFONT} $R3 1
  System::Call 'user32::SetWindowTextW(p $R4, w "$INSTDIR")'
  StrCpy $hEdit $R4

  !insertmacro PB start  544 412 140 44 $R3
  ${NSD_OnClick} $R3 StartClick
  !insertmacro PB quit   56  412 96  44 $R4
  ${NSD_OnClick} $R4 QuitClick
  !insertmacro PB browse 580 344 96  32 $R5
  ${NSD_OnClick} $R5 BrowseClick
  !insertmacro PB close  696 16  28  28 $R6
  ${NSD_OnClick} $R6 QuitClick
  Call HideNativeButtons

  ; × 再提一次最前：拖动热区(700x108)与背景同为兄弟窗口，Show 时可能被压住
  System::Call 'user32::SetWindowPos(p $R6, p 0, i 0, i 0, i 0, i 0, i 0x0013)'
  System::Call 'user32::InvalidateRect(p $R6, p 0, i 1)'

  System::Call 'user32::SetFocus(p $hEdit)'   ; 焦点给输入框，避免按钮画出虚线焦点框

  nsDialogs::Show
FunctionEnd

; 离开首页：取回路径并校验（空格路径会打断 nginx/php-cgi 的命令行）
Function PageMainLeave
  System::Call 'user32::GetWindowTextW(p $hEdit, w .R1, i 1024)'
  ${If} $R1 != ""
    StrCpy $INSTDIR $R1
  ${EndIf}
  ${If} $INSTDIR == ""
    MessageBox MB_OK|MB_ICONEXCLAMATION "$(MSG_DIR_HINT)" /SD IDOK
    Abort
  ${EndIf}
  StrCpy $R0 0
  space_scan:
    StrCpy $R2 $INSTDIR 1 $R0
    ${If} $R2 == ""
      Goto space_done
    ${EndIf}
    ${If} $R2 == " "
      MessageBox MB_OK|MB_ICONEXCLAMATION "$(MSG_DIR_HINT)" /SD IDOK
      Abort
    ${EndIf}
    IntOp $R0 $R0 + 1
    Goto space_scan
  space_done:
FunctionEnd

; ── 进度页 ──────────────────────────────────────────────────────────────────
; instfiles 的原生控件（日志列表、Show details、图标、自带进度条）都挂在里层
; #32770 上，不是 $HWNDPARENT 的直接子窗口，GetDlgItem 取不到；而且 NSIS 会在
; 进度更新时把它们重新显示出来。所以直接把整层容器挪到屏幕外。
Function HideNativeProgress
  GetDlgItem $R7 $HWNDPARENT 1028
  ${If} $R7 != 0
    System::Call 'user32::ShowWindow(p R7, i 0)'
  ${EndIf}
  System::Call 'user32::FindWindowExW(p $HWNDPARENT, p 0, w "#32770", p 0) p .R6'
  ${If} $R6 != 0
    System::Call 'user32::SetWindowPos(p R6, p 0, i -32000, i -32000, i 0, i 0, i 0x0015)'
  ${EndIf}
  ; 向导自带的页眉（白底 913x92 + 图标）是 $HWNDPARENT 的直接子窗口，且在 z 序上
  ; 比我们的底图靠前（底图有 WS_CLIPSIBLINGS 后不会再盖住它们）。直接把底图提到
  ; 最前，比逐个隐藏这些 id 更稳；进度条再提一次，保持在底图之上。
  ${If} $hBgProg != 0
    System::Call 'user32::SetWindowPos(p $hBgProg, p 0, i 0, i 0, i 0, i 0, i 0x0013)'
  ${EndIf}
  ${If} $hProg != 0
    System::Call 'user32::SetWindowPos(p $hProg, p 0, i 0, i 0, i 0, i 0, i 0x0013)'
  ${EndIf}
  ; 容器挪走后原位置没人重画，会留一块浅灰；把整窗底图打回去
  ${If} $hBgProg != 0
    System::Call 'user32::InvalidateRect(p $hBgProg, p 0, i 1)'
    System::Call 'user32::UpdateWindow(p $hBgProg)'
  ${EndIf}
  ; 底图重画会把叠在上面的进度条擦掉，必须让它接着重画一遍
  ${If} $hProg != 0
    System::Call 'user32::InvalidateRect(p $hProg, p 0, i 1)'
    System::Call 'user32::UpdateWindow(p $hProg)'
  ${EndIf}
FunctionEnd

Function SetupProgressUI
  GetDlgItem $R0 $HWNDPARENT 1016
  ShowWindow $R0 0
  Call HideNativeButtons
  Call HideChrome

  !insertmacro BG prog $HWNDPARENT
  StrCpy $hBgProg $R9
  Call HideNativeProgress

  ; 自建进度条（PBS_SMOOTH）。底槽烘焙在底图里，这条只负责白色填充部分，
  ; 所以 SetPct 只是改它的宽度，不跟 PBM_SETBARCOLOR/主题的底色打架。
  !insertmacro POS 100 364 6 10
  System::Call 'user32::CreateWindowExW(i 0, w "msctls_progress32", w "", i 0x50000001, i $X, i $Y, i $W, i $H, p $HWNDPARENT, p 0, p 0, p 0) p .R4'
  StrCpy $hProg $R4
  System::Call 'uxtheme::SetWindowTheme(p R4, w "", w "")'
  System::Call 'user32::SendMessageW(p R4, i 0x0409, i 0, i 0xFFFFFF)'        ; PBM_SETBARCOLOR
  System::Call 'user32::SendMessageW(p R4, i 0x0402, i 100, i 0)'             ; PBM_SETPOS
FunctionEnd

; $0 = 百分比
Function SetPct
  IntOp $R1 $0 * 540
  IntOp $R1 $R1 / 100
  ${If} $R1 < 8
    StrCpy $R1 8
  ${EndIf}
  IntOp $R1 $R1 * $kPct
  IntOp $R1 $R1 / 100
  !insertmacro POS 100 364 0 10
  System::Call 'user32::SetWindowPos(p $hProg, p 0, i $X, i $Y, i R1, i $H, i 0x0014)'
  Call HideNativeProgress
FunctionEnd

; 100% 后直接翻到完成页：NSIS 在 section 结束时会销毁我们自己加的窗口，
; 停在进度页只会剩一片灰，所以这里主动点一下「下一步」。
Function GotoDonePage
  GetDlgItem $R0 $HWNDPARENT ${ID_NEXT}
  System::Call 'user32::EnableWindow(p R0, i 1)'
  SendMessage $R0 ${BM_CLICK} 0 0
FunctionEnd

; ── 完成页 ──────────────────────────────────────────────────────────────────
Function PageDoneCreate
  nsDialogs::Create 1018
  Pop $hPage
  ${If} $hPage == error
    Abort
  ${EndIf}

  Call ShapeWindow

  ; 进度页那张底图是挂在 $HWNDPARENT 上的，而且被反复提到最前 —— 进完成页时必须
  ; 收掉，否则它会盖在完成页（及其按钮）上面，按钮点不动。
  ${If} $hBgProg != 0
    System::Call 'user32::ShowWindow(p $hBgProg, i 0)'
    StrCpy $hBgProg 0
  ${EndIf}
  ${If} $hProg != 0
    System::Call 'user32::ShowWindow(p $hProg, i 0)'
    StrCpy $hProg 0
  ${EndIf}

  !insertmacro POS 0 0 ${BW} ${BH}
  System::Call 'user32::SetWindowPos(p $hPage, p 0, i 0, i 0, i $W, i $H, i 0x0014)'

  !insertmacro BG done $hPage
  !insertmacro DRAG done $hDrag
  ${NSD_OnClick} $hDrag DragClick

  !insertmacro PB run    202 372 160 44 $R3
  ${NSD_OnClick} $R3 RunClick
  !insertmacro PB finish 378 372 160 44 $R4
  ${NSD_OnClick} $R4 FinishClick
  Call HideNativeButtons

  System::Call 'user32::SetFocus(p $hPage)'

  nsDialogs::Show
FunctionEnd

Function PageDoneLeave
  StrCpy $bRun 1
FunctionEnd

; ── 页面按钮回调 ────────────────────────────────────────────────────────────
; 注意：完成页上 NSIS 会把「下一步 / 取消 / 上一步」全部 EnableWindow(0)，
; 对禁用按钮发 BM_CLICK 是没有任何反应的。所以每次代点前先启用一次。
Function StartClick
  GetDlgItem $0 $HWNDPARENT ${ID_NEXT}
  System::Call 'user32::EnableWindow(p $0, i 1)'
  SendMessage $0 ${BM_CLICK} 0 0
FunctionEnd

Function RunClick
  StrCpy $bRun 1
  GetDlgItem $0 $HWNDPARENT ${ID_NEXT}
  System::Call 'user32::EnableWindow(p $0, i 1)'
  SendMessage $0 ${BM_CLICK} 0 0
FunctionEnd

; 注意：页面回调里直接 Quit 不生效（向导的消息循环还没退），必须走原生
; 取消按钮的点击，让 NSIS 自己的 abort 流程关窗。
; 让系统接管拖动：WM_NCLBUTTONDOWN + HTCAPTION，DefWindowProc 内部会跑一个
; 模态的移动循环，直到用户松开左键才返回（所以这里 SendMessage 是「同步」的）。
Function DragClick
  System::Call 'user32::SendMessageW(p $HWNDPARENT, i 0x00A1, i 2, i 0)'
FunctionEnd

Function QuitClick
  GetDlgItem $0 $HWNDPARENT ${ID_CANCEL}
  System::Call 'user32::EnableWindow(p $0, i 1)'
  SendMessage $0 ${BM_CLICK} 0 0
FunctionEnd

Function FinishClick
  StrCpy $bRun 0
  GetDlgItem $0 $HWNDPARENT ${ID_CANCEL}
  System::Call 'user32::EnableWindow(p $0, i 1)'
  SendMessage $0 ${BM_CLICK} 0 0
FunctionEnd

Function BrowseClick
  nsDialogs::SelectFolderDialog "选择安装目录" "$INSTDIR"
  Pop $0
  ${If} $0 != error
    StrCpy $INSTDIR $0
    System::Call 'user32::SetWindowTextW(p $hEdit, w "$0")'
  ${EndIf}
FunctionEnd

; MUI2 的 MUI_LANGUAGE 已经定义了 .onUserAbort（取消确认框），这里不再重复。

; 只有点「立即体验」才拉起程序；经 explorer 启动，避免继承提权令牌
Function .onGUIEnd
  ${If} $bRun == 1
    Exec 'explorer.exe "$INSTDIR\${BINARY}"'
  ${EndIf}
FunctionEnd

; ── 卸载器页面（保持 MUI 经典外观）───────────────────────────────────────────
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

; The wizard pages localize to the OS language, so the custom dialogs below
; must follow suit via LangString instead of hardcoded Chinese.
LangString MSG_FORCE_CLOSE_PROMPT ${LANG_SIMPCHINESE} "${PRODUCT_NAME} 正在运行。安装前需要先关闭它。是否强制关闭进程？"
LangString MSG_FORCE_CLOSE_PROMPT ${LANG_ENGLISH} "${PRODUCT_NAME} is currently running and must be closed before installing. Force-close the process?"
LangString MSG_DIR_HINT ${LANG_SIMPCHINESE} "安装路径不能包含空格（如 D:\${PRODUCT_NAME_EN}）。含空格的路径会导致内置的网站环境无法启动。"
LangString MSG_DIR_HINT ${LANG_ENGLISH} "The install path must not contain spaces (e.g. D:\${PRODUCT_NAME_EN}). Paths with spaces will break the bundled web environment."
LangString MSG_CLOSE_MANUALLY ${LANG_SIMPCHINESE} "请手动关闭 ${PRODUCT_NAME} 后再继续安装。"
LangString MSG_CLOSE_MANUALLY ${LANG_ENGLISH} "Please close ${PRODUCT_NAME} manually and then continue installing."
LangString MSG_WEBVIEW2_INSTALLING ${LANG_SIMPCHINESE} "正在安装 WebView2 运行时（需要联网，约 1~2 分钟）……"
LangString MSG_WEBVIEW2_INSTALLING ${LANG_ENGLISH} "Installing WebView2 Runtime (internet required, 1-2 minutes)..."
LangString MSG_WEBVIEW2_FAILED ${LANG_SIMPCHINESE} "WebView2 运行时安装失败或被跳过。$INSTDIR 下保留了 ${WEBVIEW2_BOOTSTRAPPER}，可联网后手动运行；部分组件可能无法正常显示。"
LangString MSG_WEBVIEW2_FAILED ${LANG_ENGLISH} "WebView2 Runtime installation failed or was skipped. ${WEBVIEW2_BOOTSTRAPPER} is kept in $INSTDIR — run it manually once online; some UI components may not render."
LangString MSG_UNINSTALL_KEEP_DATA ${LANG_SIMPCHINESE} "是否同时删除用户数据（配置、连接、凭据）？$\n$\n选择「否」仅卸载程序，数据保留在对号数据目录中。"
LangString MSG_UNINSTALL_KEEP_DATA ${LANG_ENGLISH} "Also delete user data (settings, connections, credentials)?$\n$\nChoose No to keep data and uninstall the program only."

; PickDefaultDir：机器级安装默认 D:\Duihao；D 盘不存在时回落 Program Files。
; 用户级安装保持原默认（LOCALAPPDATA）不动。
; 注意：NSIS 的 ${FileExists} 对 "D:\"（盘根带尾反斜杠）永远判 false，
; 必须用 "D:\*.*" 探测（实测踩坑）。
Function PickDefaultDir
  ${If} "${SHELL_CONTEXT}" == "HKLM"
    ${If} ${FileExists} "D:\*.*"
      StrCpy $INSTDIR "D:\${PRODUCT_NAME_EN}"
    ${Else}
      StrCpy $INSTDIR "$PROGRAMFILES64\${PRODUCT_NAME_EN}"
    ${EndIf}
  ${EndIf}
FunctionEnd

Function CheckAndCloseProcess
  check_process:
  ; Use findstr to filter tasklist output: only match if process name appears in output
  nsExec::ExecToStack 'cmd /c tasklist /FI "IMAGENAME eq ${BINARY}" /NH 2>nul | findstr /I "${BINARY}"'
  Pop $0
  Pop $1
  ; findstr returns 0 only if the process name is found in output
  ${If} $0 != "0"
    Return
  ${EndIf}

  ; Process is running — ask user
  MessageBox MB_YESNO|MB_ICONQUESTION "$(MSG_FORCE_CLOSE_PROMPT)" /SD IDYES IDNO no_kill
  nsExec::ExecToStack 'cmd /c taskkill /F /IM "${BINARY}"'
  Pop $0
  Sleep 1500
  Goto check_process

  no_kill:
  MessageBox MB_OK|MB_ICONEXCLAMATION "$(MSG_CLOSE_MANUALLY)" /SD IDOK
  Abort
FunctionEnd

; ── G4: WebView2 Evergreen Runtime detection + silent install ────────────────
; Checks the per-machine registration under both registry views (64-bit native
; and 32-bit WOW6432Node) plus per-user (HKCU). If absent, silently runs the
; bundled bootstrapper (downloads the runtime online). Never blocks install:
; on failure we warn and keep the bootstrapper for a manual retry.
Function EnsureWebView2
  ; Machine-wide, 64-bit view
  SetRegView 64
  ClearErrors
  ReadRegStr $0 HKLM "${WEBVIEW2_REGKEY}" "pv"
  ${If} $0 != ""
    Goto webview2_done
  ${EndIf}
  ; Machine-wide, 32-bit view (WOW6432Node)
  SetRegView 32
  ClearErrors
  ReadRegStr $0 HKLM "${WEBVIEW2_REGKEY}" "pv"
  ${If} $0 != ""
    Goto webview2_done
  ${EndIf}
  ; Per-user fallback
  ClearErrors
  ReadRegStr $0 HKCU "${WEBVIEW2_REGKEY}" "pv"
  ${If} $0 != ""
    Goto webview2_done
  ${EndIf}

  ; Not found → silent install from the bundled bootstrapper
  ${If} ${FileExists} "$INSTDIR\${WEBVIEW2_BOOTSTRAPPER}"
    DetailPrint "$(MSG_WEBVIEW2_INSTALLING)"
    nsExec::ExecToLog '"$INSTDIR\${WEBVIEW2_BOOTSTRAPPER}" /silent /install'
    Pop $0
    ${If} $0 != "0"
      MessageBox MB_OK|MB_ICONEXCLAMATION "$(MSG_WEBVIEW2_FAILED)" /SD IDOK
    ${EndIf}
  ${Else}
    MessageBox MB_OK|MB_ICONEXCLAMATION "$(MSG_WEBVIEW2_FAILED)" /SD IDOK
  ${EndIf}

  webview2_done:
  SetRegView lastused
FunctionEnd

Section "Install"
  ; 进度页视觉（背景、进度条、隐藏原生控件）必须在 Section 第一行装好：
  ; instfiles 的控件是切页时才创建的。
  Call SetupProgressUI
  StrCpy $0 5
  Call SetPct
  ; 切到 instfiles 时 NSIS 会在页面首次显示后再把原生容器 Show 回来一次，
  ; 这时我们早先那次隐藏会被覆盖。等它显示完再补一刀，期间就不会露出原生界面。
  Sleep 240
  Call HideNativeProgress
  Sleep 120
  Call HideNativeProgress

  ; Shortcuts follow the install scope: current user for user-scope installs,
  ; all users for machine-scope (needs the admin token we already have).
  !if "${SHELL_CONTEXT}" == "HKCU"
    SetShellVarContext current
  !else
    SetShellVarContext all
  !endif
  SetOutPath "$INSTDIR"
!ifdef ARG_WAILS_AMD64_BINARY
  File "/oname=${BINARY}" "${ARG_WAILS_AMD64_BINARY}"
!else
  File "/oname=${BINARY}" "${ARG_WAILS_ARM64_BINARY}"
!endif
  ; WebView2 bootstrapper (generated by `wails3 generate webview2bootstrapper`)
  File "/oname=${WEBVIEW2_BOOTSTRAPPER}" "${WEBVIEW2_BOOTSTRAPPER}"
  ; M6: dedicated editions ship editions.json next to the exe — exactly where
  ; editionpkg reads it. Source path stays relative (app\editions\...), so no
  ; path travels through the shell and bin\ never keeps a stray editions.json
  ; that would also re-brand the core build.
!ifdef EDITIONS_BUNDLE
  !ifndef EDITION_JSON
    !define EDITION_JSON "editions.drive.json"
  !endif
  File "/oname=editions.json" "..\..\..\editions\${EDITION_JSON}"
!endif
!ifdef INCLUDE_VCXSRV
  SetOutPath "$INSTDIR\plugins\vcxsrv"
  File /r "${VCXSRV_DIR}\*"
  SetOutPath "$INSTDIR"
!endif
  StrCpy $0 55
  Call SetPct

  Call EnsureWebView2
  StrCpy $0 82
  Call SetPct

  CreateShortCut "$DESKTOP\${PRODUCT_NAME}.lnk" "$INSTDIR\${BINARY}"
  CreateDirectory "$SMPROGRAMS\${PRODUCT_NAME}"
  CreateShortCut "$SMPROGRAMS\${PRODUCT_NAME}\${PRODUCT_NAME}.lnk" "$INSTDIR\${BINARY}"
  CreateShortCut "$SMPROGRAMS\${PRODUCT_NAME}\Uninstall.lnk" "$INSTDIR\uninstall.exe"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  StrCpy $0 92
  Call SetPct

  WriteRegStr ${SHELL_CONTEXT} "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME_EN}" "DisplayName" "${PRODUCT_NAME}"
  WriteRegStr ${SHELL_CONTEXT} "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME_EN}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr ${SHELL_CONTEXT} "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME_EN}" "DisplayIcon" '"$INSTDIR\${BINARY}"'
  WriteRegStr ${SHELL_CONTEXT} "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME_EN}" "Publisher" "${PUBLISHER}"
  WriteRegStr ${SHELL_CONTEXT} "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME_EN}" "DisplayVersion" "${VERSION}"
  WriteRegDWORD ${SHELL_CONTEXT} "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME_EN}" "NoModify" 1
  WriteRegDWORD ${SHELL_CONTEXT} "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME_EN}" "NoRepair" 1
  StrCpy $0 100
  Call SetPct
  Sleep 350
  Call GotoDonePage
SectionEnd

Section "Uninstall"
  !if "${SHELL_CONTEXT}" == "HKCU"
    SetShellVarContext current
  !else
    SetShellVarContext all
  !endif
  Delete "$INSTDIR\${BINARY}"
  Delete "$INSTDIR\${WEBVIEW2_BOOTSTRAPPER}"
!ifdef EDITIONS_BUNDLE
  Delete "$INSTDIR\editions.json"
!endif
  Delete "$INSTDIR\uninstall.exe"
  RMDir /r "$INSTDIR\plugins\vcxsrv"
  RMDir "$INSTDIR\plugins"
  RMDir "$INSTDIR"
  Delete "$DESKTOP\${PRODUCT_NAME}.lnk"
  Delete "$SMPROGRAMS\${PRODUCT_NAME}\${PRODUCT_NAME}.lnk"
  Delete "$SMPROGRAMS\${PRODUCT_NAME}\Uninstall.lnk"
  RMDir "$SMPROGRAMS\${PRODUCT_NAME}"
  DeleteRegKey ${SHELL_CONTEXT} "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME_EN}"
  ; User data (~/.duihao, %APPDATA%\Duihao, %LOCALAPPDATA%\Duihao) is never
  ; touched automatically — uninstalling the program must not destroy data.
SectionEnd
