package proxy

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
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

// ProtoWireToJSON converts protobuf wire-format bytes into a schema-less JSON
// representation using field numbers as keys: {"1": "hello", "2": 42}.
// Bytes fields that look like valid UTF-8 text are emitted as strings;
// bytes that decode as nested protobuf messages are emitted as objects;
// otherwise they are emitted as hex strings.
func ProtoWireToJSON(data []byte) ([]byte, error) {
	m, err := protoWireToMap(data)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(m, "", "  ")
}

func protoWireToMap(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	m := make(map[string]any)
	for len(data) > 0 {
		num, wtype, n := protowire.ConsumeTag(data)
		if n < 0 {
			return nil, fmt.Errorf("invalid protobuf tag")
		}
		data = data[n:]
		key := strconv.FormatInt(int64(num), 10)

		switch wtype {
		case protowire.VarintType:
			v, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return nil, fmt.Errorf("invalid varint for field %d", num)
			}
			data = data[n:]
			m[key] = v
		case protowire.Fixed32Type:
			v, n := protowire.ConsumeFixed32(data)
			if n < 0 {
				return nil, fmt.Errorf("invalid fixed32 for field %d", num)
			}
			data = data[n:]
			m[key] = v
		case protowire.Fixed64Type:
			v, n := protowire.ConsumeFixed64(data)
			if n < 0 {
				return nil, fmt.Errorf("invalid fixed64 for field %d", num)
			}
			data = data[n:]
			m[key] = v
		case protowire.BytesType:
			v, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return nil, fmt.Errorf("invalid bytes for field %d", num)
			}
			data = data[n:]
			// Try nested message
			if nested, err := protoWireToMap(v); err == nil && len(nested) > 0 {
				m[key] = nested
			} else if utf8.Valid(v) && isPrintableBytes(v) {
				m[key] = string(v)
			} else {
				m[key] = fmt.Sprintf("%x", v)
			}
		default:
			return nil, fmt.Errorf("unsupported wire type %d for field %d", wtype, num)
		}
	}
	return m, nil
}

// JSONToProtoWire converts a schema-less JSON object (field numbers as keys)
// back into protobuf wire format. It applies heuristics to determine wire types:
//   - string → bytes (field type 2)
//   - integer (float64 with no fraction) → varint
//   - object → nested message (bytes)
func JSONToProtoWire(data []byte) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}
	return mapToProtoWire(m)
}

func mapToProtoWire(m map[string]any) ([]byte, error) {
	// Sort keys for deterministic output.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, _ := strconv.ParseInt(keys[i], 10, 64)
		b, _ := strconv.ParseInt(keys[j], 10, 64)
		return a < b
	})

	var buf []byte
	for _, key := range keys {
		num, err := strconv.ParseInt(key, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid field number %q", key)
		}
		fieldNum := protowire.Number(num)

		switch v := m[key].(type) {
		case string:
			buf = protowire.AppendTag(buf, fieldNum, protowire.BytesType)
			buf = protowire.AppendString(buf, v)
		case float64:
			if v == math.Trunc(v) && v >= 0 && v <= math.MaxInt64 {
				buf = protowire.AppendTag(buf, fieldNum, protowire.VarintType)
				buf = protowire.AppendVarint(buf, uint64(v))
			} else {
				buf = protowire.AppendTag(buf, fieldNum, protowire.Fixed64Type)
				buf = protowire.AppendFixed64(buf, math.Float64bits(v))
			}
		case map[string]any:
			nested, err := mapToProtoWire(v)
			if err != nil {
				return nil, fmt.Errorf("field %d: %w", num, err)
			}
			buf = protowire.AppendTag(buf, fieldNum, protowire.BytesType)
			buf = protowire.AppendBytes(buf, nested)
		case bool:
			buf = protowire.AppendTag(buf, fieldNum, protowire.VarintType)
			if v {
				buf = protowire.AppendVarint(buf, 1)
			} else {
				buf = protowire.AppendVarint(buf, 0)
			}
		default:
			return nil, fmt.Errorf("unsupported type %T for field %d", v, num)
		}
	}
	return buf, nil
}

func isPrintableBytes(data []byte) bool {
	for _, b := range data {
		if b < 0x20 && b != '\n' && b != '\r' && b != '\t' {
			return false
		}
	}
	return true
}
