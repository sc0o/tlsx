// Package ztls implements a tls grabbing implementation using
// zmap zcrypto/tls library.
package ztls

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/projectdiscovery/tlsx/pkg/tlsx/clients"
	"github.com/zmap/zcrypto/tls"
	"github.com/zmap/zcrypto/x509"
)

// Client is a TLS grabbing client using crypto/tls
type Client struct {
	dialer    *net.Dialer
	tlsConfig *tls.Config
}

// versionStringToTLSVersion converts tls version string to version
var versionStringToTLSVersion = map[string]uint16{
	"ssl30": tls.VersionSSL30,
	"tls10": tls.VersionTLS10,
	"tls11": tls.VersionTLS11,
	"tls12": tls.VersionTLS12,
}

// versionToTLSVersionString converts tls version to version string
var versionToTLSVersionString = map[uint16]string{
	tls.VersionSSL30: "ssl30",
	tls.VersionTLS10: "tls10",
	tls.VersionTLS11: "tls11",
	tls.VersionTLS12: "tls12",
}

// New creates a new grabbing client using crypto/tls
func New(options *clients.Options) (*Client, error) {
	c := &Client{
		dialer: &net.Dialer{
			Timeout: time.Duration(options.Timeout) * time.Second,
		},
		tlsConfig: &tls.Config{
			CertsOnly:          options.CertsOnly,
			MinVersion:         tls.VersionSSL30,
			MaxVersion:         tls.VersionTLS12,
			InsecureSkipVerify: !options.VerifyServerCertificate,
		},
	}
	if options.ServerName != "" {
		c.tlsConfig.ServerName = options.ServerName
	}
	if options.MinVersion != "" {
		version, ok := versionStringToTLSVersion[options.MinVersion]
		if !ok {
			return nil, fmt.Errorf("invalid min version specified: %s", options.MinVersion)
		} else {
			c.tlsConfig.MinVersion = version
		}
	}
	if options.MaxVersion != "" {
		version, ok := versionStringToTLSVersion[options.MaxVersion]
		if !ok {
			return nil, fmt.Errorf("invalid max version specified: %s", options.MaxVersion)
		} else {
			c.tlsConfig.MaxVersion = version
		}
	}
	return c, nil
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "tls: DialWithDialer timed out" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// Connect connects to a host and grabs the response data
func (c *Client) Connect(hostname, port string) (*clients.Response, error) {
	address := net.JoinHostPort(hostname, port)
	timeout := c.dialer.Timeout

	var errChannel chan error
	if timeout != 0 {
		errChannel = make(chan error, 2)
		time.AfterFunc(timeout, func() {
			errChannel <- timeoutError{}
		})
	}

	conn, err := c.dialer.Dial("tcp", address)
	if err != nil {
		return nil, errors.Wrap(err, "could not connect to address")
	}
	defer conn.Close()

	colonPos := strings.LastIndex(address, ":")
	if colonPos == -1 {
		colonPos = len(address)
	}
	hostnameValue := address[:colonPos]

	config := c.tlsConfig
	if config.ServerName == "" {
		c := *config
		c.ServerName = hostnameValue
		config = &c
	}

	tlsConn := tls.Client(conn, c.tlsConfig)
	if timeout == 0 {
		err = tlsConn.Handshake()
	} else {
		go func() {
			errChannel <- tlsConn.Handshake()
		}()
		err = <-errChannel
	}
	if err == tls.ErrCertsOnly {
		err = nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "could not do tls handshake")
	}
	hl := tlsConn.GetHandshakeLog()

	tlsVersion := versionToTLSVersionString[uint16(hl.ServerHello.Version)]
	response := &clients.Response{
		Host:          hostname,
		Port:          port,
		Version:       tlsVersion,
		TLSConnection: "ztls",
		Leaf:          convertCertificateToResponse(parseSimpleTLSCertificate(hl.ServerCertificates.Certificate)),
	}
	for _, cert := range hl.ServerCertificates.Chain {
		response.Chain = append(response.Chain, convertCertificateToResponse(parseSimpleTLSCertificate(cert)))
	}
	return response, nil
}

func parseSimpleTLSCertificate(cert tls.SimpleCertificate) *x509.Certificate {
	parsed, _ := x509.ParseCertificate(cert.Raw)
	return parsed
}

func convertCertificateToResponse(cert *x509.Certificate) clients.CertificateResponse {
	if cert == nil {
		return clients.CertificateResponse{}
	}
	return clients.CertificateResponse{
		DNSNames:            cert.DNSNames,
		Emails:              cert.EmailAddresses,
		IssuerCommonName:    cert.Issuer.CommonName,
		IssuerOrganization:  cert.Issuer.Organization,
		SubjectCommonName:   cert.Subject.CommonName,
		SubjectOrganization: cert.Subject.Organization,
	}
}
