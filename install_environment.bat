@echo off
setlocal

REM --- 检查 Poetry 是否已安装 ---
echo 正在检查 Poetry 是否已安装...
where poetry >nul 2>nul
if %errorlevel% == 0 (
    echo Poetry 已安装。
) else (
    echo Poetry 未安装，正在尝试安装...
    (
        powershell -NoProfile -ExecutionPolicy Bypass -Command "iex ((New-Object System.Net.WebClient).DownloadString('https://install.python-poetry.org'))"
    ) || (
        echo.
        echo 错误：通过 PowerShell 自动安装 Poetry 失败。
        echo 请访问 https://python-poetry.org/docs/#installation 手动安装 Poetry，
        echo 然后重新运行此脚本。
        exit /b 1
    )
    echo.
    echo 请注意：您可能需要重新启动终端才能使 'poetry' 命令生效。
    echo 如果后续步骤失败，请重启终端后再次运行此脚本。
    echo.
)

REM --- 使用 Poetry 安装项目依赖 ---
echo.
echo 正在使用 Poetry 安装项目依赖...
poetry install --no-root
if %errorlevel% neq 0 (
    echo.
    echo 错误：'poetry install' 命令执行失败。
    echo 请检查您的 Python 环境和 pyproject.toml 文件是否正确。
    exit /b 1
)

echo.
echo --- 环境安装成功！ ---
echo 您现在可以运行 start.bat 或 start_headless.bat 来启动应用。
echo.
pause
endlocal