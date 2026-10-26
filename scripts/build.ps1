# 对号 — 本地构建基线脚本（CI 的本地等价物）
# 用法: powershell -NoProfile -ExecutionPolicy Bypass -File scripts\build.ps1 [-Package]
# 产物: app\bin\duihao.exe（+ 可选 NSIS 安装包 app\build\bin\duihao-amd64-installer.exe）
# 注意: 本脚本必须存 UTF-8 with BOM（PowerShell 5.1 兼容）
param(
    [switch]$Package
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot   # 对号主工程 app/ 的上级（E:/workbudy/duihao）
$app  = Join-Path $root 'app'

# 工具链（与 M0 实测路径一致）
$go     = 'C:\Users\HUAWEI\.workbuddy\binaries\go\go\bin'
$wails  = 'E:\workbudy\duihao\.tools\bin'
$nsis   = 'C:\Program Files (x86)\NSIS'
$env:PATH    = "$go;$wails;$nsis;$env:PATH"
$env:GOPROXY = 'https://goproxy.cn,direct'
$env:GOFLAGS = '-mod=mod'

$version = if ($env:VERSION) { $env:VERSION } else { Get-Date -Format 'yyyy.MM.dd.HHmm' }
Write-Host "== 对号构建 v$version ==" -ForegroundColor Cyan

$t0 = Get-Date
Push-Location $app
try {
    if ($Package) {
        wails3 task package "VERSION=$version"
        if ($LASTEXITCODE -ne 0) { throw "wails3 task package 失败 (exit $LASTEXITCODE)" }
        $exe  = Get-Item 'bin\duihao.exe' -ErrorAction SilentlyContinue
        $inst = Get-Item "build\bin\duihao-amd64-installer.exe" -ErrorAction SilentlyContinue
        Write-Host ''
        Write-Host ('exe       : {0}  {1:N2} MiB' -f $exe.Name, ($exe.Length / 1MB))
        Write-Host ('installer : {0}  {1:N2} MiB' -f $inst.Name, ($inst.Length / 1MB))
    }
    else {
        wails3 build "VERSION=$version"
        if ($LASTEXITCODE -ne 0) { throw "wails3 build 失败 (exit $LASTEXITCODE)" }
        $exe = Get-Item 'bin\duihao.exe' -ErrorAction SilentlyContinue
        Write-Host ''
        Write-Host ('exe       : {0}  {1:N2} MiB' -f $exe.Name, ($exe.Length / 1MB))
    }
    $mins = [math]::Round(((Get-Date) - $t0).TotalMinutes, 1)
    Write-Host ("耗时       : {0} 分钟" -f $mins) -ForegroundColor Green
}
finally {
    Pop-Location
}
