package tunnel

import (
	"bytes"
	"io"
	"net"
	"testing"
)

func TestAcceptSOCKS5DomainConnect(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	errCh := make(chan error, 1)
	go func() {
		defer close(errCh)
		if _, err := client.Write([]byte{0x05, 0x01, 0x00}); err != nil {
			errCh <- err
			return
		}

		reply := make([]byte, 2)
		if _, err := io.ReadFull(client, reply); err != nil {
			errCh <- err
			return
		}

		if !bytes.Equal(reply, []byte{0x05, 0x00}) {
			errCh <- io.ErrUnexpectedEOF
			return
		}

		_, err := client.Write([]byte{
			0x05, 0x01, 0x00, 0x03,
			0x0b,
			'e', 'x', 'a', 'm', 'p', 'l', 'e', '.', 'c', 'o', 'm',
			0x01, 0xbb,
		})
		errCh <- err
	}()

	addr, err := AcceptSOCKS5(server)
	if err != nil {
		t.Fatalf("accept socks5: %v", err)
	}

	if got, want := addr.Host, "example.com"; got != want {
		t.Fatalf("host = %q, want %q", got, want)
	}
	if got, want := addr.Port, 443; got != want {
		t.Fatalf("port = %d, want %d", got, want)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("client write failed: %v", err)
	}
}

func TestAcceptSOCKS5RejectsUnsupportedCommand(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	replyCh := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go func() {
		defer close(errCh)
		_, _ = client.Write([]byte{0x05, 0x01, 0x00})
		reply := make([]byte, 2)
		if _, err := io.ReadFull(client, reply); err != nil {
			errCh <- err
			return
		}
		if !bytes.Equal(reply, []byte{0x05, 0x00}) {
			errCh <- io.ErrUnexpectedEOF
			return
		}
		_, err := client.Write([]byte{0x05, 0x02, 0x00, 0x01})
		if err != nil {
			errCh <- err
			return
		}

		failureReply := make([]byte, 10)
		if _, err := io.ReadFull(client, failureReply); err != nil {
			errCh <- err
			return
		}

		replyCh <- failureReply
		errCh <- nil
	}()

	_, err := AcceptSOCKS5(server)
	if err == nil {
		t.Fatalf("expected unsupported command error")
	}

	reply := <-replyCh
	if reply[0] != 0x05 || reply[1] != socksReplyCmdNotSup {
		t.Fatalf("command failure reply = %v", reply)
	}
	if reply[3] != 0x01 {
		t.Fatalf("reply atyp = %d, want 1", reply[3])
	}

	if err := <-errCh; err != nil {
		t.Fatalf("client exchange failed: %v", err)
	}
}

func TestWriteSOCKSReply(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() {
		_ = WriteSOCKSReply(server, socksReplyFailure)
	}()

	payload := make([]byte, 10)
	if _, err := io.ReadFull(client, payload); err != nil {
		t.Fatalf("read reply: %v", err)
	}

	want := []byte{0x05, socksReplyFailure, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	if !bytes.Equal(payload, want) {
		t.Fatalf("reply = %v, want %v", payload, want)
	}
}
