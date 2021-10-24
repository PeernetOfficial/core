package udt

import (
	"sync/atomic"
)

type atomicUint32 struct {
	val uint32
}

func (s *atomicUint32) get() uint32 {
	return atomic.LoadUint32(&s.val)
}

func (s *atomicUint32) set(v uint32) {
	atomic.StoreUint32(&s.val, v)
}
