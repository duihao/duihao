; 对号网盘 专有版品牌定义（NSIS Unicode，UTF-8 with BOM）
; 由 editions/build-drive.ps1 复制到 build/windows/installer/edition.nsh，
; project.nsi 通过 -DEDITION=1 !include 引入。所有值均为 ASCII 或中文见下：
; PRODUCT_NAME 显示给用户（中文），PRODUCT_NAME_EN 用于安装目录 / 注册表键（ASCII）。
!define PRODUCT_NAME "对号网盘"
!define PRODUCT_NAME_EN "DuihaoDrive"
!define PUBLISHER "Duihao"
!define BINARY "duihao-drive.exe"
; 安装包文件名前缀（core 默认 duihao）
!define INSTALLER_BASENAME "DuihaoDrive"
; 运行时 editions 文件（相对 app/editions/），安装时重命名为 editions.json
!define EDITION_JSON "editions.drive.json"
