#!/usr/bin/env python3
import re
import tkinter as tk
from tkinter import ttk, messagebox, simpledialog, scrolledtext
import subprocess
import os
import sys
import platform
import threading
import time
import socket
import signal
from typing import List, Dict, Any, Optional, Tuple
from urllib.parse import urlparse
import shlex
import logging
import json
import requests # 新增导入
from dotenv import load_dotenv

# 加载 .env 文件
load_dotenv()

# --- Configuration & Globals ---
PYTHON_EXECUTABLE = sys.executable
SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
LAUNCH_CAMOUFOX_PY = os.path.join(SCRIPT_DIR, "launch_camoufox.py")
SERVER_PY_FILENAME = "server.py" # For context

AUTH_PROFILES_DIR = os.path.join(SCRIPT_DIR, "auth_profiles") # 确保这些目录存在
ACTIVE_AUTH_DIR = os.path.join(AUTH_PROFILES_DIR, "active")
SAVED_AUTH_DIR = os.path.join(AUTH_PROFILES_DIR, "saved")

DEFAULT_FASTAPI_PORT = int(os.environ.get('DEFAULT_FASTAPI_PORT', '2048'))
DEFAULT_CAMOUFOX_PORT_GUI = int(os.environ.get('DEFAULT_CAMOUFOX_PORT', '9222'))  # 与 launch_camoufox.py 中的 DEFAULT_CAMOUFOX_PORT 一致

managed_process_info: Dict[str, Any] = {
    "popen": None,
    "service_name_key": None,
    "monitor_thread": None,
    "stdout_thread": None,
    "stderr_thread": None,
    "output_area": None,
    "fully_detached": False # 新增：标记进程是否完全独立
}

# 添加按钮防抖机制
button_debounce_info: Dict[str, float] = {}

def debounce_button(func_name: str, delay_seconds: float = 2.0):
    """
    按钮防抖装饰器，防止在指定时间内重复执行同一函数
    """
    def decorator(func):
        def wrapper(*args, **kwargs):
            import time
            current_time = time.time()
            last_call_time = button_debounce_info.get(func_name, 0)

            if current_time - last_call_time < delay_seconds:
                logger.info(f"按钮防抖：忽略 {func_name} 的重复调用")
                return

            button_debounce_info[func_name] = current_time
            return func(*args, **kwargs)
        return wrapper
    return decorator

# 添加全局logger定义
logger = logging.getLogger("GUILauncher")
logger.setLevel(logging.INFO)
console_handler = logging.StreamHandler()
console_handler.setFormatter(logging.Formatter('%(asctime)s - %(name)s - %(levelname)s - %(message)s'))
logger.addHandler(console_handler)
os.makedirs(os.path.join(SCRIPT_DIR, "logs"), exist_ok=True)
file_handler = logging.FileHandler(os.path.join(SCRIPT_DIR, "logs", "gui_launcher.log"), encoding='utf-8')
file_handler.setFormatter(logging.Formatter('%(asctime)s - %(name)s - %(levelname)s - %(message)s'))
logger.addHandler(file_handler)

# 在LANG_TEXTS声明之前定义长文本
service_closing_guide_message_zh = """由于服务在独立终端中运行，您可以通过以下方式关闭服务：

1. 使用端口管理功能:
   - 点击"查询端口进程"按钮
   - 选择相关的Python进程
   - 点击"停止选中进程"

2. 手动终止进程:
   - Windows: 使用任务管理器
   - macOS: 使用活动监视器或terminal
   - Linux: 使用kill命令

3. 直接关闭服务运行的终端窗口"""

service_closing_guide_message_en = """Since the service runs in an independent terminal, you can close it using these methods:

1. Using port management in GUI:
   - Click "Query Port Processes" button
   - Select the relevant Python process
   - Click "Stop Selected Process"

2. Manually terminate process:
   - Windows: Use Task Manager
   - macOS: Use Activity Monitor or terminal
   - Linux: Use kill command

3. Directly close the terminal window running the service"""

# --- Internationalization (i18n) ---
LANG_TEXTS = {
    "title": {"zh": "AI Studio Proxy API Launcher GUI", "en": "AI Studio Proxy API Launcher GUI"},
    "status_idle": {"zh": "空闲，请选择操作。", "en": "Idle. Select an action."},
    "port_section_label": {"zh": "服务端口配置", "en": "Service Port Configuration"},
    "port_input_description_lbl": {"zh": "提示: 启动时将使用下方指定的FastAPI服务端口和Camoufox调试端口。", "en": "Note: The FastAPI service port and Camoufox debug port specified below will be used for launch."},
    "fastapi_port_label": {"zh": "FastAPI 服务端口:", "en": "FastAPI Port:"},
    "camoufox_debug_port_label": {"zh": "Camoufox 调试端口:", "en": "Camoufox Debug Port:"},
    "query_pids_btn": {"zh": "查询端口进程", "en": "Query Port Processes"},
    "stop_selected_pid_btn": {"zh": "停止选中进程", "en": "Stop Selected Process"},
    "pids_on_port_label": {"zh": "端口占用情况 (PID - 名称):", "en": "Processes on Port (PID - Name):"}, # Static version for initialization
    "pids_on_port_label_dynamic": {"zh": "端口 {port} 占用情况 (PID - 名称):", "en": "Processes on Port {port} (PID - Name):"}, # Dynamic version
    "no_pids_found": {"zh": "未找到占用该端口的进程。", "en": "No processes found on this port."},
    "static_pid_list_title": {"zh": "启动所需端口占用情况 (PID - 名称)", "en": "Required Ports Usage (PID - Name)"}, # 新增标题
    "launch_options_label": {"zh": "启动选项", "en": "Launch Options"},
    "launch_options_note_revised": {"zh": "提示：有头/无头模式均会在新的独立终端窗口中启动服务。\n有头模式用于调试和认证。无头模式需预先认证。\n关闭此GUI不会停止已独立启动的服务。",
                                    "en": "Tip: Headed/Headless modes will launch the service in a new independent terminal window.\nHeaded mode is for debug and auth. Headless mode requires pre-auth.\nClosing this GUI will NOT stop independently launched services."},
    "launch_headed_interactive_btn": {"zh": "启动有头模式 (新终端)", "en": "Launch Headed Mode (New Terminal)"},
    "launch_headless_btn": {"zh": "启动无头模式 (新终端)", "en": "Launch Headless Mode (New Terminal)"},
    "launch_virtual_display_btn": {"zh": "启动虚拟显示模式 (Linux)", "en": "Launch Virtual Display (Linux)"},
    "stop_gui_service_btn": {"zh": "停止当前GUI管理的服务", "en": "Stop Current GUI-Managed Service"},
    "status_label": {"zh": "状态", "en": "Status"},
    "output_label": {"zh": "输出日志", "en": "Output Log"},
    "menu_language_fixed": {"zh": "Language", "en": "Language"},
    "menu_lang_zh_option": {"zh": "中文 (Chinese)", "en": "中文 (Chinese)"},
    "menu_lang_en_option": {"zh": "英文 (English)", "en": "英文 (English)"},
    "confirm_quit_title": {"zh": "确认退出", "en": "Confirm Quit"},
    "confirm_quit_message": {"zh": "服务可能仍在独立终端中运行。确认退出GUI吗?", "en": "Services may still be running in independent terminals. Confirm quit GUI?"},
    "confirm_quit_message_independent": {"zh": "独立后台服务 '{service_name}' 可能仍在运行。直接退出GUI吗 (服务将继续运行)?", "en": "Independent background service '{service_name}' may still be running. Quit GUI (service will continue to run)?"},
    "error_title": {"zh": "错误", "en": "Error"},
    "info_title": {"zh": "信息", "en": "Info"},
    "warning_title": {"zh": "警告", "en": "Warning"},
    "service_already_running": {"zh": "服务 ({service_name}) 已在运行。", "en": "A service ({service_name}) is already running."},
    "proxy_config_title": {"zh": "代理配置", "en": "Proxy Configuration"},
    "proxy_config_message_generic": {"zh": "是否为此启动启用 HTTP/HTTPS 代理?", "en": "Enable HTTP/HTTPS proxy for this launch?"},
    "proxy_address_title": {"zh": "代理地址", "en": "Proxy Address"},
    "proxy_address_prompt": {"zh": "输入代理地址 (例如 http://host:port)\n默认: {default_proxy}", "en": "Enter proxy address (e.g., http://host:port)\nDefault: {default_proxy}"},
    "proxy_configured_status": {"zh": "代理已配置: {proxy_addr}", "en": "Proxy configured: {proxy_addr}"},
    "proxy_skip_status": {"zh": "用户跳过代理设置。", "en": "Proxy setup skipped by user."},
    "script_not_found_error_msgbox": {"zh": "启动失败: 未找到 Python 执行文件或脚本。\n命令: {cmd}", "en": "Failed to start: Python executable or script not found.\nCommand: {cmd}"},
    "startup_error_title": {"zh": "启动错误", "en": "Startup Error"},
    "startup_script_not_found_msgbox": {"zh": "必需的脚本 '{script}' 在当前目录未找到。\n请将此GUI启动器与 launch_camoufox.py 和 server.py 放在同一目录。", "en": "Required script '{script}' not found in the current directory.\nPlace this GUI launcher in the same directory as launch_camoufox.py and server.py."},
    "service_starting_status": {"zh": "{service_name} 启动中... PID: {pid}", "en": "{service_name} starting... PID: {pid}"},
    "service_stopped_gracefully_status": {"zh": "{service_name} 已平稳停止。", "en": "{service_name} stopped gracefully."},
    "service_stopped_exit_code_status": {"zh": "{service_name} 已停止。退出码: {code}", "en": "{service_name} stopped. Exit code: {code}"},
    "service_stop_fail_status": {"zh": "{service_name} (PID: {pid}) 未能平稳终止。正在强制停止...", "en": "{service_name} (PID: {pid}) did not terminate gracefully. Forcing kill..."},
    "service_killed_status": {"zh": "{service_name} (PID: {pid}) 已被强制停止。", "en": "{service_name} (PID: {pid}) killed."},
    "error_stopping_service_msgbox": {"zh": "停止 {service_name} (PID: {pid}) 时出错: {e}", "en": "Error stopping {service_name} (PID: {pid}): {e}"},
    "no_service_running_status": {"zh": "当前没有GUI管理的服务在运行。", "en": "No GUI-managed service is currently running."},
    "stopping_initiated_status": {"zh": "{service_name} (PID: {pid}) 停止已启动。最终状态待定。", "en": "{service_name} (PID: {pid}) stopping initiated. Final status pending."},
    "service_name_headed_interactive": {"zh": "有头交互服务", "en": "Headed Interactive Service"},
    "service_name_headless": {"zh": "无头服务", "en": "Headless Service"}, # Key 修改
    "service_name_virtual_display": {"zh": "虚拟显示无头服务", "en": "Virtual Display Headless Service"},
    "status_headed_launch": {"zh": "有头模式：启动中，请关注新控制台的提示...", "en": "Headed Mode: Launching, check new console for prompts..."},
    "status_headless_launch": {"zh": "无头服务：启动中...新的独立终端将打开。", "en": "Headless Service: Launching... A new independent terminal will open."},
    "status_virtual_display_launch": {"zh": "虚拟显示模式启动中...", "en": "Virtual Display Mode launching..."},
    "info_service_is_independent": {"zh": "当前服务为独立后台进程，关闭GUI不会停止它。请使用系统工具或端口管理手动停止此服务。", "en": "The current service is an independent background process. Closing the GUI will not stop it. Please manage this service manually using system tools or port management."},
    "info_service_new_terminal": {"zh": "服务已在新的独立终端启动。关闭此GUI不会影响该服务。", "en": "Service has been launched in a new independent terminal. Closing this GUI will not affect the service."},
    "warn_cannot_stop_independent_service": {"zh": "通过此GUI启动的服务在独立终端中运行，无法通过此按钮停止。请直接管理其终端或使用系统工具。", "en": "Services launched via this GUI run in independent terminals and cannot be stopped by this button. Please manage their terminals directly or use system tools."},
    "enter_valid_port_warn": {"zh": "请输入有效的端口号 (1024-65535)。", "en": "Please enter a valid port number (1024-65535)."},
    "pid_list_empty_for_stop_warn": {"zh": "进程列表为空或未选择进程。", "en": "PID list is empty or no process selected."},
    "confirm_stop_pid_title": {"zh": "确认停止进程", "en": "Confirm Stop Process"},
    "confirm_stop_pid_message": {"zh": "确定要尝试停止 PID {pid} ({name}) 吗?", "en": "Are you sure you want to attempt to stop PID {pid} ({name})?"},
    "confirm_stop_pid_admin_title": {"zh": "以管理员权限停止进程", "en": "Stop Process with Admin Privileges"},
    "confirm_stop_pid_admin_message": {"zh": "以普通权限停止 PID {pid} ({name}) 可能失败。是否尝试使用管理员权限停止?", "en": "Stopping PID {pid} ({name}) with normal privileges may fail. Try with admin privileges?"},
    "admin_stop_success": {"zh": "已成功使用管理员权限停止 PID {pid}", "en": "Successfully stopped PID {pid} with admin privileges"},
    "admin_stop_failure": {"zh": "使用管理员权限停止 PID {pid} 失败: {error}", "en": "Failed to stop PID {pid} with admin privileges: {error}"},
    "status_error_starting": {"zh": "启动 {service_name} 失败。", "en": "Error starting {service_name}"},
    "status_script_not_found": {"zh": "错误: 未找到 {service_name} 的可执行文件/脚本。", "en": "Error: Executable/script not found for {service_name}."},
    "error_getting_process_name": {"zh": "获取 PID {pid} 的进程名失败。", "en": "Failed to get process name for PID {pid}."},
    "pid_info_format": {"zh": "PID: {pid} (端口: {port}) - 名称: {name}", "en": "PID: {pid} (Port: {port}) - Name: {name}"},
    "status_stopping_service": {"zh": "正在停止 {service_name} (PID: {pid})...", "en": "Stopping {service_name} (PID: {pid})..."},
    "error_title_invalid_selection": {"zh": "无效的选择格式: {selection}", "en": "Invalid selection format: {selection}"},
    "error_parsing_pid": {"zh": "无法从 '{selection}' 解析PID。", "en": "Could not parse PID from '{selection}'."},
    "terminate_request_sent": {"zh": "终止请求已发送。", "en": "Termination request sent."},
    "terminate_attempt_failed": {"zh": "尝试终止 PID {pid} ({name}) 可能失败。", "en": "Attempt to terminate PID {pid} ({name}) may have failed."},
    "unknown_process_name_placeholder": {"zh": "未知进程名", "en": "Unknown Process Name"},
    "kill_custom_pid_label": {"zh": "或输入PID终止:", "en": "Or Enter PID to Kill:"},
    "kill_custom_pid_btn": {"zh": "终止指定PID", "en": "Kill Specified PID"},
    "pid_input_empty_warn": {"zh": "请输入要终止的PID。", "en": "Please enter a PID to kill."},
    "pid_input_invalid_warn": {"zh": "输入的PID无效，请输入纯数字。", "en": "Invalid PID entered. Please enter numbers only."},
    "confirm_kill_custom_pid_title": {"zh": "确认终止PID", "en": "Confirm Kill PID"},
    "status_sending_sigint": {"zh": "正在向 {service_name} (PID: {pid}) 发送 SIGINT...", "en": "Sending SIGINT to {service_name} (PID: {pid})..."},
    "status_waiting_after_sigint": {"zh": "{service_name} (PID: {pid})：SIGINT 已发送，等待 {timeout} 秒优雅退出...", "en": "{service_name} (PID: {pid}): SIGINT sent, waiting {timeout}s for graceful exit..."},
    "status_sigint_effective": {"zh": "{service_name} (PID: {pid}) 已响应 SIGINT 并停止。", "en": "{service_name} (PID: {pid}) responded to SIGINT and stopped."},
    "status_sending_sigterm": {"zh": "{service_name} (PID: {pid})：未在规定时间内响应 SIGINT，正在发送 SIGTERM...", "en": "{service_name} (PID: {pid}): Did not respond to SIGINT in time, sending SIGTERM..."},
    "status_waiting_after_sigterm": {"zh": "{service_name} (PID: {pid})：SIGTERM 已发送，等待 {timeout} 秒优雅退出...", "en": "{service_name} (PID: {pid}): SIGTERM sent, waiting {timeout}s for graceful exit..."},
    "status_sigterm_effective": {"zh": "{service_name} (PID: {pid}) 已响应 SIGTERM 并停止。", "en": "{service_name} (PID: {pid}) responded to SIGTERM and stopped."},
    "status_forcing_kill": {"zh": "{service_name} (PID: {pid})：未在规定时间内响应 SIGTERM，正在强制终止 (SIGKILL)...", "en": "{service_name} (PID: {pid}): Did not respond to SIGTERM in time, forcing kill (SIGKILL)..."},
    "enable_stream_proxy_label": {"zh": "启用流式代理服务", "en": "Enable Stream Proxy Service"},
    "stream_proxy_port_label": {"zh": "流式代理端口:", "en": "Stream Proxy Port:"},
    "enable_helper_label": {"zh": "启用外部Helper服务", "en": "Enable External Helper Service"},
    "helper_endpoint_label": {"zh": "Helper端点URL:", "en": "Helper Endpoint URL:"},
    "auth_manager_title": {"zh": "认证文件管理", "en": "Authentication File Manager"},
    "saved_auth_files_label": {"zh": "已保存的认证文件:", "en": "Saved Authentication Files:"},
    "no_file_selected": {"zh": "请选择一个认证文件", "en": "Please select an authentication file"},
    "auth_file_activated": {"zh": "认证文件 '{file}' 已成功激活", "en": "Authentication file '{file}' has been activated successfully"},
    "error_activating_file": {"zh": "激活文件 '{file}' 时出错: {error}", "en": "Error activating file '{file}': {error}"},
    "auth_deactivated": {"zh": "已取消激活所有认证", "en": "All authentications deactivated"},
    "error_deactivating": {"zh": "取消激活时出错: {error}", "en": "Error during deactivation: {error}"},
    "activate_selected_btn": {"zh": "激活选中的文件", "en": "Activate Selected File"},
    "deactivate_btn": {"zh": "取消激活", "en": "Deactivate"},
    "cancel_btn": {"zh": "取消", "en": "Cancel"},
    "auth_files_management": {"zh": "认证文件管理", "en": "Auth Files Management"},
    "manage_auth_files_btn": {"zh": "管理认证文件", "en": "Manage Auth Files"},
    "no_saved_auth_files": {"zh": "保存目录中没有认证文件", "en": "No authentication files in saved directory"},
    "auth_dirs_missing": {"zh": "认证目录不存在，请确保目录结构正确", "en": "Authentication directories missing, please ensure correct directory structure"},
    "confirm_kill_port_title": {"zh": "确认清理端口", "en": "Confirm Port Cleanup"},
    "confirm_kill_port_message": {"zh": "端口 {port} 被以下PID占用: {pids}。是否尝试终止这些进程?", "en": "Port {port} is in use by PID(s): {pids}. Try to terminate them?"},
    "port_cleared_success": {"zh": "端口 {port} 已成功清理", "en": "Port {port} has been cleared successfully"},
    "port_still_in_use": {"zh": "端口 {port} 仍被占用，请手动处理", "en": "Port {port} is still in use, please handle manually"},
    "port_in_use_no_pids": {"zh": "端口 {port} 被占用，但无法识别进程", "en": "Port {port} is in use, but processes cannot be identified"},
    "error_removing_file": {"zh": "删除文件 '{file}' 时出错: {error}", "en": "Error removing file '{file}': {error}"},
    "stream_port_out_of_range": {"zh": "流式代理端口必须为0(禁用)或1024-65535之间的值", "en": "Stream proxy port must be 0 (disabled) or a value between 1024-65535"},
    "port_auto_check": {"zh": "启动前自动检查端口", "en": "Auto-check port before launch"},
    "auto_port_check_enabled": {"zh": "已启用端口自动检查", "en": "Port auto-check enabled"},
    "port_check_running": {"zh": "正在检查端口 {port}...", "en": "Checking port {port}..."},
    "port_name_fastapi": {"zh": "FastAPI服务", "en": "FastAPI Service"},
    "port_name_camoufox_debug": {"zh": "Camoufox调试", "en": "Camoufox Debug"},
    "port_name_stream_proxy": {"zh": "流式代理", "en": "Stream Proxy"},
    "checking_port_with_name": {"zh": "正在检查{port_name}端口 {port}...", "en": "Checking {port_name} port {port}..."},
    "port_check_all_completed": {"zh": "所有端口检查完成", "en": "All port checks completed"},
    "port_check_failed": {"zh": "{port_name}端口 {port} 检查失败，启动已中止", "en": "{port_name} port {port} check failed, launch aborted"},
    "port_name_helper_service": {"zh": "Helper服务", "en": "Helper Service"},
    "confirm_kill_multiple_ports_title": {"zh": "确认清理多个端口", "en": "Confirm Multiple Ports Cleanup"},
    "confirm_kill_multiple_ports_message": {"zh": "以下端口被占用:\n{occupied_ports_details}\n是否尝试终止这些进程?", "en": "The following ports are in use:\n{occupied_ports_details}\nAttempt to terminate these processes?"},
    "all_ports_cleared_success": {"zh": "所有选定端口已成功清理。", "en": "All selected ports have been cleared successfully."},
    "some_ports_still_in_use": {"zh": "部分端口在清理后仍被占用，请手动处理。启动已中止。", "en": "Some ports are still in use after cleanup attempt. Please handle manually. Launch aborted."},
    "port_check_user_declined_cleanup": {"zh": "用户选择不清理占用的端口，启动已中止。", "en": "User chose not to clean up occupied ports. Launch aborted."},
    "reset_button": {"zh": "重置为默认设置", "en": "Reset to Defaults"},
    "confirm_reset_title": {"zh": "确认重置", "en": "Confirm Reset"},
    "confirm_reset_message": {"zh": "确定要重置所有设置为默认值吗？", "en": "Are you sure you want to reset all settings to default values?"},
    "reset_success": {"zh": "已重置为默认设置", "en": "Reset to default settings successfully"},
    "proxy_config_last_used": {"zh": "使用上次的代理: {proxy}", "en": "Using last proxy: {proxy}"},
    "proxy_config_other": {"zh": "使用其他代理地址", "en": "Use a different proxy address"},
    "service_closing_guide": {"zh": "关闭服务指南", "en": "Service Closing Guide"},
    "service_closing_guide_btn": {"zh": "如何关闭服务?", "en": "How to Close Service?"},
    "service_closing_guide_message": {"zh": service_closing_guide_message_zh, "en": service_closing_guide_message_en},
    "enable_proxy_label": {"zh": "启用浏览器代理", "en": "Enable Browser Proxy"},
    "proxy_address_label": {"zh": "代理地址:", "en": "Proxy Address:"},
    "current_auth_file_display_label": {"zh": "当前认证: ", "en": "Current Auth: "},
    "current_auth_file_none": {"zh": "无", "en": "None"},
    "current_auth_file_selected_format": {"zh": "{file}", "en": "{file}"},
    "test_proxy_btn": {"zh": "测试", "en": "Test"},
    "proxy_section_label": {"zh": "代理配置", "en": "Proxy Configuration"},
    "proxy_test_url_default": "http://httpbin.org/get", # 默认测试URL
    "proxy_test_url_backup": "http://www.google.com", # 备用测试URL
    "proxy_not_enabled_warn": {"zh": "代理未启用或地址为空，请先配置。", "en": "Proxy not enabled or address is empty. Please configure first."},
    "proxy_test_success": {"zh": "代理连接成功 ({url})", "en": "Proxy connection successful ({url})"},
    "proxy_test_failure": {"zh": "代理连接失败 ({url}):\n{error}", "en": "Proxy connection failed ({url}):\n{error}"},
    "proxy_testing_status": {"zh": "正在测试代理 {proxy_addr}...", "en": "Testing proxy {proxy_addr}..."},
    "proxy_test_success_status": {"zh": "代理测试成功 ({url})", "en": "Proxy test successful ({url})"},
    "proxy_test_failure_status": {"zh": "代理测试失败: {error}", "en": "Proxy test failed: {error}"},
    "proxy_test_retrying": {"zh": "代理测试失败，正在重试 ({attempt}/{max_attempts})...", "en": "Proxy test failed, retrying ({attempt}/{max_attempts})..."},
    "proxy_test_backup_url": {"zh": "主测试URL失败，尝试备用URL...", "en": "Primary test URL failed, trying backup URL..."},
    "proxy_test_all_failed": {"zh": "所有代理测试尝试均失败", "en": "All proxy test attempts failed"},
    "querying_ports_status": {"zh": "正在查询端口: {ports_desc}...", "en": "Querying ports: {ports_desc}..."},
    "port_query_result_format": {"zh": "[{port_type} - {port_num}] {pid_info}", "en": "[{port_type} - {port_num}] {pid_info}"},
    "port_not_in_use_format": {"zh": "[{port_type} - {port_num}] 未被占用", "en": "[{port_type} - {port_num}] Not in use"},
    "pids_on_multiple_ports_label": {"zh": "多端口占用情况:", "en": "Multi-Port Usage:"},
    "launch_llm_service_btn": {"zh": "启动本地LLM模拟服务", "en": "Launch Local LLM Mock Service"},
    "stop_llm_service_btn": {"zh": "停止本地LLM模拟服务", "en": "Stop Local LLM Mock Service"},
    "llm_service_name_key": {"zh": "本地LLM模拟服务", "en": "Local LLM Mock Service"},
    "status_llm_starting": {"zh": "本地LLM模拟服务启动中 (PID: {pid})...", "en": "Local LLM Mock Service starting (PID: {pid})..."},
    "status_llm_stopped": {"zh": "本地LLM模拟服务已停止。", "en": "Local LLM Mock Service stopped."},
    "status_llm_stop_error": {"zh": "停止本地LLM模拟服务时出错。", "en": "Error stopping Local LLM Mock Service."},
    "status_llm_already_running": {"zh": "本地LLM模拟服务已在运行 (PID: {pid})。", "en": "Local LLM Mock Service is already running (PID: {pid})."},
    "status_llm_not_running": {"zh": "本地LLM模拟服务未在运行。", "en": "Local LLM Mock Service is not running."},
    "status_llm_backend_check": {"zh": "正在检查LLM后端服务 ...", "en": "Checking LLM backend service ..."},
    "status_llm_backend_ok_starting": {"zh": "LLM后端服务 (localhost:{port}) 正常，正在启动模拟服务...", "en": "LLM backend service (localhost:{port}) OK, starting mock service..."},
    "status_llm_backend_fail": {"zh": "LLM后端服务 (localhost:{port}) 未响应，无法启动模拟服务。", "en": "LLM backend service (localhost:{port}) not responding, cannot start mock service."},
    "confirm_stop_llm_title": {"zh": "确认停止LLM服务", "en": "Confirm Stop LLM Service"},
    "confirm_stop_llm_message": {"zh": "确定要停止本地LLM模拟服务吗?", "en": "Are you sure you want to stop the Local LLM Mock Service?"}
}

