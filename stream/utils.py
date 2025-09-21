import logging
from urllib.parse import urlparse

def is_generate_content_endpoint(url):
    return 'GenerateContent' in url

def parse_proxy_url(proxy_url):
    if not proxy_url:
        return (None, None, None, None, None)
    parsed = urlparse(proxy_url)
    scheme = parsed.scheme
    host = parsed.hostname
    port = parsed.port
    username = parsed.username
    password = parsed.password
    return (scheme, host, port, username, password)

def setup_logger(name, log_file=None, level=logging.INFO):
    logger = logging.getLogger(name)
    logger.setLevel(level)
    formatter = logging.Formatter('%(asctime)s - %(name)s - %(levelname)s - %(message)s')
    console_handler = logging.StreamHandler()
    console_handler.setFormatter(formatter)
    logger.addHandler(console_handler)
    if log_file:
        file_handler = logging.FileHandler(log_file)
        file_handler.setFormatter(formatter)
        logger.addHandler(file_handler)
    return logger