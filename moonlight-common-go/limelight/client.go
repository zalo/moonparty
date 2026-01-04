// Package limelight provides the main client for the Moonlight streaming protocol.
package limelight

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/zalo/moonparty/moonlight-common-go/audio"
	"github.com/zalo/moonparty/moonlight-common-go/control"
	"github.com/zalo/moonparty/moonlight-common-go/fec"
	"github.com/zalo/moonparty/moonlight-common-go/input"
	"github.com/zalo/moonparty/moonlight-common-go/rtsp"
	"github.com/zalo/moonparty/moonlight-common-go/video"
)

// Client represents a Moonlight streaming client
type Client struct {
	mu sync.Mutex

	// Configuration
	Config     StreamConfiguration
	ServerInfo ServerInformation

	// Callbacks
	Decoder   DecoderCallbacks
	Audio     AudioCallbacks
	Listener  ConnectionCallbacks

	// Connection state
	ctx       context.Context
	cancel    context.CancelFunc
	stage     Stage
	connected bool

	// Server information
	appVersion   [4]int
	isSunshine   bool
	remoteAddr   *net.UDPAddr
	localAddr    *net.UDPAddr

	// Stream components
	rtspClient    *rtsp.Client
	controlStream *control.Stream
	videoStream   *video.Stream
	audioStream   *audio.Stream
	inputStream   *input.Stream

	// Negotiated settings
	videoFormat     VideoFormat
	opusConfig      *OpusConfig
	audioPacketDuration int

	// Ports
	videoPort   int
	audioPort   int
	controlPort int
}

// NewClient creates a new Moonlight client
func NewClient(config StreamConfiguration, serverInfo ServerInformation,
	decoder DecoderCallbacks, audioCallbacks AudioCallbacks, listener ConnectionCallbacks) *Client {

	// Initialize FEC
	fec.Init()

	return &Client{
		Config:     config,
		ServerInfo: serverInfo,
		Decoder:    decoder,
		Audio:      audioCallbacks,
		Listener:   listener,
	}
}

// Start initiates the streaming connection
func (c *Client) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return fmt.Errorf("already connected")
	}

	c.ctx, c.cancel = context.WithCancel(ctx)

	// Parse server address
	host, port, err := net.SplitHostPort(c.ServerInfo.Address)
	if err != nil {
		// Try as host only
		host = c.ServerInfo.Address
		port = "47989" // Default HTTPS port
	}

	portNum, _ := strconv.Atoi(port)
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("failed to resolve host: %s", host)
	}

	c.remoteAddr = &net.UDPAddr{IP: ips[0], Port: portNum}

	// Parse app version
	c.parseAppVersion()

	// Check for Sunshine server
	c.isSunshine = strings.Contains(strings.ToLower(c.ServerInfo.ServerInfoAppVersion), "sunshine")

	// Stage: Platform Init
	c.notifyStageStarting(StagePlatformInit)
	// Platform init would go here (usually no-op in Go)
	c.notifyStageComplete(StagePlatformInit)

	// Stage: RTSP Handshake
	c.notifyStageStarting(StageRTSPHandshake)
	if err := c.doRTSPHandshake(); err != nil {
		c.notifyStageFailed(StageRTSPHandshake, err)
		return err
	}
	c.notifyStageComplete(StageRTSPHandshake)

	// Stage: Control Stream Init
	c.notifyStageStarting(StageControlStreamInit)
	if err := c.initControlStream(); err != nil {
		c.notifyStageFailed(StageControlStreamInit, err)
		c.cleanup()
		return err
	}
	c.notifyStageComplete(StageControlStreamInit)

	// Stage: Video Stream Init
	c.notifyStageStarting(StageVideoStreamInit)
	if err := c.initVideoStream(); err != nil {
		c.notifyStageFailed(StageVideoStreamInit, err)
		c.cleanup()
		return err
	}
	c.notifyStageComplete(StageVideoStreamInit)

	// Stage: Audio Stream Init
	c.notifyStageStarting(StageAudioStreamInit)
	if err := c.initAudioStream(); err != nil {
		c.notifyStageFailed(StageAudioStreamInit, err)
		c.cleanup()
		return err
	}
	c.notifyStageComplete(StageAudioStreamInit)

	// Stage: Input Stream Init
	c.notifyStageStarting(StageInputStreamInit)
	if err := c.initInputStream(); err != nil {
		c.notifyStageFailed(StageInputStreamInit, err)
		c.cleanup()
		return err
	}
	c.notifyStageComplete(StageInputStreamInit)

	// Start all streams
	c.notifyStageStarting(StageControlStreamStart)
	// Control stream already started during init
	c.notifyStageComplete(StageControlStreamStart)

	c.notifyStageStarting(StageVideoStreamStart)
	// Video stream already started during init
	c.notifyStageComplete(StageVideoStreamStart)

	c.notifyStageStarting(StageAudioStreamStart)
	// Audio stream already started during init
	c.notifyStageComplete(StageAudioStreamStart)

	c.notifyStageStarting(StageInputStreamStart)
	// Input stream already started during init
	c.notifyStageComplete(StageInputStreamStart)

	// Complete
	c.stage = StageComplete
	c.connected = true
	c.Listener.ConnectionStarted()

	return nil
}