# 删除重复的定义
current_language = 'zh'
root_widget: Optional[tk.Tk] = None
process_status_text_var: Optional[tk.StringVar] = None
port_entry_var: Optional[tk.StringVar] = None # 将用于 FastAPI 端口
camoufox_debug_port_var: Optional[tk.StringVar] = None
pid_listbox_widget: Optional[tk.Listbox] = None
custom_pid_entry_var: Optional[tk.StringVar] = None
widgets_to_translate: List[Dict[str, Any]] = []
proxy_address_var: Optional[tk.StringVar] = None  # 添加变量存储代理地址
proxy_enabled_var: Optional[tk.BooleanVar] = None  # 添加变量标记代理是否启用
active_auth_file_display_var: Optional[tk.StringVar] = None # 用于显示当前认证文件

LLM_PY_FILENAME = "llm.py"
llm_service_process_info: Dict[str, Any] = {
    "popen": None,
    "monitor_thread": None,
    "stdout_thread": None,
    "stderr_thread": None,
    "service_name_key": "llm_service_name_key" # Corresponds to a LANG_TEXTS key
}

# 将所有辅助函数定义移到 build_gui 之前

def get_text(key: str, **kwargs) -> str:
    try:
        text_template = LANG_TEXTS[key][current_language]
    except KeyError:
        text_template = LANG_TEXTS[key].get('en', f"<{key}_MISSING_{current_language}>")
    return text_template.format(**kwargs) if kwargs else text_template

def update_status_bar(message_key: str, **kwargs):
    message = get_text(message_key, **kwargs)
    
    def _perform_gui_updates():
        # Update the status bar label's text variable
        if process_status_text_var:
            process_status_text_var.set(message)

        # Update the main log text area (if it exists)
        if managed_process_info.get("output_area"):
            # The 'message' variable is captured from the outer scope (closure)
            if root_widget: # Ensure root_widget is still valid
                output_area_widget = managed_process_info["output_area"]
                output_area_widget.config(state=tk.NORMAL)
                output_area_widget.insert(tk.END, f"[STATUS] {message}\n")
                output_area_widget.see(tk.END)
                output_area_widget.config(state=tk.DISABLED)
                
    if root_widget:
        root_widget.after_idle(_perform_gui_updates)

def is_port_in_use(port: int) -> bool:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        try:
            s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            s.bind(("0.0.0.0", port))
            return False
        except OSError: return True
        except Exception: return True

def get_process_name_by_pid(pid: int) -> str:
    system = platform.system()
    name = get_text("unknown_process_name_placeholder")
    cmd_args = []
    try:
        if system == "Windows":
            cmd_args = ["tasklist", "/NH", "/FO", "CSV", "/FI", f"PID eq {pid}"]
            process = subprocess.run(cmd_args, capture_output=True, text=True, check=True, timeout=3, creationflags=subprocess.CREATE_NO_WINDOW)
            if process.stdout.strip():
                parts = process.stdout.strip().split('","')
                if len(parts) > 0: name = parts[0].strip('"')
        elif system == "Linux":
            cmd_args = ["ps", "-p", str(pid), "-o", "comm="]
            process = subprocess.run(cmd_args, capture_output=True, text=True, check=True, timeout=3)
            if process.stdout.strip(): name = process.stdout.strip()
        elif system == "Darwin":
            cmd_args = ["ps", "-p", str(pid), "-o", "comm="]
            process = subprocess.run(cmd_args, capture_output=True, text=True, check=True, timeout=3)
            raw_path = process.stdout.strip() if process.stdout.strip() else ""
            cmd_args = ["ps", "-p", str(pid), "-o", "command="]
            process = subprocess.run(cmd_args, capture_output=True, text=True, check=True, timeout=3)
            if raw_path:
                base_name = os.path.basename(raw_path)
                name = f"{base_name} ({raw_path})"
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired, FileNotFoundError):
        pass
    except Exception:
        pass
    return name

def find_processes_on_port(port: int) -> List[Dict[str, Any]]:
    process_details = []
    pids_only: List[int] = []
    system = platform.system()
    command_pid = ""
    try:
        if system == "Linux" or system == "Darwin":
            command_pid = f"lsof -ti tcp:{port} -sTCP:LISTEN"
            process = subprocess.Popen(command_pid, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, universal_newlines=True, close_fds=True)
            stdout_pid, _ = process.communicate(timeout=5)
            if process.returncode == 0 and stdout_pid:
                pids_only = [int(p) for p in stdout_pid.strip().splitlines() if p.isdigit()]
        elif system == "Windows":
            command_pid = 'netstat -ano -p TCP'
            process = subprocess.Popen(command_pid, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, universal_newlines=True, creationflags=subprocess.CREATE_NO_WINDOW)
            stdout_pid, _ = process.communicate(timeout=10)
            if process.returncode == 0 and stdout_pid:
                for line in stdout_pid.strip().splitlines():
                    parts = line.split()
                    if len(parts) >= 5 and parts[0].upper() == 'TCP':
                        if parts[3].upper() != 'LISTENING':
                            continue
                        local_address_full = parts[1]
                        try:
                            last_colon_idx = local_address_full.rfind(':')
                            if last_colon_idx == -1:
                                continue
                            extracted_port_str = local_address_full[last_colon_idx+1:]
                            if extracted_port_str.isdigit() and int(extracted_port_str) == port:
                                pid_str = parts[4]
                                if pid_str.isdigit():
                                    pids_only.append(int(pid_str))
                        except (ValueError, IndexError):
                            continue
                pids_only = list(set(pids_only))
    except Exception:
        pass
    for pid_val in pids_only:
        name = get_process_name_by_pid(pid_val)
        process_details.append({"pid": pid_val, "name": name})
    return process_details

