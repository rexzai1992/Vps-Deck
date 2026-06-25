package nginxscanner

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"time"
)

// ReadCertExpiry reads an X.509 PEM certificate file and returns the
// NotAfter time. Returns (nil, nil) when the file is unreadable so that
// permission errors on /etc/letsencrypt are silently skipped.
func ReadCertExpiry(path string) (*time.Time, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil // unreadable — not fatal
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block in %s", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse cert %s: %w", path, err)
	}
	expiry := cert.NotAfter.UTC()
	return &expiry, nil
}
