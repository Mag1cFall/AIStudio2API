@echo off
cd /d "%~dp0"

where git >nul 2>&1
if %errorlevel% neq 0 (
    echo Git not found, skipping update check.
    exit /b 0
)

echo Checking for updates...

git fetch origin main --quiet 2>nul
if %errorlevel% neq 0 (
    echo Could not reach GitHub, skipping update check.
    exit /b 0
)

for /f %%i in ('git rev-parse HEAD') do set LOCAL=%%i
for /f %%i in ('git rev-parse origin/main') do set REMOTE=%%i

if "%LOCAL%"=="%REMOTE%" (
    echo Already up to date.
    exit /b 0
)

echo Update available! Applying...
git pull origin main --quiet
if %errorlevel% neq 0 (
    echo Update failed. Please run 'git pull' manually.
    exit /b 1
)

echo Running uv sync...
uv sync --quiet
if %errorlevel% neq 0 (
    echo Dependency update failed.
    exit /b 1
)

echo Update complete!
exit /b 0
