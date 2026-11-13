# Records the clips and still pictures of the app for its page on khoabug.com.
#
# Nothing of the user's own work is shown or read: the app runs against a
# made-up Claude Code config and a scripted stand-in for Claude Code, both on a
# temporary drive letter so that no real path appears on screen, and with its
# own data folders. Only the app's window is captured.
#
# Everything it makes goes under -Work, outside the repository: a copy of the
# project with the test door built in, the made-up config, the frames, and in
# out\ the clips, the still pictures and, in out\page\, the page's pictures.
param(
    [string] $Project = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path,
    [string] $Work = (Join-Path $env:TEMP "agent-desk-demo"),
    [string] $Drive = "R:",
    [int] $Port = 47833,
    [double] $Scale = 0.75,
    [switch] $SkipBuild,
    [switch] $OnlyGif
)

$here = $PSScriptRoot
$src = Join-Path $Work "src"
$exe = Join-Path $src "build\bin\agent-desk-e2e.exe"
$rootDir = Join-Path $Work "root"
$take = Join-Path $Work "take"
$out = Join-Path $Work "out"
$tools = Join-Path $Work "tools"
$clip = Join-Path $tools "clip.exe"
$stand = Join-Path $tools "claude.exe"
$pageimg = Join-Path $tools "pageimg.exe"
$helpersFile = Join-Path $Work "helpers.json"
$utf8 = New-Object System.Text.UTF8Encoding $false

New-Item -ItemType Directory -Force $Work, $tools | Out-Null
Push-Location $here
go build -o $clip ./clip; $ok = $LASTEXITCODE
if ($ok -eq 0) { go build -o $stand ./democlaude; $ok = $LASTEXITCODE }
if ($ok -eq 0) { go build -o $pageimg ./pageimg; $ok = $LASTEXITCODE }
Pop-Location
if ($ok -ne 0) { "could not build the tools"; exit 1 }

# What the tools print goes to the host: anything left in the pipeline of a
# function becomes part of what it returns, and only the exit code should.
function MakeMedia {
    node (Join-Path $here "post.mjs") $take $clip $out $Scale | Out-Host
    if ($LASTEXITCODE -ne 0) { return $LASTEXITCODE }
    & $pageimg (Join-Path $out "shots") (Join-Path $Project "build\appicon.png") (Join-Path $out "page") | Out-Host
    if ($LASTEXITCODE -ne 0) { return $LASTEXITCODE }
    Copy-Item (Join-Path $out "watch.gif"), (Join-Path $out "work.gif") (Join-Path $out "page")
    Write-Host "the page's pictures are in $(Join-Path $out 'page')"
    return 0
}
if ($OnlyGif) { exit (MakeMedia) }

if (-not $SkipBuild) {
    # The copy leaves out what is built or installed, and these tools.
    robocopy $Project $src /MIR /XD node_modules bin .git (Join-Path $Project "tools") /NFL /NDL /NJH /NJS /NP | Out-Null
    if ($LASTEXITCODE -ge 8) { "copying the project failed ($LASTEXITCODE)"; exit 1 }
    Copy-Item (Join-Path $here "e2e\e2e_hook.go") (Join-Path $src "e2e_hook.go") -Force
    $appGo = Join-Path $src "app.go"
    $code = [IO.File]::ReadAllText($appGo)
    $hooked = $code.Replace("`tgo a.watch(loopCtx)`n}", "`tgo a.watch(loopCtx)`n`ta.startE2E(ctx)`n}")
    if ($hooked -eq $code) { $hooked = $code.Replace("`tgo a.watch(loopCtx)`r`n}", "`tgo a.watch(loopCtx)`r`n`ta.startE2E(ctx)`r`n}") }
    if ($hooked -eq $code) { "could not hook the test door into startup"; exit 1 }
    [IO.File]::WriteAllText($appGo, $hooked, $utf8)
    Push-Location $src
    wails build -tags e2e -trimpath -webview2 error -s -skipbindings -o agent-desk-e2e.exe | Select-String -Pattern 'Built|ERROR|rror:' | ForEach-Object { $_.Line }
    $built = $LASTEXITCODE
    Pop-Location
    if ($built -ne 0 -or -not (Test-Path $exe)) { "the test build failed"; exit 1 }
}
if (-not (Test-Path $exe)) { "no test build under $src; run without -SkipBuild first"; exit 1 }

