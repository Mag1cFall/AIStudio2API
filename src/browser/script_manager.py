import json
import logging
import os
from typing import Any, Dict, List, Optional

from playwright.async_api import Page as AsyncPage

log = logging.getLogger('AIStudioProxyServer')


class ScriptManager:

    def __init__(self, base_dir: str = 'browser'):
        self.base_dir = base_dir
        self.cache: Dict[str, str] = {}
        self.models: Dict[str, List[Dict[str, Any]]] = {}

    def load_script(self, filename: str) -> Optional[str]:
        path = os.path.join(self.base_dir, filename)
        if not os.path.exists(path):
            log.error(f'脚本文件不存在: {path}')
            return None
        try:
            with open(path, 'r', encoding='utf-8') as f:
                content = f.read()
                self.cache[filename] = content
                log.info(f'成功加载脚本: {filename}')
                return content
        except Exception as e:
            log.error(f'加载脚本失败 {filename}: {e}')
            return None

    def load_model_config(self, path: str) -> Optional[List[Dict[str, Any]]]:
        if not os.path.exists(path):
            log.warning(f'模型配置文件不存在: {path}')
            return None
        try:
            with open(path, 'r', encoding='utf-8') as f:
                data = json.load(f)
                items = data.get('models', [])
                self.models[path] = items
                log.info(f'成功加载模型配置: {len(items)} 个模型')
                return items
        except Exception as e:
            log.error(f'加载模型配置失败 {path}: {e}')
            return None

    def generate_dynamic_script(
        self,
        base: str,
        model_list: List[Dict[str, Any]],
        version: str = 'dynamic'
    ) -> str:
        try:
            js_models = 'const MODELS_TO_INJECT = [\n'
            for m in model_list:
                name = m.get('name', '')
                display = m.get('displayName', m.get('display_name', ''))
                desc = m.get('description', f'Model injected by script {version}')
                if f'(Script {version})' not in display:
                    display = f'{display} (Script {version})'
                js_models += f"       {{\n          name: '{name}',\n          displayName: `{display}`,\n          description: `{desc}`\n       }},\n"
            js_models += '    ];'

            marker = 'const MODELS_TO_INJECT = ['
            start = base.find(marker)
            if start == -1:
                log.error('未找到模型定义开始标记')
                return base

            depth = 0
            end = start + len(marker)
            found = False
            for i in range(end, len(base)):
                if base[i] == '[':
                    depth += 1
                elif base[i] == ']':
                    if depth == 0:
                        end = i + 1
                        found = True
                        break
                    depth -= 1

            if not found:
                log.error('未找到模型定义结束标记')
                return base

            result = base[:start] + js_models + base[end:]
            result = result.replace('const SCRIPT_VERSION = "v1.6";', f'const SCRIPT_VERSION = "{version}";')
            log.info(f'成功生成动态脚本，包含 {len(model_list)} 个模型')
            return result
        except Exception as e:
            log.error(f'生成动态脚本失败: {e}')
            return base

    async def inject_script_to_page(
        self,
        page: AsyncPage,
        content: str,
        name: str = 'injected_script'
    ) -> bool:
        try:
            cleaned = self._strip_userscript_header(content)
            await page.add_init_script(cleaned)
            log.info(f'成功注入脚本到页面: {name}')
            return True
        except Exception as e:
            log.error(f'注入脚本到页面失败 {name}: {e}')
            return False

    def _strip_userscript_header(self, content: str) -> str:
        lines = content.split('\n')
        result = []
        in_header = False
        for line in lines:
            stripped = line.strip()
            if stripped.startswith('// ==UserScript=='):
                in_header = True
                continue
            elif stripped.startswith('// ==/UserScript=='):
                in_header = False
                continue
            elif in_header:
                continue
            result.append(line)
        return '\n'.join(result)

    async def setup_model_injection(
        self,
        page: AsyncPage,
        filename: str = 'more_models.js'
    ) -> bool:
        path = os.path.join(self.base_dir, filename)
        if not os.path.exists(path):
            return False
        log.info('开始设置模型注入...')
        content = self.load_script(filename)
        if not content:
            return False
        return await self.inject_script_to_page(page, content, filename)


script_manager = ScriptManager()