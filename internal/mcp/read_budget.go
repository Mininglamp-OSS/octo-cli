package mcp

import "sync/atomic"

// Keep a separate reserve for small JSON-RPC messages so slow large uploads
// cannot stop discovery and health requests. Charges bound accepted body
// capacity through parsing, execution and response writes: Content-Length,
// or the full per-request cap
// for chunked bodies after their prefix. JSON decoding and brief prefix copies
// add memory outside this admission budget. A minimum charge limits concurrent
// small calls; multipart assembly has its own two-slot execution bound.
const (
	smallReadThreshold = 64 << 10
	minReadCharge      = 4 << 10
	smallReadLimit     = 8 << 20
	largeReadLimit     = 56 << 20
)

type readBudget struct {
	small atomic.Int64
	large atomic.Int64
}

func (b *readBudget) reserve(contentLength int64) (func(), bool) {
	charge := contentLength
	if charge < 0 {
		charge = maxHTTPBodyBytes
	}
	if charge < minReadCharge {
		charge = minReadCharge
	}
	used := &b.large
	limit := int64(largeReadLimit)
	if charge <= smallReadThreshold {
		used = &b.small
		limit = smallReadLimit
	}
	return reserveReadCounter(used, charge, limit)
}

// An unknown-size body starts with at most threshold+1 bytes in memory.
// Reserve the extra classification byte explicitly in the small pool.
func (b *readBudget) reserveUnknownPrefix() (func(), bool) {
	return reserveReadCounter(&b.small, smallReadThreshold+1, smallReadLimit)
}

func reserveReadCounter(used *atomic.Int64, charge, limit int64) (func(), bool) {
	for {
		current := used.Load()
		if current > limit-charge {
			return nil, false
		}
		if used.CompareAndSwap(current, current+charge) {
			return func() { used.Add(-charge) }, true
		}
	}
}