// Stop terminates the streaming connection
func (c *Client) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return
	}

	c.cleanup()
	c.connected = false
}

// cleanup shuts down all stream components
func (c *Client) cleanup() {
	if c.cancel != nil {
		c.cancel()
	}

	if c.inputStream != nil {
		c.inputStream.Close()
		c.inputStream = nil
	}

	if c.audioStream != nil {
		c.audioStream.Stop()
		c.audioStream = nil
	}

	if c.videoStream != nil {
		c.videoStream.Stop()
		c.videoStream = nil
	}

	if c.controlStream != nil {
		c.controlStream.Stop()
		c.controlStream = nil
	}

	if c.rtspClient != nil {
		c.rtspClient.DoTeardown()
		c.rtspClient.Close()
		c.rtspClient = nil
	}
}

// doRTSPHandshake performs the RTSP session setup
func (c *Client) doRTSPHandshake() error {
	c.rtspClient = rtsp.NewClient(c.remoteAddr.IP.String(), 48010)

	if err := c.rtspClient.Connect(); err != nil {
		return err
	}

	// Build and send SDP
	sdp := rtsp.BuildSDP(
		c.appVersion[0]*1000000+c.appVersion[1]*10000+c.appVersion[2]*100+c.appVersion[3],
		c.Config.Width,
		c.Config.Height,
		c.Config.FPS,
		c.Config.PacketSize,
		uint32(c.Config.SupportedVideoFormats),
		uint32(c.Config.AudioConfiguration),
		true, // GCM supported
		0,    // RI key ID
		c.Config.RemoteInputAesKey,
	)

	resp, err := c.rtspClient.DoAnnounce(sdp)
	if err != nil {
		return fmt.Errorf("ANNOUNCE failed: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("ANNOUNCE failed: %d %s", resp.StatusCode, resp.StatusText)
	}

	// DESCRIBE to get server capabilities
	resp, err = c.rtspClient.DoDescribe()
	if err != nil {
		return fmt.Errorf("DESCRIBE failed: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("DESCRIBE failed: %d %s", resp.StatusCode, resp.StatusText)
	}

	// Parse server SDP
	serverSDP := rtsp.ParseSDP(resp.Body)
	c.parseServerSDP(serverSDP)

	// SETUP streams
	ports, err := c.rtspClient.DoSetup()
	if err != nil {
		return err
	}

	c.videoPort = ports.VideoPort
	c.audioPort = ports.AudioPort
	c.controlPort = ports.ControlPort

	// Fallback ports
	if c.videoPort == 0 {
		c.videoPort = 47998
	}
	if c.audioPort == 0 {
		c.audioPort = 48000
	}
	if c.controlPort == 0 {
		c.controlPort = 47999
	}

	// PLAY
	resp, err = c.rtspClient.DoPlay()
	if err != nil {
		return fmt.Errorf("PLAY failed: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("PLAY failed: %d %s", resp.StatusCode, resp.StatusText)
	}

	return nil
}

// parseServerSDP extracts settings from the server's SDP response
func (c *Client) parseServerSDP(sdp map[string]string) {
	// Default video format
	c.videoFormat = VideoFormatH264

	// Check for HEVC support
	if val, ok := sdp["x-nv-video[0].hevcSupport"]; ok && val == "1" {
		if c.Config.SupportedVideoFormats&VideoFormatH265 != 0 {
			c.videoFormat = VideoFormatH265
		}
	}

	// Check for AV1 support
	if val, ok := sdp["x-nv-video[0].av1Support"]; ok && val == "1" {
		if c.Config.SupportedVideoFormats&VideoFormatAV1 != 0 {
			c.videoFormat = VideoFormatAV1
		}
	}

	// Default Opus config
	c.opusConfig = &OpusConfig{
		SampleRate:      48000,
		ChannelCount:    2,
		Streams:         1,
		CoupledStreams:  1,
		ChannelMapping:  []uint8{0, 1},
	}

	// Audio packet duration (default 5ms)
	c.audioPacketDuration = 5
	if val, ok := sdp["x-nv-audio.packetDuration"]; ok {
		if dur, err := strconv.Atoi(val); err == nil {
			c.audioPacketDuration = dur
		}
	}

	c.opusConfig.SamplesPerFrame = 48 * c.audioPacketDuration
}

// initControlStream initializes the control stream
func (c *Client) initControlStream() error {
	c.controlStream = control.NewStream(c.Config, c.Listener, c.appVersion, c.isSunshine)
	return c.controlStream.Start(c.ctx, c.remoteAddr, c.controlPort)
}

// initVideoStream initializes the video stream
func (c *Client) initVideoStream() error {
	c.videoStream = video.NewStream(c.Config, c.Decoder)
	return c.videoStream.Start(c.ctx, c.remoteAddr, c.localAddr, c.videoPort)
}

// initAudioStream initializes the audio stream
func (c *Client) initAudioStream() error {
	c.audioStream = audio.NewStream(c.Config, c.Audio)
	return c.audioStream.Start(c.ctx, c.remoteAddr, c.localAddr, c.audioPort, c.opusConfig, c.audioPacketDuration)
}

// initInputStream initializes the input stream
func (c *Client) initInputStream() error {
	sendFunc := func(channelID uint8, flags uint32, data []byte, moreData bool) error {
		return c.controlStream.SendInputPacket(channelID, flags, data, moreData)
	}

	c.inputStream = input.NewStream(c.appVersion, c.isSunshine, c.Config.RemoteInputAesKey, c.Config.RemoteInputAesIV, sendFunc)
	return nil
}

// parseAppVersion parses the server version string
func (c *Client) parseAppVersion() {
	parts := strings.Split(c.ServerInfo.ServerInfoAppVersion, ".")
	for i := 0; i < 4 && i < len(parts); i++ {
		// Strip non-numeric suffixes
		numStr := parts[i]
		for j, ch := range numStr {
			if ch < '0' || ch > '9' {
				numStr = numStr[:j]
				break
			}
		}
		c.appVersion[i], _ = strconv.Atoi(numStr)
	}
}

// Stage notification helpers

func (c *Client) notifyStageStarting(stage Stage) {
	c.stage = stage
	c.Listener.StageStarting(stage)
}

func (c *Client) notifyStageComplete(stage Stage) {
	c.Listener.StageComplete(stage)
}

func (c *Client) notifyStageFailed(stage Stage, err error) {
	c.Listener.StageFailed(stage, err)
}

// Input API

// SendMouseMove sends a relative mouse movement event
func (c *Client) SendMouseMove(deltaX, deltaY int16) error {
	if c.inputStream == nil {
		return fmt.Errorf("not connected")
	}
	return c.inputStream.SendMouseMove(deltaX, deltaY)
}

// SendMousePosition sends an absolute mouse position event
func (c *Client) SendMousePosition(x, y, refWidth, refHeight int16) error {
	if c.inputStream == nil {
		return fmt.Errorf("not connected")
	}
	return c.inputStream.SendMousePosition(x, y, refWidth, refHeight)
}

// SendMouseButton sends a mouse button event
func (c *Client) SendMouseButton(action uint8, button int) error {
	if c.inputStream == nil {
		return fmt.Errorf("not connected")
	}
	return c.inputStream.SendMouseButton(action, button)
}

// SendKeyboard sends a keyboard event
func (c *Client) SendKeyboard(keyCode int16, keyAction uint8, modifiers uint8) error {
	if c.inputStream == nil {
		return fmt.Errorf("not connected")
	}
	return c.inputStream.SendKeyboard(keyCode, keyAction, modifiers, 0)
}

// SendScroll sends a scroll wheel event
func (c *Client) SendScroll(amount int16) error {
	if c.inputStream == nil {
		return fmt.Errorf("not connected")
	}
	return c.inputStream.SendScroll(amount)
}

// SendController sends a controller state event
func (c *Client) SendController(buttonFlags int, leftTrigger, rightTrigger uint8,
	leftStickX, leftStickY, rightStickX, rightStickY int16) error {
	if c.inputStream == nil {
		return fmt.Errorf("not connected")
	}
	return c.inputStream.SendController(buttonFlags, leftTrigger, rightTrigger,
		leftStickX, leftStickY, rightStickX, rightStickY)
}

// SendMultiController sends a multi-controller state event
func (c *Client) SendMultiController(controllerNumber, activeGamepadMask int16, buttonFlags int,
	leftTrigger, rightTrigger uint8, leftStickX, leftStickY, rightStickX, rightStickY int16) error {
	if c.inputStream == nil {
		return fmt.Errorf("not connected")
	}
	return c.inputStream.SendMultiController(controllerNumber, activeGamepadMask, buttonFlags,
		leftTrigger, rightTrigger, leftStickX, leftStickY, rightStickX, rightStickY)
}

// SendUTF8Text sends UTF-8 text input
func (c *Client) SendUTF8Text(text string) error {
	if c.inputStream == nil {
		return fmt.Errorf("not connected")
	}
	return c.inputStream.SendUTF8Text(text)
}

// Video API

// RequestIDRFrame requests a keyframe from the server
func (c *Client) RequestIDRFrame() {
	if c.videoStream != nil {
		c.videoStream.RequestIDRFrame()
	}
	if c.controlStream != nil {
		c.controlStream.RequestIDRFrame()
	}
}

// WaitForNextVideoFrame waits for and returns the next video frame
func (c *Client) WaitForNextVideoFrame() (*DecodeUnit, bool) {
	if c.videoStream == nil {
		return nil, false
	}
	return c.videoStream.WaitForNextFrame()
}

// GetVideoStats returns current video statistics
func (c *Client) GetVideoStats() RTPVideoStats {
	if c.videoStream == nil {
		return RTPVideoStats{}
	}
	return c.videoStream.GetStats()
}

// Audio API

// GetPendingAudioFrames returns the number of pending audio frames
func (c *Client) GetPendingAudioFrames() int {
	if c.audioStream == nil {
		return 0
	}
	return c.audioStream.GetPendingFrames()
}

// GetPendingAudioDuration returns the pending audio duration in milliseconds
func (c *Client) GetPendingAudioDuration() int {
	if c.audioStream == nil {
		return 0
	}
	return c.audioStream.GetPendingDuration()
}

// GetAudioStats returns current audio statistics
func (c *Client) GetAudioStats() RTPAudioStats {
	if c.audioStream == nil {
		return RTPAudioStats{}
	}
	return c.audioStream.GetStats()
}

// Control API

// GetRTTInfo returns estimated round-trip time information
func (c *Client) GetRTTInfo() (RTTInfo, bool) {
	if c.controlStream == nil {
		return RTTInfo{}, false
	}
	return c.controlStream.GetRTTInfo()
}

// IsHDREnabled returns whether HDR is currently enabled
func (c *Client) IsHDREnabled() bool {
	if c.controlStream == nil {
		return false
	}
	return c.controlStream.IsHDREnabled()
}

// GetHDRMetadata returns the current HDR metadata
func (c *Client) GetHDRMetadata() (HDRMetadata, bool) {
	if c.controlStream == nil {
		return HDRMetadata{}, false
	}
	return c.controlStream.GetHDRMetadata()
}

// GetNegotiatedVideoFormat returns the negotiated video format
func (c *Client) GetNegotiatedVideoFormat() VideoFormat {
	return c.videoFormat
}

// IsConnected returns whether the client is currently connected
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// GetCurrentStage returns the current connection stage
func (c *Client) GetCurrentStage() Stage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stage
}
