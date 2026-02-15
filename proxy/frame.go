package proxy

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
)

// FrameCounter wraps an io.Reader and counts gRPC length-prefixed
// message frames that pass through it.
//
// Frame format: [1-byte flags][4-byte big-endian length][payload]
type FrameCounter struct {
	r      io.Reader
	Count  int
	state  int // 0 = header, 1 = payload
	hdrBuf [5]byte
	hdrN   int
	remain uint32
}

// NewFrameCounter creates a FrameCounter wrapping the given reader.
func NewFrameCounter(r io.Reader) *FrameCounter {
	return &FrameCounter{r: r}
}

func (fc *FrameCounter) Read(p []byte) (int, error) {
	n, err := fc.r.Read(p)
	fc.scan(p[:n])
	return n, err
}

func (fc *FrameCounter) scan(data []byte) {
	for len(data) > 0 {
		if fc.state == 0 {
			need := 5 - fc.hdrN
			take := need
			if take > len(data) {
				take = len(data)
			}
			copy(fc.hdrBuf[fc.hdrN:], data[:take])
			fc.hdrN += take
			data = data[take:]
			if fc.hdrN == 5 {
				fc.remain = binary.BigEndian.Uint32(fc.hdrBuf[1:5])
				fc.Count++
				fc.hdrN = 0
				if fc.remain > 0 {
					fc.state = 1
				}
			}
		} else {
			skip := uint32(len(data))
			if skip > fc.remain {
				skip = fc.remain
			}
			fc.remain -= skip
			data = data[skip:]
			if fc.remain == 0 {
				fc.state = 0
			}
		}
	}
}

// DetectCallType determines the CallType based on protocol, content type,
// and observed frame counts.
func DetectCallType(protocol Protocol, contentType string, reqFrames, respFrames *FrameCounter) CallType {
	if protocol == ProtocolConnect {
		if len(contentType) > 0 &&
			(hasPrefix(contentType, "application/connect+proto") ||
				hasPrefix(contentType, "application/connect+json")) {
			return ServerStream
		}
		return Unary
	}

	reqCount := 0
	if reqFrames != nil {
		reqCount = reqFrames.Count
	}
	respCount := 0
	if respFrames != nil {
		respCount = respFrames.Count
	}

	switch {
	case reqCount <= 1 && respCount <= 1:
		return Unary
	case reqCount <= 1:
		return ServerStream
	case respCount <= 1:
		return ClientStream
	default:
		return BidiStream
	}
}

// CaptureReader wraps an io.Reader and stores the first maxSize bytes
// that pass through it.
type CaptureReader struct {
	r       io.Reader
	buf     []byte
	maxSize int
}

// NewCaptureReader creates a CaptureReader that captures up to maxSize bytes.
func NewCaptureReader(r io.Reader, maxSize int) *CaptureReader {
	return &CaptureReader{r: r, maxSize: maxSize}
}

func (cr *CaptureReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	if remaining := cr.maxSize - len(cr.buf); remaining > 0 && n > 0 {
		take := n
		if take > remaining {
			take = remaining
		}
		cr.buf = append(cr.buf, p[:take]...)
	}
	return n, err
}

// Bytes returns the captured data.
func (cr *CaptureReader) Bytes() []byte {
	return cr.buf
}

// ExtractPayload parses the first gRPC length-prefixed frame and returns the
// decompressed payload. If the data is not valid gRPC framing, it is returned
// as-is.
func ExtractPayload(data []byte) []byte {
	if len(data) < 5 {
		return data
	}
	compressed := data[0]
	length := binary.BigEndian.Uint32(data[1:5])
	if uint32(len(data)-5) < length {
		return data
	}
	payload := data[5 : 5+length]
	if compressed == 1 {
		r, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return payload
		}
		decoded, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			return payload
		}
		return decoded
	}
	return payload
}

// DecompressGzip decompresses data if it starts with a gzip magic header.
// Returns the original data unchanged if it is not gzip.
func DecompressGzip(data []byte) []byte {
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		return data
	}
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return data
	}
	decoded, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		return data
	}
	return decoded
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
