import os
import datetime
from pathlib import Path
from cryptography import x509
from cryptography.x509.oid import NameOID
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.hazmat.backends import default_backend

class CertificateManager:
    def __init__(self, cert_dir='certs'):
        self.cert_dir = Path(cert_dir)
        self.cert_dir.mkdir(exist_ok=True)
        
        self.ca_key_path = self.cert_dir / 'ca.key'
        self.ca_cert_path = self.cert_dir / 'ca.crt'
        
        # Generate or load CA certificate
        if not self.ca_cert_path.exists() or not self.ca_key_path.exists():
            self._generate_ca_cert()
        
        self._load_ca_cert()
    
    def _generate_ca_cert(self):
        """Generate a self-signed CA certificate"""
        # Generate private key
        private_key = rsa.generate_private_key(
            public_exponent=65537,
            key_size=2048,
            backend=default_backend()
        )
        
        # Write private key to file
        with open(self.ca_key_path, 'wb') as f:
            f.write(private_key.private_bytes(
                encoding=serialization.Encoding.PEM,
                format=serialization.PrivateFormat.PKCS8,
                encryption_algorithm=serialization.NoEncryption()
            ))
        
        # Create self-signed certificate
        subject = issuer = x509.Name([
            x509.NameAttribute(NameOID.COUNTRY_NAME, "US"),
            x509.NameAttribute(NameOID.STATE_OR_PROVINCE_NAME, "California"),
            x509.NameAttribute(NameOID.LOCALITY_NAME, "San Francisco"),
            x509.NameAttribute(NameOID.ORGANIZATION_NAME, "Proxy CA"),
            x509.NameAttribute(NameOID.COMMON_NAME, "Proxy CA Root"),
        ])
        
        cert = x509.CertificateBuilder().subject_name(
            subject
        ).issuer_name(
            issuer
        ).public_key(
            private_key.public_key()
        ).serial_number(
            x509.random_serial_number()
        ).not_valid_before(
            datetime.datetime.utcnow()
        ).not_valid_after(
            datetime.datetime.utcnow() + datetime.timedelta(days=3650)
        ).add_extension(
            x509.BasicConstraints(ca=True, path_length=None), critical=True
        ).add_extension(
            x509.KeyUsage(
                digital_signature=True,
                content_commitment=False,
                key_encipherment=True,
                data_encipherment=False,
                key_agreement=False,
                key_cert_sign=True,
                crl_sign=True,
                encipher_only=False,
                decipher_only=False
            ), critical=True
        ).sign(private_key, hashes.SHA256(), default_backend())
        
        # Write certificate to file
        with open(self.ca_cert_path, 'wb') as f:
            f.write(cert.public_bytes(serialization.Encoding.PEM))
    
    def _load_ca_cert(self):
        """Load the CA certificate and private key"""
        with open(self.ca_key_path, 'rb') as f:
            self.ca_key = serialization.load_pem_private_key(
                f.read(),
                password=None,
                backend=default_backend()
            )
        
        with open(self.ca_cert_path, 'rb') as f:
            self.ca_cert = x509.load_pem_x509_certificate(
                f.read(),
                default_backend()
            )
    
    def get_domain_cert(self, domain):
        """Get or generate a certificate for the specified domain"""
        cert_path = self.cert_dir / f"{domain}.crt"
        key_path = self.cert_dir / f"{domain}.key"
        
        if cert_path.exists() and key_path.exists():
            # Load existing certificate and key
            with open(key_path, 'rb') as f:
                private_key = serialization.load_pem_private_key(
                    f.read(),
                    password=None,
                    backend=default_backend()
                )
            
            with open(cert_path, 'rb') as f:
                cert = x509.load_pem_x509_certificate(
                    f.read(),
                    default_backend()
                )
            
            return private_key, cert
        
        # Generate new certificate
        return self._generate_domain_cert(domain)
    
    def _generate_domain_cert(self, domain):
        """Generate a certificate for the specified domain signed by the CA"""
        # Generate private key
        private_key = rsa.generate_private_key(
            public_exponent=65537,
            key_size=2048,
            backend=default_backend()
        )
        
        # Write private key to file
        key_path = self.cert_dir / f"{domain}.key"
        with open(key_path, 'wb') as f:
            f.write(private_key.private_bytes(
                encoding=serialization.Encoding.PEM,
                format=serialization.PrivateFormat.PKCS8,
                encryption_algorithm=serialization.NoEncryption()
            ))
        
        # Create certificate
        subject = x509.Name([
            x509.NameAttribute(NameOID.COUNTRY_NAME, "US"),
            x509.NameAttribute(NameOID.STATE_OR_PROVINCE_NAME, "California"),
            x509.NameAttribute(NameOID.LOCALITY_NAME, "San Francisco"),
            x509.NameAttribute(NameOID.ORGANIZATION_NAME, "Proxy Server"),
            x509.NameAttribute(NameOID.COMMON_NAME, domain),
        ])
        
        cert = x509.CertificateBuilder().subject_name(
            subject
        ).issuer_name(
            self.ca_cert.subject
        ).public_key(
            private_key.public_key()
        ).serial_number(
            x509.random_serial_number()
        ).not_valid_before(
            datetime.datetime.utcnow()
        ).not_valid_after(
            datetime.datetime.utcnow() + datetime.timedelta(days=365)
        ).add_extension(
            x509.SubjectAlternativeName([x509.DNSName(domain)]),
            critical=False
        ).sign(self.ca_key, hashes.SHA256(), default_backend())
        
        # Write certificate to file
        cert_path = self.cert_dir / f"{domain}.crt"
        with open(cert_path, 'wb') as f:
            f.write(cert.public_bytes(serialization.Encoding.PEM))
        
        return private_key, cert
