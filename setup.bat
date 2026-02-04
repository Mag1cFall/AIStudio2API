@echo off
setlocal

echo ===================================================
echo       AI Studio Proxy API - Setup (Windows)
echo ===================================================
echo.

python --version >nul 2>&1
if %errorlevel% neq 0 (
    echo [ERROR] Python not found. Please install Python 3.9+
    echo https://www.python.org/downloads/
    pause
    exit /b 1
)
echo [OK] Python detected.

set "UV_CMD=uv"
uv --version >nul 2>&1
if %errorlevel% neq 0 (
    echo [INFO] Installing uv...
    powershell -ExecutionPolicy ByPass -c "irm https://astral.sh/uv/install.ps1 | iex"
    
    set "PATH=%USERPROFILE%\.local\bin;%PATH%"
    
    uv --version >nul 2>&1
    if %errorlevel% neq 0 (
        if exist "%USERPROFILE%\.local\bin\uv.exe" (
            echo [INFO] Using uv from absolute path...
            set "UV_CMD=%USERPROFILE%\.local\bin\uv.exe"
        ) else (
            echo [ERROR] uv installation failed.
            echo Please restart terminal and try again.
            pause
            exit /b 1
        )
    )
)
echo [OK] uv ready.

echo.
echo [INFO] Installing dependencies...
call "%UV_CMD%" sync
if %errorlevel% neq 0 (
    echo [ERROR] Dependency installation failed.
    pause
    exit /b 1
)

echo.
echo [INFO] Downloading Camoufox browser...
call "%UV_CMD%" run camoufox fetch
if %errorlevel% neq 0 (
    echo [WARNING] Browser download may have issues.
    echo You can try later: uv run camoufox fetch
) else (
    echo [OK] Browser downloaded.
)

echo.
echo ===================================================
echo       Setup Complete!
echo ===================================================
echo.
echo To start:
echo 1. Double-click 'start_webui.bat' for Web UI
echo 2. Double-click 'start_cmd.bat' for CLI
echo.
pause
