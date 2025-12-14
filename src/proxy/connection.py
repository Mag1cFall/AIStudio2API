import datetime
import os
import ssl
import random
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


from config.settings import DATA_DIR

CERTS_DIR = str(Path(DATA_DIR) / 'certs')

CERT_PROFILES = [
    # --- Taiwan (15 entries) ---
    {'country': 'TW', 'state': 'Taipei', 'city': 'Xinyi District', 'org': 'Chunghwa Telecom', 'cn': 'HiNet CA'},
    {'country': 'TW', 'state': 'Taipei', 'city': 'Da-an District', 'org': 'National Taiwan University', 'cn': 'NTU Root CA'},
    {'country': 'TW', 'state': 'New Taipei', 'city': 'Banqiao', 'org': 'Far EasTone', 'cn': 'FET Network CA'},
    {'country': 'TW', 'state': 'Hsinchu', 'city': 'Hsinchu Science Park', 'org': 'TSMC IT Services', 'cn': 'Fab Network Root'},
    {'country': 'TW', 'state': 'Hsinchu', 'city': 'Zhubei', 'org': 'MediaTek Internal', 'cn': 'MTK Secure CA'},
    {'country': 'TW', 'state': 'Taichung', 'city': 'Xitun', 'org': 'Taichung City Gov', 'cn': 'Taichung Smart City CA'},
    {'country': 'TW', 'state': 'Tainan', 'city': 'East District', 'org': 'NCKU Network', 'cn': 'NCKU Academic CA'},
    {'country': 'TW', 'state': 'Kaohsiung', 'city': 'Zuoying', 'org': 'Kaohsiung Rapid Transit', 'cn': 'KRT Secure Net'},
    {'country': 'TW', 'state': 'Kaohsiung', 'city': 'Qianzhen', 'org': 'China Steel Corp', 'cn': 'CSC Enterprise CA'},
    {'country': 'TW', 'state': 'Taoyuan', 'city': 'Dayuan', 'org': 'Taoyuan Airport', 'cn': 'TPE Airport Free WiFi'},
    {'country': 'TW', 'state': 'Keelung', 'city': 'Ren-ai', 'org': 'Keelung Harbor', 'cn': 'Port Authority CA'},
    {'country': 'TW', 'state': 'Yilan', 'city': 'Yilan City', 'org': 'Lanyang Network', 'cn': 'Yilan County CA'},
    {'country': 'TW', 'state': 'Hualien', 'city': 'Hualien City', 'org': 'Tzu Chi Foundation', 'cn': 'TC Foundation Root'},
    {'country': 'TW', 'state': 'Taitung', 'city': 'Taitung City', 'org': 'Taitung Univ', 'cn': 'NTTU Campus CA'},
    {'country': 'TW', 'state': 'Penghu', 'city': 'Magong', 'org': 'Penghu Telecom', 'cn': 'Islands Connect CA'},

    # --- USA (10 entries) ---
    {'country': 'US', 'state': 'California', 'city': 'Mountain View', 'org': 'Alphabet Inc', 'cn': 'Google Internal CA'},
    {'country': 'US', 'state': 'California', 'city': 'Cupertino', 'org': 'Apple Inc', 'cn': 'Apple Engineering CA'},
    {'country': 'US', 'state': 'Washington', 'city': 'Redmond', 'org': 'Microsoft Corp', 'cn': 'MSFT Corporate Root'},
    {'country': 'US', 'state': 'Washington', 'city': 'Seattle', 'org': 'Amazon Web Services', 'cn': 'AWS Internal Root'},
    {'country': 'US', 'state': 'New York', 'city': 'New York', 'org': 'JPMorgan Chase', 'cn': 'JPMC Secure Net'},
    {'country': 'US', 'state': 'Massachusetts', 'city': 'Cambridge', 'org': 'MIT CSAIL', 'cn': 'MIT Research CA'},
    {'country': 'US', 'state': 'Texas', 'city': 'Austin', 'org': 'Tesla Motors', 'cn': 'Tesla Factory Net'},
    {'country': 'US', 'state': 'Illinois', 'city': 'Chicago', 'org': 'Boeing', 'cn': 'Boeing Enterprise CA'},
    {'country': 'US', 'state': 'California', 'city': 'San Francisco', 'org': 'OpenAI', 'cn': 'OAI Research Root'},
    {'country': 'US', 'state': 'California', 'city': 'Menlo Park', 'org': 'Meta Platforms', 'cn': 'Meta Corporate CA'},

    # --- Japan (8 entries) ---
    {'country': 'JP', 'state': 'Tokyo', 'city': 'Chiyoda', 'org': 'Hitachi Ltd', 'cn': 'Hitachi Group CA'},
    {'country': 'JP', 'state': 'Tokyo', 'city': 'Minato', 'org': 'Sony Corporation', 'cn': 'Sony Global Root'},
    {'country': 'JP', 'state': 'Tokyo', 'city': 'Shibuya', 'org': 'LINE Corp', 'cn': 'LINE Internal CA'},
    {'country': 'JP', 'state': 'Osaka', 'city': 'Kadoma', 'org': 'Panasonic', 'cn': 'Panasonic Net'},
    {'country': 'JP', 'state': 'Aichi', 'city': 'Toyota', 'org': 'Toyota Motor', 'cn': 'Toyota Global CA'},
    {'country': 'JP', 'state': 'Kyoto', 'city': 'Kyoto', 'org': 'Nintendo Co Ltd', 'cn': 'Nintendo Dev Net'},
    {'country': 'JP', 'state': 'Tokyo', 'city': 'Ota', 'org': 'Canon Inc', 'cn': 'Canon Enterprise CA'},
    {'country': 'JP', 'state': 'Fukuoka', 'city': 'Fukuoka', 'org': 'SoftBank Corp', 'cn': 'SoftBank Internal'},

    # --- Europe (10 entries) ---
    {'country': 'GB', 'state': 'London', 'city': 'London', 'org': 'DeepMind Technologies', 'cn': 'DeepMind Research CA'},
    {'country': 'GB', 'state': 'London', 'city': 'Westminster', 'org': 'BBC', 'cn': 'BBC Internal Root'},
    {'country': 'DE', 'state': 'Bavaria', 'city': 'Munich', 'org': 'Siemens AG', 'cn': 'Siemens Corporate CA'},
    {'country': 'DE', 'state': 'Hessen', 'city': 'Frankfurt', 'org': 'Deutsche Bank', 'cn': 'DB Secure Net'},
    {'country': 'DE', 'state': 'Berlin', 'city': 'Berlin', 'org': 'SAP SE', 'cn': 'SAP Global Root'},
    {'country': 'FR', 'state': 'Ile-de-France', 'city': 'Paris', 'org': 'Dassault Systemes', 'cn': 'Dassault Internal'},
    {'country': 'FR', 'state': 'Ile-de-France', 'city': 'Issy-les-Moulineaux', 'org': 'Orange SA', 'cn': 'Orange Network CA'},
    {'country': 'NL', 'state': 'North Holland', 'city': 'Amsterdam', 'org': 'Booking.com', 'cn': 'Booking Corporate CA'},
    {'country': 'NL', 'state': 'Eindhoven', 'city': 'Eindhoven', 'org': 'ASML', 'cn': 'ASML Engineering CA'},
    {'country': 'SE', 'state': 'Stockholm', 'city': 'Stockholm', 'org': 'Spotify AB', 'cn': 'Spotify Internal'},

    # --- Other Asia/Pacific (9 entries) ---
    {'country': 'SG', 'state': 'Singapore', 'city': 'Singapore', 'org': 'Singtel', 'cn': 'Singtel OnePass'},
    {'country': 'SG', 'state': 'Singapore', 'city': 'Singapore', 'org': 'DBS Bank', 'cn': 'DBS Secure Access'},
    {'country': 'KR', 'state': 'Seoul', 'city': 'Seocho', 'org': 'Samsung Electronics', 'cn': 'Samsung Global CA'},
    {'country': 'KR', 'state': 'Seoul', 'city': 'Songpa', 'org': 'Lotte Corp', 'cn': 'Lotte Group Root'},
    {'country': 'KR', 'state': 'Gyeonggi', 'city': 'Seongnam', 'org': 'Naver Corp', 'cn': 'Naver Service CA'},
    {'country': 'AU', 'state': 'New South Wales', 'city': 'Sydney', 'org': 'Atlassian', 'cn': 'Atlassian Corp CA'},
    {'country': 'AU', 'state': 'Victoria', 'city': 'Melbourne', 'org': 'Telstra', 'cn': 'Telstra Network Root'},
    {'country': 'IN', 'state': 'Karnataka', 'city': 'Bangalore', 'org': 'Infosys', 'cn': 'Infosys Limited CA'},
    {'country': 'IN', 'state': 'Maharashtra', 'city': 'Mumbai', 'org': 'Tata Consultancy', 'cn': 'TCS Global Root'},
]


class CertStore:

    def __init__(self, storage_path: str = CERTS_DIR):
        self.storage_dir = Path(storage_path)
        self.storage_dir.mkdir(exist_ok=True)
        self.authority_key_file = self.storage_dir / 'ca.key'
        self.authority_cert_file = self.storage_dir / 'ca.crt'
        self._profile = random.choice(CERT_PROFILES)
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
            x509.NameAttribute(NameOID.COUNTRY_NAME, self._profile['country']),
            x509.NameAttribute(NameOID.STATE_OR_PROVINCE_NAME, self._profile['state']),
            x509.NameAttribute(NameOID.LOCALITY_NAME, self._profile['city']),
            x509.NameAttribute(NameOID.ORGANIZATION_NAME, self._profile['org']),
            x509.NameAttribute(NameOID.COMMON_NAME, self._profile['cn'])
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
