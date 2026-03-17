@echo off
cd /d "%~dp0"

call auto_update.bat

set PYTHONPATH=%~dp0src;%PYTHONPATH%
uv run python src/launch_camoufox.py
pause
