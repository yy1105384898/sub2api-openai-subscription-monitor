param([string]$Version = "0.2.1")
$ErrorActionPreference = "Stop"
$env:CGO_ENABLED = "0"
$env:GOOS = "linux"
$env:GOARCH = "amd64"
New-Item -ItemType Directory -Force "build/linux-amd64", "dist" | Out-Null
go build -trimpath -ldflags "-s -w -X main.version=$Version" -o "build/linux-amd64/plugin" ./cmd/plugin
$env:GOOS = "windows"
$env:GOARCH = "amd64"
New-Item -ItemType Directory -Force "build/windows-amd64" | Out-Null
go build -trimpath -ldflags "-s -w -X main.version=$Version" -o "build/windows-amd64/plugin.exe" ./cmd/plugin
go run ./cmd/packager -binary "build/linux-amd64/plugin" -windows-binary "build/windows-amd64/plugin.exe" -ui "ui" -output "dist/openai-subscription-monitor-$Version.s2plugin" -version $Version
