#!/bin/bash

set -euo pipefail
set -x

# Cd to the target directory so that the certs are generated there.
# It is provided as an argument to the script.
cd "$1"

# Generate a private key and a self-signed certificate for the certificate authority.
openssl ecparam -genkey -noout -name prime256v1 -out ca-private-key.pem
openssl req -new -x509 -nodes -days 3650 -sha256 -key ca-private-key.pem -subj "/CN=CA" -out ca-cert.pem

# Generate a private key, a certificate signing request for the server, and sign it with the CA.
openssl ecparam -genkey -name prime256v1 -out server-private-key.pem
openssl req -new -sha256 -key server-private-key.pem -addext "subjectAltName = IP:127.0.0.1" -subj "/CN=localhost" -out server-csr.pem
openssl x509 -req -sha256 -in server-csr.pem -CA ca-cert.pem -CAkey ca-private-key.pem -CAcreateserial -out server-cert.pem -days 3650 -extfile <(echo "subjectAltName=IP:127.0.0.1,DNS:localhost")

# Generate a private key, a certificate signing request for the client, and sign it with the CA.
openssl ecparam -genkey -name prime256v1 -out client-private-key.pem
openssl req -new -sha256 -key client-private-key.pem -subj "/CN=client" -out client-csr.pem
openssl x509 -req -sha256 -in client-csr.pem -CA ca-cert.pem -CAkey ca-private-key.pem -CAcreateserial -out client-cert.pem -days 3650