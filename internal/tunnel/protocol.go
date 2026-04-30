package tunnel

import (
	"encoding/binary"
	"fmt"
)

const (
	FrameData       byte = 'D'
	FrameEOF        byte = 'E'
	frameHeaderSize      = 9
)

type ConnectRequest struct {
	CID  uint64 `json:"cid"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

func ConnectSubject(prefix string) string {
	return prefix + ".connect"
}

func UpSubject(prefix string, shard int) string {
	return fmt.Sprintf("%s.up.%02d", prefix, shard)
}

func DownSubject(prefix string, shard int) string {
	return fmt.Sprintf("%s.down.%02d", prefix, shard)
}

func DataShard(sessionID uint64, shardCount int) int {
	if shardCount <= 1 {
		return 0
	}
	return int(sessionID % uint64(shardCount))
}

func FrameBufferSize(payloadSize int) int {
	return frameHeaderSize + payloadSize
}

func DataFrame(buf []byte, sessionID uint64, n int) []byte {
	buf[0] = FrameData
	binary.BigEndian.PutUint64(buf[1:frameHeaderSize], sessionID)
	return buf[:n+frameHeaderSize]
}

func EOFFrame(sessionID uint64) []byte {
	buf := make([]byte, frameHeaderSize)
	buf[0] = FrameEOF
	binary.BigEndian.PutUint64(buf[1:frameHeaderSize], sessionID)
	return buf
}

func SessionIDFromFrame(payload []byte) (uint64, error) {
	if len(payload) < frameHeaderSize {
		return 0, fmt.Errorf("frame too short: %d", len(payload))
	}

	return binary.BigEndian.Uint64(payload[1:frameHeaderSize]), nil
}

func FrameType(payload []byte) (byte, error) {
	if len(payload) < frameHeaderSize {
		return 0, fmt.Errorf("frame too short: %d", len(payload))
	}

	return payload[0], nil
}

func FramePayload(payload []byte) ([]byte, error) {
	if len(payload) < frameHeaderSize {
		return nil, fmt.Errorf("frame too short: %d", len(payload))
	}

	return payload[frameHeaderSize:], nil
}
