import datetime
import os
import ssl
from pathlib import Path
from typing import Optional, Tuple
from urllib.parse import urlparse

import asyncio
from aiohttp import TCPConnector
from cryptography import x509
from cryptography.hazmat.backends import default_backend
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.x509.oid import NameOID
from python_socks.async_.asyncio import Proxy
import ssl as ssl_module


class CertStore:

    def __init__(self, storage_path: str = 'certs'):
        self.storage_dir = Path(storage_path)
        self.storage_dir.mkdir(exist_ok=True)
        self.authority_key_file = self.storage_dir / 'ca.key'
        self.authority_cert_file = self.storage_dir / 'ca.crt'
        if not self.authority_cert_file.exists() or not self.authority_key_file.exists():
            self._create_authority()
        self._load_authority()

    def _create_authority(self) -> None:
        key = rsa.generate_private_key(
            public_exponent=65537,
            key_size=2048,
            backend=default_backend()
        )
        with open(self.authority_key_file, 'wb') as f:
            f.write(key.private_bytes(
                encoding=serialization.Encoding.PEM,
                format=serialization.PrivateFormat.PKCS8,
                encryption_algorithm=serialization.NoEncryption()
            ))
        
        name = x509.Name([
            x509.NameAttribute(NameOID.COUNTRY_NAME, 'US'),
            x509.NameAttribute(NameOID.STATE_OR_PROVINCE_NAME, 'California'),
            x509.NameAttribute(NameOID.LOCALITY_NAME, 'San Francisco'),
            x509.NameAttribute(NameOID.ORGANIZATION_NAME, 'Proxy CA'),
            x509.NameAttribute(NameOID.COMMON_NAME, 'Proxy CA Root')
        ])
        
        cert = (
            x509.CertificateBuilder()
            .subject_name(name)
            .issuer_name(name)
            .public_key(key.public_key())
            .serial_number(x509.random_serial_number())
            .not_valid_before(datetime.datetime.utcnow())
            .not_valid_after(datetime.datetime.utcnow() + datetime.timedelta(days=3650))
            .add_extension(x509.BasicConstraints(ca=True, path_length=None), critical=True)
            .add_extension(
                x509.KeyUsage(
                    digital_signature=True, content_commitment=False,
                    key_encipherment=True, data_encipherment=False,
                    key_agreement=False, key_cert_sign=True, crl_sign=True,
                    encipher_only=False, decipher_only=False
                ),
                critical=True
            )
            .sign(key, hashes.SHA256(), default_backend())
        )
        
        with open(self.authority_cert_file, 'wb') as f:
            f.write(cert.public_bytes(serialization.Encoding.PEM))

    def _load_authority(self) -> None:
        with open(self.authority_key_file, 'rb') as f:
            self.authority_key = serialization.load_pem_private_key(
                f.read(), password=None, backend=default_backend()
            )
        with open(self.authority_cert_file, 'rb') as f:
            self.authority_cert = x509.load_pem_x509_certificate(
                f.read(), default_backend()
            )

    def get_cert_for_domain(self, domain: str) -> Tuple:
        cert_file = self.storage_dir / f'{domain}.crt'
        key_file = self.storage_dir / f'{domain}.key'
        
        if cert_file.exists() and key_file.exists():
            with open(key_file, 'rb') as f:
                key = serialization.load_pem_private_key(
                    f.read(), password=None, backend=default_backend()
                )
            with open(cert_file, 'rb') as f:
                cert = x509.load_pem_x509_certificate(f.read(), default_backend())
            return key, cert
        
        return self._create_domain_cert(domain)

    def _create_domain_cert(self, domain: str) -> Tuple:
        key = rsa.generate_private_key(
            public_exponent=65537,
            key_size=2048,
            backend=default_backend()
        )
        
        key_file = self.storage_dir / f'{domain}.key'
        with open(key_file, 'wb') as f:
            f.write(key.private_bytes(
                encoding=serialization.Encoding.PEM,
                format=serialization.PrivateFormat.PKCS8,
                encryption_algorithm=serialization.NoEncryption()
            ))
        
        name = x509.Name([
            x509.NameAttribute(NameOID.COUNTRY_NAME, 'US'),
            x509.NameAttribute(NameOID.STATE_OR_PROVINCE_NAME, 'California'),
            x509.NameAttribute(NameOID.LOCALITY_NAME, 'San Francisco'),
            x509.NameAttribute(NameOID.ORGANIZATION_NAME, 'Proxy Server'),
            x509.NameAttribute(NameOID.COMMON_NAME, domain)
        ])
        
        cert = (
            x509.CertificateBuilder()
            .subject_name(name)
            .issuer_name(self.authority_cert.subject)
            .public_key(key.public_key())
            .serial_number(x509.random_serial_number())
            .not_valid_before(datetime.datetime.utcnow())
            .not_valid_after(datetime.datetime.utcnow() + datetime.timedelta(days=365))
            .add_extension(x509.SubjectAlternativeName([x509.DNSName(domain)]), critical=False)
            .sign(self.authority_key, hashes.SHA256(), default_backend())
        )
        
        cert_file = self.storage_dir / f'{domain}.crt'
        with open(cert_file, 'wb') as f:
            f.write(cert.public_bytes(serialization.Encoding.PEM))
        
        return key, cert


class UpstreamConnector:

    def __init__(self, upstream_url: Optional[str] = None):
        self.upstream_url = upstream_url
        self.tcp_connector = None
        if upstream_url:
            self._init_connector()

    def _init_connector(self) -> None:
        if not self.upstream_url:
            self.tcp_connector = TCPConnector()
            return
        
        parsed = urlparse(self.upstream_url)
        scheme = parsed.scheme.lower()
        
        if scheme in ('http', 'https', 'socks4', 'socks5'):
            self.tcp_connector = 'SocksConnector'
        else:
            raise ValueError(f'Unsupported proxy scheme: {scheme}')

    async def open_connection(
        self,
        target_host: str,
        target_port: int,
        use_ssl: Optional[bool] = None
    ) -> Tuple:
        if not self.tcp_connector:
            reader, writer = await asyncio.open_connection(
                target_host, target_port, ssl=use_ssl
            )
            return reader, writer
        
        upstream = Proxy.from_url(self.upstream_url)
        sock = await upstream.connect(dest_host=target_host, dest_port=target_port)
        
        if use_ssl is None:
            reader, writer = await asyncio.open_connection(
                host=None, port=None, sock=sock, ssl=None
            )
            return reader, writer
        
        ctx = ssl_module.SSLContext(ssl_module.PROTOCOL_TLS_CLIENT)
        ctx.check_hostname = False
        ctx.verify_mode = ssl_module.CERT_NONE
        ctx.minimum_version = ssl_module.TLSVersion.TLSv1_2
        ctx.maximum_version = ssl_module.TLSVersion.TLSv1_3
        ctx.set_ciphers('DEFAULT@SECLEVEL=2')
        
        reader, writer = await asyncio.open_connection(
            host=None, port=None, sock=sock, ssl=ctx, server_hostname=target_host
        )
        return reader, writer
