# Build fast-oj binaries. Default target is the current dev host.
#   .\build.ps1                 # windows/amd64 -> dist\
#   .\build.ps1 -Arch 386       # legacy 32-bit judge box
#   .\build.ps1 -Test           # run vet + tests instead

param(
    [ValidateSet("amd64", "386", "arm64")]
    [string]$Arch = "amd64",
    [string]$OS = "windows",
    [string]$OutDir = "dist",
    [switch]$Test
)

$ErrorActionPreference = "Stop"
$env:CGO_ENABLED = "0"
$env:GOOS = $OS
$env:GOARCH = $Arch

if ($Test) {
    $env:GOOS = ""
    $env:GOARCH = ""
    go vet ./...
    go test ./...
    return
}

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
go build -o "$OutDir/" ./cmd/...
Get-ChildItem $OutDir | ForEach-Object { Write-Host ("{0,-20} {1,8:N0} KB" -f $_.Name, ($_.Length / 1KB)) }
