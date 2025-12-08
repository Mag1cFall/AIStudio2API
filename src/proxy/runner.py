import argparse
import asyncio
import logging
import multiprocessing
import sys
from pathlib import Path

from proxy.server import MitmProxy


def parse_arguments():
    parser = argparse.ArgumentParser(description='HTTPS Proxy with SSL Inspection')
    parser.add_argument('--host', default='127.0.0.1', help='Bind address')
    parser.add_argument('--port', type=int, default=3120, help='Bind port')
    parser.add_argument('--domains', nargs='+', default=['*.google.com'], help='Target domains')
    parser.add_argument('--proxy', help='Upstream proxy URL')
    return parser.parse_args()


async def standalone():
    args = parse_arguments()
    logging.basicConfig(
        level=logging.INFO,
        format='%(asctime)s - %(name)s - %(levelname)s - %(message)s',
        handlers=[logging.StreamHandler()]
    )
    log = logging.getLogger('proxy_runner')
    
    cert_path = Path('certs')
    cert_path.mkdir(exist_ok=True)
    
    log.info(f'Starting on {args.host}:{args.port}')
    log.info(f'Target domains: {args.domains}')
    if args.proxy:
        log.info(f'Upstream: {args.proxy}')
    
    proxy = MitmProxy(
        bind_host=args.host,
        bind_port=args.port,
        target_domains=args.domains,
        upstream_url=args.proxy,
        message_queue=None
    )
    
    try:
        await proxy.run()
    except KeyboardInterrupt:
        log.info('Shutting down')
    except Exception as e:
        log.error(f'Startup failed: {e}')
        sys.exit(1)


async def embedded(
    queue: multiprocessing.Queue = None,
    port: int = None,
    proxy_url: str = None
):
    logging.basicConfig(
        level=logging.INFO,
        format='%(asctime)s - %(name)s - %(levelname)s - %(message)s',
        handlers=[logging.StreamHandler()]
    )
    log = logging.getLogger('proxy_runner')
    
    cert_path = Path('certs')
    cert_path.mkdir(exist_ok=True)
    
    if port is None:
        port = 3120
    
    proxy = MitmProxy(
        bind_host='127.0.0.1',
        bind_port=port,
        target_domains=['*.google.com'],
        upstream_url=proxy_url,
        message_queue=queue
    )
    
    try:
        await proxy.run()
    except KeyboardInterrupt:
        log.info('Shutting down')
    except Exception as e:
        log.error(f'Startup failed: {e}')
        sys.exit(1)


if __name__ == '__main__':
    asyncio.run(standalone())
