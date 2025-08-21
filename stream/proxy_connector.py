import asyncio
import ssl as ssl_module
import urllib.parse
from aiohttp import TCPConnector
from python_socks.async_.asyncio import Proxy


class ProxyConnector:
    """
    Class to handle connections through different types of proxies
    """

    def __init__(self, proxy_url=None):
        self.proxy_url = proxy_url
        self.connector = None

        if proxy_url:
            self._setup_connector()

    def _setup_connector(self):
        """Set up the appropriate connector based on the proxy URL"""
        if not self.proxy_url:
            self.connector = TCPConnector()
            return

        # Parse the proxy URL
        parsed = urllib.parse.urlparse(self.proxy_url)
        proxy_type = parsed.scheme.lower()

        if proxy_type in ('http', 'https', 'socks4', 'socks5'):
            self.connector = "SocksConnector"
        else:
            raise ValueError(f"Unsupported proxy type: {proxy_type}")

    async def create_connection(self, host, port, ssl=None):
        """Create a connection to the target host through the proxy"""
        if not self.connector:
            # Direct connection without proxy
            reader, writer = await asyncio.open_connection(host, port, ssl=ssl)
            return reader, writer

        # SOCKS proxy connection
        proxy = Proxy.from_url(self.proxy_url)
        sock = await proxy.connect(dest_host=host, dest_port=port)
        if ssl is None:
            reader, writer = await asyncio.open_connection(
                host=None,
                port=None,
                sock=sock,
                ssl=None,
            )
            return reader, writer
        else:
            ssl_context = ssl_module.SSLContext(ssl_module.PROTOCOL_TLS_CLIENT)
            ssl_context.check_hostname = False
            ssl_context.verify_mode = ssl_module.CERT_NONE
            ssl_context.minimum_version = ssl_module.TLSVersion.TLSv1_2  # Force TLS 1.2 or higher
            ssl_context.maximum_version = ssl_module.TLSVersion.TLSv1_3  # Allow TLS 1.3 if supported
            ssl_context.set_ciphers('DEFAULT@SECLEVEL=2')  # Use secure ciphers

            reader, writer = await asyncio.open_connection(
                host=None,
                port=None,
                sock=sock,
                ssl=ssl_context,
                server_hostname=host,
            )
            return reader, writer
