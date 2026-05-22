<#
.SYNOPSIS
    prompto installer for Windows.

.DESCRIPTION
    Downloads the prompto binary from the latest GitHub release (or a
    pinned version), verifies its SHA-256 checksum, installs it to
    %LOCALAPPDATA%\Programs\prompto, adds that directory to the user
    PATH, and optionally walks you through a config setup.

    Re-running the installer upgrades in place. If the installed version
    already matches the target, the binary is left alone and only PATH
    wiring and config are re-checked.

.PARAMETER Version
    Specific version to install, e.g. "v0.1.0". Defaults to latest.

.PARAMETER Prefix
    Override install directory. Default: $env:LOCALAPPDATA\Programs\prompto

.PARAMETER NoConfig
    Skip the interactive config wizard.

.PARAMETER NoPath
    Skip PATH modification.

.PARAMETER Uninstall
    Remove the binary and PATH entry; optionally remove config.

.PARAMETER Yes
    Assume "yes" for prompts (non-interactive install).

.EXAMPLE
    # One-line install:
    irm https://raw.githubusercontent.com/marcomoesman/prompto/main/install.ps1 | iex
#>

[CmdletBinding()]
param(
    [string]$Version,
    [string]$Prefix,
    [switch]$NoConfig,
    [switch]$NoPath,
    [switch]$Uninstall,
    [switch]$Yes,
    [switch]$SelfTest
)

$ErrorActionPreference = 'Stop'

$Repo = 'marcomoesman/prompto'

if (-not $Prefix) {
    if ($env:LOCALAPPDATA) {
        $Prefix = Join-Path $env:LOCALAPPDATA 'Programs\prompto'
    } else {
        $Prefix = Join-Path ([System.IO.Path]::GetTempPath()) 'prompto'
    }
}
$BinPath = Join-Path $Prefix 'prompto.exe'

function Info($msg)  { Write-Host $msg }
function Warn($msg)  { Write-Warning $msg }
function Fail($msg)  { Write-Error $msg; exit 1 }

function ConvertTo-ConfigJson($config) {
    return ($config | ConvertTo-Json -Depth 10)
}

function Get-Arch {
    switch ([System.Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture) {
        'X64'   { 'amd64' }
        'Arm64' { 'arm64' }
        default { Fail "unsupported architecture: $_" }
    }
}

function Resolve-LatestTag {
    $url = "https://api.github.com/repos/$Repo/releases/latest"
    try {
        $r = Invoke-RestMethod -Uri $url -Headers @{ 'User-Agent' = 'prompto-installer' }
    } catch {
        Fail "could not query GitHub for latest release: $($_.Exception.Message)"
    }
    if (-not $r.tag_name) { Fail "latest release has no tag_name field" }
    return $r.tag_name
}

function Get-InstalledVersion {
    if (-not (Test-Path -LiteralPath $BinPath)) { return $null }
    try {
        $out = & $BinPath --version 2>$null
    } catch { return $null }
    if (-not $out) { return $null }
    # Expected: "prompto v0.1.0"
    $parts = $out.Trim() -split '\s+'
    if ($parts.Length -lt 2) { return $null }
    return $parts[1].TrimStart('v')
}

function Invoke-SelfTest {
    $config = [ordered]@{
        providers = [ordered]@{
            local = [ordered]@{
                kind = 'openai'
                base_url = 'http://localhost:1234'
                api_key = "a`"b\c`nd"
                models = @(
                    [ordered]@{
                        name = "m`"q"
                        max_tokens = 1
                    }
                )
            }
        }
        default = [ordered]@{
            provider = 'local'
            model = "m`"q"
        }
    }
    $json = ConvertTo-ConfigJson $config
    $roundTrip = $json | ConvertFrom-Json
    if ($roundTrip.providers.local.api_key -ne "a`"b\c`nd") {
        Fail 'JSON config self-test failed for api_key'
    }
    if ($roundTrip.default.model -ne "m`"q") {
        Fail 'JSON config self-test failed for model'
    }
    if ('v1.2.3'.TrimStart('v') -ne '1.2.3') {
        Fail 'version normalization self-test failed'
    }
    Info 'installer self-test OK'
}

