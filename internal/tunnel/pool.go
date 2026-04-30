package tunnel

import "sync"

type FramePool struct {
	bufSize int
	pool    sync.Pool
}

func NewFramePool(bufSize int) *FramePool {
	fp := &FramePool{bufSize: bufSize}
	fp.pool.New = func() any {
		buf := make([]byte, bufSize)
		return &buf
	}
	return fp
}

func (fp *FramePool) Get() []byte {
	buf := *(fp.pool.Get().(*[]byte))
	return buf[:cap(buf)]
}

func (fp *FramePool) Put(buf []byte) {
	if cap(buf) != fp.bufSize {
		return
	}
	buf = buf[:fp.bufSize]
	fp.pool.Put(&buf)
}

func (fp *FramePool) Clone(payload []byte) []byte {
	buf := fp.Get()
	copy(buf, payload)
	return buf[:len(payload)]
}
