<#
.SYNOPSIS
  Repeatable end-to-end demo (plan 16): simulated passengers keep a route at
  HIGH crowding until the operator alert appears, then optionally report
  recovery.

.DESCRIPTION
  Uses only the public passenger API, exactly as real passengers would. Each
  simulated passenger has its own X-Client-Id and re-reports every 2.5 minutes
  (the duplicate-control interval is 2 minutes) so the evidence stays inside
  the 10-minute analytics window.

  The production rule is 10 minutes of sustained HIGH. For a faster demo, start
  the server with a shorter sustain time (development only), e.g.:

      $env:ALERT_SUSTAIN = "2m"; .\bin\smart-transit.exe

  and run:  .\scripts\demo.ps1 -Minutes 3

.EXAMPLE
  .\scripts\demo.ps1                       # full 10-minute rule; route 62 (or the first route)
  .\scripts\demo.ps1 -Route bb_5 -Minutes 3 -Recover
#>
param(
    [string]$BaseUrl = "http://localhost:8080",
    [string]$Route = "",   # default: bb_62 (real data) if present, otherwise the first route
    [double]$Minutes = 11,
    [int]$Passengers = 4,
    [switch]$Recover
)

$ErrorActionPreference = "Stop"

if (-not $Route) {
    $ids = (Invoke-RestMethod "$BaseUrl/api/v1/routes").data | ForEach-Object { $_.id }
    if (-not $ids) { throw "No routes found at $BaseUrl. Is the server running?" }
    $Route = if ($ids -contains "bb_62") { "bb_62" } else { @($ids)[0] }
}
$clients = 1..$Passengers | ForEach-Object { "demo-passenger-$_-" + [guid]::NewGuid().ToString("N").Substring(0, 8) }

function Send-Report([string]$clientId, [string]$level) {
    $body = @{ route_id = $Route; crowd_level = $level } | ConvertTo-Json
    try {
        $null = Invoke-RestMethod "$BaseUrl/api/v1/crowd-reports" -Method Post -Body $body `
            -ContentType "application/json" -Headers @{ "X-Client-Id" = $clientId }
        return "ok"
    } catch {
        return "rejected ($([int]$_.Exception.Response.StatusCode))"
    }
}

function Get-Status {
    (Invoke-RestMethod "$BaseUrl/api/v1/routes/$Route/crowd-status").data
}

Write-Host "Smart Transit demo: route $Route, $Passengers simulated passengers, up to $Minutes min" -ForegroundColor Cyan
Write-Host "Watch the operator dashboard at $BaseUrl/operator.html`n"

$start = Get-Date
$lastReport = [datetime]::MinValue

while (((Get-Date) - $start).TotalMinutes -lt $Minutes) {
    if (((Get-Date) - $lastReport).TotalSeconds -ge 150) {
        $results = $clients | ForEach-Object { Send-Report $_ "HIGH" }
        $lastReport = Get-Date
        Write-Host ("[{0:mm\:ss}] {1} passengers reported HIGH: {2}" -f ((Get-Date) - $start), $Passengers, ($results -join ", "))
    }

    $s = Get-Status
    $since = if ($s.high_since) { "HIGH for {0:N1} min" -f ((Get-Date).ToUniversalTime() - [datetime]$s.high_since).TotalMinutes } else { "timer not running" }
    $alertNote = if ($s.status -eq "HIGH") { $since } else { "" }
    Write-Host ("[{0:mm\:ss}] status={1} reports={2} (L/M/H {3}/{4}/{5}) {6}" -f ((Get-Date) - $start), $s.status,
        $s.valid_report_count, $s.low_count, $s.medium_count, $s.high_count, $alertNote)
    Start-Sleep -Seconds 15
}

Write-Host "`nCheck the operator dashboard: one ACTIVE alert should be shown for this route," -ForegroundColor Green
Write-Host "with the recommendation to evaluate allocating a spare/additional bus." -ForegroundColor Green

if ($Recover) {
    Write-Host "`nWaiting for the duplicate interval, then reporting LOW (recovery)..." -ForegroundColor Cyan
    Start-Sleep -Seconds 125
    $results = $clients | ForEach-Object { Send-Report $_ "LOW" }
    Write-Host "Reported LOW: $($results -join ', ')"
    Start-Sleep -Seconds 2
    $s = Get-Status
    Write-Host "Status now: $($s.status). The open alert should show 'ready for review'; it is NOT auto-resolved." -ForegroundColor Green
}
