package util

import (
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/gorilla/websocket"
)

// TerminalSize is a cols/rows pair, decoupled from any k8s import so
// this package doesn't drag in client-go.
type TerminalSize struct {
	Cols uint16
	Rows uint16
}

type wsCtrlMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// WsWriter adapts a gorilla websocket connection into an io.Writer.
// Each Write call sends a single binary frame. The mutex is required
// because gorilla websocket connections are not safe for concurrent
// writes (stdout and stderr may both be wired to the same writer).
type WsWriter struct {
	Conn *websocket.Conn
	mu   sync.Mutex
}

func (w *WsWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.Conn.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// WsSession multiplexes a single websocket connection:
//   - binary frames → stdin pipe (terminal input bytes)
//   - text frames   → parsed as JSON control messages; resize events
//     surface on the Resize channel
//   - the embedded *WsWriter sends binary frames back to the client
//     (terminal output)
//
// Call Start once in a goroutine after construction. The session
// closes when the websocket closes or the supplied context cancels.
type WsSession struct {
	Conn   *websocket.Conn
	Writer *WsWriter

	stdinR *io.PipeReader
	stdinW *io.PipeWriter
	resize chan TerminalSize

	closeOnce sync.Once
}

func NewWsSession(c *websocket.Conn) *WsSession {
	pr, pw := io.Pipe()
	return &WsSession{
		Conn:   c,
		Writer: &WsWriter{Conn: c},
		stdinR: pr,
		stdinW: pw,
		resize: make(chan TerminalSize, 8),
	}
}

func (s *WsSession) Stdin() io.Reader         { return s.stdinR }
func (s *WsSession) Stdout() io.Writer        { return s.Writer }
func (s *WsSession) Stderr() io.Writer        { return s.Writer }
func (s *WsSession) Resize() <-chan TerminalSize { return s.resize }

// Start blocks reading WS frames and dispatching them. When it
// returns, both the stdin pipe and the resize channel are closed,
// which unblocks any consumer that was reading from them.
func (s *WsSession) Start(ctx context.Context) {
	defer s.Close()

	// If the caller's context cancels, force-close the connection so
	// ReadMessage returns and we tear down.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = s.Conn.Close()
		case <-done:
		}
	}()

	for {
		mt, data, err := s.Conn.ReadMessage()
		if err != nil {
			return
		}
		switch mt {
		case websocket.BinaryMessage:
			if len(data) == 0 {
				continue
			}
			if _, err := s.stdinW.Write(data); err != nil {
				return
			}
		case websocket.TextMessage:
			var m wsCtrlMsg
			if err := json.Unmarshal(data, &m); err != nil {
				continue
			}
			if m.Type == "resize" && m.Cols > 0 && m.Rows > 0 {
				s.pushResize(TerminalSize{Cols: m.Cols, Rows: m.Rows})
			}
		}
	}
}

// pushResize never blocks: if the consumer is slow we discard the
// oldest pending size and keep only the latest, since intermediate
// sizes are uninteresting.
func (s *WsSession) pushResize(sz TerminalSize) {
	for {
		select {
		case s.resize <- sz:
			return
		default:
			select {
			case <-s.resize:
			default:
				return
			}
		}
	}
}

func (s *WsSession) Close() {
	s.closeOnce.Do(func() {
		_ = s.stdinW.Close()
		close(s.resize)
	})
}
