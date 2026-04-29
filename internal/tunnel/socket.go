package tunnel

import (
	"net"
	"time"
)

type SocketOptions struct {
	NoDelay         bool
	KeepAlive       time.Duration
	ReadBufferSize  int
	WriteBufferSize int
}

func TuneTCP(conn net.Conn, options SocketOptions) {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return
	}

	_ = tcpConn.SetNoDelay(options.NoDelay)
	_ = tcpConn.SetKeepAlive(true)
	_ = tcpConn.SetKeepAlivePeriod(options.KeepAlive)

	if options.ReadBufferSize > 0 {
		_ = tcpConn.SetReadBuffer(options.ReadBufferSize)
	}

	if options.WriteBufferSize > 0 {
		_ = tcpConn.SetWriteBuffer(options.WriteBufferSize)
	}
}
