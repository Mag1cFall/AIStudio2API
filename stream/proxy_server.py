import asyncio
from typing import Optional
import json
import logging
import ssl
import multiprocessing
from pathlib import Path
from urllib.parse import urlparse
from stream.cert_manager import CertificateManager
from stream.proxy_connector import ProxyConnector
from stream.interceptors import HttpInterceptor

class ProxyServer:

    def __init__(self, host='0.0.0.0', port=3120, intercept_domains=None, upstream_proxy=None, queue: Optional[multiprocessing.Queue]=None):
        self.host = host
        self.port = port
        self.intercept_domains = intercept_domains or []
        self.upstream_proxy = upstream_proxy
        self.queue = queue
        self.cert_manager = CertificateManager()
        self.proxy_connector = ProxyConnector(upstream_proxy)
        log_dir = Path('logs')
        log_dir.mkdir(exist_ok=True)
        self.interceptor = HttpInterceptor(str(log_dir))
        self.logger = logging.getLogger('proxy_server')

    def should_intercept(self, host):
        if host in self.intercept_domains:
            return True
        for d in self.intercept_domains:
            if d.startswith('*.'):
                suffix = d[1:]
                if host.endswith(suffix):
                    return True
        return False

    async def handle_client(self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter):
        """
        Handle a client connection
        """
        try:
            request_line = await reader.readline()
            request_line = request_line.decode('utf-8').strip()
            if not request_line:
                writer.close()
                return
            method, target, version = request_line.split(' ')
            if method == 'CONNECT':
                await self._handle_connect(reader, writer, target)
        except Exception as e:
            self.logger.error(f'Error handling client: {e}')
        finally:
            writer.close()

    async def _handle_connect(self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter, target: str):
        """
        Handle CONNECT method (for HTTPS connections)
        """
        host, port = target.split(':')
        port = int(port)
        intercept = self.should_intercept(host)
        if intercept:
            self.logger.info(f'Sniff HTTPS requests to : {target}')
            self.cert_manager.get_domain_cert(host)
            writer.write(b'HTTP/1.1 200 Connection Established\r\n\r\n')
            await writer.drain()
            await reader.read(8192)
            loop = asyncio.get_running_loop()
            transport = writer.transport
            if transport is None:
                self.logger.warning(f'Client writer transport is None for {host}:{port} before TLS upgrade. Closing.')
                return
            ssl_context = ssl.create_default_context(ssl.Purpose.CLIENT_AUTH)
            ssl_context.load_cert_chain(certfile=self.cert_manager.cert_dir / f'{host}.crt', keyfile=self.cert_manager.cert_dir / f'{host}.key')
            client_protocol = transport.get_protocol()
            new_transport = await loop.start_tls(transport=transport, protocol=client_protocol, sslcontext=ssl_context, server_side=True)
            if new_transport is None:
                self.logger.error(f'loop.start_tls returned None for {host}:{port}, which is unexpected. Closing connection.')
                writer.close()
                return
            client_reader = reader
            client_writer = asyncio.StreamWriter(transport=new_transport, protocol=client_protocol, reader=client_reader, loop=loop)
            try:
                server_reader, server_writer = await self.proxy_connector.create_connection(host, port, ssl=ssl.create_default_context())
                await self._forward_data_with_interception(client_reader, client_writer, server_reader, server_writer, host)
            except Exception as e:
                client_writer.close()
        else:
            writer.write(b'HTTP/1.1 200 Connection Established\r\n\r\n')
            await writer.drain()
            await reader.read(8192)
            try:
                server_reader, server_writer = await self.proxy_connector.create_connection(host, port, ssl=None)
                await self._forward_data(reader, writer, server_reader, server_writer)
            except Exception as e:
                writer.close()

    async def _forward_data(self, client_reader, client_writer, server_reader, server_writer):
        """
        Forward data between client and server without interception
        """

        async def _forward(reader, writer):
            try:
                while True:
                    data = await reader.read(8192)
                    if not data:
                        break
                    writer.write(data)
                    await writer.drain()
            except Exception as e:
                self.logger.error(f'Error forwarding data: {e}')
            finally:
                writer.close()
        client_to_server = asyncio.create_task(_forward(client_reader, server_writer))
        server_to_client = asyncio.create_task(_forward(server_reader, client_writer))
        tasks = [client_to_server, server_to_client]
        await asyncio.gather(*tasks)

    async def _forward_data_with_interception(self, client_reader, client_writer, server_reader, server_writer, host):
        """
        Forward data between client and server with interception
        """
        client_buffer = bytearray()
        server_buffer = bytearray()
        should_sniff = False

        async def _process_client_data():
            nonlocal client_buffer, should_sniff
            try:
                while True:
                    data = await client_reader.read(8192)
                    if not data:
                        break
                    client_buffer.extend(data)
                    if b'\r\n\r\n' in client_buffer:
                        headers_end = client_buffer.find(b'\r\n\r\n') + 4
                        headers_data = client_buffer[:headers_end]
                        body_data = client_buffer[headers_end:]
                        lines = headers_data.split(b'\r\n')
                        request_line = lines[0].decode('utf-8')
                        try:
                            method, path, _ = request_line.split(' ')
                        except ValueError:
                            server_writer.write(client_buffer)
                            await server_writer.drain()
                            client_buffer.clear()
                            continue
                        if 'GenerateContent' in path:
                            should_sniff = True
                            processed_body = await self.interceptor.process_request(body_data, host, path)
                            server_writer.write(headers_data)
                            server_writer.write(processed_body)
                        else:
                            should_sniff = False
                            server_writer.write(client_buffer)
                        await server_writer.drain()
                        client_buffer.clear()
                    else:
                        server_writer.write(data)
                        await server_writer.drain()
                        client_buffer.clear()
            except Exception as e:
                self.logger.error(f'Error processing client data: {e}')
            finally:
                server_writer.close()

        async def _process_server_data():
            nonlocal server_buffer, should_sniff
            try:
                while True:
                    data = await server_reader.read(8192)
                    if not data:
                        break
                    server_buffer.extend(data)
                    if b'\r\n\r\n' in server_buffer:
                        headers_end = server_buffer.find(b'\r\n\r\n') + 4
                        headers_data = server_buffer[:headers_end]
                        body_data = server_buffer[headers_end:]
                        lines = headers_data.split(b'\r\n')
                        headers = {}
                        for i in range(1, len(lines)):
                            if not lines[i]:
                                continue
                            try:
                                key, value = lines[i].decode('utf-8').split(':', 1)
                                headers[key.strip()] = value.strip()
                            except ValueError:
                                continue
                        if should_sniff:
                            try:
                                resp = await self.interceptor.process_response(body_data, host, '', headers)
                                if self.queue is not None:
                                    self.queue.put(json.dumps(resp))
                            except Exception as e:
                                pass
                    client_writer.write(data)
                    if b'0\r\n\r\n' in server_buffer:
                        server_buffer.clear()
            except Exception as e:
                self.logger.error(f'Error processing server data: {e}')
            finally:
                client_writer.close()
        client_to_server = asyncio.create_task(_process_client_data())
        server_to_client = asyncio.create_task(_process_server_data())
        tasks = [client_to_server, server_to_client]
        await asyncio.gather(*tasks)

    async def start(self):
        """
        Start the proxy server
        """
        server = await asyncio.start_server(self.handle_client, self.host, self.port)
        addr = server.sockets[0].getsockname()
        self.logger.info(f'Serving on {addr}')
        async with server:
            await server.serve_forever()