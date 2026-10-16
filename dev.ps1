param([ValidateSet('run','test','build')][string]$Task = 'run')
$ErrorActionPreference = 'Stop'
if (-not $env:JAVA_HOME -or -not (Test-Path "$env:JAVA_HOME\bin\java.exe")) {
    $jdk = Get-ChildItem "$env:USERPROFILE\.jdks" -Directory -ErrorAction SilentlyContinue | Where-Object { $_.Name -match '(ms-25|corretto-25|openjdk-25|jdk-21|temurin-21)' -and (Test-Path (Join-Path $_.FullName 'bin\java.exe')) } | Select-Object -First 1
    if ($jdk) { $env:JAVA_HOME = $jdk.FullName }
    elseif (-not (Get-Command java -ErrorAction SilentlyContinue)) { throw 'Install JDK 21 or 25 and set JAVA_HOME.' }
}
Push-Location $PSScriptRoot
try { & .\gradlew.bat $Task; if ($LASTEXITCODE -ne 0) { throw "Gradle failed: $LASTEXITCODE" } }
finally { Pop-Location }