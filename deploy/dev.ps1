param(
  [ValidateSet("start", "stop", "restart", "status", "build")]
  [string]$Action = "start",

  [switch]$Build,
  [switch]$Install,
  [switch]$ForcePortStop
)

$ErrorActionPreference = "Stop"

if ($PSVersionTable.PSVersion.Major -lt 7) {
  throw "PowerShell 7+ is required. Current: $($PSVersionTable.PSVersion)"
}

$CoreRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
$WorkspaceRoot = Resolve-Path (Join-Path $CoreRoot "..")
$SdkFrontend = Join-Path $WorkspaceRoot "airgate-sdk\frontend"
$OpenAIPluginRoot = Join-Path $WorkspaceRoot "airgate-openai"
$ClaudePluginRoot = Join-Path $WorkspaceRoot "airgate-claude"
$WebDir = Join-Path $CoreRoot "web"
$BackendDir = Join-Path $CoreRoot "backend"
$WebDist = Join-Path $WebDir "dist"
$WebDistEmbed = Join-Path $BackendDir "internal\web\webdist"
$StateDir = Join-Path $CoreRoot ".dev"
$BackendPidFile = Join-Path $StateDir "backend.pid"
$FrontendPidFile = Join-Path $StateDir "frontend.pid"
$BackendOut = Join-Path $BackendDir "tmp\backend.out.log"
$BackendErr = Join-Path $BackendDir "tmp\backend.err.log"
$FrontendOut = Join-Path $WebDir "tmp\frontend.out.log"
$FrontendErr = Join-Path $WebDir "tmp\frontend.err.log"
$FrontendDevPort = 80

$PluginSpecs = @(
  [pscustomobject]@{
    Name = "gateway-openai"
    Root = $OpenAIPluginRoot
    WebDir = Join-Path $OpenAIPluginRoot "web"
    BackendDir = Join-Path $OpenAIPluginRoot "backend"
    WebDist = Join-Path $OpenAIPluginRoot "web\dist"
    EmbedDir = Join-Path $OpenAIPluginRoot "backend\internal\gateway\webdist"
    WatchPidFile = Join-Path $StateDir "gateway-openai-web.pid"
    WatchOut = Join-Path $OpenAIPluginRoot "tmp\web-watch.out.log"
    WatchErr = Join-Path $OpenAIPluginRoot "tmp\web-watch.err.log"
  },
  [pscustomobject]@{
    Name = "gateway-claude"
    Root = $ClaudePluginRoot
    WebDir = Join-Path $ClaudePluginRoot "web"
    BackendDir = Join-Path $ClaudePluginRoot "backend"
    WebDist = Join-Path $ClaudePluginRoot "web\dist"
    EmbedDir = Join-Path $ClaudePluginRoot "backend\internal\gateway\webdist"
    WatchPidFile = Join-Path $StateDir "gateway-claude-web.pid"
    WatchOut = Join-Path $ClaudePluginRoot "tmp\web-watch.out.log"
    WatchErr = Join-Path $ClaudePluginRoot "tmp\web-watch.err.log"
  }
)

function Write-Step([string]$Message) {
  Write-Host "==> $Message"
}

function Invoke-InDir([string]$Directory, [string]$Command) {
  Write-Step "$Directory > $Command"
  Push-Location $Directory
  try {
    pwsh -NoLogo -NoProfile -Command $Command
    if ($LASTEXITCODE -ne 0) {
      throw "Command failed with exit code ${LASTEXITCODE}: $Command"
    }
  } finally {
    Pop-Location
  }
}

function Assert-Command([string]$Name) {
  if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
    throw "Missing command: $Name"
  }
}

function Assert-Paths {
  if (-not (Test-Path $SdkFrontend)) {
    throw "SDK frontend not found: $SdkFrontend"
  }
  if (-not (Test-Path $WebDir)) {
    throw "Core web not found: $WebDir"
  }
  if (-not (Test-Path $BackendDir)) {
    throw "Core backend not found: $BackendDir"
  }
  foreach ($plugin in $PluginSpecs) {
    if (-not (Test-Path $plugin.WebDir)) {
      throw "$($plugin.Name) web not found: $($plugin.WebDir)"
    }
    if (-not (Test-Path $plugin.BackendDir)) {
      throw "$($plugin.Name) backend not found: $($plugin.BackendDir)"
    }
  }
}

