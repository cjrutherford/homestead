package everything

import (
	"crypto/tls"
	"crypto/x509"
)

func newTLS(pool *x509.CertPool) *tls.Config {
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}
