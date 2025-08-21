# --- browser_utils/script_manager.py ---
# 油猴脚本管理模块 - 动态挂载和注入脚本功能

import os
import json
import logging
from typing import Dict, List, Optional, Any
from playwright.async_api import Page as AsyncPage

logger = logging.getLogger("AIStudioProxyServer")

class ScriptManager:
    """油猴脚本管理器 - 负责动态加载和注入脚本"""
    
    def __init__(self, script_dir: str = "browser_utils"):
        self.script_dir = script_dir
        self.loaded_scripts: Dict[str, str] = {}
        self.model_configs: Dict[str, List[Dict[str, Any]]] = {}
        
    def load_script(self, script_name: str) -> Optional[str]:
        """加载指定的JavaScript脚本文件"""
        script_path = os.path.join(self.script_dir, script_name)
        
        if not os.path.exists(script_path):
            logger.error(f"脚本文件不存在: {script_path}")
            return None
            
        try:
            with open(script_path, 'r', encoding='utf-8') as f:
                script_content = f.read()
                self.loaded_scripts[script_name] = script_content
                logger.info(f"成功加载脚本: {script_name}")
                return script_content
        except Exception as e:
            logger.error(f"加载脚本失败 {script_name}: {e}")
            return None
    
    def load_model_config(self, config_path: str) -> Optional[List[Dict[str, Any]]]:
        """加载模型配置文件"""
        if not os.path.exists(config_path):
            logger.warning(f"模型配置文件不存在: {config_path}")
            return None
            
        try:
            with open(config_path, 'r', encoding='utf-8') as f:
                config_data = json.load(f)
                models = config_data.get('models', [])
                self.model_configs[config_path] = models
                logger.info(f"成功加载模型配置: {len(models)} 个模型")
                return models
        except Exception as e:
            logger.error(f"加载模型配置失败 {config_path}: {e}")
            return None
    
    def generate_dynamic_script(self, base_script: str, models: List[Dict[str, Any]], 
                              script_version: str = "dynamic") -> str:
        """基于模型配置动态生成脚本内容"""
        try:
            # 构建模型列表的JavaScript代码
            models_js = "const MODELS_TO_INJECT = [\n"
            for model in models:
                name = model.get('name', '')
                display_name = model.get('displayName', model.get('display_name', ''))
                description = model.get('description', f'Model injected by script {script_version}')
                
                # 如果displayName中没有包含版本信息，添加版本信息
                if f"(Script {script_version})" not in display_name:
                    display_name = f"{display_name} (Script {script_version})"
                
                models_js += f"""       {{
          name: '{name}',
          displayName: `{display_name}`,
          description: `{description}`
       }},\n"""
            
            models_js += "    ];"
            
            # 替换脚本中的模型定义部分
            # 查找模型定义的开始和结束标记
            start_marker = "const MODELS_TO_INJECT = ["
            end_marker = "];"
            
            start_idx = base_script.find(start_marker)
            if start_idx == -1:
                logger.error("未找到模型定义开始标记")
                return base_script
                
            # 找到对应的结束标记
            bracket_count = 0
            end_idx = start_idx + len(start_marker)
            found_end = False
            
            for i in range(end_idx, len(base_script)):
                if base_script[i] == '[':
                    bracket_count += 1
                elif base_script[i] == ']':
                    if bracket_count == 0:
                        end_idx = i + 1
                        found_end = True
                        break
                    bracket_count -= 1
            
            if not found_end:
                logger.error("未找到模型定义结束标记")
                return base_script
            
            # 替换模型定义部分
            new_script = (base_script[:start_idx] + 
                         models_js + 
                         base_script[end_idx:])
            
            # 更新版本号
            new_script = new_script.replace(
                f'const SCRIPT_VERSION = "v1.6";',
                f'const SCRIPT_VERSION = "{script_version}";'
            )
            
            logger.info(f"成功生成动态脚本，包含 {len(models)} 个模型")
            return new_script
            
        except Exception as e:
            logger.error(f"生成动态脚本失败: {e}")
            return base_script
    
    async def inject_script_to_page(self, page: AsyncPage, script_content: str, 
                                  script_name: str = "injected_script") -> bool:
        """将脚本注入到页面中"""
        try:
            # 移除UserScript头部信息，因为我们是直接注入而不是通过油猴
            cleaned_script = self._clean_userscript_headers(script_content)
            
            # 注入脚本
            await page.add_init_script(cleaned_script)
            logger.info(f"成功注入脚本到页面: {script_name}")
            return True
            
        except Exception as e:
            logger.error(f"注入脚本到页面失败 {script_name}: {e}")
            return False
    
    def _clean_userscript_headers(self, script_content: str) -> str:
        """清理UserScript头部信息"""
        lines = script_content.split('\n')
        cleaned_lines = []
        in_userscript_block = False
        
        for line in lines:
            if line.strip().startswith('// ==UserScript=='):
                in_userscript_block = True
                continue
            elif line.strip().startswith('// ==/UserScript=='):
                in_userscript_block = False
                continue
            elif in_userscript_block:
                continue
            else:
                cleaned_lines.append(line)
        
        return '\n'.join(cleaned_lines)
    
    async def setup_model_injection(self, page: AsyncPage,
                                  script_name: str = "more_modles.js") -> bool:
        """设置模型注入 - 直接注入油猴脚本"""

        # 检查脚本文件是否存在
        script_path = os.path.join(self.script_dir, script_name)
        if not os.path.exists(script_path):
            # 脚本文件不存在，静默跳过注入
            return False

        logger.info("开始设置模型注入...")

        # 加载油猴脚本
        script_content = self.load_script(script_name)
        if not script_content:
            return False

        # 直接注入原始脚本（不修改内容）
        return await self.inject_script_to_page(page, script_content, script_name)


# 全局脚本管理器实例
script_manager = ScriptManager()
