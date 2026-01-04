// Package rtsp implements the RTSP handshake for the Moonlight streaming protocol.
package rtsp

import (
	"bufio"
	"fmt"
	"io"
	"log"
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
	PingPayload string // X-SS-Ping-Payload from Sunshine
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

// DoOptions performs the RTSP OPTIONS request
func (c *Client) DoOptions() (*Response, error) {
	return c.doRequest("OPTIONS", "", nil, "")
}

// DoAnnounce performs the RTSP ANNOUNCE request
func (c *Client) DoAnnounce(sdp string) (*Response, error) {
	headers := map[string]string{
		// Note: Sunshine expects lowercase 't' in Content-type
		"Content-type": "application/sdp",
	}
	return c.doRequest("ANNOUNCE", "", headers, sdp)
}

// DoDescribe performs the RTSP DESCRIBE request
func (c *Client) DoDescribe() (*Response, error) {
	headers := map[string]string{
		"Accept": "application/sdp",
	}
	return c.doRequest("DESCRIBE", "", headers, "")
}

// DoSetup performs the RTSP SETUP requests for all streams
// clientPorts specifies local UDP ports for video, audio, and control
func (c *Client) DoSetup() (*StreamPorts, error) {
	ports := &StreamPorts{}

	// Setup audio stream first (like working client)
	// Path format: streamid=audio/0/0
	headers := map[string]string{
		"Transport": "unicast;client_port=48000",
	}
	resp, err := c.doRequest("SETUP", "streamid=audio/0/0", headers, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("SETUP audio failed: %d %s", resp.StatusCode, resp.StatusText)
	}
	// Debug: log all headers from SETUP response
	log.Printf("SETUP audio response headers:")
	for k, v := range resp.Headers {
		log.Printf("  %s: %s", k, v)
	}
	// Parse session ID (format: "DEADBEEFCAFE;timeout = 90")
	if session := resp.Headers["Session"]; session != "" && c.sessionID == "" {
		parts := strings.Split(session, ";")
		c.sessionID = strings.TrimSpace(parts[0])
	}
	// Parse X-SS-Ping-Payload from Sunshine
	if ping := resp.Headers["X-SS-Ping-Payload"]; ping != "" {
		ports.PingPayload = ping
		log.Printf("Found ping payload in audio SETUP: %s", ping)
	}
	ports.AudioPort = parseTransportPort(resp.Headers["Transport"])

	// Setup video stream
	headers = map[string]string{
		"Transport": "unicast;client_port=47998",
	}
	resp, err = c.doRequest("SETUP", "streamid=video/0/0", headers, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("SETUP video failed: %d %s", resp.StatusCode, resp.StatusText)
	}
	// Debug: log all headers from video SETUP response
	log.Printf("SETUP video response headers:")
	for k, v := range resp.Headers {
		log.Printf("  %s: %s", k, v)
	}
	// Parse X-SS-Ping-Payload from Sunshine (may be in any SETUP response)
	if ping := resp.Headers["X-SS-Ping-Payload"]; ping != "" && ports.PingPayload == "" {
		ports.PingPayload = ping
		log.Printf("Found ping payload in video SETUP: %s", ping)
	}
	ports.VideoPort = parseTransportPort(resp.Headers["Transport"])

	// Setup control stream (path includes /13/0)
	headers = map[string]string{
		"Transport": "unicast;client_port=47999",
	}
	resp, err = c.doRequest("SETUP", "streamid=control/13/0", headers, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("SETUP control failed: %d %s", resp.StatusCode, resp.StatusText)
	}
	ports.ControlPort = parseTransportPort(resp.Headers["Transport"])

	log.Printf("RTSP SETUP complete: VideoPort=%d AudioPort=%d ControlPort=%d PingPayload=%q (len=%d)",
		ports.VideoPort, ports.AudioPort, ports.ControlPort, ports.PingPayload, len(ports.PingPayload))

	return ports, nil
}

// DoPlay performs the RTSP PLAY request
func (c *Client) DoPlay() (*Response, error) {
	return c.doRequest("PLAY", "", nil, "")
}

// DoTeardown performs the RTSP TEARDOWN request
func (c *Client) DoTeardown() (*Response, error) {
	return c.doRequest("TEARDOWN", "", nil, "")
}

// doRequest performs an RTSP request and returns the response
// NOTE: Sunshine closes the connection after each response, so we reconnect for each request
// uri should be empty for ANNOUNCE/DESCRIBE/PLAY, or "streamid=video/0/0" etc. for SETUP
func (c *Client) doRequest(method, uri string, headers map[string]string, body string) (*Response, error) {
	// Reconnect for each request (Sunshine closes connection after each response)
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	if err := c.Connect(); err != nil {
		return nil, err
	}

	c.cseq++

	// Build request target
	// For SETUP, include the streamid path; for others, just host:port
	var req strings.Builder
	var target string
	if uri != "" && method == "SETUP" {
		target = fmt.Sprintf("rtsp://%s:%d/%s", c.serverIP, c.serverPort, uri)
	} else {
		target = fmt.Sprintf("rtsp://%s:%d", c.serverIP, c.serverPort)
	}
	req.WriteString(fmt.Sprintf("%s %s RTSP/1.0\r\n", method, target))
	req.WriteString(fmt.Sprintf("CSeq: %d\r\n", c.cseq))
	req.WriteString("X-GS-ClientVersion: 14\r\n")
	req.WriteString(fmt.Sprintf("Host: %s\r\n", c.serverIP))

	if c.sessionID != "" {
		req.WriteString(fmt.Sprintf("Session: %s\r\n", c.sessionID))
	}

	for k, v := range headers {
		req.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}

	if body != "" {
		// Note: Sunshine expects "Content-length" (lowercase 'l'), not "Content-Length"
		req.WriteString(fmt.Sprintf("Content-length: %d\r\n", len(body)))
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

	// Video parameters
	sdp.WriteString(fmt.Sprintf("a=x-nv-video[0].clientViewportWd:%d\r\n", clientWidth))
	sdp.WriteString(fmt.Sprintf("a=x-nv-video[0].clientViewportHt:%d\r\n", clientHeight))
	sdp.WriteString(fmt.Sprintf("a=x-nv-video[0].maxFPS:%d\r\n", fps))
	sdp.WriteString("a=x-nv-vqos[0].bw.maximumBitrateKbps:20000\r\n")
	sdp.WriteString(fmt.Sprintf("a=x-nv-video[0].packetSize:%d\r\n", packetSize))
	sdp.WriteString("a=x-nv-video[0].rateControlMode:4\r\n")
	sdp.WriteString("a=x-nv-video[0].timeoutLengthMs:7000\r\n")
	sdp.WriteString("a=x-nv-video[0].framesWithInvalidRefThreshold:0\r\n")
	sdp.WriteString("a=x-nv-vqos[0].bitStreamFormat:0\r\n") // 0=H264, 1=HEVC
	sdp.WriteString("a=x-nv-video[0].encoderCscMode:0\r\n")
	sdp.WriteString("a=x-nv-video[0].maxNumReferenceFrames:1\r\n")
	sdp.WriteString("a=x-nv-video[0].videoEncoderSlicesPerFrame:1\r\n")

	// Audio parameters
	sdp.WriteString("a=x-nv-audio.surround.numChannels:2\r\n")
	sdp.WriteString("a=x-nv-audio.surround.channelMask:3\r\n")
	sdp.WriteString("a=x-nv-audio.surround.enable:0\r\n")
	sdp.WriteString("a=x-nv-audio.surround.AudioQuality:0\r\n")
	sdp.WriteString("a=x-nv-aqos.packetDuration:5\r\n")

	// General settings
	sdp.WriteString("a=x-nv-general.useReliableUdp:1\r\n")
	sdp.WriteString("a=x-nv-vqos[0].fec.minRequiredFecPackets:0\r\n")
	sdp.WriteString("a=x-nv-general.featureFlags:135\r\n")
	// ML_FF_FEC_STATUS (0x01) | ML_FF_SESSION_ID_V1 (0x02) = 3
	sdp.WriteString("a=x-ml-general.featureFlags:3\r\n")
	// QOS traffic types
	sdp.WriteString("a=x-nv-vqos[0].qosTrafficType:5\r\n")
	sdp.WriteString("a=x-nv-aqos.qosTrafficType:4\r\n")
	// Configured bitrate (0 = use maximumBitrateKbps)
	sdp.WriteString("a=x-ml-video.configuredBitrateKbps:0\r\n")

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
