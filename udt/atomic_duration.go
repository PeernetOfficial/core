package udt

import (
	"sync/atomic"
	"time"
)

type atomicDuration struct {
	val int64
}

func (s *atomicDuration) get() time.Duration {
	return time.Duration(atomic.LoadInt64(&s.val))
}

func (s *atomicDuration) set(v time.Duration) {
	atomic.StoreInt64(&s.val, int64(v))
}