def kill_process_pid(pid: int) -> bool:
    system = platform.system()
    success = False
    logger.info(f"Attempting to kill PID {pid} with normal privileges on {system}")
    try:
        if system == "Linux" or system == "Darwin":
            # 1. Attempt SIGTERM (best effort)
            logger.debug(f"Sending SIGTERM to PID {pid}")
            subprocess.run(["kill", "-TERM", str(pid)], capture_output=True, text=True, timeout=3) # check=False
            time.sleep(0.5)

            # 2. Check if process is gone (or if we lack permission to check)
            try:
                logger.debug(f"Checking PID {pid} with kill -0 after SIGTERM attempt")
                # This will raise CalledProcessError if process is gone OR user lacks permission for kill -0
                subprocess.run(["kill", "-0", str(pid)], check=True, capture_output=True, text=True, timeout=1)
                
                # If kill -0 succeeded, process is still alive and we have permission to signal it.
                # 3. Attempt SIGKILL
                logger.info(f"PID {pid} still alive after SIGTERM attempt (kill -0 succeeded). Sending SIGKILL.")
                subprocess.run(["kill", "-KILL", str(pid)], check=True, capture_output=True, text=True, timeout=3) # Raises on perm error for SIGKILL
                
                # 4. Verify with kill -0 again that it's gone
                time.sleep(0.1)
                logger.debug(f"Verifying PID {pid} with kill -0 after SIGKILL attempt")
                try:
                    subprocess.run(["kill", "-0", str(pid)], check=True, capture_output=True, text=True, timeout=1)
                    # If kill -0 still succeeds, SIGKILL failed to terminate it or it's unkillable
                    logger.warning(f"PID {pid} still alive even after SIGKILL was sent and did not error.")
                    success = False
                except subprocess.CalledProcessError as e_final_check:
                    # kill -0 failed, means process is gone. Check stderr for "No such process".
                    if e_final_check.stderr and "no such process" in e_final_check.stderr.lower():
                        logger.info(f"PID {pid} successfully terminated with SIGKILL (confirmed by final kill -0).")
                        success = True
                    else:
                        # kill -0 failed for other reason (e.g. perms, though unlikely if SIGKILL 'succeeded')
                        logger.warning(f"Final kill -0 check for PID {pid} failed unexpectedly. Stderr: {e_final_check.stderr}")
                        success = False # Unsure, so treat as failure for normal kill
            
            except subprocess.CalledProcessError as e:
                # This block is reached if initial `kill -0` fails, or `kill -KILL` fails.
                # `e` is the error from the *first* command that failed with check=True in the try block.
                if e.stderr and "no such process" in e.stderr.lower():
                    logger.info(f"Process {pid} is gone (kill -0 or kill -KILL reported 'No such process'). SIGTERM might have worked or it was already gone.")
                    success = True
                else:
                    # Failure was likely due to permissions (e.g., "Operation not permitted") or other reasons.
                    # This means normal kill attempt failed.
                    logger.warning(f"Normal kill attempt for PID {pid} failed or encountered permission issue. Stderr from failing cmd: {e.stderr}")
                    success = False
        
        elif system == "Windows":
            logger.debug(f"Using taskkill for PID {pid} on Windows.")
            result = subprocess.run(["taskkill", "/PID", str(pid), "/T", "/F"], capture_output=True, text=True, check=False, timeout=5, creationflags=subprocess.CREATE_NO_WINDOW)
            if result.returncode == 0:
                logger.info(f"Taskkill for PID {pid} succeeded (rc=0).")
                success = True
            else:
                # Check if process was not found
                output_lower = (result.stdout + result.stderr).lower()
                if "pid" in output_lower and ("not found" in output_lower or "no running instance" in output_lower or ("could not be terminated" in output_lower and "reason: there is no running instance" in output_lower)) :
                    logger.info(f"Taskkill for PID {pid} reported process not found or already terminated.")
                    success = True
                else:
                    logger.warning(f"Taskkill for PID {pid} failed. RC: {result.returncode}. Output: {output_lower}")
                    success = False

    except Exception as e_outer: # Catch any other unexpected exceptions
        logger.error(f"Outer exception in kill_process_pid for PID {pid}: {e_outer}", exc_info=True)
        success = False
    
    logger.info(f"kill_process_pid for PID {pid} final result: {success}")
    return success

def enhanced_port_check(port, port_name_key=""):
    port_display_name = get_text(f"port_name_{port_name_key}") if port_name_key else ""
    update_status_bar("checking_port_with_name", port_name=port_display_name, port=port)
    
    if is_port_in_use(port):
        pids_data = find_processes_on_port(port)
        if pids_data:
            pids_info_str_list = []
            for proc_info in pids_data:
                pids_info_str_list.append(f"{proc_info['pid']} ({proc_info['name']})")
            return {"port": port, "name_key": port_name_key, "pids_data": pids_data, "pids_str": ", ".join(pids_info_str_list)}
        else:
            return {"port": port, "name_key": port_name_key, "pids_data": [], "pids_str": get_text("unknown_process_name_placeholder")}
    return None

def check_all_required_ports(ports_to_check: List[Tuple[int, str]]) -> bool:
    occupied_ports_info = []
    for port, port_name_key in ports_to_check:
        result = enhanced_port_check(port, port_name_key)
        if result: 
            occupied_ports_info.append(result)

    if not occupied_ports_info:
        update_status_bar("port_check_all_completed")
        return True 

    occupied_ports_details_for_msg = []
    for info in occupied_ports_info:
        port_display_name = get_text(f"port_name_{info['name_key']}") if info['name_key'] else ""
        occupied_ports_details_for_msg.append(f"  - {port_display_name} (端口 {info['port']}): 被 PID(s) {info['pids_str']} 占用")
    
    details_str = "\n".join(occupied_ports_details_for_msg)

    if messagebox.askyesno(
        get_text("confirm_kill_multiple_ports_title"),
        get_text("confirm_kill_multiple_ports_message", occupied_ports_details=details_str),
        parent=root_widget
    ):
        pids_processed_this_cycle = set() # Tracks PIDs for which kill attempts (normal or admin) have been made in this call

        for info in occupied_ports_info:
            if info['pids_data']:
                for p_data in info['pids_data']:
                    pid = p_data['pid']
                    name = p_data['name']

                    if pid in pids_processed_this_cycle:
                        continue # Avoid reprocessing a PID if it appeared for multiple ports

                    logger.info(f"Port Check Cleanup: Attempting normal kill for PID {pid} ({name}) on port {info['port']}")
                    normal_kill_ok = kill_process_pid(pid)
                    
                    if normal_kill_ok:
                        logger.info(f"Port Check Cleanup: Normal kill succeeded for PID {pid} ({name})")
                        pids_processed_this_cycle.add(pid)
                    else:
                        logger.warning(f"Port Check Cleanup: Normal kill FAILED for PID {pid} ({name}). Prompting for admin kill.")
                        if messagebox.askyesno(
                            get_text("confirm_stop_pid_admin_title"),
                            get_text("confirm_stop_pid_admin_message", pid=pid, name=name),
                            parent=root_widget
                        ):
                            logger.info(f"Port Check Cleanup: User approved admin kill for PID {pid} ({name}). Attempting.")
                            admin_kill_initiated = kill_process_pid_admin(pid) # Optimistic for macOS
                            if admin_kill_initiated:
                                logger.info(f"Port Check Cleanup: Admin kill attempt for PID {pid} ({name}) initiated (result optimistic: {admin_kill_initiated}).")
                                # We still rely on the final port check, so no success message here.
                            else:
                                logger.warning(f"Port Check Cleanup: Admin kill attempt for PID {pid} ({name}) failed to initiate or was denied by user at OS level.")
                        else:
                            logger.info(f"Port Check Cleanup: User declined admin kill for PID {pid} ({name}).")
                        pids_processed_this_cycle.add(pid) # Mark as processed even if admin declined/failed, to avoid re-prompting in this cycle
        
        logger.info("Port Check Cleanup: Waiting for 2 seconds for processes to terminate...")
        time.sleep(2) 

        still_occupied_after_cleanup = False
        for info in occupied_ports_info: # Re-check all originally occupied ports
            if is_port_in_use(info['port']):
                port_display_name = get_text(f"port_name_{info['name_key']}") if info['name_key'] else str(info['port'])
                logger.warning(f"Port Check Cleanup: Port {port_display_name} ({info['port']}) is still in use after cleanup attempts.")
                still_occupied_after_cleanup = True
                break 
        
        if not still_occupied_after_cleanup:
            messagebox.showinfo(get_text("info_title"), get_text("all_ports_cleared_success"), parent=root_widget)
            update_status_bar("port_check_all_completed")
            return True
        else:
            messagebox.showwarning(get_text("warning_title"), get_text("some_ports_still_in_use"), parent=root_widget)
            return False
    else: 
        update_status_bar("port_check_user_declined_cleanup")
        return False

def _update_active_auth_display():
    """更新GUI中显示的当前活动认证文件"""
    if not active_auth_file_display_var or not root_widget:
        return

    active_files = [f for f in os.listdir(ACTIVE_AUTH_DIR) if f.lower().endswith('.json')]
    if active_files:
        # 通常 active 目录只有一个文件，但以防万一，取第一个
        active_file_name = sorted(active_files)[0]
        active_auth_file_display_var.set(get_text("current_auth_file_selected_format", file=active_file_name))
    else:
        active_auth_file_display_var.set(get_text("current_auth_file_none"))


def manage_auth_files_gui():
    if not os.path.exists(AUTH_PROFILES_DIR): # 检查根目录
        messagebox.showerror(get_text("error_title"), get_text("auth_dirs_missing"), parent=root_widget)
        return
    
    # 确保 active 和 saved 目录存在，如果不存在则创建
    os.makedirs(ACTIVE_AUTH_DIR, exist_ok=True)
    os.makedirs(SAVED_AUTH_DIR, exist_ok=True)

    all_auth_files = set()
    # 扫描 auth_profiles/ 根目录
    if os.path.exists(AUTH_PROFILES_DIR):
        for f in os.listdir(AUTH_PROFILES_DIR):
            if f.lower().endswith('.json') and os.path.isfile(os.path.join(AUTH_PROFILES_DIR, f)):
                all_auth_files.add(f)
    # 扫描 auth_profiles/active/ 子目录
    if os.path.exists(ACTIVE_AUTH_DIR):
        for f in os.listdir(ACTIVE_AUTH_DIR):
            if f.lower().endswith('.json') and os.path.isfile(os.path.join(ACTIVE_AUTH_DIR, f)):
                all_auth_files.add(f) # 通常 active 目录的文件也应该在 saved 或根目录有副本
    # 扫描 auth_profiles/saved/ 子目录
    if os.path.exists(SAVED_AUTH_DIR):
        for f in os.listdir(SAVED_AUTH_DIR):
            if f.lower().endswith('.json') and os.path.isfile(os.path.join(SAVED_AUTH_DIR, f)):
                all_auth_files.add(f)

    sorted_auth_files = sorted(list(all_auth_files))

    if not sorted_auth_files:
        messagebox.showinfo(get_text("info_title"), get_text("no_saved_auth_files"), parent=root_widget) # 可以复用这个文本
        return

    auth_window = tk.Toplevel(root_widget)
    auth_window.title(get_text("auth_manager_title"))
    auth_window.geometry("400x300")
    auth_window.resizable(True, True)
    ttk.Label(auth_window, text=get_text("saved_auth_files_label")).pack(pady=5)
    files_frame = ttk.Frame(auth_window)
    files_frame.pack(fill=tk.BOTH, expand=True, padx=10, pady=5)
    files_listbox = tk.Listbox(files_frame, selectmode=tk.SINGLE)
    files_listbox.pack(side=tk.LEFT, fill=tk.BOTH, expand=True)
    files_scrollbar = ttk.Scrollbar(files_frame, command=files_listbox.yview)
    files_scrollbar.pack(side=tk.RIGHT, fill=tk.Y)
    files_listbox.config(yscrollcommand=files_scrollbar.set)
    for file_name in sorted_auth_files:
        files_listbox.insert(tk.END, file_name)

    def activate_selected_file():
        if not files_listbox.curselection():
            messagebox.showwarning(get_text("warning_title"), get_text("no_file_selected"), parent=auth_window)
            return
        selected_file_name = files_listbox.get(files_listbox.curselection()[0])
        
        # 确定源文件路径，优先从 saved, 然后是根目录
        source_path = None
        potential_saved_path = os.path.join(SAVED_AUTH_DIR, selected_file_name)
        potential_root_path = os.path.join(AUTH_PROFILES_DIR, selected_file_name)

        if os.path.exists(potential_saved_path):
            source_path = potential_saved_path
        elif os.path.exists(potential_root_path):
            source_path = potential_root_path
        else:
            # 如果文件在 active 目录但不在 saved 或根目录，也允许激活（理论上不应发生，但作为容错）
            potential_active_path = os.path.join(ACTIVE_AUTH_DIR, selected_file_name)
            if os.path.exists(potential_active_path):
                 source_path = potential_active_path
            else:
                messagebox.showerror(get_text("error_title"), f"源文件 {selected_file_name} 未找到!", parent=auth_window)
                return

        # 清空 active 目录
        for existing_file in os.listdir(ACTIVE_AUTH_DIR):
            if existing_file.lower().endswith('.json'):
                try:
                    os.remove(os.path.join(ACTIVE_AUTH_DIR, existing_file))
                except OSError as e:
                    messagebox.showerror(get_text("error_title"), get_text("error_removing_file", file=existing_file, error=str(e)), parent=auth_window)
                    return
        try:
            import shutil
            dest_path = os.path.join(ACTIVE_AUTH_DIR, selected_file_name)
            shutil.copy2(source_path, dest_path) # 将选中的文件复制到 active 目录
            messagebox.showinfo(get_text("info_title"), get_text("auth_file_activated", file=selected_file_name), parent=auth_window)
            _update_active_auth_display() # 更新主界面显示
            auth_window.destroy()
        except Exception as e:
            messagebox.showerror(get_text("error_title"), get_text("error_activating_file", file=selected_file_name, error=str(e)), parent=auth_window)
            _update_active_auth_display() # 即使失败也尝试更新显示

    def deactivate_current_auth():
        """清空 active 目录，取消激活"""
        try:
            for existing_file in os.listdir(ACTIVE_AUTH_DIR):
                if existing_file.lower().endswith('.json'):
                    os.remove(os.path.join(ACTIVE_AUTH_DIR, existing_file))
            messagebox.showinfo(get_text("info_title"), get_text("auth_deactivated"), parent=auth_window)
            _update_active_auth_display()
            auth_window.destroy()
        except Exception as e:
            messagebox.showerror(get_text("error_title"), get_text("error_deactivating", error=str(e)), parent=auth_window)
            _update_active_auth_display()

    buttons_frame = ttk.Frame(auth_window)
    buttons_frame.pack(fill=tk.X, padx=10, pady=10)
    
    # 激活按钮
    activate_btn = ttk.Button(buttons_frame, text=get_text("activate_selected_btn"), command=activate_selected_file)
    activate_btn.pack(side=tk.LEFT, expand=True, fill=tk.X, padx=(0, 5))

    # 取消激活按钮
    deactivate_btn = ttk.Button(buttons_frame, text=get_text("deactivate_btn"), command=deactivate_current_auth)
    deactivate_btn.pack(side=tk.LEFT, expand=True, fill=tk.X, padx=5)

    # 取消按钮
    cancel_btn = ttk.Button(buttons_frame, text=get_text("cancel_btn"), command=auth_window.destroy)
    cancel_btn.pack(side=tk.RIGHT, expand=True, fill=tk.X, padx=(5, 0))

