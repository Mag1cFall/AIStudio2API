#!/bin/bash

echo "==================================================="
echo "      AI Studio Proxy API - 一键安装脚本 (Linux/macOS)"
echo "==================================================="
echo ""

# 1. 检查 Python
if ! command -v python3 &> /dev/null; then
    echo "[ERROR] 未检测到 Python3，请先安装 Python 3.9+。"
    exit 1
fi
echo "[OK] Python 环境已检测。"

# 2. 检查并安装 uv
if ! command -v uv &> /dev/null; then
    echo "[INFO] 正在安装 uv 包管理器..."
    curl -LsSf https://astral.sh/uv/install.sh | sh
    
    # 尝试加载 uv 到环境变量
    source $HOME/.cargo/env 2>/dev/null || source $HOME/.local/bin/env 2>/dev/null
    export PATH="$HOME/.local/bin:$PATH"
    
    if ! command -v uv &> /dev/null; then
        echo "[ERROR] uv 安装成功但不在 PATH 中。请运行 'source $HOME/.local/bin/env' 或重启终端。"
        exit 1
    fi
fi
echo "[OK] uv 包管理器已就绪。"

# 3. 安装依赖
echo ""
echo "[INFO] 正在同步 Python 依赖..."
uv sync
if [ $? -ne 0 ]; then
    echo "[ERROR] 依赖安装失败。"
    exit 1
fi

# 4. 下载浏览器
echo ""
echo "[INFO] 正在下载 Camoufox 浏览器核心..."
uv run camoufox fetch
if [ $? -ne 0 ]; then
    echo "[WARNING] 浏览器下载似乎遇到问题。"
    echo "您可以稍后尝试运行 'uv run python scripts/fetch_camoufox_data.py'"
else
    echo "[OK] 浏览器下载完成。"
fi

# 5. 设置执行权限
chmod +x start_webui.bat start_cmd.bat

echo ""
echo "==================================================="
echo "      安装完成！"
echo "==================================================="
echo ""
echo "启动方式:"
echo "1. 图形界面: PYTHONPATH=src uv run python src/app_launcher.py"
echo "2. 命令行版: PYTHONPATH=src uv run python src/launch_camoufox.py --headless"
echo ""