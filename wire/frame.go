// Package wire implements the bounded, bidirectional Cordis session protocol.
package wire

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

type Frame struct {
	Schema     string          `json:"schema"`
	Kind       string          `json:"kind"`
	Activation string          `json:"activation"`
	ID         string          `json:"id"`
	Method     string          `json:"method,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Error      *contract.Error `json:"error,omitempty"`
	DeadlineMS int64           `json:"deadlineMs,omitempty"`
	Parent     string          `json:"parent,omitempty"`
}

type receivedField[T any] struct {
	value   T
	present bool
}

func (f *receivedField[T]) UnmarshalJSON(data []byte) error {
	f.present = true
	return contract.Decode(data, &f.value)
}

type receivedFrame struct {
	Schema     string                        `json:"schema"`
	Kind       string                        `json:"kind"`
	Activation string                        `json:"activation"`
	ID         string                        `json:"id"`
	Method     receivedField[string]         `json:"method"`
	Payload    json.RawMessage               `json:"payload"`
	Error      receivedField[contract.Error] `json:"error"`
	DeadlineMS receivedField[int64]          `json:"deadlineMs"`
	Parent     receivedField[string]         `json:"parent"`
}

type Limits struct {
	FrameBytes int
	Inflight   int
	Deadline   time.Duration
	Grace      time.Duration
}

func DefaultLimits() Limits {
	return Limits{FrameBytes: 1 << 20, Inflight: 64, Deadline: 30 * time.Second, Grace: time.Second}
}

func readFrame(reader *bufio.Reader, limit int) (Frame, error) {
	data := make([]byte, 0, min(limit, 4096))
	for {
		part, err := reader.ReadSlice('\n')
		if len(data)+len(part) > limit {
			return Frame{}, contract.Fail(contract.Invalid, "frame exceeds byte limit")
		}
		data = append(data, part...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			if errors.Is(err, io.EOF) && len(data) != 0 {
				return Frame{}, contract.Fail(contract.Invalid, "unterminated frame")
			}
			return Frame{}, fmt.Errorf("read frame: %w", err)
		}
		break
	}
	var received receivedFrame
	if err := contract.Decode(data, &received); err != nil {
		return Frame{}, err
	}
	frame := Frame{Schema: received.Schema, Kind: received.Kind, Activation: received.Activation, ID: received.ID, Method: received.Method.value, Payload: received.Payload, DeadlineMS: received.DeadlineMS.value, Parent: received.Parent.value}
	if received.Error.present {
		frame.Error = &received.Error.value
	}
	if frame.Schema != contract.SessionSchema || frame.ID == "" || frame.Activation == "" {
		return Frame{}, contract.Fail(contract.Invalid, "invalid frame identity")
	}
	if len(frame.ID) > 128 || len(frame.Method) > 256 || len(frame.Parent) > 128 {
		return Frame{}, contract.Fail(contract.Invalid, "frame identifier limit exceeded")
	}
	switch frame.Kind {
	case "request":
		if frame.Method == "" || frame.Error != nil || len(frame.Payload) == 0 || frame.DeadlineMS < 1 || frame.DeadlineMS > 120000 {
			return Frame{}, contract.Fail(contract.Invalid, "invalid request fields")
		}
	case "response":
		if frame.Method == "" || (frame.Error == nil) == (len(frame.Payload) == 0) || received.Parent.present || received.DeadlineMS.present {
			return Frame{}, contract.Fail(contract.Invalid, "invalid response fields")
		}
	case "cancel":
		if received.Method.present || received.Error.present || len(frame.Payload) != 0 || received.DeadlineMS.present || received.Parent.present {
			return Frame{}, contract.Fail(contract.Invalid, "invalid cancel fields")
		}
	default:
		return Frame{}, contract.Fail(contract.Invalid, "unknown frame kind")
	}
	return frame, nil
}

func encodeFrame(frame Frame, limit int) ([]byte, error) {
	data, err := json.Marshal(frame)
	if err != nil {
		return nil, fmt.Errorf("encode frame: %w", err)
	}
	if len(data)+1 > limit {
		return nil, contract.Fail(contract.Invalid, "frame exceeds byte limit")
	}
	return append(data, '\n'), nil
}

type requestKey struct{}
type requestReference struct {
	endpoint *Endpoint
	id       string
}

func RequestID(ctx interface{ Value(any) any }) string {
	ref, _ := ctx.Value(requestKey{}).(requestReference)
	return ref.id
}
