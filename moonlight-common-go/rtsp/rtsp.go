// Package rtsp implements the RTSP handshake for the Moonlight streaming protocol.
package rtsp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultPort is the default RTSP port
	DefaultPort = 48010
	// TimeoutSec is the RTSP operation timeout
	TimeoutSec = 10
)

// Client handles RTSP communication with the streaming server
type Client struct {
	conn       net.Conn
	reader     *bufio.Reader
	cseq       int
	sessionID  string
	serverIP   string
	serverPort int
}

// Response represents an RTSP response
type Response struct {
	StatusCode int
	StatusText string
	Headers    map[string]string
	Body       string
}

// StreamPorts contains the negotiated streaming ports
type StreamPorts struct {
	VideoPort   int
	AudioPort   int
	ControlPort int
}

// NewClient creates a new RTSP client
func NewClient(serverIP string, serverPort int) *Client {
	if serverPort == 0 {
		serverPort = DefaultPort
	}
	return &Client{
		serverIP:   serverIP,
		serverPort: serverPort,
	}
}

// Connect establishes the RTSP connection
func (c *Client) Connect() error {
	addr := fmt.Sprintf("%s:%d", c.serverIP, c.serverPort)
	conn, err := net.DialTimeout("tcp", addr, TimeoutSec*time.Second)
	if err != nil {
		return fmt.Errorf("RTSP connect failed: %w", err)
	}

	c.conn = conn
	c.reader = bufio.NewReader(conn)
	return nil
}

// Close closes the RTSP connection
func (c *Client) Close() {
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

// DoAnnounce performs the RTSP ANNOUNCE request
func (c *Client) DoAnnounce(sdp string) (*Response, error) {
	headers := map[string]string{
		"Content-Type": "application/sdp",
	}
	return c.doRequest("ANNOUNCE", "streamid=control", headers, sdp)
}

// DoDescribe performs the RTSP DESCRIBE request
func (c *Client) DoDescribe() (*Response, error) {
	headers := map[string]string{
		"Accept": "application/sdp",
	}
	return c.doRequest("DESCRIBE", "streamid=control", headers, "")
}

// DoSetup performs the RTSP SETUP requests for all streams
func (c *Client) DoSetup() (*StreamPorts, error) {
	ports := &StreamPorts{}

	// Setup video stream
	resp, err := c.doRequest("SETUP", "streamid=video", nil, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("SETUP video failed: %d %s", resp.StatusCode, resp.StatusText)
	}
	c.sessionID = resp.Headers["Session"]
	ports.VideoPort = parseTransportPort(resp.Headers["Transport"])

	// Setup audio stream
	resp, err = c.doRequest("SETUP", "streamid=audio", nil, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("SETUP audio failed: %d %s", resp.StatusCode, resp.StatusText)
	}
	ports.AudioPort = parseTransportPort(resp.Headers["Transport"])

	// Setup control stream
	resp, err = c.doRequest("SETUP", "streamid=control", nil, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("SETUP control failed: %d %s", resp.StatusCode, resp.StatusText)
	}
	ports.ControlPort = parseTransportPort(resp.Headers["Transport"])

	return ports, nil
}

// DoPlay performs the RTSP PLAY request
func (c *Client) DoPlay() (*Response, error) {
	return c.doRequest("PLAY", "streamid=control", nil, "")
}

// DoTeardown performs the RTSP TEARDOWN request
func (c *Client) DoTeardown() (*Response, error) {
	return c.doRequest("TEARDOWN", "streamid=control", nil, "")
}

// doRequest performs an RTSP request and returns the response
func (c *Client) doRequest(method, uri string, headers map[string]string, body string) (*Response, error) {
	if c.conn == nil {
		return nil, errors.New("not connected")
	}

	c.cseq++

	// Build request
	var req strings.Builder
	req.WriteString(fmt.Sprintf("%s rtsp://%s:%d/%s RTSP/1.0\r\n", method, c.serverIP, c.serverPort, uri))
	req.WriteString(fmt.Sprintf("CSeq: %d\r\n", c.cseq))

	if c.sessionID != "" {
		req.WriteString(fmt.Sprintf("Session: %s\r\n", c.sessionID))
	}

	for k, v := range headers {
		req.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}

	if body != "" {
		req.WriteString(fmt.Sprintf("Content-Length: %d\r\n", len(body)))
	}

	req.WriteString("\r\n")
	if body != "" {
		req.WriteString(body)
	}

	// Set timeout
	c.conn.SetDeadline(time.Now().Add(TimeoutSec * time.Second))

	// Send request
	_, err := c.conn.Write([]byte(req.String()))
	if err != nil {
		return nil, fmt.Errorf("RTSP send failed: %w", err)
	}

	// Read response
	return c.readResponse()
}

// readResponse reads and parses an RTSP response
func (c *Client) readResponse() (*Response, error) {
	resp := &Response{
		Headers: make(map[string]string),
	}

	// Read status line
	statusLine, err := c.reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read status line: %w", err)
	}

	statusLine = strings.TrimSpace(statusLine)
	parts := strings.SplitN(statusLine, " ", 3)
	if len(parts) < 3 || !strings.HasPrefix(parts[0], "RTSP/") {
		return nil, fmt.Errorf("invalid RTSP response: %s", statusLine)
	}

	resp.StatusCode, _ = strconv.Atoi(parts[1])
	resp.StatusText = parts[2]

	// Read headers
	var contentLength int
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("failed to read header: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" {
			break
		}

		colonIdx := strings.Index(line, ":")
		if colonIdx > 0 {
			key := strings.TrimSpace(line[:colonIdx])
			value := strings.TrimSpace(line[colonIdx+1:])
			resp.Headers[key] = value

			if strings.EqualFold(key, "Content-Length") {
				contentLength, _ = strconv.Atoi(value)
			}
		}
	}

	// Read body if present
	if contentLength > 0 {
		body := make([]byte, contentLength)
		_, err := io.ReadFull(c.reader, body)
		if err != nil {
			return nil, fmt.Errorf("failed to read body: %w", err)
		}
		resp.Body = string(body)
	}

	return resp, nil
}