if ($SelfTest) {
    Invoke-SelfTest
    return
}

# ---------- PATH helpers ----------

function Test-PathHas([string]$dir) {
    $cur = [Environment]::GetEnvironmentVariable('Path','User')
    if (-not $cur) { return $false }
    $entries = $cur -split ';' | Where-Object { $_ -ne '' }
    foreach ($e in $entries) {
        if ([string]::Equals($e.TrimEnd('\'), $dir.TrimEnd('\'), [StringComparison]::OrdinalIgnoreCase)) {
            return $true
        }
    }
    return $false
}

function Add-ToUserPath([string]$dir) {
    if (Test-PathHas $dir) {
        Info "$dir is already on the user PATH."
        return
    }
    $cur = [Environment]::GetEnvironmentVariable('Path','User')
    if ([string]::IsNullOrEmpty($cur)) {
        $new = $dir
    } else {
        $new = "$cur;$dir"
    }
    [Environment]::SetEnvironmentVariable('Path', $new, 'User')
    Info "Added $dir to user PATH."
    Info "Open a new terminal window for the PATH change to take effect."
}

function Remove-FromUserPath([string]$dir) {
    $cur = [Environment]::GetEnvironmentVariable('Path','User')
    if ([string]::IsNullOrEmpty($cur)) { return }
    $entries = $cur -split ';' | Where-Object {
        $_ -ne '' -and -not [string]::Equals($_.TrimEnd('\'), $dir.TrimEnd('\'), [StringComparison]::OrdinalIgnoreCase)
    }
    $new = ($entries -join ';')
    if ($new -ne $cur) {
        [Environment]::SetEnvironmentVariable('Path', $new, 'User')
        Info "Removed $dir from user PATH."
    }
}

# ---------- uninstall ----------

function Invoke-Uninstall {
    if (Test-Path -LiteralPath $BinPath) {
        Remove-Item -LiteralPath $BinPath -Force
        Info "Removed $BinPath."
    } else {
        Info "No prompto binary at $BinPath."
    }
    if (Test-Path -LiteralPath $Prefix) {
        # Remove the install dir if empty.
        if (-not (Get-ChildItem -LiteralPath $Prefix -Force | Select-Object -First 1)) {
            Remove-Item -LiteralPath $Prefix -Force
        }
    }
    if (-not $NoPath) { Remove-FromUserPath $Prefix }

    $cfgDir = Join-Path $env:AppData 'prompto'
    if (Test-Path -LiteralPath $cfgDir) {
        $reply = 'n'
        if (-not $Yes) {
            $reply = Read-Host "Also remove $cfgDir (config)? [y/N]"
        }
        if ($reply -match '^(y|yes)$') {
            Remove-Item -LiteralPath $cfgDir -Recurse -Force
            Info "Removed $cfgDir."
        } else {
            Info "Kept $cfgDir."
        }
    }
    Info "Uninstall complete."
}

if ($Uninstall) {
    Invoke-Uninstall
    return
}

# ---------- install / upgrade ----------

if ([Environment]::OSVersion.Platform -ne 'Win32NT') {
    Fail "this installer targets Windows; use install.sh on macOS/Linux"
}

$arch = Get-Arch
if ($arch -ne 'amd64') {
    Fail "no Windows release is published for arch '$arch' (only windows/amd64 is built)"
}

if (-not $Version) { $Version = Resolve-LatestTag }
$semver = $Version.TrimStart('v')
$tag    = "v$semver"

$existing = Get-InstalledVersion
if ($existing -and $existing -eq $semver) {
    Info "prompto v$semver is already installed at $BinPath."
    Info "Re-checking PATH wiring..."
    if (-not $NoPath) { Add-ToUserPath $Prefix }
    Info "Up to date. Re-run with -Version to change, or -Uninstall to remove."
    return
}

if ($existing) {
    Info "Upgrading prompto v$existing -> v$semver..."
} else {
    Info "Installing prompto v$semver..."
}

$archiveName = "prompto_${semver}_windows_amd64.zip"
$archiveUrl  = "https://github.com/$Repo/releases/download/$tag/$archiveName"
$sumsUrl     = "https://github.com/$Repo/releases/download/$tag/checksums.txt"

$tmp = Join-Path $env:TEMP ("prompto-install-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null

try {
    Info "Downloading $archiveName..."
    $archivePath = Join-Path $tmp $archiveName
    Invoke-WebRequest -Uri $archiveUrl -OutFile $archivePath -UseBasicParsing

    Info "Downloading checksums.txt..."
    $sumsPath = Join-Path $tmp 'checksums.txt'
    Invoke-WebRequest -Uri $sumsUrl -OutFile $sumsPath -UseBasicParsing

    $expected = $null
    foreach ($line in Get-Content -LiteralPath $sumsPath) {
        # Format: "<sha256>  <filename>"
        if ($line -match "^([0-9a-fA-F]{64})\s+\Q$archiveName\E$" -or
            $line -match "^([0-9a-fA-F]{64})\s+$([regex]::Escape($archiveName))$") {
            $expected = $Matches[1].ToLower()
            break
        }
    }
    if (-not $expected) {
        # Fallback: looser match if the strict regex missed it.
        $row = Get-Content -LiteralPath $sumsPath | Where-Object { $_ -like "*$archiveName" } | Select-Object -First 1
        if ($row) { $expected = ($row -split '\s+')[0].ToLower() }
    }
    if (-not $expected) { Fail "no entry for $archiveName in checksums.txt" }

    $actual = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLower()
    if ($expected -ne $actual) {
        Fail "checksum mismatch for $archiveName (expected $expected, got $actual)"
    }
    Info "Checksum OK ($actual)."

    $extractDir = Join-Path $tmp 'extract'
    Expand-Archive -LiteralPath $archivePath -DestinationPath $extractDir -Force

    $extractedBin = Get-ChildItem -LiteralPath $extractDir -Recurse -Filter prompto.exe | Select-Object -First 1
    if (-not $extractedBin) { Fail "prompto.exe not found in archive" }

    if (-not (Test-Path -LiteralPath $Prefix)) {
        New-Item -ItemType Directory -Path $Prefix -Force | Out-Null
    }

    # Overwrite the binary even if it's currently in use by stopping any
    # running prompto process (best-effort, non-fatal).
    Get-Process -Name prompto -ErrorAction SilentlyContinue | ForEach-Object {
        try { $_.Kill(); $_.WaitForExit(2000) } catch {}
    }

    Copy-Item -LiteralPath $extractedBin.FullName -Destination $BinPath -Force
    Info "Installed $BinPath."
} finally {
    if (Test-Path -LiteralPath $tmp) {
        Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
}

if (-not $NoPath) { Add-ToUserPath $Prefix }

# ---------- config wizard ----------

$cfgDir  = Join-Path $env:AppData 'prompto'
$cfgFile = Join-Path $cfgDir 'config.json'

function Write-Config([string]$json) {
    if (-not (Test-Path -LiteralPath $cfgDir)) {
        New-Item -ItemType Directory -Path $cfgDir -Force | Out-Null
    }
    Set-Content -LiteralPath $cfgFile -Value $json -Encoding UTF8
    Info "Wrote $cfgFile."
}

function Invoke-ConfigWizard {
    if (Test-Path -LiteralPath $cfgFile) {
        Info "Existing config found at $cfgFile; leaving it untouched."
        return
    }
    if ($Yes) {
        Info "Skipping interactive config wizard (-Yes)."
        Info "Edit $cfgFile manually. See: https://github.com/$Repo/blob/main/docs/CONFIG.md"
        return
    }

    Write-Host ''
    Info "Let's set up prompto's model provider."
    Write-Host '  1) Cloud   (Anthropic, OpenAI, OpenRouter)'
    Write-Host '  2) Local   (LM Studio, llama.cpp, Ollama)'
    Write-Host '  3) Skip    (configure manually later)'
    $choice = Read-Host 'Choice [1-3, default 3]'
    switch ($choice) {
        '1' { Set-Cloud }
        '2' { Set-Local }
        default { Info "Skipped. Edit $cfgFile; see docs/CONFIG.md." }
    }
}

function Set-Cloud {
    Write-Host ''
    Write-Host '  1) Anthropic'
    Write-Host '  2) OpenAI'
    Write-Host '  3) OpenRouter'
    $p = Read-Host 'Provider [1-3]'

    $kind = ''; $name = ''; $apiEnv = ''; $model = ''
    switch ($p) {
        '1' { $kind='anthropic'; $name='anthropic';   $apiEnv='ANTHROPIC_API_KEY';  $model='claude-sonnet-4-6' }
        '2' { $kind='openai';    $name='openai';      $apiEnv='OPENAI_API_KEY';     $model='gpt-4o' }
        '3' { $kind='openai';    $name='openrouter';  $apiEnv='OPENROUTER_API_KEY'; $model='anthropic/claude-sonnet-4-6' }
        default { Info "Unrecognised choice; skipping."; return }
    }

    $secure = Read-Host -AsSecureString "API key for $name (blank = use `$$apiEnv at runtime)"
    $bstr = [System.Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
    try { $key = [System.Runtime.InteropServices.Marshal]::PtrToStringAuto($bstr) }
    finally { [System.Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr) }

    if ([string]::IsNullOrEmpty($key)) { $apiKeyValue = "`$$apiEnv" } else { $apiKeyValue = $key }

    $provider = [ordered]@{
        kind = $kind
        api_key = $apiKeyValue
        models = @(
            [ordered]@{
                name = $model
                max_tokens = 8192
            }
        )
    }
    if ($name -eq 'openrouter') {
        $provider = [ordered]@{
            kind = $kind
            base_url = 'https://openrouter.ai/api/v1'
            api_key = $apiKeyValue
            models = @(
                [ordered]@{
                    name = $model
                    max_tokens = 8192
                }
            )
        }
    }
    $config = [ordered]@{
        providers = [ordered]@{
            ($name) = $provider
        }
        default = [ordered]@{
            provider = $name
            model = $model
        }
    }
    Write-Config (ConvertTo-ConfigJson $config)
    if ([string]::IsNullOrEmpty($key)) {
        Info "Remember to set `$env:$apiEnv before running prompto."
    }
}

function Set-Local {
    Write-Host ''
    Write-Host '  1) LM Studio   (http://localhost:1234)'
    Write-Host '  2) llama.cpp   (http://localhost:8080)'
    Write-Host '  3) Ollama      (http://localhost:11434)'
    $s = Read-Host 'Local server [1-3]'

    $url = ''; $placeholder = ''; $default = ''
    switch ($s) {
        '1' { $url='http://localhost:1234';  $placeholder='lm-studio'; $default='qwen3-coder-30b' }
        '2' { $url='http://localhost:8080';  $placeholder='llamacpp';  $default='qwen-coder-30b' }
        '3' { $url='http://localhost:11434'; $placeholder='ollama';    $default='qwen2.5-coder:32b' }
        default { Info "Unrecognised choice; skipping."; return }
    }

    $model = Read-Host "Model name [$default]"
    if ([string]::IsNullOrEmpty($model)) { $model = $default }

    $config = [ordered]@{
        providers = [ordered]@{
            local = [ordered]@{
                kind = 'openai'
                base_url = $url
                api_key = $placeholder
                local_provider = $true
                max_parallel = 1
                models = @(
                    [ordered]@{
                        name = $model
                        max_tokens = 16384
                        temperature = 0.7
                    }
                )
            }
        }
        default = [ordered]@{
            provider = 'local'
            model = $model
        }
    }
    Write-Config (ConvertTo-ConfigJson $config)
    Info "Make sure your local server is running at $url before launching prompto."
}

if (-not $NoConfig) { Invoke-ConfigWizard }

Write-Host ''
Info "Done. Open a new terminal, then run:  prompto"
