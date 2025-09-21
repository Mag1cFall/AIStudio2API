import asyncio
import ssl as ssl_module
import urllib.parse
from aiohttp import TCPConnector
from python_socks.async_.asyncio import Proxy

class ProxyConnector:

    def __init__(self, proxy_url=None):
        self.proxy_url = proxy_url
        self.connector = None
        if proxy_url:
            self._setup_connector()

    def _setup_connector(self):
        if not self.proxy_url:
            self.connector = TCPConnector()
            return
        parsed = urllib.parse.urlparse(self.proxy_url)
        proxy_type = parsed.scheme.lower()
        if proxy_type in ('http', 'https', 'socks4', 'socks5'):
            self.connector = 'SocksConnector'
        else:
            raise ValueError(f'Unsupported proxy type: {proxy_type}')

    async def create_connection(self, host, port, ssl=None):
        """Create a connection to the target host through the proxy"""
        if not self.connector:
            reader, writer = await asyncio.open_connection(host, port, ssl=ssl)
            return (reader, writer)
        proxy = Proxy.from_url(self.proxy_url)
        sock = await proxy.connect(dest_host=host, dest_port=port)
        if ssl is None:
            reader, writer = await asyncio.open_connection(host=None, port=None, sock=sock, ssl=None)
            return (reader, writer)
        else:
            ssl_context = ssl_module.SSLContext(ssl_module.PROTOCOL_TLS_CLIENT)
            ssl_context.check_hostname = False
            ssl_context.verify_mode = ssl_module.CERT_NONE
            ssl_context.minimum_version = ssl_module.TLSVersion.TLSv1_2
            ssl_context.maximum_version = ssl_module.TLSVersion.TLSv1_3
            ssl_context.set_ciphers('DEFAULT@SECLEVEL=2')
            reader, writer = await asyncio.open_connection(host=None, port=None, sock=sock, ssl=ssl_context, server_hostname=host)
            return (reader, writer)