// parseTransportPort extracts the server port from a Transport header
func parseTransportPort(transport string) int {
	// Format: RTP/AVP/UDP;unicast;server_port=XXXXX
	for _, part := range strings.Split(transport, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "server_port=") {
			portStr := strings.TrimPrefix(part, "server_port=")
			// May be a range like "48000-48001", take the first
			if idx := strings.Index(portStr, "-"); idx > 0 {
				portStr = portStr[:idx]
			}
			port, _ := strconv.Atoi(portStr)
			return port
		}
	}
	return 0
}

// BuildSDP builds an SDP offer for streaming
func BuildSDP(clientVersion, clientWidth, clientHeight, fps, packetSize int,
	videoFormats, audioConfig uint32, gcmSupported bool, riKeyID uint32, riKey []byte) string {

	var sdp strings.Builder

	sdp.WriteString("v=0\r\n")
	sdp.WriteString("o=- 0 0 IN IP4 0.0.0.0\r\n")
	sdp.WriteString("s=NVIDIA Streaming Client\r\n")
	sdp.WriteString("c=IN IP4 0.0.0.0\r\n")
	sdp.WriteString("t=0 0\r\n")

	// Video parameters
	sdp.WriteString(fmt.Sprintf("m=video %d\r\n", 48000))
	sdp.WriteString(fmt.Sprintf("a=x-nv-video[0].clientViewportWd:%d\r\n", clientWidth))
	sdp.WriteString(fmt.Sprintf("a=x-nv-video[0].clientViewportHt:%d\r\n", clientHeight))
	sdp.WriteString(fmt.Sprintf("a=x-nv-video[0].maxFPS:%d\r\n", fps))
	sdp.WriteString(fmt.Sprintf("a=x-nv-video[0].packetSize:%d\r\n", packetSize))

	// Codec support
	if videoFormats&0x0001 != 0 {
		sdp.WriteString("a=x-nv-video[0].clientSupportHevc:0\r\n")
	}
	if videoFormats&0x0100 != 0 {
		sdp.WriteString("a=x-nv-video[0].clientSupportHevc:1\r\n")
	}
	if videoFormats&0x0200 != 0 {
		sdp.WriteString("a=x-nv-video[0].clientSupportAv1:1\r\n")
	}

	// Audio parameters
	sdp.WriteString(fmt.Sprintf("m=audio %d\r\n", 48001))
	sdp.WriteString(fmt.Sprintf("a=x-nv-audio.surround:%d\r\n", audioConfig))

	// Encryption
	if len(riKey) > 0 {
		sdp.WriteString(fmt.Sprintf("a=x-nv-rikeyid:%d\r\n", riKeyID))
		sdp.WriteString(fmt.Sprintf("a=x-nv-rikey:%x\r\n", riKey))
	}

	if gcmSupported {
		sdp.WriteString("a=x-nv-gcmSupport:1\r\n")
	}

	sdp.WriteString(fmt.Sprintf("a=x-nv-clientVersion:%d\r\n", clientVersion))

	return sdp.String()
}

// ParseSDP parses an SDP response from the server
func ParseSDP(sdp string) map[string]string {
	result := make(map[string]string)

	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "a=") {
			attr := strings.TrimPrefix(line, "a=")
			if idx := strings.Index(attr, ":"); idx > 0 {
				key := attr[:idx]
				value := attr[idx+1:]
				result[key] = value
			}
		}
	}

	return result
}