def get_active_auth_json_path_for_launch() -> Optional[str]:
    """获取用于启动命令的 --active-auth-json 参数值"""
    active_files = [f for f in os.listdir(ACTIVE_AUTH_DIR) if f.lower().endswith('.json') and os.path.isfile(os.path.join(ACTIVE_AUTH_DIR, f))]
    if active_files:
        # 如果 active 目录有文件，总是使用它（按名称排序的第一个）
        return os.path.join(ACTIVE_AUTH_DIR, sorted(active_files)[0])
    return None

def build_launch_command(mode, fastapi_port, camoufox_debug_port, stream_port_enabled, stream_port, helper_enabled, helper_endpoint):
    cmd = [PYTHON_EXECUTABLE, LAUNCH_CAMOUFOX_PY, f"--{mode}", "--server-port", str(fastapi_port), "--camoufox-debug-port", str(camoufox_debug_port)]

    active_auth_path = get_active_auth_json_path_for_launch()
    if active_auth_path:
        cmd.extend(["--active-auth-json", active_auth_path])
        logger.info(f"将使用认证文件: {active_auth_path}")
    else:
        logger.info("未找到活动的认证文件，不传递 --active-auth-json 参数。")

    if stream_port_enabled:
        cmd.extend(["--stream-port", str(stream_port)])
    else:
        cmd.extend(["--stream-port", "0"]) # 显式传递0表示禁用

    if helper_enabled and helper_endpoint:
        cmd.extend(["--helper", helper_endpoint])

    # 修复：添加统一代理配置参数传递
    # 使用 --internal-camoufox-proxy 参数确保最高优先级，而不是仅依赖环境变量
    if proxy_enabled_var.get():
        proxy_addr = proxy_address_var.get().strip()
        if proxy_addr:
            cmd.extend(["--internal-camoufox-proxy", proxy_addr])
            logger.info(f"将使用GUI配置的代理: {proxy_addr}")
        else:
            cmd.extend(["--internal-camoufox-proxy", ""])
            logger.info("GUI代理已启用但地址为空，明确禁用代理")
    else:
        cmd.extend(["--internal-camoufox-proxy", ""])
        logger.info("GUI代理未启用，明确禁用代理")

    return cmd

# --- GUI构建与主逻辑区段的函数定义 ---
# (这些函数调用上面定义的辅助函数，所以它们的定义顺序很重要)

def enqueue_stream_output(stream, stream_name_prefix):
    try:
        for line_bytes in iter(stream.readline, b''):
            if not line_bytes: break
            line = line_bytes.decode(sys.stdout.encoding or 'utf-8', errors='replace')
            if managed_process_info.get("output_area") and root_widget:
                def _update_stream_output(line_to_insert):
                    current_line = line_to_insert
                    if managed_process_info.get("output_area"):
                        managed_process_info["output_area"].config(state=tk.NORMAL)
                        managed_process_info["output_area"].insert(tk.END, current_line)
                        managed_process_info["output_area"].see(tk.END)
                        managed_process_info["output_area"].config(state=tk.DISABLED)
                root_widget.after_idle(_update_stream_output, f"[{stream_name_prefix}] {line}")
            else: print(f"[{stream_name_prefix}] {line.strip()}", flush=True)
    except ValueError: pass
    except Exception: pass
    finally:
        if hasattr(stream, 'close') and not stream.closed: stream.close()

def is_service_running():
    return managed_process_info.get("popen") and \
           managed_process_info["popen"].poll() is None and \
           not managed_process_info.get("fully_detached", False)

def is_any_service_known():
    return managed_process_info.get("popen") is not None

def monitor_process_thread_target():
    popen = managed_process_info.get("popen")
    service_name_key = managed_process_info.get("service_name_key")
    is_detached = managed_process_info.get("fully_detached", False)
    if not popen or not service_name_key: return
    stdout_thread = None; stderr_thread = None
    if popen.stdout:
        stdout_thread = threading.Thread(target=enqueue_stream_output, args=(popen.stdout, "stdout"), daemon=True)
        managed_process_info["stdout_thread"] = stdout_thread
        stdout_thread.start()
    if popen.stderr:
        stderr_thread = threading.Thread(target=enqueue_stream_output, args=(popen.stderr, "stderr"), daemon=True)
        managed_process_info["stderr_thread"] = stderr_thread
        stderr_thread.start()
    popen.wait()
    exit_code = popen.returncode
    if stdout_thread and stdout_thread.is_alive(): stdout_thread.join(timeout=1)
    if stderr_thread and stderr_thread.is_alive(): stderr_thread.join(timeout=1)
    if managed_process_info.get("service_name_key") == service_name_key:
        service_name = get_text(service_name_key)
        if not is_detached:
            if exit_code == 0: update_status_bar("service_stopped_gracefully_status", service_name=service_name)
            else: update_status_bar("service_stopped_exit_code_status", service_name=service_name, code=exit_code)
        managed_process_info["popen"] = None
        managed_process_info["service_name_key"] = None
        managed_process_info["fully_detached"] = False

def get_fastapi_port_from_gui() -> int:
    try:
        port_str = port_entry_var.get()
        if not port_str: messagebox.showwarning(get_text("warning_title"), get_text("enter_valid_port_warn")); return DEFAULT_FASTAPI_PORT
        port = int(port_str)
        if not (1024 <= port <= 65535): raise ValueError("Port out of range")
        return port
    except ValueError:
        messagebox.showwarning(get_text("warning_title"), get_text("enter_valid_port_warn"))
        port_entry_var.set(str(DEFAULT_FASTAPI_PORT))
        return DEFAULT_FASTAPI_PORT

def get_camoufox_debug_port_from_gui() -> int:
    try:
        port_str = camoufox_debug_port_var.get()
        if not port_str:
            camoufox_debug_port_var.set(str(DEFAULT_CAMOUFOX_PORT_GUI))
            return DEFAULT_CAMOUFOX_PORT_GUI
        port = int(port_str)
        if not (1024 <= port <= 65535): raise ValueError("Port out of range")
        return port
    except ValueError:
        messagebox.showwarning(get_text("warning_title"), get_text("enter_valid_port_warn"))
        camoufox_debug_port_var.set(str(DEFAULT_CAMOUFOX_PORT_GUI))
        return DEFAULT_CAMOUFOX_PORT_GUI

# 配置文件路径
CONFIG_FILE_PATH = os.path.join(SCRIPT_DIR, "gui_config.json")

# 默认配置 - 从环境变量读取，如果没有则使用硬编码默认值
DEFAULT_CONFIG = {
    "fastapi_port": DEFAULT_FASTAPI_PORT,
    "camoufox_debug_port": DEFAULT_CAMOUFOX_PORT_GUI,
    "stream_port": int(os.environ.get('GUI_DEFAULT_STREAM_PORT', '3120')),
    "stream_port_enabled": True,
    "helper_endpoint": os.environ.get('GUI_DEFAULT_HELPER_ENDPOINT', ''),
    "helper_enabled": False,
    "proxy_address": os.environ.get('GUI_DEFAULT_PROXY_ADDRESS', 'http://127.0.0.1:7890'),
    "proxy_enabled": False
}

# 加载配置
def load_config():
    if os.path.exists(CONFIG_FILE_PATH):
        try:
            with open(CONFIG_FILE_PATH, 'r', encoding='utf-8') as f:
                config = json.load(f)
                logger.info(f"成功加载配置文件: {CONFIG_FILE_PATH}")
                return config
        except Exception as e:
            logger.error(f"加载配置文件失败: {e}")
    logger.info(f"使用默认配置")
    return DEFAULT_CONFIG.copy()

# 保存配置
def save_config():
    config = {
        "fastapi_port": port_entry_var.get(),
        "camoufox_debug_port": camoufox_debug_port_var.get(),
        "stream_port": stream_port_var.get(),
        "stream_port_enabled": stream_port_enabled_var.get(),
        "helper_endpoint": helper_endpoint_var.get(),
        "helper_enabled": helper_enabled_var.get(),
        "proxy_address": proxy_address_var.get(),
        "proxy_enabled": proxy_enabled_var.get()
    }
    try:
        with open(CONFIG_FILE_PATH, 'w', encoding='utf-8') as f:
            json.dump(config, f, ensure_ascii=False, indent=2)
            logger.info(f"成功保存配置到: {CONFIG_FILE_PATH}")
    except Exception as e:
        logger.error(f"保存配置失败: {e}")

# 重置为默认配置，包含代理设置
def reset_to_defaults():
    if messagebox.askyesno(get_text("confirm_reset_title"), get_text("confirm_reset_message"), parent=root_widget):
        port_entry_var.set(str(DEFAULT_FASTAPI_PORT))
        camoufox_debug_port_var.set(str(DEFAULT_CAMOUFOX_PORT_GUI))
        stream_port_var.set("3120")
        stream_port_enabled_var.set(True)
        helper_endpoint_var.set("")
        helper_enabled_var.set(False)
        proxy_address_var.set("http://127.0.0.1:7890")
        proxy_enabled_var.set(False)
        messagebox.showinfo(get_text("info_title"), get_text("reset_success"), parent=root_widget)

def _configure_proxy_env_vars() -> Dict[str, str]:
    """
    配置代理环境变量（已弃用，现在主要通过 --internal-camoufox-proxy 参数传递）
    保留此函数以维持向后兼容性，但现在主要用于状态显示
    """
    proxy_env = {}
    if proxy_enabled_var.get():
        proxy_addr = proxy_address_var.get().strip()
        if proxy_addr:
            # 注意：现在主要通过 --internal-camoufox-proxy 参数传递代理配置
            # 环境变量作为备用方案，但优先级较低
            update_status_bar("proxy_configured_status", proxy_addr=proxy_addr)
        else:
            update_status_bar("proxy_skip_status")
    else:
        update_status_bar("proxy_skip_status")
    return proxy_env

def _launch_process_gui(cmd: List[str], service_name_key: str, env_vars: Optional[Dict[str, str]] = None):
    global managed_process_info # managed_process_info is now informational for these launches
    service_name = get_text(service_name_key)

    # Clear previous output area for GUI messages, actual process output will be in the new terminal
    if managed_process_info.get("output_area"):
        managed_process_info["output_area"].config(state=tk.NORMAL)
        managed_process_info["output_area"].delete('1.0', tk.END)
        managed_process_info["output_area"].insert(tk.END, f"[INFO] Preparing to launch {service_name} in a new terminal...\\n")
        managed_process_info["output_area"].config(state=tk.DISABLED)

    effective_env = os.environ.copy()
    if env_vars: effective_env.update(env_vars)
    effective_env['PYTHONIOENCODING'] = 'utf-8'

    popen_kwargs: Dict[str, Any] = {"env": effective_env}
    system = platform.system()
    launch_cmd_for_terminal: Optional[List[str]] = None

    # Prepare command string for terminals that take a single command string
    # Ensure correct quoting for arguments with spaces
    cmd_parts_for_string = []
    for part in cmd:
        if " " in part and not (part.startswith('"') and part.endswith('"')):
            cmd_parts_for_string.append(f'"{part}"')
        else:
            cmd_parts_for_string.append(part)
    cmd_str_for_terminal_execution = " ".join(cmd_parts_for_string)


    if system == "Windows":
        # CREATE_NEW_CONSOLE opens a new window.
        # The new process will be a child of this GUI initially, but if python.exe
        # itself handles its lifecycle well, closing GUI might not kill it.
        # To be more robust for independence, one might use 'start' cmd,
        # but simple CREATE_NEW_CONSOLE often works for python scripts.
        # For true independence and GUI not waiting, Popen should be on python.exe directly.
        popen_kwargs["creationflags"] = subprocess.CREATE_NEW_CONSOLE
        launch_cmd_for_terminal = cmd # Direct command
    elif system == "Darwin": # macOS
        # import shlex # Ensure shlex is imported (should be at top of file)

        # Build the shell command string with proper quoting for each argument.
        # The command will first change to SCRIPT_DIR, then execute the python script.
        script_dir_quoted = shlex.quote(SCRIPT_DIR)
        python_executable_quoted = shlex.quote(cmd[0])
        script_path_quoted = shlex.quote(cmd[1])
        
        args_for_script_quoted = [shlex.quote(arg) for arg in cmd[2:]]

        # 构建环境变量设置字符串
        env_prefix_parts = []
        if env_vars: # env_vars 应该是从 _configure_proxy_env_vars() 来的 proxy_env
            for key, value in env_vars.items():
                if value is not None: # 确保值存在且不为空字符串
                    env_prefix_parts.append(f"{shlex.quote(key)}={shlex.quote(str(value))}")
        env_prefix_str = " ".join(env_prefix_parts)
        
        # Construct the full shell command to be executed in the new terminal
        shell_command_parts = [
            f"cd {script_dir_quoted}",
            "&&" # Ensure command separation
        ]
        if env_prefix_str:
            shell_command_parts.append(env_prefix_str)
        
        shell_command_parts.extend([
            python_executable_quoted,
            script_path_quoted
        ])
        shell_command_parts.extend(args_for_script_quoted)
        shell_command_str = " ".join(shell_command_parts)

        # Now, escape this shell_command_str for embedding within an AppleScript double-quoted string.
        # In AppleScript strings, backslash `\\` and double quote `\"` are special and need to be escaped.
        applescript_arg_escaped = shell_command_str.replace('\\\\', '\\\\\\\\').replace('\"', '\\\\\"')
        
        # Construct the AppleScript command
        # 修复：使用简化的AppleScript命令避免AppleEvent处理程序失败
        # 直接创建新窗口并执行命令，避免复杂的条件判断
        applescript_command = f'''
        tell application "Terminal"
            do script "{applescript_arg_escaped}"
            activate
        end tell
        '''

        launch_cmd_for_terminal = ["osascript", "-e", applescript_command.strip()]
    elif system == "Linux":
        import shutil
        terminal_emulator = shutil.which("x-terminal-emulator") or shutil.which("gnome-terminal") or shutil.which("konsole") or shutil.which("xfce4-terminal") or shutil.which("xterm")
        if terminal_emulator:
            # Construct command ensuring SCRIPT_DIR is CWD for the launched script
            # Some terminals might need `sh -c "cd ... && python ..."`
            # For simplicity, let's try to pass the command directly if possible or via sh -c
            cd_command = f"cd '{SCRIPT_DIR}' && "
            full_command_to_run = cd_command + cmd_str_for_terminal_execution
            
            if "gnome-terminal" in terminal_emulator or "mate-terminal" in terminal_emulator:
                launch_cmd_for_terminal = [terminal_emulator, "--", "bash", "-c", full_command_to_run + "; exec bash"]
            elif "konsole" in terminal_emulator or "xfce4-terminal" in terminal_emulator or "lxterminal" in terminal_emulator:
                 launch_cmd_for_terminal = [terminal_emulator, "-e", f"bash -c '{full_command_to_run}; exec bash'"]
            elif "xterm" in terminal_emulator: # xterm might need careful quoting
                 launch_cmd_for_terminal = [terminal_emulator, "-hold", "-e", "bash", "-c", f"{full_command_to_run}"]
            else: # Generic x-terminal-emulator
                 launch_cmd_for_terminal = [terminal_emulator, "-e", f"bash -c '{full_command_to_run}; exec bash'"]
        else:
            messagebox.showerror(get_text("error_title"), "未找到兼容的Linux终端模拟器 (如 x-terminal-emulator, gnome-terminal, xterm)。无法在新终端中启动服务。")
            update_status_bar("status_error_starting", service_name=service_name)
            return
    else: # Fallback for other OS or if specific terminal launch fails
        messagebox.showerror(get_text("error_title"), f"不支持为操作系统 {system} 在新终端中启动。")
        update_status_bar("status_error_starting", service_name=service_name)
        return

    if not launch_cmd_for_terminal: # Should not happen if logic above is correct
        messagebox.showerror(get_text("error_title"), f"无法为 {system} 构建终端启动命令。")
        update_status_bar("status_error_starting", service_name=service_name)
        return
        
    try:
        # Launch the terminal command. This Popen object is for the terminal launcher.
        # The actual Python script is a child of that new terminal.
        logger.info(f"Launching in new terminal with command: {' '.join(launch_cmd_for_terminal)}")
        logger.info(f"Effective environment for new terminal: {effective_env}")

        # For non-Windows, where we launch `osascript` or a terminal emulator,
        # these Popen objects complete quickly.
        # For Windows, `CREATE_NEW_CONSOLE` means the Popen object is for the new python process.
        # However, we are treating all as fire-and-forget for the GUI.
        process = subprocess.Popen(launch_cmd_for_terminal, **popen_kwargs)
        
        # We no longer store this popen object in managed_process_info for direct GUI management
        # as the process is meant to be independent.
        # managed_process_info["popen"] = process 
        # managed_process_info["service_name_key"] = service_name_key
        # managed_process_info["fully_detached"] = True 
        
        # No monitoring threads from GUI for these independent processes.
        # managed_process_info["monitor_thread"] = None
        # managed_process_info["stdout_thread"] = None
        # managed_process_info["stderr_thread"] = None

        update_status_bar("info_service_new_terminal")
        if managed_process_info.get("output_area"):
             managed_process_info["output_area"].config(state=tk.NORMAL)
             managed_process_info["output_area"].insert(tk.END, f"[INFO] {get_text('info_service_new_terminal')}\\n")
             managed_process_info["output_area"].insert(tk.END, f"[INFO] {service_name} (PID: {process.pid if system == 'Windows' else 'N/A for terminal launcher'}) should be running in a new window.\\n")
             managed_process_info["output_area"].see(tk.END)
             managed_process_info["output_area"].config(state=tk.DISABLED)
        
        if root_widget: # Query ports after a delay, as service might take time to start
            root_widget.after(3500, query_port_and_display_pids_gui)

    except FileNotFoundError:
        messagebox.showerror(get_text("error_title"), get_text("script_not_found_error_msgbox", cmd=' '.join(cmd)))
        update_status_bar("status_script_not_found", service_name=service_name)
    except Exception as e:
        messagebox.showerror(get_text("error_title"), f"{service_name} - {get_text('error_title')}: {e}")
        update_status_bar("status_error_starting", service_name=service_name)
        logger.error(f"Error in _launch_process_gui for {service_name}: {e}", exc_info=True)

