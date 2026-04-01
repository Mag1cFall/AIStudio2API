import os
import re
from datetime import datetime

_ENABLED = None
_DUMP_DIR = None
_COUNTER = 0

_BASE64_RE = re.compile(r'data:[^"\'>\s]{200,}')
_SCRIPT_RE = re.compile(r'<script[^>]*>[\s\S]*?</script>', re.IGNORECASE)
_SVG_PATH_RE = re.compile(r'(<path[^>]*\sd\s*=\s*["\'])[^"\']{500,}(["\'])', re.IGNORECASE)


def _is_enabled():
    global _ENABLED
    if _ENABLED is None:
        _ENABLED = os.environ.get('DOM_DEBUG', '').lower() in ('true', '1', 'yes')
    return _ENABLED


def _get_dump_dir():
    global _DUMP_DIR
    if _DUMP_DIR is None:
        project_root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
        _DUMP_DIR = os.path.join(project_root, 'test', 'dom_snapshots')
    return _DUMP_DIR


def _clean_html(raw: str) -> str:
    cleaned = _BASE64_RE.sub('[BASE64_REMOVED]', raw)
    cleaned = _SCRIPT_RE.sub('[SCRIPT_REMOVED]', cleaned)
    cleaned = _SVG_PATH_RE.sub(r'\1[SVG_TRUNCATED]\2', cleaned)
    cleaned = re.sub(r'<!--[\s\S]*?-->', '', cleaned)
    return cleaned


async def dump_page(page, label: str, logger=None):
    if not _is_enabled():
        return None

    global _COUNTER
    _COUNTER += 1

    dump_dir = _get_dump_dir()
    os.makedirs(dump_dir, exist_ok=True)

    try:
        raw = await page.content()
        cleaned = _clean_html(raw)

        ts = datetime.now().strftime('%Y%m%d_%H%M%S')
        safe_label = re.sub(r'[^\w\-]', '_', label)
        filename = f'{_COUNTER:03d}_{ts}_{safe_label}.html'
        filepath = os.path.join(dump_dir, filename)

        with open(filepath, 'w', encoding='utf-8') as f:
            f.write(f'<!-- URL: {page.url} -->\n')
            f.write(f'<!-- Label: {label} -->\n')
            f.write(f'<!-- Time: {ts} -->\n')
            f.write(f'<!-- Raw: {len(raw):,} chars | Cleaned: {len(cleaned):,} chars -->\n\n')
            f.write(cleaned)

        msg = f'[DOM_DEBUG] #{_COUNTER} {filename} ({len(cleaned):,} chars)'
        if logger:
            logger.info(msg)
        else:
            print(msg)

        return filepath
    except Exception as e:
        msg = f'[DOM_DEBUG] dump failed for "{label}": {e}'
        if logger:
            logger.warning(msg)
        else:
            print(msg)
        return None
