@echo off
cd /d "%~dp0"

echo Starting AI Studio Proxy Manager...

REM Use uv to run the launcher to ensure all dependencies (uvicorn, fastapi, etc.) are available.
call uv run python app_launcher.py

if %errorlevel% neq 0 (
    echo.
    echo Error occurred.
    echo Please make sure you have installed dependencies with 'uv sync'.
    pause
)