@debounce_button("start_headed_interactive", 3.0)
def start_headed_interactive_gui():
    launch_params = _get_launch_parameters()
    if not launch_params: return

    if port_auto_check_var.get():
        ports_to_check = [
            (launch_params["fastapi_port"], "fastapi"), 
            (launch_params["camoufox_debug_port"], "camoufox_debug")
        ]
        if launch_params["stream_port_enabled"] and launch_params["stream_port"] != 0:
            ports_to_check.append((launch_params["stream_port"], "stream_proxy"))
        if launch_params["helper_enabled"] and launch_params["helper_endpoint"]:
            try:
                pu = urlparse(launch_params["helper_endpoint"])
                if pu.hostname in ("localhost", "127.0.0.1") and pu.port: 
                    ports_to_check.append((pu.port, "helper_service"))
            except Exception as e: 
                print(f"解析Helper URL失败(有头模式): {e}")
        if not check_all_required_ports(ports_to_check): return
    
    proxy_env = _configure_proxy_env_vars()
    cmd = build_launch_command(
        "debug", 
        launch_params["fastapi_port"], 
        launch_params["camoufox_debug_port"],
        launch_params["stream_port_enabled"],
        launch_params["stream_port"],
        launch_params["helper_enabled"],
        launch_params["helper_endpoint"]
    )
    update_status_bar("status_headed_launch")
    _launch_process_gui(cmd, "service_name_headed_interactive", env_vars=proxy_env)

@debounce_button("start_headless", 3.0)
def start_headless_gui():
    launch_params = _get_launch_parameters()
    if not launch_params: return

    if port_auto_check_var.get():
        ports_to_check = [
            (launch_params["fastapi_port"], "fastapi"), 
            (launch_params["camoufox_debug_port"], "camoufox_debug")
        ]
        if launch_params["stream_port_enabled"] and launch_params["stream_port"] != 0:
            ports_to_check.append((launch_params["stream_port"], "stream_proxy"))
        if launch_params["helper_enabled"] and launch_params["helper_endpoint"]:
            try:
                pu = urlparse(launch_params["helper_endpoint"])
                if pu.hostname in ("localhost", "127.0.0.1") and pu.port: 
                    ports_to_check.append((pu.port, "helper_service"))
            except Exception as e: 
                print(f"解析Helper URL失败(无头模式): {e}")
        if not check_all_required_ports(ports_to_check): return

    proxy_env = _configure_proxy_env_vars()
    cmd = build_launch_command(
        "headless", 
        launch_params["fastapi_port"], 
        launch_params["camoufox_debug_port"],
        launch_params["stream_port_enabled"],
        launch_params["stream_port"],
        launch_params["helper_enabled"],
        launch_params["helper_endpoint"]
    )
    update_status_bar("status_headless_launch")
    _launch_process_gui(cmd, "service_name_headless", env_vars=proxy_env)

@debounce_button("start_virtual_display", 3.0)
def start_virtual_display_gui():
    if platform.system() != "Linux":
        messagebox.showwarning(get_text("warning_title"), "虚拟显示模式仅在Linux上受支持。")
        return
    
    launch_params = _get_launch_parameters()
    if not launch_params: return

    if port_auto_check_var.get():
        ports_to_check = [
            (launch_params["fastapi_port"], "fastapi"), 
            (launch_params["camoufox_debug_port"], "camoufox_debug")
        ]
        if launch_params["stream_port_enabled"] and launch_params["stream_port"] != 0:
            ports_to_check.append((launch_params["stream_port"], "stream_proxy"))
        if launch_params["helper_enabled"] and launch_params["helper_endpoint"]:
            try:
                pu = urlparse(launch_params["helper_endpoint"])
                if pu.hostname in ("localhost", "127.0.0.1") and pu.port: 
                    ports_to_check.append((pu.port, "helper_service"))
            except Exception as e: 
                print(f"解析Helper URL失败(虚拟显示模式): {e}")
        if not check_all_required_ports(ports_to_check): return

    proxy_env = _configure_proxy_env_vars()
    cmd = build_launch_command(
        "virtual-display", 
        launch_params["fastapi_port"], 
        launch_params["camoufox_debug_port"],
        launch_params["stream_port_enabled"],
        launch_params["stream_port"],
        launch_params["helper_enabled"],
        launch_params["helper_endpoint"]
    )
    update_status_bar("status_virtual_display_launch")
    _launch_process_gui(cmd, "service_name_virtual_display", env_vars=proxy_env)

# --- LLM Mock Service Management ---

def is_llm_service_running() -> bool:
    """检查本地LLM模拟服务是否正在运行"""
    return llm_service_process_info.get("popen") and \
           llm_service_process_info["popen"].poll() is None

def monitor_llm_process_thread_target():
    """监控LLM服务进程，捕获输出并更新状态"""
    popen = llm_service_process_info.get("popen")
    service_name_key = llm_service_process_info.get("service_name_key") # "llm_service_name_key"
    output_area = managed_process_info.get("output_area") # Use the main output area

    if not popen or not service_name_key or not output_area:
        logger.error("LLM monitor thread: Popen, service_name_key, or output_area is None.")
        return

    service_name = get_text(service_name_key)
    logger.info(f"Starting monitor thread for {service_name} (PID: {popen.pid})")

    # stdout/stderr redirection
    if popen.stdout:
        llm_service_process_info["stdout_thread"] = threading.Thread(
            target=enqueue_stream_output, args=(popen.stdout, f"{service_name}-stdout"), daemon=True
        )
        llm_service_process_info["stdout_thread"].start()
    
    if popen.stderr:
        llm_service_process_info["stderr_thread"] = threading.Thread(
            target=enqueue_stream_output, args=(popen.stderr, f"{service_name}-stderr"), daemon=True
        )
        llm_service_process_info["stderr_thread"].start()

    popen.wait() # Wait for the process to terminate
    exit_code = popen.returncode
    logger.info(f"{service_name} (PID: {popen.pid}) terminated with exit code {exit_code}.")

    if llm_service_process_info.get("stdout_thread") and llm_service_process_info["stdout_thread"].is_alive():
        llm_service_process_info["stdout_thread"].join(timeout=1)
    if llm_service_process_info.get("stderr_thread") and llm_service_process_info["stderr_thread"].is_alive():
        llm_service_process_info["stderr_thread"].join(timeout=1)

    # Update status only if this was the process we were tracking
    if llm_service_process_info.get("popen") == popen:
        update_status_bar("status_llm_stopped")
        llm_service_process_info["popen"] = None
        llm_service_process_info["monitor_thread"] = None
        llm_service_process_info["stdout_thread"] = None
        llm_service_process_info["stderr_thread"] = None

def _actually_launch_llm_service():
    """实际启动 llm.py 脚本"""
    global llm_service_process_info
    service_name_key = "llm_service_name_key"
    service_name = get_text(service_name_key)
    output_area = managed_process_info.get("output_area")

    if not output_area:
        logger.error("Cannot launch LLM service: Main output area is not available.")
        update_status_bar("status_error_starting", service_name=service_name)
        return

    llm_script_path = os.path.join(SCRIPT_DIR, LLM_PY_FILENAME)
    if not os.path.exists(llm_script_path):
        messagebox.showerror(get_text("error_title"), get_text("startup_script_not_found_msgbox", script=LLM_PY_FILENAME))
        update_status_bar("status_script_not_found", service_name=service_name)
        return

    # Get the main server port from GUI to pass to llm.py
    main_server_port = get_fastapi_port_from_gui() # Ensure this function is available and returns the correct port

    cmd = [PYTHON_EXECUTABLE, llm_script_path, f"--main-server-port={main_server_port}"]
    logger.info(f"Attempting to launch LLM service with command: {' '.join(cmd)}")

    try:
        # Clear previous LLM service output if any, or add a header
        output_area.config(state=tk.NORMAL)
        output_area.insert(tk.END, f"--- Starting {service_name} ---\n")
        output_area.config(state=tk.DISABLED)
        
        effective_env = os.environ.copy()
        effective_env['PYTHONUNBUFFERED'] = '1' # Ensure unbuffered output for real-time logging
        effective_env['PYTHONIOENCODING'] = 'utf-8'

        popen = subprocess.Popen(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=False, # Read as bytes for enqueue_stream_output
            cwd=SCRIPT_DIR,
            env=effective_env,
            creationflags=subprocess.CREATE_NO_WINDOW if platform.system() == "Windows" else 0
        )
        llm_service_process_info["popen"] = popen
        llm_service_process_info["service_name_key"] = service_name_key
        
        update_status_bar("status_llm_starting", pid=popen.pid)
        logger.info(f"{service_name} started with PID: {popen.pid}")

        # Start monitoring thread
        monitor_thread = threading.Thread(target=monitor_llm_process_thread_target, daemon=True)
        llm_service_process_info["monitor_thread"] = monitor_thread
        monitor_thread.start()

    except FileNotFoundError:
        messagebox.showerror(get_text("error_title"), get_text("script_not_found_error_msgbox", cmd=' '.join(cmd)))
        update_status_bar("status_script_not_found", service_name=service_name)
        logger.error(f"FileNotFoundError when trying to launch LLM service: {cmd}")
    except Exception as e:
        messagebox.showerror(get_text("error_title"), f"{service_name} - {get_text('error_title')}: {e}")
        update_status_bar("status_error_starting", service_name=service_name)
        logger.error(f"Exception when launching LLM service: {e}", exc_info=True)
        llm_service_process_info["popen"] = None # Ensure it's cleared on failure

def _check_llm_backend_and_launch_thread():
    """检查LLM后端服务 (动态端口) 并在成功后启动llm.py"""
    # Get the current FastAPI port from the GUI
    # This needs to be called within this thread, right before the check,
    # as port_entry_var might be accessed from a different thread if called outside.
    # However, Tkinter GUI updates should ideally be done from the main thread.
    # For reading a StringVar, it's generally safe.
    current_fastapi_port = get_fastapi_port_from_gui()

    # Update status bar and logger with the dynamic port
    # For status bar updates from a thread, it's better to use root_widget.after or a queue,
    # but for simplicity in this context, direct update_status_bar call is used.
    # Ensure update_status_bar is thread-safe or schedules GUI updates.
    # The existing update_status_bar uses root_widget.after_idle, which is good.
    
    # Dynamically create the message keys for status bar to include the port
    backend_check_msg_key = "status_llm_backend_check" # Original key
    backend_ok_msg_key = "status_llm_backend_ok_starting"
    backend_fail_msg_key = "status_llm_backend_fail"
    
    # It's better to pass the port as a parameter to get_text if the LANG_TEXTS are updated
    # For now, we'll just log the dynamic port separately.
    update_status_bar(backend_check_msg_key) # Still uses the generic message
    logger.info(f"Checking LLM backend service at localhost:{current_fastapi_port}...")
    
    backend_ok = False
    try:
        with socket.create_connection(("localhost", current_fastapi_port), timeout=3) as sock:
            backend_ok = True
        logger.info(f"LLM backend service (localhost:{current_fastapi_port}) is responsive.")
    except (socket.timeout, ConnectionRefusedError, OSError) as e:
        logger.warning(f"LLM backend service (localhost:{current_fastapi_port}) not responding: {e}")
        backend_ok = False
    
    if root_widget: # Ensure GUI is still there
        if backend_ok:
            update_status_bar(backend_ok_msg_key, port=current_fastapi_port) # Pass port to fill placeholder
            _actually_launch_llm_service() # This already gets the port via get_fastapi_port_from_gui()
        else:
            # Update status bar with the dynamic port for failure message
            update_status_bar(backend_fail_msg_key, port=current_fastapi_port)
            
            # Show warning messagebox with the dynamic port
            # The status bar is already updated by update_status_bar,
            # so no need to manually set process_status_text_var or write to output_area here again for the same message.
            # The update_status_bar function handles writing to the output_area if configured.
            messagebox.showwarning(
                get_text("warning_title"),
                get_text(backend_fail_msg_key, port=current_fastapi_port), # Use get_text with port for the messagebox
                parent=root_widget
            )

def start_llm_service_gui():
    """GUI命令：启动本地LLM模拟服务"""
    if is_llm_service_running():
        pid = llm_service_process_info["popen"].pid
        update_status_bar("status_llm_already_running", pid=pid)
        messagebox.showinfo(get_text("info_title"), get_text("status_llm_already_running", pid=pid), parent=root_widget)
        return

    # Run the check and actual launch in a new thread to keep GUI responsive
    # The check itself can take a few seconds if the port is unresponsive.
    threading.Thread(target=_check_llm_backend_and_launch_thread, daemon=True).start()

