# Builds the app the way it is meant to be handed out.
#
#   -trimpath        keeps this machine's folder names out of the executable
#   -webview2 error  if the WebView2 runtime is missing, say so instead of
#                    downloading and running its installer; with this the app
#                    itself never opens a network connection
#
# Run the tests first; a build that fails them is not worth handing out.
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

go vet ./...
if ($LASTEXITCODE -ne 0) { throw "go vet failed" }
go test ./internal/...
if ($LASTEXITCODE -ne 0) { throw "tests failed" }

wails build -clean -trimpath -webview2 error
if ($LASTEXITCODE -ne 0) { throw "wails build failed" }

$exe = Join-Path $PSScriptRoot "build\bin\monitor-agent.exe"
"Built $exe"
"SHA-256 " + (Get-FileHash $exe -Algorithm SHA256).Hash
go version -m $exe | Select-String -Pattern '^\S+: go|^\s+path|^\s+mod|golang.org/x/net|wailsapp/wails'
