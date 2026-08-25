@echo off
chcp 65001 >nul
setlocal
pushd "%~dp0"

set "APP=%CD%\aistudio2api.exe"
set "EXIT_CODE=0"

if exist "%APP%" goto ready

where node >nul 2>nul
if errorlevel 1 goto missing_tools
where npm >nul 2>nul
if errorlevel 1 goto missing_tools
where go >nul 2>nul
if errorlevel 1 goto missing_tools

if not exist "web\package-lock.json" goto missing_source
pushd "web"
call npm ci
if errorlevel 1 goto frontend_failed
call npm run build
if errorlevel 1 goto frontend_failed
popd

go build -o "%APP%" ./cmd/aistudio2api
if errorlevel 1 goto failed

:ready
if not defined CAMOUFOX_PATH if not exist "%CD%\runtime\camoufox\camoufox.exe" if not exist "%LOCALAPPDATA%\camoufox\camoufox\Cache\camoufox.exe" echo Camoufox will be downloaded automatically on first launch
"%APP%" %*
set "EXIT_CODE=%ERRORLEVEL%"
goto finished

:frontend_failed
set "EXIT_CODE=%ERRORLEVEL%"
popd
goto finished

:missing_tools
echo Source build requires Node.js, npm, and Go, or a Release executable
set "EXIT_CODE=1"
goto finished

:missing_source
echo Source build requires web\package-lock.json
set "EXIT_CODE=1"
goto finished

:failed
set "EXIT_CODE=%ERRORLEVEL%"
if "%EXIT_CODE%"=="0" set "EXIT_CODE=1"

:finished
popd
if not "%EXIT_CODE%"=="0" pause
exit /b %EXIT_CODE%
