$ErrorActionPreference = "Stop"

# NullCode build script: dist\nullcode.exe + dist\setup.exe (Windows),
# plus linux/darwin nullcode binaries. setup.exe embeds the Windows binary.

New-Item -ItemType Directory -Force dist | Out-Null

Write-Host "== nullcode (windows) =="
go build -trimpath -ldflags "-s -w" -o dist\nullcode.exe ./cmd/nullcode
if ($LASTEXITCODE -ne 0) { exit 1 }

Write-Host "== setup.exe (embeds dist payload) =="
New-Item -ItemType Directory -Force cmd\setup\payload | Out-Null
Copy-Item dist\nullcode.exe cmd\setup\payload\nullcode.exe -Force
go build -trimpath -ldflags "-s -w" -o dist\setup.exe ./cmd/setup
if ($LASTEXITCODE -ne 0) { exit 1 }

Write-Host "== nullcode (linux/darwin) =="
$env:GOOS = "linux";   $env:GOARCH = "amd64"
go build -trimpath -ldflags "-s -w" -o dist\nullcode-linux-amd64 ./cmd/nullcode
if ($LASTEXITCODE -ne 0) { exit 1 }
$env:GOOS = "darwin";  $env:GOARCH = "arm64"
go build -trimpath -ldflags "-s -w" -o dist\nullcode-darwin-arm64 ./cmd/nullcode
if ($LASTEXITCODE -ne 0) { exit 1 }
$env:GOOS = ""; $env:GOARCH = ""

Write-Host "== msi installer =="
$wix = "$env:USERPROFILE\.dotnet\tools\wix.exe"
if (Test-Path $wix) {
    $ver = "0.1.0"
    & $wix build -arch x64 -d "dist=$PWD\dist" -d "version=$ver" -o "dist\NullCode-$ver-setup.msi" installer\NullCode.wxs
    if ($LASTEXITCODE -ne 0) { exit 1 }
} else {
    Write-Host "wix.exe not found - skip MSI (install with: dotnet tool install --global wix --version 5.0.2)"
}

Write-Host "Done:"
Get-ChildItem dist | Select-Object Name, Length
