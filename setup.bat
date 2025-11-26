@echo off
setlocal
chcp 65001 > nul

echo ===================================================
echo       AI Studio Proxy API - 一键安装脚本 (Windows)
echo ===================================================
echo.

REM 1. 检查 Python
python --version >nul 2>&1
if %errorlevel% neq 0 (
    echo [ERROR] 未检测到 Python，请先安装 Python 3.9+。
    echo 下载地址: https://www.python.org/downloads/
    pause
    exit /b 1
)
echo [OK] Python 环境已检测。

REM 2. 检查并安装 uv
set "UV_CMD=uv"
uv --version >nul 2>&1
if %errorlevel% neq 0 (
    echo [INFO] 正在安装 uv 包管理器...
    REM 添加 -ExecutionPolicy ByPass 以绕过 PowerShell 执行策略限制 (常见于 Win11)
    powershell -ExecutionPolicy ByPass -c "irm https://astral.sh/uv/install.ps1 | iex"
    
    REM 尝试定位安装路径并添加到当前 PATH (临时)
    set "UV_INSTALL_DIR=%USERPROFILE%\.local\bin"
    set "PATH=%USERPROFILE%\.local\bin;%PATH%"
    
    uv --version >nul 2>&1
    if %errorlevel% neq 0 (
        REM 如果直接调用 uv 仍然失败，尝试检查绝对路径
        if exist "%USERPROFILE%\.local\bin\uv.exe" (
            echo [INFO] 找到 uv 但未在 PATH 中生效，将使用绝对路径继续...
            set "UV_CMD=%USERPROFILE%\.local\bin\uv.exe"
        ) else (
            echo [ERROR] uv 安装失败或未添加到 PATH。
            echo 请尝试重启终端后再次运行此脚本。
            pause
            exit /b 1
        )
    )
)
echo [OK] uv 包管理器已就绪。

REM 3. 安装依赖
echo.
echo [INFO] 正在同步 Python 依赖...
call "%UV_CMD%" sync
if %errorlevel% neq 0 (
    echo [ERROR] 依赖安装失败。
    pause
    exit /b 1
)

REM 4. 下载浏览器
echo.
echo [INFO] 正在下载 Camoufox 浏览器核心...
call "%UV_CMD%" run camoufox fetch
if %errorlevel% neq 0 (
    echo [WARNING] 浏览器下载似乎遇到问题。
    echo 您可以稍后尝试运行 'uv run python fetch_camoufox_data.py'
) else (
    echo [OK] 浏览器下载完成。
)

echo.
echo ===================================================
echo       安装完成！
echo ===================================================
echo.
echo 启动方式:
echo 1. 双击 'start_webui.bat' 启动图形管理界面 (推荐)
echo 2. 双击 'start_cmd.bat' 启动命令行版本
echo.
pause