# A drive letter of its own for everything the app will show a path of.
if (Test-Path "$Drive\") { subst $Drive /D | Out-Null }
if (Test-Path $rootDir) { Remove-Item -Recurse -Force $rootDir }
New-Item -ItemType Directory -Force $rootDir, (Join-Path $rootDir "bin"), (Join-Path $rootDir "_app\roaming"), (Join-Path $rootDir "_app\local") | Out-Null
subst $Drive $rootDir
if (-not (Test-Path "$Drive\")) { "could not map $Drive"; exit 1 }
Copy-Item $stand "$Drive\bin\claude.exe"

# Three processes stand in for the Claude Code processes of the made-up sessions.
$helpers = 1..3 | ForEach-Object { Start-Process -FilePath "powershell" -ArgumentList "-NoProfile", "-Command", "Start-Sleep 900" -PassThru -WindowStyle Hidden }
Start-Sleep -Milliseconds 400
$list = @($helpers | ForEach-Object { @{ pid = $_.Id; procStart = "$($_.StartTime.ToFileTimeUtc())" } })
[IO.File]::WriteAllText($helpersFile, (ConvertTo-Json $list -Compress), $utf8)

$changed = @{}
function SetEnv([string] $name, $value) {
    if (-not $changed.ContainsKey($name)) { $changed[$name] = [Environment]::GetEnvironmentVariable($name, "Process") }
    [Environment]::SetEnvironmentVariable($name, $value, "Process")
}
foreach ($name in @([Environment]::GetEnvironmentVariables("Process").Keys)) {
    if ($name -match '^(CLAUDE|ANTHROPIC)' -and
        $null -eq [Environment]::GetEnvironmentVariable($name, "User") -and
        $null -eq [Environment]::GetEnvironmentVariable($name, "Machine")) { SetEnv $name $null }
}
$token = [Guid]::NewGuid().ToString("N")
SetEnv "MONITOR_AGENT_E2E_PORT" "$Port"
SetEnv "MONITOR_AGENT_E2E_TOKEN" $token
SetEnv "APPDATA" "$Drive\_app\roaming"
SetEnv "LOCALAPPDATA" "$Drive\_app\local"
SetEnv "CLAUDE_CONFIG_DIR" "$Drive\claude"
SetEnv "MONITOR_AGENT_CLAUDE" "$Drive\bin\claude.exe"

$code = 1
$p = $null
try {
    node (Join-Path $here "demo.mjs") fixture "$Drive\" $helpersFile
    if ($LASTEXITCODE -ne 0) { throw "the fixture could not be written" }
    $p = Start-Process -FilePath $exe -PassThru
    node (Join-Path $here "demo.mjs") run "$Drive\" $take $p.Id $clip
    $code = $LASTEXITCODE
}
catch { "ABORTED: $_" }
finally {
    foreach ($name in $changed.Keys) { [Environment]::SetEnvironmentVariable($name, $changed[$name], "Process") }
    if ($p) {
        $p.Refresh()
        if (-not $p.HasExited) { [void]$p.CloseMainWindow(); if (-not $p.WaitForExit(15000)) { "app did not close by itself; killing it"; $p.Kill() } }
    }
    Start-Sleep -Milliseconds 800
    $helpers | ForEach-Object { if (-not $_.HasExited) { $_.Kill() } }
    Get-Process -Name claude -ErrorAction SilentlyContinue | Where-Object { $_.Path -like "$Drive\*" } | ForEach-Object { $_.Kill() }
    Start-Sleep -Milliseconds 500
    subst $Drive /D | Out-Null
    # The test build registered itself for Windows notifications; undo that.
    $reg = "HKCU:\Software\Classes\AppUserModelId\agent-desk-e2e.exe"
    if (Test-Path $reg) {
        $clsid = (Get-ItemProperty $reg).CustomActivator
        $icon = (Get-ItemProperty $reg).IconUri
        Remove-Item $reg -Recurse -Force
        if ($clsid -match '^\{[0-9a-fA-F-]{36}\}$' -and (Test-Path "HKCU:\Software\Classes\CLSID\$clsid")) { Remove-Item "HKCU:\Software\Classes\CLSID\$clsid" -Recurse -Force }
        if ($icon -and $icon -like "*agent-desk-e2e.exe*" -and (Test-Path -LiteralPath $icon)) { Remove-Item -LiteralPath $icon -Force }
    }
}
if ($code -ne 0) { "the demo did not run to its end"; exit $code }
exit (MakeMedia)
