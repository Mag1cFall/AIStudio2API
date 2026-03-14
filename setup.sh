#!/bin/bash

echo "==================================================="
echo "      AI Studio Proxy API - Setup (Linux/macOS)"
echo "==================================================="
echo ""

if ! command -v python3 &> /dev/null; then
    echo "[ERROR] Python3 not found. Please install Python 3.9+"
    exit 1
fi
echo "[OK] Python detected."

if ! command -v uv &> /dev/null; then
    echo "[INFO] Installing uv..."
    curl -LsSf https://astral.sh/uv/install.sh | sh
    
    source $HOME/.cargo/env 2>/dev/null || source $HOME/.local/bin/env 2>/dev/null
    export PATH="$HOME/.local/bin:$PATH"
    
    if ! command -v uv &> /dev/null; then
        echo "[ERROR] uv installed but not in PATH."
        echo "Run 'source \$HOME/.local/bin/env' or restart terminal."
        exit 1
    fi
fi
echo "[OK] uv ready."

echo ""
echo "[INFO] Installing dependencies..."
uv sync
if [ $? -ne 0 ]; then
    echo "[ERROR] Dependency installation failed."
    exit 1
fi

echo ""
echo "[INFO] Downloading Camoufox browser..."
uv run camoufox fetch
if [ $? -ne 0 ]; then
    echo "[WARNING] Browser download may have issues."
    echo "You can try later: uv run camoufox fetch"
else
    echo "[OK] Browser downloaded."
fi

chmod +x start_webui.bat start_cmd.bat 2>/dev/null

echo ""
echo "==================================================="
echo "      Setup Complete!"
echo "==================================================="
echo ""
echo "To start:"
echo "1. Web UI:  PYTHONPATH=src uv run python src/app_launcher.py"
echo "2. CLI:     PYTHONPATH=src uv run python src/launch_camoufox.py --headless"
echo ""
