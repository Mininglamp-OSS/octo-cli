package permission

import (
	"crypto/tls"
	"net"
)

type publicationTLSListener struct {
	net.Listener
	config *tls.Config
}

// PublicationTLSListener retains the raw owned socket beneath server TLS.
// HTTP/1.1 hijacking transfers exclusive writer ownership before publication.
func PublicationTLSListener(listener net.Listener, config *tls.Config) net.Listener {
	conf := config.Clone()
	conf.NextProtos = []string{"http/1.1"}
	return &publicationTLSListener{listener, conf}
}

// Accept wraps the raw socket before the TLS handshake starts.
func (l *publicationTLSListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return tls.Server(&wireCapture{Conn: conn}, l.config), nil
}
