@echo off
cd /d "%~dp0"

echo Starting AI Studio Proxy Manager...

set PYTHONPATH=%~dp0src;%PYTHONPATH%
call uv run python src/app_launcher.py

if %errorlevel% neq 0 (
    echo.
    echo Error occurred.
    echo Please make sure you have installed dependencies with 'uv sync'.
    pause
)