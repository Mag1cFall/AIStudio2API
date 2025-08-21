import logging
from urllib.parse import urlparse

def is_generate_content_endpoint(url):
    """
    Check if the URL is a GenerateContent endpoint
    """
    return 'GenerateContent' in url

def parse_proxy_url(proxy_url):
    """
    Parse a proxy URL into its components
    
    Returns:
        tuple: (scheme, host, port, username, password)
    """
    if not proxy_url:
        return None, None, None, None, None
    
    parsed = urlparse(proxy_url)
    
    scheme = parsed.scheme
    host = parsed.hostname
    port = parsed.port
    username = parsed.username
    password = parsed.password
    
    return scheme, host, port, username, password

def setup_logger(name, log_file=None, level=logging.INFO):
    """
    Set up a logger with the specified name and configuration
    
    Args:
        name (str): Logger name
        log_file (str, optional): Path to log file
        level (int, optional): Logging level
        
    Returns:
        logging.Logger: Configured logger
    """
    logger = logging.getLogger(name)
    logger.setLevel(level)
    
    formatter = logging.Formatter('%(asctime)s - %(name)s - %(levelname)s - %(message)s')
    
    # Add console handler
    console_handler = logging.StreamHandler()
    console_handler.setFormatter(formatter)
    logger.addHandler(console_handler)
    
    # Add file handler if specified
    if log_file:
        file_handler = logging.FileHandler(log_file)
        file_handler.setFormatter(formatter)
        logger.addHandler(file_handler)
    
    return logger