def stop_llm_service_gui():
    """GUI命令：停止本地LLM模拟服务"""
    service_name = get_text(llm_service_process_info.get("service_name_key", "llm_service_name_key"))
    popen = llm_service_process_info.get("popen")

    if not popen or popen.poll() is not None:
        update_status_bar("status_llm_not_running")
        # messagebox.showinfo(get_text("info_title"), get_text("status_llm_not_running"), parent=root_widget)
        return

    if messagebox.askyesno(get_text("confirm_stop_llm_title"), get_text("confirm_stop_llm_message"), parent=root_widget):
        logger.info(f"Attempting to stop {service_name} (PID: {popen.pid})")
        update_status_bar("status_stopping_service", service_name=service_name, pid=popen.pid)
        
        try:
            # Attempt graceful termination first
            if platform.system() == "Windows":
                # On Windows, sending SIGINT to a Popen object created with CREATE_NO_WINDOW
                # might not work as expected for Flask apps. taskkill is more reliable.
                # We can try to send Ctrl+C to the console if it had one, but llm.py is simple.
                # For Flask, direct popen.terminate() or popen.kill() is often used.
                logger.info(f"Sending SIGTERM/terminate to {service_name} (PID: {popen.pid}) on Windows.")
                popen.terminate() # Sends SIGTERM on Unix, TerminateProcess on Windows
            else: # Linux/macOS
                logger.info(f"Sending SIGINT to {service_name} (PID: {popen.pid}) on {platform.system()}.")
                popen.send_signal(signal.SIGINT)

            # Wait for a short period for graceful shutdown
            try:
                popen.wait(timeout=5) # Wait up to 5 seconds
                logger.info(f"{service_name} (PID: {popen.pid}) terminated gracefully after signal.")
                update_status_bar("status_llm_stopped")
            except subprocess.TimeoutExpired:
                logger.warning(f"{service_name} (PID: {popen.pid}) did not terminate after signal. Forcing kill.")
                popen.kill() # Force kill
                popen.wait(timeout=2) # Wait for kill to take effect
                update_status_bar("status_llm_stopped") # Assume killed
                logger.info(f"{service_name} (PID: {popen.pid}) was force-killed.")
            
        except Exception as e:
            logger.error(f"Error stopping {service_name} (PID: {popen.pid}): {e}", exc_info=True)
            update_status_bar("status_llm_stop_error")
            messagebox.showerror(get_text("error_title"), f"Error stopping {service_name}: {e}", parent=root_widget)
        finally:
            # Ensure threads are joined and resources cleaned up, even if already done by monitor
            if llm_service_process_info.get("stdout_thread") and llm_service_process_info["stdout_thread"].is_alive():
                llm_service_process_info["stdout_thread"].join(timeout=0.5)
            if llm_service_process_info.get("stderr_thread") and llm_service_process_info["stderr_thread"].is_alive():
                llm_service_process_info["stderr_thread"].join(timeout=0.5)
            
            llm_service_process_info["popen"] = None
            llm_service_process_info["monitor_thread"] = None
            llm_service_process_info["stdout_thread"] = None
            llm_service_process_info["stderr_thread"] = None
            
            # Clear related output from the main log area or add a "stopped" message
            output_area = managed_process_info.get("output_area")
            if output_area:
                output_area.config(state=tk.NORMAL)
                output_area.insert(tk.END, f"--- {service_name} stopped ---\n")
                output_area.see(tk.END)
                output_area.config(state=tk.DISABLED)
    else:
        logger.info(f"User cancelled stopping {service_name}.")

# --- End LLM Mock Service Management ---

def query_port_and_display_pids_gui():
    ports_to_query_info = []
    ports_desc_list = []

    # 1. FastAPI Port
    fastapi_port = get_fastapi_port_from_gui()
    ports_to_query_info.append({"port": fastapi_port, "type_key": "port_name_fastapi", "type_name": get_text("port_name_fastapi")})
    ports_desc_list.append(f"{get_text('port_name_fastapi')}:{fastapi_port}")

    # 2. Camoufox Debug Port
    camoufox_port = get_camoufox_debug_port_from_gui()
    ports_to_query_info.append({"port": camoufox_port, "type_key": "port_name_camoufox_debug", "type_name": get_text("port_name_camoufox_debug")})
    ports_desc_list.append(f"{get_text('port_name_camoufox_debug')}:{camoufox_port}")
    
    # 3. Stream Proxy Port (if enabled)
    if stream_port_enabled_var.get():
        try:
            stream_p_val_str = stream_port_var.get().strip()
            stream_p = int(stream_p_val_str) if stream_p_val_str else 0 # Default to 0 if empty, meaning disabled
            if stream_p != 0 and not (1024 <= stream_p <= 65535):
                 messagebox.showwarning(get_text("warning_title"), get_text("stream_port_out_of_range"), parent=root_widget)
                 # Optionally, do not query this port or handle as error
            elif stream_p != 0 : # Only query if valid and non-zero
                ports_to_query_info.append({"port": stream_p, "type_key": "port_name_stream_proxy", "type_name": get_text("port_name_stream_proxy")})
                ports_desc_list.append(f"{get_text('port_name_stream_proxy')}:{stream_p}")
        except ValueError:
            messagebox.showwarning(get_text("warning_title"), get_text("stream_port_out_of_range") + " (非数字)", parent=root_widget)


    update_status_bar("querying_ports_status", ports_desc=", ".join(ports_desc_list))
    
    if pid_listbox_widget and pid_list_lbl_frame_ref:
        pid_listbox_widget.delete(0, tk.END)
        pid_list_lbl_frame_ref.config(text=get_text("pids_on_multiple_ports_label")) # Update title

        found_any_process = False
        for port_info in ports_to_query_info:
            current_port = port_info["port"]
            port_type_name = port_info["type_name"]
            
            processes_on_current_port = find_processes_on_port(current_port)
            if processes_on_current_port:
                found_any_process = True
                for proc_info in processes_on_current_port:
                    pid_display_info = f"{proc_info['pid']} - {proc_info['name']}"
                    display_text = get_text("port_query_result_format",
                                            port_type=port_type_name,
                                            port_num=current_port,
                                            pid_info=pid_display_info)
                    pid_listbox_widget.insert(tk.END, display_text)
            else:
                display_text = get_text("port_not_in_use_format",
                                        port_type=port_type_name,
                                        port_num=current_port)
                pid_listbox_widget.insert(tk.END, display_text)
        
        if not found_any_process and not any(find_processes_on_port(p["port"]) for p in ports_to_query_info): # Recheck if all are empty
             # If after checking all, still no processes, we can add a general "no pids found on queried ports"
             # but the per-port "not in use" message is usually clearer.
             pass # Individual messages already cover this.
    else:
        logger.error("pid_listbox_widget or pid_list_lbl_frame_ref is None in query_port_and_display_pids_gui")

def _perform_proxy_test_single(proxy_address: str, test_url: str, timeout: int = 15) -> Tuple[bool, str, int]:
    """
    单次代理测试尝试
    Returns (success_status, message_or_error_string, status_code).
    """
    proxies = {
        "http": proxy_address,
        "https": proxy_address,
    }
    try:
        logger.info(f"Testing proxy {proxy_address} with URL {test_url} (timeout: {timeout}s)")
        response = requests.get(test_url, proxies=proxies, timeout=timeout, allow_redirects=True)
        status_code = response.status_code

        # 检查HTTP状态码
        if 200 <= status_code < 300:
            logger.info(f"Proxy test to {test_url} via {proxy_address} successful. Status: {status_code}")
            return True, get_text("proxy_test_success", url=test_url), status_code
        elif status_code == 503:
            # 503 Service Unavailable - 可能是临时性问题
            logger.warning(f"Proxy test got 503 Service Unavailable from {test_url} via {proxy_address}")
            return False, f"HTTP {status_code}: Service Temporarily Unavailable", status_code
        elif 400 <= status_code < 500:
            # 4xx 客户端错误
            logger.warning(f"Proxy test got client error {status_code} from {test_url} via {proxy_address}")
            return False, f"HTTP {status_code}: Client Error", status_code
        elif 500 <= status_code < 600:
            # 5xx 服务器错误
            logger.warning(f"Proxy test got server error {status_code} from {test_url} via {proxy_address}")
            return False, f"HTTP {status_code}: Server Error", status_code
        else:
            logger.warning(f"Proxy test got unexpected status {status_code} from {test_url} via {proxy_address}")
            return False, f"HTTP {status_code}: Unexpected Status", status_code

    except requests.exceptions.ProxyError as e:
        logger.error(f"ProxyError connecting to {test_url} via {proxy_address}: {e}")
        return False, f"Proxy Error: {e}", 0
    except requests.exceptions.ConnectTimeout as e:
        logger.error(f"ConnectTimeout connecting to {test_url} via {proxy_address}: {e}")
        return False, f"Connection Timeout: {e}", 0
    except requests.exceptions.ReadTimeout as e:
        logger.error(f"ReadTimeout from {test_url} via {proxy_address}: {e}")
        return False, f"Read Timeout: {e}", 0
    except requests.exceptions.SSLError as e:
        logger.error(f"SSLError connecting to {test_url} via {proxy_address}: {e}")
        return False, f"SSL Error: {e}", 0
    except requests.exceptions.RequestException as e:
        logger.error(f"RequestException connecting to {test_url} via {proxy_address}: {e}")
        return False, str(e), 0
    except Exception as e: # Catch any other unexpected errors
        logger.error(f"Unexpected error during proxy test to {test_url} via {proxy_address}: {e}", exc_info=True)
        return False, f"Unexpected error: {e}", 0

def _perform_proxy_test(proxy_address: str, test_url: str) -> Tuple[bool, str]:
    """
    增强的代理测试函数，包含重试机制和备用URL
    Returns (success_status, message_or_error_string).
    """
    max_attempts = 3
    backup_url = LANG_TEXTS["proxy_test_url_backup"]
    urls_to_try = [test_url]

    # 如果主URL不是备用URL，则添加备用URL
    if test_url != backup_url:
        urls_to_try.append(backup_url)

    for url_index, current_url in enumerate(urls_to_try):
        if url_index > 0:
            logger.info(f"Trying backup URL: {current_url}")
            update_status_bar("proxy_test_backup_url")

        for attempt in range(1, max_attempts + 1):
            if attempt > 1:
                logger.info(f"Retrying proxy test (attempt {attempt}/{max_attempts})")
                update_status_bar("proxy_test_retrying", attempt=attempt, max_attempts=max_attempts)
                time.sleep(2)  # 重试前等待2秒

            success, error_msg, status_code = _perform_proxy_test_single(proxy_address, current_url)

            if success:
                return True, get_text("proxy_test_success", url=current_url)

            # 如果是503错误或超时，值得重试
            should_retry = (
                status_code == 503 or
                "timeout" in error_msg.lower() or
                "temporarily unavailable" in error_msg.lower()
            )

            if not should_retry:
                # 对于非临时性错误，不重试，直接尝试下一个URL
                logger.info(f"Non-retryable error for {current_url}: {error_msg}")
                break

            if attempt == max_attempts:
                logger.warning(f"All {max_attempts} attempts failed for {current_url}: {error_msg}")

    # 所有URL和重试都失败了
    return False, get_text("proxy_test_all_failed")

def _proxy_test_thread(proxy_addr: str, test_url: str):
    """在后台线程中执行代理测试"""
    try:
        success, message = _perform_proxy_test(proxy_addr, test_url)

        # 在主线程中更新GUI
        def update_gui():
            if success:
                messagebox.showinfo(get_text("info_title"), message, parent=root_widget)
                update_status_bar("proxy_test_success_status", url=test_url)
            else:
                messagebox.showerror(get_text("error_title"),
                                   get_text("proxy_test_failure", url=test_url, error=message),
                                   parent=root_widget)
                update_status_bar("proxy_test_failure_status", error=message)

        if root_widget:
            root_widget.after_idle(update_gui)

    except Exception as e:
        logger.error(f"Proxy test thread error: {e}", exc_info=True)
        def show_error():
            messagebox.showerror(get_text("error_title"),
                               f"代理测试过程中发生错误: {e}",
                               parent=root_widget)
            update_status_bar("proxy_test_failure_status", error=str(e))

        if root_widget:
            root_widget.after_idle(show_error)

def test_proxy_connectivity_gui():
    if not proxy_enabled_var.get() or not proxy_address_var.get().strip():
        messagebox.showwarning(get_text("warning_title"), get_text("proxy_not_enabled_warn"), parent=root_widget)
        return

    proxy_addr_to_test = proxy_address_var.get().strip()
    test_url = LANG_TEXTS["proxy_test_url_default"] # Use the default from LANG_TEXTS

    # 显示测试开始状态
    update_status_bar("proxy_testing_status", proxy_addr=proxy_addr_to_test)

    # 在后台线程中执行测试，避免阻塞GUI
    test_thread = threading.Thread(
        target=_proxy_test_thread,
        args=(proxy_addr_to_test, test_url),
        daemon=True
    )
    test_thread.start()

def stop_selected_pid_from_list_gui():
    if not pid_listbox_widget: return
    selected_indices = pid_listbox_widget.curselection()
    if not selected_indices:
        messagebox.showwarning(get_text("warning_title"), get_text("pid_list_empty_for_stop_warn"), parent=root_widget)
        return
    selected_text = pid_listbox_widget.get(selected_indices[0]).strip()
    pid_to_stop = -1
    process_name_to_stop = get_text("unknown_process_name_placeholder")
    try:
        # Check for "no process" entry first, as it's a known non-PID format
        no_process_indicator_zh = get_text("port_not_in_use_format", port_type="_", port_num="_").split("] ")[-1].strip()
        no_process_indicator_en = LANG_TEXTS["port_not_in_use_format"]["en"].split("] ")[-1].strip()
        general_no_pids_msg_zh = get_text("no_pids_found")
        general_no_pids_msg_en = LANG_TEXTS["no_pids_found"]["en"]

        is_no_process_entry = (no_process_indicator_zh in selected_text or \
                               no_process_indicator_en in selected_text or \
                               selected_text == general_no_pids_msg_zh or \
                               selected_text == general_no_pids_msg_en)
        if is_no_process_entry:
            logger.info(f"Selected item is a 'no process' entry: {selected_text}")
            return # Silently return for "no process" entries

        # Try to parse the format: "[Type - Port] PID - Name (Path)" or "PID - Name (Path)"
        # This regex will match either the detailed format or the simple "PID - Name" format
        # It's flexible enough to handle the optional leading "[...]" part
        match = re.match(r"^(?:\[[^\]]+\]\s*)?(\d+)\s*-\s*(.*)$", selected_text)
        if match:
            pid_to_stop = int(match.group(1))
            process_name_to_stop = match.group(2).strip()
        elif selected_text.isdigit(): # Handles if the listbox item is just a PID
            pid_to_stop = int(selected_text)
            # process_name_to_stop remains the default unknown
        else:
            # Genuine parsing error for an unexpected format
            messagebox.showerror(get_text("error_title"), get_text("error_parsing_pid", selection=selected_text), parent=root_widget)
            return
    except ValueError: # Catches int() conversion errors
        messagebox.showerror(get_text("error_title"), get_text("error_parsing_pid", selection=selected_text), parent=root_widget)
        return

    # If pid_to_stop is still -1 at this point, it means an unhandled case or logic error in parsing.
    # The returns above should prevent reaching here with pid_to_stop == -1 if it's an error or "no process".
    if pid_to_stop == -1:
        # This path implies a non-parsable string that wasn't identified as a "no process" message and didn't raise ValueError.
        logger.warning(f"PID parsing resulted in -1 for non-'no process' entry: {selected_text}. This indicates an unexpected format or logic gap.")
        messagebox.showerror(get_text("error_title"), get_text("error_parsing_pid", selection=selected_text), parent=root_widget)
        return
    if messagebox.askyesno(get_text("confirm_stop_pid_title"), get_text("confirm_stop_pid_message", pid=pid_to_stop, name=process_name_to_stop), parent=root_widget):
        normal_kill_success = kill_process_pid(pid_to_stop)
        if normal_kill_success:
            messagebox.showinfo(get_text("info_title"), get_text("terminate_request_sent", pid=pid_to_stop, name=process_name_to_stop), parent=root_widget)
        else:
            # 普通权限停止失败，询问是否尝试管理员权限
            if messagebox.askyesno(get_text("confirm_stop_pid_admin_title"), 
                               get_text("confirm_stop_pid_admin_message", pid=pid_to_stop, name=process_name_to_stop), 
                               parent=root_widget):
                admin_kill_success = kill_process_pid_admin(pid_to_stop)
                if admin_kill_success:
                    messagebox.showinfo(get_text("info_title"), get_text("admin_stop_success", pid=pid_to_stop), parent=root_widget)
                else:
                    messagebox.showwarning(get_text("warning_title"), get_text("admin_stop_failure", pid=pid_to_stop, error="未知错误"), parent=root_widget)
            else:
                messagebox.showwarning(get_text("warning_title"), get_text("terminate_attempt_failed", pid=pid_to_stop, name=process_name_to_stop), parent=root_widget)
        query_port_and_display_pids_gui()

