$ErrorActionPreference = 'Stop'
# Run from the Cordis module root on a real Windows amd64 runner.
$work = Join-Path ([IO.Path]::GetTempPath()) ('cordis-' + [guid]::NewGuid())
New-Item -ItemType Directory -Path $work | Out-Null
function Run-Go {
    & go @args
    if ($LASTEXITCODE -ne 0) { throw "go failed with exit code $LASTEXITCODE" }
}
try {
    Run-Go env GOOS GOARCH GOVERSION
    Run-Go test -tags=integration -race ./... -timeout 5m
    Run-Go build -o "$work/basic-host.exe" ./examples/basic-host
    Run-Go build -o "$work/lifecycle-smoke.exe" ./conformance/cmd/lifecycle-smoke
    Push-Location examples/plugins/counter
    try {
        Run-Go build -o "$work/counter.exe" .
    } finally { Pop-Location }
    Push-Location examples/plugins/chain
    try {
        Run-Go build -o "$work/chain.exe" .
        Run-Go build -ldflags '-X main.rejectInitialization=true' -o "$work/chain-bad.exe" .
    } finally { Pop-Location }
    & "$work/basic-host.exe" -native "$work/counter.exe"
    if ($LASTEXITCODE -ne 0) { throw 'Basic host smoke failed' }
    & "$work/lifecycle-smoke.exe" -guest "$work/chain.exe" -bad-guest "$work/chain-bad.exe"
    if ($LASTEXITCODE -ne 0) { throw 'Native lifecycle smoke failed' }
    Write-Output 'Full Windows acceptance passed.'
} finally {
    # Windows can retain an exited executable image briefly after Wait returns.
    $deadline=[DateTime]::UtcNow.AddSeconds(2)
    while ($true) {
        try { Remove-Item -Recurse -Force $work; break }
        catch [UnauthorizedAccessException] {
            if ([DateTime]::UtcNow -ge $deadline) { throw }
            Start-Sleep -Milliseconds 10
        }
        catch [IO.IOException] {
            if ([DateTime]::UtcNow -ge $deadline) { throw }
            Start-Sleep -Milliseconds 10
        }
    }
}
