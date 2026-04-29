package tunnel

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

const (
	socksVersion5        = 0x05
	socksCmdConnect      = 0x01
	socksAddrIPv4        = 0x01
	socksAddrDomain      = 0x03
	socksAddrIPv6        = 0x04
	socksReplySucceeded  = 0x00
	socksReplyFailure    = 0x01
	socksReplyCmdNotSup  = 0x07
	socksReplyAddrNotSup = 0x08
)

type SocksAddress struct {
	Host string
	Port int
}

func AcceptSOCKS5(conn net.Conn) (SocksAddress, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return SocksAddress{}, err
	}

	version := header[0]
	methodCount := int(header[1])
	if version != socksVersion5 {
		return SocksAddress{}, fmt.Errorf("unsupported SOCKS version %d", version)
	}

	methods := make([]byte, methodCount)
	if _, err := io.ReadFull(conn, methods); err != nil {
		return SocksAddress{}, err
	}

	if _, err := conn.Write([]byte{socksVersion5, 0x00}); err != nil {
		return SocksAddress{}, err
	}

	requestHeader := make([]byte, 4)
	if _, err := io.ReadFull(conn, requestHeader); err != nil {
		return SocksAddress{}, err
	}

	if requestHeader[0] != socksVersion5 {
		return SocksAddress{}, fmt.Errorf("unsupported SOCKS version %d", requestHeader[0])
	}

	if requestHeader[1] != socksCmdConnect {
		_ = WriteSOCKSReply(conn, socksReplyCmdNotSup)
		return SocksAddress{}, fmt.Errorf("unsupported SOCKS command %d", requestHeader[1])
	}

	addr, err := readSOCKSAddress(conn, requestHeader[3])
	if err != nil {
		_ = WriteSOCKSReply(conn, socksReplyAddrNotSup)
		return SocksAddress{}, err
	}

	return addr, nil
}

func WriteSOCKSReply(conn net.Conn, code byte) error {
	_, err := conn.Write([]byte{socksVersion5, code, 0x00, socksAddrIPv4, 0, 0, 0, 0, 0, 0})
	return err
}

func SuccessReply(conn net.Conn) error {
	return WriteSOCKSReply(conn, socksReplySucceeded)
}

func FailureReply(conn net.Conn) error {
	return WriteSOCKSReply(conn, socksReplyFailure)
}

func readSOCKSAddress(conn net.Conn, atyp byte) (SocksAddress, error) {
	switch atyp {
	case socksAddrIPv4:
		buf := make([]byte, 6)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return SocksAddress{}, err
		}
		return SocksAddress{
			Host: net.IP(buf[:4]).String(),
			Port: int(binary.BigEndian.Uint16(buf[4:])),
		}, nil
	case socksAddrDomain:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return SocksAddress{}, err
		}
		buf := make([]byte, int(length[0])+2)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return SocksAddress{}, err
		}
		return SocksAddress{
			Host: string(buf[:len(buf)-2]),
			Port: int(binary.BigEndian.Uint16(buf[len(buf)-2:])),
		}, nil
	case socksAddrIPv6:
		buf := make([]byte, 18)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return SocksAddress{}, err
		}
		return SocksAddress{
			Host: net.IP(buf[:16]).String(),
			Port: int(binary.BigEndian.Uint16(buf[16:])),
		}, nil
	default:
		return SocksAddress{}, fmt.Errorf("unsupported SOCKS address type %d", atyp)
	}
}