def kill_process_pid_admin(pid: int) -> bool:
    """使用管理员权限尝试终止进程。"""
    system = platform.system()
    success = False
    logger.info(f"尝试以管理员权限终止进程 PID: {pid} (系统: {system})")
    try:
        if system == "Windows":
            # 在Windows上使用PowerShell以管理员权限运行taskkill
            import ctypes
            if ctypes.windll.shell32.IsUserAnAdmin() == 0:
                # 如果当前不是管理员，则尝试用管理员权限启动新进程
                # 准备 PowerShell 命令
                logger.info(f"当前非管理员权限，使用PowerShell提升权限")
                ps_cmd = f"Start-Process -Verb RunAs taskkill -ArgumentList '/PID {pid} /F /T'"
                logger.debug(f"执行PowerShell命令: {ps_cmd}")
                result = subprocess.run(["powershell", "-Command", ps_cmd], 
                                     capture_output=True, text=True, creationflags=subprocess.CREATE_NO_WINDOW)
                logger.info(f"PowerShell命令结果: 返回码={result.returncode}, 输出={result.stdout}, 错误={result.stderr}")
                success = result.returncode == 0
            else:
                # 如果已经是管理员，则直接运行taskkill
                logger.info(f"当前已是管理员权限，直接执行taskkill")
                result = subprocess.run(["taskkill", "/PID", str(pid), "/F", "/T"], 
                                     capture_output=True, text=True, creationflags=subprocess.CREATE_NO_WINDOW)
                logger.info(f"Taskkill命令结果: 返回码={result.returncode}, 输出={result.stdout}, 错误={result.stderr}")
                success = result.returncode == 0
        elif system in ["Linux", "Darwin"]:  # Linux或macOS
            # 使用sudo尝试终止进程
            logger.info(f"使用sudo在新终端中终止进程")
            cmd = ["sudo", "kill", "-9", str(pid)]
            # 对于GUI程序，我们需要让用户在终端输入密码，所以使用新终端窗口
            if system == "Darwin":  # macOS
                logger.info(f"在macOS上使用AppleScript打开Terminal并执行sudo命令")
                applescript = f'tell application "Terminal" to do script "sudo kill -9 {pid}"'
                result = subprocess.run(["osascript", "-e", applescript], capture_output=True, text=True)
                logger.info(f"AppleScript结果: 返回码={result.returncode}, 输出={result.stdout}, 错误={result.stderr}")
                success = result.returncode == 0
            else:  # Linux
                # 查找可用的终端模拟器
                import shutil
                logger.info(f"在Linux上查找可用的终端模拟器")
                terminal_emulator = shutil.which("x-terminal-emulator") or shutil.which("gnome-terminal") or \
                                   shutil.which("konsole") or shutil.which("xfce4-terminal") or shutil.which("xterm")
                if terminal_emulator:
                    logger.info(f"使用终端模拟器: {terminal_emulator}")
                    if "gnome-terminal" in terminal_emulator:
                        logger.info(f"针对gnome-terminal的特殊处理")
                        result = subprocess.run([terminal_emulator, "--", "sudo", "kill", "-9", str(pid)])
                    else:
                        logger.info(f"使用通用终端启动命令")
                        result = subprocess.run([terminal_emulator, "-e", f"sudo kill -9 {pid}"])
                    logger.info(f"终端命令结果: 返回码={result.returncode}")
                    success = result.returncode == 0
                else:
                    # 如果找不到终端模拟器，尝试直接使用sudo
                    logger.warning(f"未找到终端模拟器，尝试直接使用sudo (可能需要当前进程已有sudo权限)")
                    result = subprocess.run(["sudo", "kill", "-9", str(pid)], capture_output=True, text=True)
                    logger.info(f"直接sudo命令结果: 返回码={result.returncode}, 输出={result.stdout}, 错误={result.stderr}")
                    success = result.returncode == 0
    except Exception as e:
        logger.error(f"使用管理员权限终止PID {pid}时出错: {e}", exc_info=True)
        success = False
    
    logger.info(f"管理员权限终止进程 PID: {pid} 结果: {'成功' if success else '失败'}")
    return success

def kill_custom_pid_gui():
    if not custom_pid_entry_var or not root_widget: return
    pid_str = custom_pid_entry_var.get()
    if not pid_str:
        messagebox.showwarning(get_text("warning_title"), get_text("pid_input_empty_warn"), parent=root_widget)
        return
    if not pid_str.isdigit():
        messagebox.showwarning(get_text("warning_title"), get_text("pid_input_invalid_warn"), parent=root_widget)
        return
    pid_to_kill = int(pid_str)
    process_name_to_kill = get_process_name_by_pid(pid_to_kill)
    confirm_msg = get_text("confirm_stop_pid_message", pid=pid_to_kill, name=process_name_to_kill)
    if messagebox.askyesno(get_text("confirm_kill_custom_pid_title"), confirm_msg, parent=root_widget):
        normal_kill_success = kill_process_pid(pid_to_kill)
        if normal_kill_success:
            messagebox.showinfo(get_text("info_title"), get_text("terminate_request_sent", pid=pid_to_kill, name=process_name_to_kill), parent=root_widget)
        else:
            # 普通权限停止失败，询问是否尝试管理员权限
            if messagebox.askyesno(get_text("confirm_stop_pid_admin_title"), 
                                get_text("confirm_stop_pid_admin_message", pid=pid_to_kill, name=process_name_to_kill), 
                                parent=root_widget):
                admin_kill_success = kill_process_pid_admin(pid_to_kill)
                if admin_kill_success:
                    messagebox.showinfo(get_text("info_title"), get_text("admin_stop_success", pid=pid_to_kill), parent=root_widget)
                else:
                    messagebox.showwarning(get_text("warning_title"), get_text("admin_stop_failure", pid=pid_to_kill, error="未知错误"), parent=root_widget)
            else:
                messagebox.showwarning(get_text("warning_title"), get_text("terminate_attempt_failed", pid=pid_to_kill, name=process_name_to_kill), parent=root_widget)
        custom_pid_entry_var.set("")
        query_port_and_display_pids_gui()

menu_bar_ref: Optional[tk.Menu] = None

def update_all_ui_texts_gui():
    if not root_widget: return
    root_widget.title(get_text("title"))
    for item in widgets_to_translate:
        widget = item["widget"]
        key = item["key"]
        prop = item.get("property", "text")
        text_val = get_text(key, **item.get("kwargs", {}))
        if hasattr(widget, 'config'):
            try: widget.config(**{prop: text_val})
            except tk.TclError: pass
    current_status_text = process_status_text_var.get() if process_status_text_var else ""
    is_idle_status = any(current_status_text == LANG_TEXTS["status_idle"].get(lang_code, "") for lang_code in LANG_TEXTS["status_idle"])
    if is_idle_status: update_status_bar("status_idle")

def switch_language_gui(lang_code: str):
    global current_language
    if lang_code in LANG_TEXTS["title"]:
        current_language = lang_code
        update_all_ui_texts_gui()

