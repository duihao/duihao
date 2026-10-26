Unicode true
ManifestDPIAware true

; ─────────────────────────────────────────────────────────────────────────────
; 对号 (Duihao) — NSIS installer
; Brand: PRODUCT_NAME shows in wizard/shortcuts/uninstall list (Chinese OK,
; file is UTF-8 with BOM + Unicode true). Install dir keeps ASCII "Duihao".
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
!ifndef VERSION
  !define VERSION "dev"
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
!define INSTALLER_OUT "${INSTALLER_BASENAME}-${ARCH_SUFFIX}-installer.exe"
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
  InstallDir "$PROGRAMFILES64\${PRODUCT_NAME_EN}"
!endif
!ifndef REQUEST_EXECUTION_LEVEL
  !define REQUEST_EXECUTION_LEVEL admin
!endif
RequestExecutionLevel ${REQUEST_EXECUTION_LEVEL}
SetCompressor /SOLID lzma
SetCompressorDictSize 128

!include "MUI2.nsh"
!include "LogicLib.nsh"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\${BINARY}"
; Launch via Explorer on finish-page "Run" so it never inherits the
; installer's elevated token. Admin is opted in explicitly by the user.
!define MUI_FINISHPAGE_RUN_FUNCTION "LaunchUnelevated"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

Function .onInit
  Call CheckAndCloseProcess
FunctionEnd

; Launch via Explorer (normal privileges) so the app does not inherit the
; installer's elevated token.
Function LaunchUnelevated
  Exec 'explorer.exe "$INSTDIR\${BINARY}"'
FunctionEnd

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

; The wizard pages localize to the OS language, so the custom dialogs below
; must follow suit via LangString instead of hardcoded Chinese.
LangString MSG_FORCE_CLOSE_PROMPT ${LANG_SIMPCHINESE} "${PRODUCT_NAME} 正在运行。安装前需要先关闭它。是否强制关闭进程？"
LangString MSG_FORCE_CLOSE_PROMPT ${LANG_ENGLISH} "${PRODUCT_NAME} is currently running and must be closed before installing. Force-close the process?"
LangString MSG_CLOSE_MANUALLY ${LANG_SIMPCHINESE} "请手动关闭 ${PRODUCT_NAME} 后再继续安装。"
LangString MSG_CLOSE_MANUALLY ${LANG_ENGLISH} "Please close ${PRODUCT_NAME} manually and then continue installing."
LangString MSG_WEBVIEW2_INSTALLING ${LANG_SIMPCHINESE} "正在安装 WebView2 运行时（需要联网，约 1~2 分钟）……"
LangString MSG_WEBVIEW2_INSTALLING ${LANG_ENGLISH} "Installing WebView2 Runtime (internet required, 1-2 minutes)..."
LangString MSG_WEBVIEW2_FAILED ${LANG_SIMPCHINESE} "WebView2 运行时安装失败或被跳过。$INSTDIR 下保留了 ${WEBVIEW2_BOOTSTRAPPER}，可联网后手动运行；部分组件可能无法正常显示。"
LangString MSG_WEBVIEW2_FAILED ${LANG_ENGLISH} "WebView2 Runtime installation failed or was skipped. ${WEBVIEW2_BOOTSTRAPPER} is kept in $INSTDIR — run it manually once online; some UI components may not render."
LangString MSG_UNINSTALL_KEEP_DATA ${LANG_SIMPCHINESE} "是否同时删除用户数据（配置、连接、凭据）？$\n$\n选择「否」仅卸载程序，数据保留在对号数据目录中。"
LangString MSG_UNINSTALL_KEEP_DATA ${LANG_ENGLISH} "Also delete user data (settings, connections, credentials)?$\n$\nChoose No to keep data and uninstall the program only."

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
  Call EnsureWebView2
  CreateShortCut "$DESKTOP\${PRODUCT_NAME}.lnk" "$INSTDIR\${BINARY}"
  CreateDirectory "$SMPROGRAMS\${PRODUCT_NAME}"
  CreateShortCut "$SMPROGRAMS\${PRODUCT_NAME}\${PRODUCT_NAME}.lnk" "$INSTDIR\${BINARY}"
  CreateShortCut "$SMPROGRAMS\${PRODUCT_NAME}\Uninstall.lnk" "$INSTDIR\uninstall.exe"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  WriteRegStr ${SHELL_CONTEXT} "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME_EN}" "DisplayName" "${PRODUCT_NAME}"
  WriteRegStr ${SHELL_CONTEXT} "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME_EN}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr ${SHELL_CONTEXT} "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME_EN}" "DisplayIcon" '"$INSTDIR\${BINARY}"'
  WriteRegStr ${SHELL_CONTEXT} "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME_EN}" "Publisher" "${PUBLISHER}"
  WriteRegStr ${SHELL_CONTEXT} "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME_EN}" "DisplayVersion" "${VERSION}"
  WriteRegDWORD ${SHELL_CONTEXT} "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME_EN}" "NoModify" 1
  WriteRegDWORD ${SHELL_CONTEXT} "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME_EN}" "NoRepair" 1
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
