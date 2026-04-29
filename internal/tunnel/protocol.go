package tunnel

import (
	"fmt"
	"strings"
)

const (
	FrameData byte = 'D'
	FrameEOF  byte = 'E'
)

type ConnectRequest struct {
	CID  string `json:"cid"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

func ConnectSubject(prefix string) string {
	return prefix + ".connect"
}

func UpSubject(prefix, id string) string {
	return prefix + ".up." + id
}

func DownSubject(prefix, id string) string {
	return prefix + ".down." + id
}

func UpWildcard(prefix string) string {
	return prefix + ".up.*"
}

func DownWildcard(prefix string) string {
	return prefix + ".down.*"
}

func SessionIDFromSubject(subject string) (string, error) {
	idx := strings.LastIndexByte(subject, '.')
	if idx == -1 || idx == len(subject)-1 {
		return "", fmt.Errorf("invalid subject %q", subject)
	}

	return subject[idx+1:], nil
}

func DataFrame(buf []byte, n int) []byte {
	buf[0] = FrameData
	return buf[:n+1]
}

func EOFFrame() []byte {
	return []byte{FrameEOF}
}
