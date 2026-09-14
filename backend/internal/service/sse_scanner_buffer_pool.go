package service

import (
	"bufio"
	"sync"
)

const sseScannerBuf64KSize = 64 * 1024

// Scanner never shrinks after a large token. Do not pass gateway.max_line_size
// (multi-MB) as maxTokenSize or each SSE connection retains a huge arena.
const sseScannerTokenMax = 1024 * 1024

// OpenAI /v1/responses 首包之后允许大于 8MiB 的图片 delta，但不能跟 500MiB max_line_size 扩。
const sseScannerTokenMaxOpenAI = 16 * 1024 * 1024

func attachSSEScannerBuffer(scanner *bufio.Scanner, scratch []byte, maxToken int) {
	attachSSEScannerBufferCapped(scanner, scratch, maxToken, sseScannerTokenMax)
}

func attachSSEScannerBufferCapped(scanner *bufio.Scanner, scratch []byte, maxToken, capBytes int) {
	if scanner == nil {
		return
	}
	if capBytes <= 0 {
		capBytes = sseScannerTokenMax
	}
	if maxToken <= 0 || maxToken > capBytes {
		maxToken = capBytes
	}
	if cap(scratch) == 0 {
		initial := 64 * 1024
		if initial > maxToken {
			initial = maxToken
		}
		scratch = make([]byte, 0, initial)
	}
	// Buffer 把 cap(buf) 当初始缓冲长度。cap 大于 maxToken 时，短于 cap 的超长行不会触发 ErrTooLong。
	if cap(scratch) > maxToken {
		scratch = scratch[:0:maxToken]
	} else {
		scratch = scratch[:0]
	}
	scanner.Buffer(scratch, maxToken)
}

type sseScannerBuf64K [sseScannerBuf64KSize]byte

var sseScannerBuf64KPool = sync.Pool{
	New: func() any {
		return new(sseScannerBuf64K)
	},
}

func getSSEScannerBuf64K() *sseScannerBuf64K {
	v := sseScannerBuf64KPool.Get()
	buf, ok := v.(*sseScannerBuf64K)
	if !ok || buf == nil {
		return new(sseScannerBuf64K)
	}
	return buf
}

func putSSEScannerBuf64K(buf *sseScannerBuf64K) {
	if buf == nil {
		return
	}
	sseScannerBuf64KPool.Put(buf)
}
