package tool

import "bytes"

// limitedOutputBuffer records the total bytes written while retaining only
// the first cap bytes. It is for subprocess output where the producer may be
// untrusted or effectively infinite.
type limitedOutputBuffer struct {
	buf      bytes.Buffer
	limit    int
	observed int
}

func newLimitedOutputBuffer(limit int) *limitedOutputBuffer {
	return &limitedOutputBuffer{limit: limit}
}

func (b *limitedOutputBuffer) Write(p []byte) (int, error) {
	b.observed += len(p)
	if b.limit <= 0 || b.buf.Len() >= b.limit {
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if len(p) > remaining {
		p = p[:remaining]
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *limitedOutputBuffer) WriteString(s string) {
	_, _ = b.Write([]byte(s))
}

func (b *limitedOutputBuffer) String() string {
	return truncateOnRune(b.buf.String(), b.buf.Len())
}

func (b *limitedOutputBuffer) Len() int {
	return b.buf.Len()
}

func (b *limitedOutputBuffer) Observed() int {
	return b.observed
}

func (b *limitedOutputBuffer) Truncated() bool {
	return b.observed > b.buf.Len()
}
