# HomeOwl installer for Windows (run in an elevated PowerShell):
#
#   irm https://github.com/h4ux/HomeOwl/releases/latest/download/install.ps1 | iex
#
# $env:HOMEOWL_VERSION = "v1.2.3" to install a specific release.
$ErrorActionPreference = "Stop"
$repo = "h4ux/HomeOwl"

function Say($m) { Write-Host $m -ForegroundColor Cyan }
function Ok($m) { Write-Host "  OK  $m" -ForegroundColor Green }

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  throw "Please run PowerShell as Administrator (HomeOwl installs as a Windows service)."
}
if ($env:PROCESSOR_ARCHITECTURE -ne "AMD64") { throw "HomeOwl for Windows needs a 64-bit Intel/AMD PC." }

$name = "homeowl_windows_amd64.zip"
$base = if ($env:HOMEOWL_VERSION) { "https://github.com/$repo/releases/download/$($env:HOMEOWL_VERSION)" } else { "https://github.com/$repo/releases/latest/download" }
$tmp = Join-Path $env:TEMP ("homeowl-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null

Say "Installing HomeOwl"
Invoke-WebRequest "$base/$name" -OutFile "$tmp\$name" -UseBasicParsing
try {
  Invoke-WebRequest "$base/SHA256SUMS" -OutFile "$tmp\SHA256SUMS" -UseBasicParsing
  $want = (Select-String -Path "$tmp\SHA256SUMS" -Pattern " $name$").Line.Split(" ")[0]
  $got = (Get-FileHash "$tmp\$name" -Algorithm SHA256).Hash.ToLower()
  if ($want -ne $got) { throw "Checksum mismatch - the download is damaged or was tampered with" }
  Ok "Downloaded and verified"
} catch { if ($_.Exception.Message -like "Checksum*") { throw } }

$dir = Join-Path $env:ProgramFiles "HomeOwl"
$exe = Join-Path $dir "homeowl.exe"
if (Test-Path $exe) { & $exe service stop 2>$null; & $exe service uninstall 2>$null }
New-Item -ItemType Directory -Force -Path $dir | Out-Null
Expand-Archive "$tmp\$name" -DestinationPath $dir -Force
Ok "Installed $exe"

$path = [Environment]::GetEnvironmentVariable("Path", "Machine")
if ($path -notlike "*$dir*") { [Environment]::SetEnvironmentVariable("Path", "$path;$dir", "Machine"); Ok "Added HomeOwl to PATH" }

& $exe service install
& $exe service start
Ok "Runs as a Windows service (starts with Windows)"

try { New-NetFirewallRule -DisplayName "HomeOwl" -Direction Inbound -Program $exe -Action Allow -Profile Private | Out-Null; Ok "Allowed HomeOwl on private networks in Windows Firewall" } catch {}

Remove-Item -Recurse -Force $tmp
Start-Sleep 2
Say ""
Say "HomeOwl is ready: http://127.0.0.1:8555 - create your admin account there."
Start-Process "http://127.0.0.1:8555"