def build_gui(root: tk.Tk):
    global process_status_text_var, port_entry_var, camoufox_debug_port_var, pid_listbox_widget, widgets_to_translate, managed_process_info, root_widget, menu_bar_ref, custom_pid_entry_var
    global stream_port_enabled_var, stream_port_var, helper_enabled_var, helper_endpoint_var, port_auto_check_var, proxy_address_var, proxy_enabled_var
    global active_auth_file_display_var # 添加新的全局变量
    global pid_list_lbl_frame_ref # 确保全局变量在此处声明

    root_widget = root
    root.title(get_text("title"))
    root.minsize(950, 600) 

    # 加载保存的配置
    config = load_config()

    s = ttk.Style()
    s.configure('TButton', padding=3)
    s.configure('TLabelFrame.Label', font=('Default', 10, 'bold'))
    s.configure('TLabelFrame', padding=4)
    try:
        os.makedirs(ACTIVE_AUTH_DIR, exist_ok=True)
        os.makedirs(SAVED_AUTH_DIR, exist_ok=True)
    except OSError as e:
        messagebox.showerror(get_text("error_title"), f"无法创建认证目录: {e}")

    process_status_text_var = tk.StringVar(value=get_text("status_idle"))
    port_entry_var = tk.StringVar(value=str(config.get("fastapi_port", DEFAULT_FASTAPI_PORT)))
    camoufox_debug_port_var = tk.StringVar(value=str(config.get("camoufox_debug_port", DEFAULT_CAMOUFOX_PORT_GUI)))
    custom_pid_entry_var = tk.StringVar()
    stream_port_enabled_var = tk.BooleanVar(value=config.get("stream_port_enabled", True))
    stream_port_var = tk.StringVar(value=str(config.get("stream_port", "3120")))
    helper_enabled_var = tk.BooleanVar(value=config.get("helper_enabled", False))
    helper_endpoint_var = tk.StringVar(value=config.get("helper_endpoint", ""))
    port_auto_check_var = tk.BooleanVar(value=True)
    proxy_address_var = tk.StringVar(value=config.get("proxy_address", "http://127.0.0.1:7890"))
    proxy_enabled_var = tk.BooleanVar(value=config.get("proxy_enabled", False))
    active_auth_file_display_var = tk.StringVar() # 初始化为空，后续由 _update_active_auth_display 更新

    # 联动逻辑：移除强制启用代理的逻辑，现在代理配置更加灵活
    # 用户可以根据需要独立配置流式代理和浏览器代理
    def on_stream_proxy_toggle(*args):
        # 不再强制启用代理，用户可以自由选择
        pass
    stream_port_enabled_var.trace_add("write", on_stream_proxy_toggle)


    menu_bar_ref = tk.Menu(root)
    lang_menu = tk.Menu(menu_bar_ref, tearoff=0)
    lang_menu.add_command(label="中文 (Chinese)", command=lambda: switch_language_gui('zh'))
    lang_menu.add_command(label="English", command=lambda: switch_language_gui('en'))
    menu_bar_ref.add_cascade(label="Language", menu=lang_menu)
    root.config(menu=menu_bar_ref)

    # --- 主 PanedWindow 实现三栏 --- 
    main_paned_window = ttk.PanedWindow(root, orient=tk.HORIZONTAL)
    main_paned_window.pack(fill=tk.BOTH, expand=True, padx=5, pady=5)

    # --- 左栏 Frame --- 
    left_frame_container = ttk.Frame(main_paned_window, padding="5")
    main_paned_window.add(left_frame_container, weight=3) # 增大左栏初始权重
    left_frame_container.columnconfigure(0, weight=1)
    # 配置行权重，使得launch_options_frame和auth_section之间可以有空白，或者让它们紧凑排列
    # 假设 port_section, launch_options_frame, auth_section 依次排列
    left_frame_container.rowconfigure(0, weight=0) # port_section
    left_frame_container.rowconfigure(1, weight=0) # launch_options_frame
    left_frame_container.rowconfigure(2, weight=0) # auth_section (移到此处后)
    left_frame_container.rowconfigure(3, weight=1) # 添加一个占位符Frame，使其填充剩余空间

    left_current_row = 0
    # 端口配置部分
    port_section = ttk.LabelFrame(left_frame_container, text="")
    port_section.grid(row=left_current_row, column=0, sticky="ew", padx=2, pady=(2,10))
    widgets_to_translate.append({"widget": port_section, "key": "port_section_label", "property": "text"})
    left_current_row += 1
    
    # 添加重置按钮和服务关闭指南按钮
    port_controls_frame = ttk.Frame(port_section)
    port_controls_frame.pack(fill=tk.X, padx=5, pady=3)
    btn_reset = ttk.Button(port_controls_frame, text="", command=reset_to_defaults)
    btn_reset.pack(side=tk.LEFT, padx=(0,5))
    widgets_to_translate.append({"widget": btn_reset, "key": "reset_button"})
    
    btn_closing_guide = ttk.Button(port_controls_frame, text="", command=show_service_closing_guide)
    btn_closing_guide.pack(side=tk.RIGHT, padx=(5,0))
    widgets_to_translate.append({"widget": btn_closing_guide, "key": "service_closing_guide_btn"})
    
    # (内部控件保持在port_section中，使用pack使其紧凑)
    # FastAPI Port
    fastapi_frame = ttk.Frame(port_section)
    fastapi_frame.pack(fill=tk.X, padx=5, pady=3)
    lbl_port = ttk.Label(fastapi_frame, text="")
    lbl_port.pack(side=tk.LEFT, padx=(0,5))
    widgets_to_translate.append({"widget": lbl_port, "key": "fastapi_port_label"})
    entry_port = ttk.Entry(fastapi_frame, textvariable=port_entry_var, width=12)
    entry_port.pack(side=tk.LEFT, expand=True, fill=tk.X)
    # Camoufox Debug Port
    camoufox_frame = ttk.Frame(port_section)
    camoufox_frame.pack(fill=tk.X, padx=5, pady=3)
    lbl_camoufox_debug_port = ttk.Label(camoufox_frame, text="")
    lbl_camoufox_debug_port.pack(side=tk.LEFT, padx=(0,5))
    widgets_to_translate.append({"widget": lbl_camoufox_debug_port, "key": "camoufox_debug_port_label"})
    entry_camoufox_debug_port = ttk.Entry(camoufox_frame, textvariable=camoufox_debug_port_var, width=12)
    entry_camoufox_debug_port.pack(side=tk.LEFT, expand=True, fill=tk.X)
    # Stream Proxy Port
    stream_port_frame_outer = ttk.Frame(port_section)
    stream_port_frame_outer.pack(fill=tk.X, padx=5, pady=3)
    stream_port_checkbox = ttk.Checkbutton(stream_port_frame_outer, variable=stream_port_enabled_var, text="")
    stream_port_checkbox.pack(side=tk.LEFT, padx=(0,2))
    widgets_to_translate.append({"widget": stream_port_checkbox, "key": "enable_stream_proxy_label", "property": "text"})
    stream_port_details_frame = ttk.Frame(stream_port_frame_outer)
    stream_port_details_frame.pack(side=tk.LEFT, fill=tk.X, expand=True)
    lbl_stream_port = ttk.Label(stream_port_details_frame, text="")
    lbl_stream_port.pack(side=tk.LEFT, padx=(0,5))
    widgets_to_translate.append({"widget": lbl_stream_port, "key": "stream_proxy_port_label"})
    entry_stream_port = ttk.Entry(stream_port_details_frame, textvariable=stream_port_var, width=10)
    entry_stream_port.pack(side=tk.LEFT, expand=True, fill=tk.X)
    # Helper Service
    helper_frame_outer = ttk.Frame(port_section)
    helper_frame_outer.pack(fill=tk.X, padx=5, pady=3)
    helper_checkbox = ttk.Checkbutton(helper_frame_outer, variable=helper_enabled_var, text="")
    helper_checkbox.pack(side=tk.LEFT, padx=(0,2))
    widgets_to_translate.append({"widget": helper_checkbox, "key": "enable_helper_label", "property": "text"})
    helper_details_frame = ttk.Frame(helper_frame_outer)
    helper_details_frame.pack(side=tk.LEFT, fill=tk.X, expand=True)
    lbl_helper_endpoint = ttk.Label(helper_details_frame, text="")
    lbl_helper_endpoint.pack(side=tk.LEFT, padx=(0,5))
    widgets_to_translate.append({"widget": lbl_helper_endpoint, "key": "helper_endpoint_label"})
    entry_helper_endpoint = ttk.Entry(helper_details_frame, textvariable=helper_endpoint_var)
    entry_helper_endpoint.pack(side=tk.LEFT, fill=tk.X, expand=True)
    
    # 添加分隔符
    ttk.Separator(port_section, orient=tk.HORIZONTAL).pack(fill=tk.X, padx=5, pady=(8,5))

    # 代理配置部分 - 独立的LabelFrame
    proxy_section = ttk.LabelFrame(port_section, text="")
    proxy_section.pack(fill=tk.X, padx=5, pady=(5,8))
    widgets_to_translate.append({"widget": proxy_section, "key": "proxy_section_label", "property": "text"})

    # 代理启用复选框
    proxy_enable_frame = ttk.Frame(proxy_section)
    proxy_enable_frame.pack(fill=tk.X, padx=5, pady=(5,3))
    proxy_checkbox = ttk.Checkbutton(proxy_enable_frame, variable=proxy_enabled_var, text="")
    proxy_checkbox.pack(side=tk.LEFT)
    widgets_to_translate.append({"widget": proxy_checkbox, "key": "enable_proxy_label", "property": "text"})

    # 代理地址输入
    proxy_address_frame = ttk.Frame(proxy_section)
    proxy_address_frame.pack(fill=tk.X, padx=5, pady=(0,5))
    lbl_proxy_address = ttk.Label(proxy_address_frame, text="")
    lbl_proxy_address.pack(side=tk.LEFT, padx=(0,5))
    widgets_to_translate.append({"widget": lbl_proxy_address, "key": "proxy_address_label"})
    entry_proxy_address = ttk.Entry(proxy_address_frame, textvariable=proxy_address_var)
    entry_proxy_address.pack(side=tk.LEFT, expand=True, fill=tk.X, padx=(0,5))

    # 代理测试按钮
    btn_test_proxy_inline = ttk.Button(proxy_address_frame, text="", command=test_proxy_connectivity_gui, width=8)
    btn_test_proxy_inline.pack(side=tk.RIGHT)
    widgets_to_translate.append({"widget": btn_test_proxy_inline, "key": "test_proxy_btn"})
    
    # Port auto check
    port_auto_check_frame = ttk.Frame(port_section)
    port_auto_check_frame.pack(fill=tk.X, padx=5, pady=3)
    port_auto_check_btn = ttk.Checkbutton(port_auto_check_frame, variable=port_auto_check_var, text="")
    port_auto_check_btn.pack(side=tk.LEFT)
    widgets_to_translate.append({"widget": port_auto_check_btn, "key": "port_auto_check", "property": "text"})

    # 启动选项部分
    launch_options_frame = ttk.LabelFrame(left_frame_container, text="")
    launch_options_frame.grid(row=left_current_row, column=0, sticky="ew", padx=2, pady=5)
    widgets_to_translate.append({"widget": launch_options_frame, "key": "launch_options_label", "property": "text"})
    left_current_row += 1
    lbl_launch_options_note = ttk.Label(launch_options_frame, text="", wraplength=240) # 调整wraplength
    lbl_launch_options_note.pack(fill=tk.X, padx=5, pady=(5, 8))
    widgets_to_translate.append({"widget": lbl_launch_options_note, "key": "launch_options_note_revised"})
    # (启动按钮)
    btn_headed = ttk.Button(launch_options_frame, text="", command=start_headed_interactive_gui)
    btn_headed.pack(fill=tk.X, padx=5, pady=3)
    widgets_to_translate.append({"widget": btn_headed, "key": "launch_headed_interactive_btn"})
    btn_headless = ttk.Button(launch_options_frame, text="", command=start_headless_gui) # command 和 key 修改
    btn_headless.pack(fill=tk.X, padx=5, pady=3)
    widgets_to_translate.append({"widget": btn_headless, "key": "launch_headless_btn"}) # key 修改
    btn_virtual_display = ttk.Button(launch_options_frame, text="", command=start_virtual_display_gui)
    btn_virtual_display.pack(fill=tk.X, padx=5, pady=3)
    widgets_to_translate.append({"widget": btn_virtual_display, "key": "launch_virtual_display_btn"})
    if platform.system() != "Linux":
        btn_virtual_display.state(['disabled'])

    # Separator for LLM service buttons
    ttk.Separator(launch_options_frame, orient=tk.HORIZONTAL).pack(fill=tk.X, padx=5, pady=(8,5))

    # LLM Service Buttons
    btn_start_llm_service = ttk.Button(launch_options_frame, text="", command=start_llm_service_gui)
    btn_start_llm_service.pack(fill=tk.X, padx=5, pady=3)
    widgets_to_translate.append({"widget": btn_start_llm_service, "key": "launch_llm_service_btn"})

    btn_stop_llm_service = ttk.Button(launch_options_frame, text="", command=stop_llm_service_gui)
    btn_stop_llm_service.pack(fill=tk.X, padx=5, pady=3)
    widgets_to_translate.append({"widget": btn_stop_llm_service, "key": "stop_llm_service_btn"})
    
    # 移除不再有用的"停止当前GUI管理的服务"按钮
    # btn_stop_service = ttk.Button(launch_options_frame, text="", command=stop_managed_service_gui)
    # btn_stop_service.pack(fill=tk.X, padx=5, pady=3)
    # widgets_to_translate.append({"widget": btn_stop_service, "key": "stop_gui_service_btn"})
    


    # 添加一个占位符Frame以推高左侧内容 (如果需要消除底部所有空白)
    spacer_frame_left = ttk.Frame(left_frame_container)
    spacer_frame_left.grid(row=left_current_row, column=0, sticky="nsew")
    left_frame_container.rowconfigure(left_current_row, weight=1) # 让这个spacer扩展

    # --- 中栏 Frame --- 
    middle_frame_container = ttk.Frame(main_paned_window, padding="5")
    main_paned_window.add(middle_frame_container, weight=2) # 调整中栏初始权重
    middle_frame_container.columnconfigure(0, weight=1)
    middle_frame_container.rowconfigure(0, weight=1)
    middle_frame_container.rowconfigure(1, weight=0)
    middle_frame_container.rowconfigure(2, weight=0) # 认证管理现在在中栏

    middle_current_row = 0
    pid_section_frame = ttk.Frame(middle_frame_container)
    pid_section_frame.grid(row=middle_current_row, column=0, sticky="nsew", padx=2, pady=2)
    pid_section_frame.columnconfigure(0, weight=1)
    pid_section_frame.rowconfigure(0, weight=1) 
    middle_current_row +=1
    
    global pid_list_lbl_frame_ref 
    pid_list_lbl_frame_ref = ttk.LabelFrame(pid_section_frame, text=get_text("static_pid_list_title")) # 使用新的固定标题
    pid_list_lbl_frame_ref.grid(row=0, column=0, columnspan=2, sticky="nsew", padx=2, pady=2)
    pid_list_lbl_frame_ref.columnconfigure(0, weight=1)
    pid_list_lbl_frame_ref.rowconfigure(0, weight=1)
    pid_listbox_widget = tk.Listbox(pid_list_lbl_frame_ref, height=4, exportselection=False)
    pid_listbox_widget.grid(row=0, column=0, padx=5, pady=5, sticky="nsew")
    scrollbar = ttk.Scrollbar(pid_list_lbl_frame_ref, orient="vertical", command=pid_listbox_widget.yview)
    scrollbar.grid(row=0, column=1, sticky="ns", padx=(0,5), pady=5)
    pid_listbox_widget.config(yscrollcommand=scrollbar.set)

    pid_buttons_frame = ttk.Frame(pid_section_frame)
    pid_buttons_frame.grid(row=1, column=0, columnspan=2, sticky="ew", pady=(5,2))
    pid_buttons_frame.columnconfigure(0, weight=1)
    pid_buttons_frame.columnconfigure(1, weight=1)
    btn_query = ttk.Button(pid_buttons_frame, text="", command=query_port_and_display_pids_gui)
    btn_query.grid(row=0, column=0, sticky="ew", padx=(0,2))
    widgets_to_translate.append({"widget": btn_query, "key": "query_pids_btn"})
    btn_stop_pid = ttk.Button(pid_buttons_frame, text="", command=stop_selected_pid_from_list_gui)
    btn_stop_pid.grid(row=0, column=1, sticky="ew", padx=(2,0))
    widgets_to_translate.append({"widget": btn_stop_pid, "key": "stop_selected_pid_btn"})
    
    # 代理测试按钮已移至代理配置部分，此处不再重复

    kill_custom_frame = ttk.LabelFrame(middle_frame_container, text="")
    kill_custom_frame.grid(row=middle_current_row, column=0, sticky="ew", padx=2, pady=5)
    widgets_to_translate.append({"widget": kill_custom_frame, "key": "kill_custom_pid_label", "property":"text"})
    middle_current_row += 1
    kill_custom_frame.columnconfigure(0, weight=1)
    entry_custom_pid = ttk.Entry(kill_custom_frame, textvariable=custom_pid_entry_var, width=10)
    entry_custom_pid.pack(side=tk.LEFT, padx=5, pady=5, fill=tk.X, expand=True)
    btn_kill_custom_pid = ttk.Button(kill_custom_frame, text="", command=kill_custom_pid_gui)
    btn_kill_custom_pid.pack(side=tk.LEFT, padx=5, pady=5)
    widgets_to_translate.append({"widget": btn_kill_custom_pid, "key": "kill_custom_pid_btn"})

    # 认证文件管理 (移到中栏PID终止功能下方)
    auth_section_middle = ttk.LabelFrame(middle_frame_container, text="")
    auth_section_middle.grid(row=middle_current_row, column=0, sticky="ew", padx=2, pady=5)
    widgets_to_translate.append({"widget": auth_section_middle, "key": "auth_files_management", "property": "text"})
    middle_current_row += 1
    btn_manage_auth_middle = ttk.Button(auth_section_middle, text="", command=manage_auth_files_gui)
    btn_manage_auth_middle.pack(fill=tk.X, padx=5, pady=5)
    widgets_to_translate.append({"widget": btn_manage_auth_middle, "key": "manage_auth_files_btn"})

    # 显示当前认证文件
    auth_display_frame = ttk.Frame(auth_section_middle)
    auth_display_frame.pack(fill=tk.X, padx=5, pady=(0,5))
    lbl_current_auth_static = ttk.Label(auth_display_frame, text="")
    lbl_current_auth_static.pack(side=tk.LEFT)
    widgets_to_translate.append({"widget": lbl_current_auth_static, "key": "current_auth_file_display_label"})
    lbl_current_auth_dynamic = ttk.Label(auth_display_frame, textvariable=active_auth_file_display_var, wraplength=180)
    lbl_current_auth_dynamic.pack(side=tk.LEFT, fill=tk.X, expand=True)

    # --- 右栏 Frame --- 
    right_frame_container = ttk.Frame(main_paned_window, padding="5")
    main_paned_window.add(right_frame_container, weight=2) # 调整右栏初始权重，使其相对小一些
    right_frame_container.columnconfigure(0, weight=1)
    right_frame_container.rowconfigure(1, weight=1) 
    right_current_row = 0
    status_area_frame = ttk.LabelFrame(right_frame_container, text="")
    status_area_frame.grid(row=right_current_row, column=0, padx=2, pady=2, sticky="ew")
    widgets_to_translate.append({"widget": status_area_frame, "key": "status_label", "property": "text"})
    right_current_row += 1
    lbl_status_val = ttk.Label(status_area_frame, textvariable=process_status_text_var, wraplength=280)
    lbl_status_val.pack(fill=tk.X, padx=5, pady=5)
    def rewrap_status_label(event=None):
        if root_widget and lbl_status_val.winfo_exists():
            new_width = status_area_frame.winfo_width() - 20
            if new_width > 100: lbl_status_val.config(wraplength=new_width)
    status_area_frame.bind("<Configure>", rewrap_status_label)

    output_log_area_frame = ttk.LabelFrame(right_frame_container, text="")
    output_log_area_frame.grid(row=right_current_row, column=0, padx=2, pady=2, sticky="nsew")
    widgets_to_translate.append({"widget": output_log_area_frame, "key": "output_label", "property": "text"})
    output_log_area_frame.columnconfigure(0, weight=1)
    output_log_area_frame.rowconfigure(0, weight=1)
    output_scrolled_text = scrolledtext.ScrolledText(output_log_area_frame, height=10, width=35, wrap=tk.WORD, state=tk.DISABLED) # 调整宽度
    output_scrolled_text.grid(row=0, column=0, padx=5, pady=5, sticky="nsew")
    managed_process_info["output_area"] = output_scrolled_text

    update_all_ui_texts_gui()
    query_port_and_display_pids_gui() # 初始化时查询一次FastAPI端口
    _update_active_auth_display() # 初始化时更新认证文件显示
    root.protocol("WM_DELETE_WINDOW", on_app_close_main)

pid_list_lbl_frame_ref: Optional[ttk.LabelFrame] = None

# 新增辅助函数用于获取和验证启动参数
def _get_launch_parameters() -> Optional[Dict[str, Any]]:
    """从GUI收集并验证启动参数。如果无效则返回None。"""
    params = {}
    try:
        params["fastapi_port"] = get_fastapi_port_from_gui()
        params["camoufox_debug_port"] = get_camoufox_debug_port_from_gui()
        
        params["stream_port_enabled"] = stream_port_enabled_var.get()
        sp_val_str = stream_port_var.get().strip()
        if params["stream_port_enabled"]:
            params["stream_port"] = int(sp_val_str) if sp_val_str else 3120
            if not (params["stream_port"] == 0 or 1024 <= params["stream_port"] <= 65535):
                messagebox.showwarning(get_text("warning_title"), get_text("stream_port_out_of_range"))
                return None
        else:
            params["stream_port"] = 0 # 如果未启用，则端口视为0（禁用）
            
        params["helper_enabled"] = helper_enabled_var.get()
        params["helper_endpoint"] = helper_endpoint_var.get().strip() if params["helper_enabled"] else ""
        
        return params
    except ValueError: # 通常来自 int() 转换失败
        messagebox.showwarning(get_text("warning_title"), get_text("enter_valid_port_warn")) # 或者更具体的错误
        return None
    except Exception as e:
        messagebox.showerror(get_text("error_title"), f"获取启动参数时出错: {e}")
        return None

# 更新on_app_close_main函数，反映服务独立性
def on_app_close_main():
    # 保存当前配置
    save_config()

    # Attempt to stop LLM service if it's running
    if is_llm_service_running():
        logger.info("LLM service is running. Attempting to stop it before exiting GUI.")
        # We can call stop_llm_service_gui directly, but it shows a confirmation.
        # For closing, we might want a more direct stop or a specific "closing" stop.
        # For now, let's try a direct stop without user confirmation for this specific path.
        popen = llm_service_process_info.get("popen")
        service_name = get_text(llm_service_process_info.get("service_name_key", "llm_service_name_key"))
        if popen:
            try:
                logger.info(f"Sending SIGINT to {service_name} (PID: {popen.pid}) during app close.")
                if platform.system() == "Windows":
                    popen.terminate() # TerminateProcess on Windows
                else:
                    popen.send_signal(signal.SIGINT)
                
                # Give it a very short time to exit, don't block GUI closing for too long
                popen.wait(timeout=1.5)
                logger.info(f"{service_name} (PID: {popen.pid}) hopefully stopped during app close.")
            except subprocess.TimeoutExpired:
                logger.warning(f"{service_name} (PID: {popen.pid}) did not stop quickly during app close. May need manual cleanup.")
                popen.kill() # Force kill if it didn't stop
            except Exception as e:
                logger.error(f"Error stopping {service_name} during app close: {e}")
            finally:
                llm_service_process_info["popen"] = None # Clear it
    
    # 服务都是在独立终端中启动的，所以只需确认用户是否想关闭GUI
    if messagebox.askyesno(get_text("confirm_quit_title"), get_text("confirm_quit_message"), parent=root_widget):
        if root_widget:
            root_widget.destroy()

def show_service_closing_guide():
    messagebox.showinfo(
        get_text("service_closing_guide"),
        get_text("service_closing_guide_message"),
        parent=root_widget
    )

if __name__ == "__main__":
    if not os.path.exists(LAUNCH_CAMOUFOX_PY) or not os.path.exists(os.path.join(SCRIPT_DIR, SERVER_PY_FILENAME)):
        err_lang = current_language
        err_title_key = "startup_error_title"
        err_msg_key = "startup_script_not_found_msgbox"
        err_title = LANG_TEXTS[err_title_key].get(err_lang, LANG_TEXTS[err_title_key]['en'])
        err_msg_template = LANG_TEXTS[err_msg_key].get(err_lang, LANG_TEXTS[err_msg_key]['en'])
        err_msg = err_msg_template.format(script=f"{os.path.basename(LAUNCH_CAMOUFOX_PY)} or {SERVER_PY_FILENAME}")
        try:
            root_err = tk.Tk(); root_err.withdraw()
            messagebox.showerror(err_title, err_msg, parent=None)
            root_err.destroy()
        except tk.TclError:
            print(f"ERROR: {err_msg}", file=sys.stderr)
        sys.exit(1)
    app_root = tk.Tk()
    build_gui(app_root)
    app_root.mainloop()