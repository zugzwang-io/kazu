#!/bin/sh
# Regenerates the test CA and fakestripe's certificate. Test-only: the CA key
# is committed on purpose so Kazu can terminate TLS on this edge (DESIGN §4.4).
set -eu
cd "$(dirname "$0")"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 7300 \
  -subj "/CN=shop test CA" -keyout ca-key.pem -out ca.pem \
  -addext basicConstraints=critical,CA:TRUE -addext keyUsage=critical,keyCertSign,cRLSign
openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
  -subj "/CN=fakestripe" -keyout fakestripe-key.pem -out fakestripe.csr
openssl x509 -req -in fakestripe.csr -CA ca.pem -CAkey ca-key.pem -CAcreateserial -days 7300 -out fakestripe.pem \
  -extfile /dev/stdin <<EXT
subjectAltName=DNS:fakestripe,DNS:localhost,IP:127.0.0.1
extendedKeyUsage=serverAuth
EXT
rm -f fakestripe.csr ca.srl
chmod 644 *.pem
