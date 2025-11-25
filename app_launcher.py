import os
import sys
import time
import subprocess
import threading
import uvicorn
import webbrowser
from manager import app

# Configuration
PORT = 9000
HOST = "127.0.0.1"
DASHBOARD_URL = f"http://{HOST}:{PORT}"

def open_dashboard_window():
    """
    Attempts to open the dashboard in a dedicated 'App' window using Edge or Chrome.
    Falls back to default system browser.
    """
    time.sleep(2) # Give server a moment to start
    print(f"Opening dashboard at {DASHBOARD_URL}")

    if sys.platform == 'win32':
        # Try to find standard paths for Edge and Chrome
        paths = [
            r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe",
            r"C:\Program Files\Microsoft\Edge\Application\msedge.exe",
            r"C:\Program Files\Google\Chrome\Application\chrome.exe",
            r"C:\Program Files (x86)\Google\Chrome\Application\chrome.exe",
        ]
        
        for path in paths:
            if os.path.exists(path):
                try:
                    subprocess.Popen([path, f'--app={DASHBOARD_URL}'])
                    print(f"Launched App Mode using: {path}")
                    return
                except Exception as e:
                    print(f"Failed to launch {path}: {e}")
        
    # Fallback to standard browser opening
    print("Browser executable for App Mode not found, opening in default browser...")
    webbrowser.open(DASHBOARD_URL)

def main():
    print("--- AI Studio Proxy Manager ---")
    print(f"Starting Manager Backend on {DASHBOARD_URL}...")
    
    # Launch browser in a separate thread
    threading.Thread(target=open_dashboard_window, daemon=True).start()
    
    # Run server
    try:
        uvicorn.run(app, host=HOST, port=PORT, log_level="error")
    except KeyboardInterrupt:
        print("\nStopping...")
    except Exception as e:
        print(f"\nError starting server: {e}")
        # input("Press Enter to exit...") # CLI模式下通常不阻塞退出

if __name__ == '__main__':
    main()
    input("Press Enter to exit...")