function Ensure-Dirs {
  New-Item -ItemType Directory -Force -Path `
    $StateDir, `
    (Join-Path $BackendDir "tmp"), `
    (Join-Path $WebDir "tmp"), `
    $WebDistEmbed | Out-Null

  foreach ($plugin in $PluginSpecs) {
    New-Item -ItemType Directory -Force -Path `
      (Join-Path $plugin.Root "tmp"), `
      $plugin.EmbedDir | Out-Null
  }
}

function Install-Deps {
  Assert-Command "pnpm"
  Assert-Command "go"
  Invoke-InDir $SdkFrontend "pnpm install"
  Invoke-InDir $SdkFrontend "pnpm build"
  Invoke-InDir $WebDir "pnpm install --force"
  Invoke-InDir $BackendDir "`$env:GOTOOLCHAIN = 'local'; go mod download"
  foreach ($plugin in $PluginSpecs) {
    Ensure-PluginGoWork $plugin
    Invoke-InDir $plugin.WebDir "pnpm install --force"
    Invoke-InDir $plugin.BackendDir "`$env:GOTOOLCHAIN = 'local'; go mod download"
  }
}

function Sync-Webdist {
  if (-not (Test-Path (Join-Path $WebDist "index.html"))) {
    throw "Core web dist is missing. Run: .\deploy\dev.ps1 build"
  }

  $resolvedCore = (Resolve-Path $CoreRoot).Path
  $resolvedEmbed = (Resolve-Path $WebDistEmbed).Path
  if (-not $resolvedEmbed.StartsWith($resolvedCore, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Refusing to sync outside core repo: $resolvedEmbed"
  }

  Get-ChildItem -LiteralPath $WebDistEmbed -Force |
    Where-Object { $_.Name -ne ".gitkeep" } |
    Remove-Item -Recurse -Force

  Copy-Item -Path (Join-Path $WebDist "*") -Destination $WebDistEmbed -Recurse -Force
  Write-Step "synced web/dist -> backend/internal/web/webdist"
}

function Ensure-PluginGoWork($Plugin) {
  $goWork = Join-Path $Plugin.BackendDir "go.work"
  $desired = @"
go 1.25.7

use .

replace github.com/DouDOU-start/airgate-sdk => ../../airgate-sdk
"@

  if (-not (Test-Path $goWork) -or ((Get-Content -Raw $goWork) -ne $desired)) {
    Set-Content -Path $goWork -Value $desired
    Write-Step "wrote $($Plugin.Name) backend/go.work for local SDK"
  }
}

function Sync-PluginWebdist($Plugin) {
  if (-not (Test-Path (Join-Path $Plugin.WebDist "index.js"))) {
    throw "$($Plugin.Name) web dist is missing. Run: .\deploy\dev.ps1 build"
  }

  $resolvedRoot = (Resolve-Path $Plugin.Root).Path
  $resolvedEmbed = (Resolve-Path $Plugin.EmbedDir).Path
  if (-not $resolvedEmbed.StartsWith($resolvedRoot, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Refusing to sync outside plugin repo: $resolvedEmbed"
  }

  Get-ChildItem -LiteralPath $Plugin.EmbedDir -Force |
    Where-Object { $_.Name -ne ".gitkeep" } |
    Remove-Item -Recurse -Force

  Copy-Item -Path (Join-Path $Plugin.WebDist "*") -Destination $Plugin.EmbedDir -Recurse -Force
  Write-Step "synced $($Plugin.Name) web/dist -> backend/internal/gateway/webdist"
}

function Build-Plugin($Plugin) {
  Ensure-PluginGoWork $Plugin

  $themeTypes = Join-Path $Plugin.WebDir "node_modules\@airgate\theme\dist\index.d.ts"
  if (-not (Test-Path $themeTypes)) {
    Invoke-InDir $Plugin.WebDir "pnpm install --force"
  }

  Invoke-InDir $Plugin.WebDir "pnpm build"
  Sync-PluginWebdist $Plugin

  New-Item -ItemType Directory -Force -Path (Join-Path $Plugin.Root "bin") | Out-Null
  Invoke-InDir $Plugin.BackendDir "`$env:GOTOOLCHAIN = 'local'; go build -o ..\bin\$($Plugin.Name) ."
}

function Build-All {
  Assert-Command "pnpm"
  Invoke-InDir $SdkFrontend "pnpm build"

  $themeTypes = Join-Path $WebDir "node_modules\@airgate\theme\dist\index.d.ts"
  if (-not (Test-Path $themeTypes)) {
    Invoke-InDir $WebDir "pnpm install --force"
  }

  Invoke-InDir $WebDir "pnpm build"
  Sync-Webdist

  foreach ($plugin in $PluginSpecs) {
    Build-Plugin $plugin
  }
}

function Get-ChildProcessIds([int]$RootProcessId) {
  $all = Get-CimInstance Win32_Process
  $result = New-Object System.Collections.Generic.List[int]

  function Add-Children([int]$ParentId) {
    $children = $all | Where-Object { $_.ParentProcessId -eq $ParentId }
    foreach ($child in $children) {
      Add-Children ([int]$child.ProcessId)
      $result.Add([int]$child.ProcessId)
    }
  }

  Add-Children $RootProcessId
  $result.Add($RootProcessId)
  $result | Select-Object -Unique
}

function Stop-ProcessTree([int]$RootProcessId) {
  if (-not (Get-Process -Id $RootProcessId -ErrorAction SilentlyContinue)) {
    return
  }

  $ids = Get-ChildProcessIds $RootProcessId
  foreach ($id in $ids) {
    Stop-Process -Id $id -Force -ErrorAction SilentlyContinue
  }
}

function Stop-FromPidFile([string]$Name, [string]$PidFile) {
  if (-not (Test-Path $PidFile)) {
    Write-Step "$Name pid file not found"
    return
  }

  $raw = (Get-Content -Raw $PidFile).Trim()
  if ($raw -match "^\d+$") {
    Write-Step "stopping $Name pid $raw"
    Stop-ProcessTree ([int]$raw)
  }
  Remove-Item -Force $PidFile -ErrorAction SilentlyContinue
}

function Stop-PortListeners([int[]]$Ports) {
  foreach ($port in $Ports) {
    $listeners = Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue
    foreach ($listener in $listeners) {
      Write-Step "force stopping listener on port $port pid $($listener.OwningProcess)"
      Stop-ProcessTree ([int]$listener.OwningProcess)
    }
  }
}

function Stop-Dev {
  foreach ($plugin in $PluginSpecs) {
    Stop-FromPidFile "$($plugin.Name) web watch" $plugin.WatchPidFile
  }
  Stop-FromPidFile "frontend" $FrontendPidFile
  Stop-FromPidFile "backend" $BackendPidFile

  if ($ForcePortStop) {
    Stop-PortListeners @($FrontendDevPort, 9517)
  }
}

function Test-PortFree([int]$Port) {
  -not (Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue)
}

function Ensure-PluginDists {
  foreach ($plugin in $PluginSpecs) {
    Ensure-PluginGoWork $plugin
    if (-not (Test-Path (Join-Path $plugin.WebDist "index.js")) -or -not (Test-Path (Join-Path $plugin.EmbedDir "index.js"))) {
      Write-Step "$($plugin.Name) webdist is missing; building plugin"
      Build-Plugin $plugin
    }
  }
}

function Start-PluginWatchers {
  foreach ($plugin in $PluginSpecs) {
    Remove-Item -Force $plugin.WatchOut, $plugin.WatchErr -ErrorAction SilentlyContinue
    $watch = Start-Process `
      -FilePath "pwsh" `
      -ArgumentList @("-NoLogo", "-NoProfile", "-Command", "pnpm dev") `
      -WorkingDirectory $plugin.WebDir `
      -WindowStyle Hidden `
      -RedirectStandardOutput $plugin.WatchOut `
      -RedirectStandardError $plugin.WatchErr `
      -PassThru
    Set-Content -Path $plugin.WatchPidFile -Value $watch.Id
    Write-Step "$($plugin.Name) web watch starting, pid $($watch.Id), logs: $($plugin.WatchOut)"
  }
}

function Start-Dev {
  Assert-Command "pnpm"
  Assert-Command "go"
  Ensure-Dirs

  if ($Install) {
    Install-Deps
  }
  if ($Build) {
    Build-All
  } elseif (-not (Test-Path (Join-Path $WebDistEmbed "index.html"))) {
    Write-Step "embedded webdist is missing; building once"
    Build-All
  }
  Ensure-PluginDists

  if (-not (Test-PortFree 9517)) {
    throw "Port 9517 is already in use. Run: .\deploy\dev.ps1 stop -ForcePortStop"
  }
  if (-not (Test-PortFree $FrontendDevPort)) {
    throw "Port $FrontendDevPort is already in use. Run: .\deploy\dev.ps1 stop -ForcePortStop"
  }

  Remove-Item -Force $BackendOut, $BackendErr, $FrontendOut, $FrontendErr -ErrorAction SilentlyContinue
  Start-PluginWatchers

  $backend = Start-Process `
    -FilePath "pwsh" `
    -ArgumentList @("-NoLogo", "-NoProfile", "-Command", "`$env:GOTOOLCHAIN = 'local'; go run ./cmd/server") `
    -WorkingDirectory $BackendDir `
    -WindowStyle Hidden `
    -RedirectStandardOutput $BackendOut `
    -RedirectStandardError $BackendErr `
    -PassThru
  Set-Content -Path $BackendPidFile -Value $backend.Id

  $frontend = Start-Process `
    -FilePath "pwsh" `
    -ArgumentList @("-NoLogo", "-NoProfile", "-Command", "pnpm dev -- --port $FrontendDevPort --strictPort") `
    -WorkingDirectory $WebDir `
    -WindowStyle Hidden `
    -RedirectStandardOutput $FrontendOut `
    -RedirectStandardError $FrontendErr `
    -PassThru
  Set-Content -Path $FrontendPidFile -Value $frontend.Id

  Write-Step "backend starting, pid $($backend.Id), logs: $BackendOut"
  Write-Step "frontend starting, pid $($frontend.Id), logs: $FrontendOut"
  Start-Sleep -Seconds 3
  Show-Status
}

function Show-Status {
  $rows = foreach ($port in $FrontendDevPort, 9517) {
    $listeners = Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue
    if ($listeners) {
      foreach ($listener in $listeners) {
        $proc = Get-Process -Id $listener.OwningProcess -ErrorAction SilentlyContinue
        [pscustomobject]@{
          Port = $port
          Status = "listening"
          PID = $listener.OwningProcess
          Process = $proc.ProcessName
          Address = $listener.LocalAddress
        }
      }
    } else {
      [pscustomobject]@{
        Port = $port
        Status = "free"
        PID = ""
        Process = ""
        Address = ""
      }
    }
  }

  $rows | Format-Table -AutoSize
  Write-Host "Frontend: http://localhost/"
  Write-Host "Backend:  http://127.0.0.1:9517"
  Write-Host "Backend log:  $BackendOut"
  Write-Host "Frontend log: $FrontendOut"
  foreach ($plugin in $PluginSpecs) {
    $watchPid = if (Test-Path $plugin.WatchPidFile) { (Get-Content -Raw $plugin.WatchPidFile).Trim() } else { "" }
    $devLoaded = if (Test-Path (Join-Path $plugin.WebDist "index.js")) { "web dist ok" } else { "web dist missing" }
    Write-Host "$($plugin.Name): $devLoaded, watch pid $watchPid, log: $($plugin.WatchOut)"
  }
}

Assert-Paths
Ensure-Dirs

switch ($Action) {
  "start" {
    Start-Dev
  }
  "stop" {
    Stop-Dev
    Show-Status
  }
  "restart" {
    Stop-Dev
    Start-Sleep -Seconds 1
    Start-Dev
  }
  "status" {
    Show-Status
  }
  "build" {
    if ($Install) {
      Install-Deps
    }
    Build-All
  }
}
