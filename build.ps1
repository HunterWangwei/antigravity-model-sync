$ErrorActionPreference = "Stop"
$output = Join-Path $PSScriptRoot "dist\windows\amd64"
New-Item -ItemType Directory -Force -Path $output | Out-Null
Push-Location $PSScriptRoot
try {
    go test ./...
    go build -buildmode=c-shared -o (Join-Path $output "antigravity-model-sync.dll") .
    Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path $output "antigravity-model-sync.h")
} finally {
    Pop-Location
}
