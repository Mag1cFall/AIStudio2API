import asyncio
import json
import logging
import multiprocessing
import ssl
from pathlib import Path
from typing import Dict, Optional

from proxy.connection import CertStore, UpstreamConnector
from proxy.handler import ResponseHandler


class MitmProxy:
    def __init__(
        self,
        bind_host: str = "0.0.0.0",
        bind_port: int = 3120,
        target_domains: Optional[list] = None,
        upstream_url: Optional[str] = None,
        message_queue: Optional[multiprocessing.Queue] = None,
    ):
        self.bind_host = bind_host
        self.bind_port = bind_port
        self.target_domains = target_domains or []
        self.upstream_url = upstream_url
        self.message_queue = message_queue

        self.cert_store = CertStore()
        self.connector = UpstreamConnector(upstream_url)

        log_path = Path("logs")
        log_path.mkdir(exist_ok=True)
        self.response_handler = ResponseHandler()

        self.log = logging.getLogger("mitm_proxy")
        self._context_cache = {}

    @staticmethod
    def _parse_headers(header_bytes: bytes) -> Dict[str, str]:
        headers = {}
        for line in header_bytes.split(b"\r\n")[1:]:
            if not line:
                continue
            try:
                key, value = line.decode("utf-8").split(":", 1)
            except ValueError:
                continue
            headers[key.strip()] = value.strip()
        return headers

    _IGNORABLE_ERRORS = (
        "APPLICATION_DATA_AFTER_CLOSE_NOTIFY",
        "DECRYPTION_FAILED_OR_BAD_RECORD_MAC",
        "decryption failed",
        "bad record mac",
        "Connection reset",
        "Connection aborted",
        "ConnectionResetError",
        "EOF occurred in violation",
        "WRONG_VERSION_NUMBER",
        "\u9023\u7dda\u5df2\u88ab\u60a8\u4e3b\u6a5f\u4e0a\u7684\u8edf\u9ad4\u4e2d\u6b62",
        "\u9060\u7aef\u4e3b\u6a5f\u5df2\u5f37\u5236\u95dc\u9589",
        # Japanese locale (WinError 10053 / 10054)
        "\u78ba\u7acb\u3055\u308c\u305f\u63a5\u7d9a\u304c\u30db\u30b9\u30c8",
        "\u30ea\u30e2\u30fc\u30c8 \u30db\u30b9\u30c8\u306b\u3088\u3063\u3066\u5f37\u5236\u7684\u306b\u9589\u3058\u3089\u308c",
    )

    @classmethod
    def _should_ignore_connection_error(cls, exc: BaseException) -> bool:
        err_str = f"{type(exc).__name__}: {exc}"
        return any(marker in err_str for marker in cls._IGNORABLE_ERRORS)

    async def _run_relay_tasks(self, *coroutines) -> None:
        tasks = [asyncio.create_task(coro) for coro in coroutines]
        try:
            await asyncio.gather(*tasks)
        except BaseException:
            for task in tasks:
                if not task.done():
                    task.cancel()
            await asyncio.gather(*tasks, return_exceptions=True)
            raise

    def _get_tls_context(self, domain: str):
        if domain in self._context_cache:
            return self._context_cache[domain]

        ctx = ssl.create_default_context(ssl.Purpose.CLIENT_AUTH)
        ctx.load_cert_chain(
            certfile=self.cert_store.storage_dir / f"{domain}.crt",
            keyfile=self.cert_store.storage_dir / f"{domain}.key",
        )
        try:
            ctx.set_alpn_protocols(["http/1.1"])
        except Exception:
            pass

        if len(self._context_cache) > 50:
            self._context_cache.clear()

        self._context_cache[domain] = ctx
        return ctx

    def _matches_target(self, domain: str) -> bool:
        if domain in self.target_domains:
            return True
        for pattern in self.target_domains:
            if pattern.startswith("*."):
                suffix = pattern[1:]
                if domain.endswith(suffix):
                    return True
        return False

    async def accept_client(
        self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter
    ) -> None:
        try:
            first_line = await reader.readline()
            first_line = first_line.decode("utf-8").strip()
            if not first_line:
                writer.close()
                return

            method, target, _ = first_line.split(" ")
            if method == "CONNECT":
                await self._process_tunnel(reader, writer, target)
        except Exception as e:
            if not self._should_ignore_connection_error(e):
                self.log.error(f"Client error: {e!r}", exc_info=True)
        finally:
            writer.close()

    async def _process_tunnel(
        self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter, target: str
    ) -> None:
        host, port_str = target.split(":")
        port = int(port_str)
        should_inspect = self._matches_target(host)

        if should_inspect:
            self.log.info(f"Inspecting: {target}")
            self.cert_store.get_cert_for_domain(host)

            writer.write(b"HTTP/1.1 200 Connection Established\r\n\r\n")
            await writer.drain()
            await reader.read(8192)

            loop = asyncio.get_running_loop()
            transport = writer.transport

            if transport is None:
                self.log.warning(f"Transport None for {host}:{port}")
                return

            ctx = self._get_tls_context(host)
            protocol = transport.get_protocol()

            new_transport = await loop.start_tls(
                transport=transport, protocol=protocol, sslcontext=ctx, server_side=True
            )

            if new_transport is None:
                self.log.error(f"TLS upgrade failed for {host}:{port}")
                writer.close()
                return

            client_reader = reader
            client_writer = asyncio.StreamWriter(
                transport=new_transport,
                protocol=protocol,
                reader=client_reader,
                loop=loop,
            )

            try:
                upstream_ctx = ssl.create_default_context()
                try:
                    upstream_ctx.set_alpn_protocols(["http/1.1"])
                except Exception:
                    pass
                server_reader, server_writer = await self.connector.open_connection(
                    host, port, upstream_ctx
                )
                await self._relay_with_inspection(
                    client_reader, client_writer, server_reader, server_writer, host
                )
            except Exception:
                client_writer.close()
        else:
            writer.write(b"HTTP/1.1 200 Connection Established\r\n\r\n")
            await writer.drain()
            await reader.read(8192)

            try:
                server_reader, server_writer = await self.connector.open_connection(
                    host, port, None
                )
                await self._relay_transparent(
                    reader, writer, server_reader, server_writer
                )
            except Exception:
                writer.close()

    async def _relay_transparent(
        self, client_reader, client_writer, server_reader, server_writer
    ) -> None:
        async def forward(src, dst):
            try:
                while True:
                    chunk = await src.read(8192)
                    if not chunk:
                        break
                    dst.write(chunk)
                    await dst.drain()
            except Exception as e:
                if not self._should_ignore_connection_error(e):
                    self.log.error(f"Relay error: {e}")
                    raise
            finally:
                dst.close()

        await self._run_relay_tasks(
            forward(client_reader, server_writer),
            forward(server_reader, client_writer),
        )

    async def _relay_with_inspection(
        self, client_reader, client_writer, server_reader, server_writer, host: str
    ) -> None:
        client_buf = bytearray()
        server_buf = bytearray()
        inspect_response = False
        response_headers = None

        async def process_upstream():
            nonlocal client_buf, inspect_response
            try:
                while True:
                    data = await client_reader.read(8192)
                    if not data:
                        break

                    client_buf.extend(data)

                    if b"\r\n\r\n" in client_buf:
                        split_pos = client_buf.find(b"\r\n\r\n") + 4
                        header_bytes = client_buf[:split_pos]
                        body_bytes = client_buf[split_pos:]

                        lines = header_bytes.split(b"\r\n")
                        request_line = lines[0].decode("utf-8")

                        try:
                            method, path, _ = request_line.split(" ")
                        except ValueError:
                            server_writer.write(client_buf)
                            await server_writer.drain()
                            client_buf.clear()
                            continue

                        if "jserror" in path:
                            inspect_response = False
                            try:
                                path_str = path
                                if (
                                    "quota" in path_str
                                    or "limit" in path_str
                                    or "exceeded" in path_str
                                ):
                                    self.log.info(
                                        f"Rate limit keyword found in jserror: {path_str}"
                                    )
                                    if self.message_queue is not None:
                                        self.message_queue.put(
                                            {
                                                "error": "rate_limit",
                                                "detail": "Rate limit detected via jserror",
                                                "source": "jserror",
                                                "path": path_str,
                                            }
                                        )
                            except Exception as e:
                                self.log.error(f"Error inspecting jserror: {e}")
                            server_writer.write(client_buf)
                        elif "GenerateContent" in path:
                            inspect_response = True
                            processed = await self.response_handler.handle_request(
                                body_bytes, host, path
                            )
                            server_writer.write(header_bytes)
                            server_writer.write(processed)
                        else:
                            inspect_response = False
                            server_writer.write(client_buf)

                        await server_writer.drain()
                        client_buf.clear()
                    else:
                        server_writer.write(data)
                        await server_writer.drain()
                        client_buf.clear()
            except Exception as e:
                if not self._should_ignore_connection_error(e):
                    self.log.error(f"Upstream error: {e}")
                    raise
            finally:
                server_writer.close()

        async def process_downstream():
            nonlocal response_headers, server_buf, inspect_response
            try:
                while True:
                    data = await server_reader.read(8192)
                    if not data:
                        break
                    client_writer.write(data)
                    await client_writer.drain()

                    if not inspect_response and response_headers is None:
                        continue

                    server_buf.extend(data)

                    if response_headers is None:
                        if b"\r\n\r\n" not in server_buf:
                            continue
                        split_pos = server_buf.find(b"\r\n\r\n") + 4
                        response_headers = self._parse_headers(
                            bytes(server_buf[:split_pos])
                        )
                        server_buf = bytearray(server_buf[split_pos:])

                    if inspect_response and response_headers is not None and server_buf:
                        try:
                            result = await self.response_handler.handle_response(
                                bytes(server_buf), host, "", response_headers
                            )
                            if self.message_queue is not None:
                                self.message_queue.put(json.dumps(result))
                            if result.get("done"):
                                inspect_response = False
                                response_headers = None
                                server_buf.clear()
                        except Exception:
                            pass
            except Exception as e:
                if not self._should_ignore_connection_error(e):
                    self.log.error(f"Downstream error: {e}")
                    raise
            finally:
                client_writer.close()

        await self._run_relay_tasks(
            process_upstream(),
            process_downstream(),
        )

    async def run(self) -> None:
        logging.getLogger("asyncio").setLevel(logging.ERROR)
        server = await asyncio.start_server(
            self.accept_client, self.bind_host, self.bind_port
        )
        addr = server.sockets[0].getsockname()
        self.log.info(f"Listening on {addr}")
        async with server:
            await server.serve_forever()
