package h2spec

import (
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/bradfitz/http2"
	"github.com/bradfitz/http2/hpack"
	"net"
	"os"
	"strings"
	"time"
)

type TcpConn struct {
	conn   net.Conn
	dataCh chan []byte
	errCh  chan error
}

type Http2Conn struct {
	conn   net.Conn
	fr     *http2.Framer
	dataCh chan http2.Frame
	errCh  chan error
}

// ReadFrame reads a complete HTTP/2 frame from underlying connection.
// This function blocks until a complete frame is received or timeout
// t is expired.  The returned http2.Frame must not be used after next
// ReadFrame call.
func (h2Conn *Http2Conn) ReadFrame(t time.Duration) (http2.Frame, error) {
	go func() {
		f, err := h2Conn.fr.ReadFrame()
		if err != nil {
			h2Conn.errCh <- err
			return
		}
		h2Conn.dataCh <- f
	}()

	select {
	case f := <-h2Conn.dataCh:
		return f, nil
	case err := <-h2Conn.errCh:
		return nil, err
	case <-time.After(t):
		return nil, errors.New("timeout waiting for frame")
	}
}

type Context struct {
	Port      int
	Host      string
	Tls       bool
	TlsConfig *tls.Config
	Sections  map[string]bool
	Timeout   time.Duration
}

func (ctx *Context) Authority() string {
	return fmt.Sprintf("%s:%d", ctx.Host, ctx.Port)
}

func (ctx *Context) IsTarget(section string) bool {
	if ctx.Sections == nil {
		return true
	}

	_, ok := ctx.Sections[section]
	return ok
}

func Run(ctx *Context) {
	TestHttp2ConnectionPreface(ctx)
	TestFrameSize(ctx)
	TestHeaderCompressionAndDecompression(ctx)
	TestStreamStates(ctx)
	TestErrorHandling(ctx)
	TestData(ctx)
	TestHeaders(ctx)
	TestPriority(ctx)
	TestRstStream(ctx)
	TestSettings(ctx)
	TestPing(ctx)
	TestGoaway(ctx)
	TestWindowUpdate(ctx)
	TestContinuation(ctx)
	TestHTTPRequestResponseExchange(ctx)
	TestServerPush(ctx)
}

func connectTls(ctx *Context) (net.Conn, error) {
	if ctx.TlsConfig == nil {
		ctx.TlsConfig = new(tls.Config)
	}
	if ctx.TlsConfig.NextProtos == nil {
		ctx.TlsConfig.NextProtos = append(ctx.TlsConfig.NextProtos, "h2-14", "h2-15", "h2-16")
	}
	conn, err := tls.Dial("tcp", ctx.Authority(), ctx.TlsConfig)
	if err != nil {
		return nil, err
	}

	cs := conn.ConnectionState()
	if !cs.NegotiatedProtocolIsMutual {
		return nil, fmt.Errorf("HTTP/2 protocol was not negotiated")
	}

	return conn, err
}

func CreateTcpConn(ctx *Context) *TcpConn {
	var conn net.Conn
	var err error
	if ctx.Tls {
		conn, err = connectTls(ctx)
	} else {
		conn, err = net.Dial("tcp", ctx.Authority())
	}

	if err != nil {
		fmt.Printf("Unable to connect to the target server: %v\n", err)
		os.Exit(1)
	}

	dataCh := make(chan []byte)
	errCh := make(chan error, 1)

	tcpConn := &TcpConn{
		conn:   conn,
		dataCh: dataCh,
		errCh:  errCh,
	}

	go func() {
		for {
			buf := make([]byte, 512)
			_, err := conn.Read(buf)
			dataCh <- buf
			if err != nil {
				errCh <- err
				return
			}
		}
	}()

	return tcpConn
}

func CreateHttp2Conn(ctx *Context, sn bool) *Http2Conn {
	var conn net.Conn
	var err error
	if ctx.Tls {
		conn, err = connectTls(ctx)
	} else {
		conn, err = net.Dial("tcp", ctx.Authority())
	}

	if err != nil {
		fmt.Printf("Unable to connect to the target server: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(conn, "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n")

	fr := http2.NewFramer(conn, conn)

	if sn {
		done := false
		fr.WriteSettings()

		for {
			f, _ := fr.ReadFrame()
			switch f := f.(type) {
			case *http2.SettingsFrame:
				if f.IsAck() {
					done = true
				} else {
					fr.WriteSettingsAck()
				}
			default:
				done = true
			}

			if done {
				break
			}
		}
	}

	fr.AllowIllegalWrites = true
	dataCh := make(chan http2.Frame)
	errCh := make(chan error, 1)

	http2Conn := &Http2Conn{
		conn:   conn,
		fr:     fr,
		dataCh: dataCh,
		errCh:  errCh,
	}

	return http2Conn
}

func SetReadTimer(conn net.Conn, sec time.Duration) {
	now := time.Now()
	conn.SetReadDeadline(now.Add(time.Second * sec))
}

func PrintHeader(title string, i int) {
	fmt.Printf("%s%s\n", strings.Repeat("  ", i), title)
}

func PrintFooter() {
	fmt.Println("")
}

func PrintResult(result bool, desc string, msg string, i int) {
	var mark string
	indent := strings.Repeat("  ", i+1)
	if result {
		mark = "✓"
		fmt.Printf("%s\x1b[32m%s\x1b[0m \x1b[90m%s\x1b[0m\n", indent, mark, desc)
	} else {
		mark = "×"
		fmt.Printf("%s\x1b[31m%s %s\x1b[0m\n", indent, mark, desc)
		fmt.Printf("%s\x1b[31m  - %s\x1b[0m\n", indent, msg)
	}
}

func pair(name, value string) hpack.HeaderField {
	return hpack.HeaderField{Name: name, Value: value}
}
