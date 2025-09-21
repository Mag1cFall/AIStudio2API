import argparse
import asyncio
import logging
import multiprocessing
import sys
from pathlib import Path
from stream.proxy_server import ProxyServer

def parse_args():
    parser = argparse.ArgumentParser(description='HTTPS Proxy Server with SSL Inspection')
    parser.add_argument('--host', default='127.0.0.1', help='Host to bind the proxy server')
    parser.add_argument('--port', type=int, default=3120, help='Port to bind the proxy server')
    parser.add_argument('--domains', nargs='+', default=['*.google.com'], help='List of domain patterns to intercept (regex)')
    parser.add_argument('--proxy', help='Upstream proxy URL (e.g., http://user:pass@host:port)')
    return parser.parse_args()

async def main():
    """Main entry point"""
    args = parse_args()
    logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(name)s - %(levelname)s - %(message)s', handlers=[logging.StreamHandler()])
    logger = logging.getLogger('main')
    cert_dir = Path('certs')
    cert_dir.mkdir(exist_ok=True)
    logger.info(f'Starting proxy server on {args.host}:{args.port}')
    logger.info(f'Intercepting domains: {args.domains}')
    if args.proxy:
        logger.info(f'Using upstream proxy: {args.proxy}')
    proxy_server = ProxyServer(host=args.host, port=args.port, intercept_domains=args.domains, upstream_proxy=args.proxy, queue=None)
    try:
        await proxy_server.start()
    except KeyboardInterrupt:
        logger.info('Shutting down proxy server')
    except Exception as e:
        logger.error(f'Error starting proxy server: {e}')
        sys.exit(1)

async def builtin(queue: multiprocessing.Queue=None, port=None, proxy=None):
    logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(name)s - %(levelname)s - %(message)s', handlers=[logging.StreamHandler()])
    logger = logging.getLogger('main')
    cert_dir = Path('certs')
    cert_dir.mkdir(exist_ok=True)
    if port is None:
        port = 3120
    proxy_server = ProxyServer(host='127.0.0.1', port=port, intercept_domains=['*.google.com'], upstream_proxy=proxy, queue=queue)
    try:
        await proxy_server.start()
    except KeyboardInterrupt:
        logger.info('Shutting down proxy server')
    except Exception as e:
        logger.error(f'Error starting proxy server: {e}')
        sys.exit(1)
if __name__ == '__main__':
    asyncio.run(main())