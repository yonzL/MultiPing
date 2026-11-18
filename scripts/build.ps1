$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    $env:GOTOOLCHAIN = "go1.21.13"

    $go = (Get-Command go -ErrorAction SilentlyContinue).Source
    if (-not $go) {
        $candidates = @(
            (Join-Path $env:ProgramFiles "Go\bin\go.exe"),
            "C:\Go\bin\go.exe"
        )
        $go = $candidates | Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1
    }
    if (-not $go) {
        throw "go not found. Install Go 1.21 and add it to PATH."
    }

    & $go test ./src/...
    if ($LASTEXITCODE -ne 0) {
        throw "go test failed"
    }

    $rsrc = (Get-Command rsrc -ErrorAction SilentlyContinue).Source
    if (-not $rsrc) {
        $candidate = Join-Path $env:USERPROFILE "go\bin\rsrc.exe"
        if (Test-Path -LiteralPath $candidate) {
            $rsrc = $candidate
        }
    }
    if (-not $rsrc) {
        throw "rsrc not found. Install it with: go install github.com/akavel/rsrc@v0.10.2"
    }

    & $rsrc -manifest build/app.manifest -ico build/app.ico -o src/rsrc.syso -arch amd64
    if ($LASTEXITCODE -ne 0) {
        throw "rsrc failed"
    }

    New-Item -ItemType Directory -Force dist | Out-Null
    & $go build -ldflags="-H windowsgui" -o dist/MultiPing.exe ./src
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed"
    }

    Copy-Item resource/hosts.csv dist/hosts.csv -Force
    Write-Host "Built dist/MultiPing.exe"
} finally {
    Pop-Location
}
