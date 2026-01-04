package moonlight

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zalo/moonparty/internal/moonlight/limelight"
)

// LimelightStream uses moonlight-common-c for streaming
type LimelightStream struct {
	client *Client
	ctx    context.Context
	cancel context.CancelFunc

	// Channels for video/audio data
	videoFrames chan []byte
	audioFrames chan []byte
	inputChan   chan InputPacket

	// Stream configuration
	width   int
	height  int
	fps     int
	bitrate int

	// Encryption keys (from launch response)
	riKey   []byte
	riKeyID uint32

	// State
	connected bool
	mu        sync.RWMutex
}

// StartStreamWithLimelight begins streaming using moonlight-common-c
func (c *Client) StartStreamWithLimelight(ctx context.Context, width, height, fps, bitrate int) (*LimelightStream, error) {
	if !c.paired {
		return nil, fmt.Errorf("not paired with Sunshine")
	}

	streamCtx, cancel := context.WithCancel(ctx)

	s := &LimelightStream{
		client:      c,
		ctx:         streamCtx,
		cancel:      cancel,
		videoFrames: make(chan []byte, 60),
		audioFrames: make(chan []byte, 120),
		inputChan:   make(chan InputPacket, 256),
		width:       width,
		height:      height,
		fps:         fps,
		bitrate:     bitrate,
	}

	// Set up limelight callbacks that push to our channels
	s.setupCallbacks()

	// Launch the desktop app (app ID 0 is typically Desktop)
	if err := s.launchApp(ctx, 0, width, height, fps, bitrate); err != nil {
		cancel()
		return nil, err
	}

	// Start the connection using moonlight-common-c
	if err := s.startLimelightConnection(); err != nil {
		cancel()
		return nil, fmt.Errorf("limelight connection failed: %w", err)
	}

	return s, nil
}

// setupCallbacks configures the limelight callbacks
func (s *LimelightStream) setupCallbacks() {
	limelight.SetCallbacks(&limelight.Callbacks{
		OnDecoderSetup: func(videoFormat, width, height, redrawRate int) {
			log.Printf("Video decoder setup: format=%d, %dx%d @ %dHz", videoFormat, width, height, redrawRate)
		},
		OnDecoderStart: func() {
			log.Println("Video decoder started")
		},
		OnDecoderStop: func() {
			log.Println("Video decoder stopped")
		},
		OnDecoderCleanup: func() {
			log.Println("Video decoder cleanup")
		},
		OnDecodeUnit: func(unit *limelight.DecodeUnit) int {
			// Send video frame data to channel
			select {
			case s.videoFrames <- unit.Data:
			default:
				// Channel full, drop frame
			}
			return limelight.DrOk
		},
		OnAudioInit: func(audioConfig int, opusConfig *limelight.OpusConfig) int {
			log.Printf("Audio init: config=%d, sampleRate=%d, channels=%d",
				audioConfig, opusConfig.SampleRate, opusConfig.ChannelCount)
			return 0
		},
		OnAudioStart: func() {
			log.Println("Audio started")
		},
		OnAudioStop: func() {
			log.Println("Audio stopped")
		},
		OnAudioCleanup: func() {
			log.Println("Audio cleanup")
		},
		OnAudioSample: func(data []byte) {
			// Send audio sample to channel
			select {
			case s.audioFrames <- data:
			default:
				// Channel full, drop sample
			}
		},
		OnConnectionStarted: func() {
			s.mu.Lock()
			s.connected = true
			s.mu.Unlock()
			log.Println("Streaming connection established")
		},
		OnConnectionTerminated: func(errorCode int) {
			s.mu.Lock()
			s.connected = false
			s.mu.Unlock()
			if errorCode != 0 {
				log.Printf("Connection terminated with error: %d", errorCode)
			} else {
				log.Println("Connection terminated gracefully")
			}
		},
		OnRumble: func(controllerNumber, lowFreq, highFreq uint16) {
			// TODO: Forward rumble events to WebRTC clients
			log.Printf("Rumble: controller=%d, low=%d, high=%d", controllerNumber, lowFreq, highFreq)
		},
	})
}

// launchApp starts an application on Sunshine (same as before, but stores riKey)
func (s *LimelightStream) launchApp(ctx context.Context, appID, width, height, fps, bitrate int) error {
	// Generate random AES key for stream encryption
	s.riKey = make([]byte, 16)
	if _, err := rand.Read(s.riKey); err != nil {
		return err
	}
	s.riKeyID = uint32(time.Now().UnixNano() & 0xFFFFFFFF)

	// Build launch URL with parameters (must use HTTPS port 47984)
	riKeyHex := strings.ToUpper(hex.EncodeToString(s.riKey))

	params := fmt.Sprintf("uniqueid=%s&appid=%d&mode=%dx%dx%d&additionalStates=1&sops=0&rikey=%s&rikeyid=%d&localAudioPlayMode=0&gcmap=0&gcpersist=0",
		s.client.uniqueID, appID, width, height, fps, riKeyHex, s.riKeyID)

	// Use HTTPS port 47984 for launch
	url := fmt.Sprintf("https://%s:47984/launch?%s", s.client.host, params)

	log.Printf("Launching app %d at %dx%d@%dfps...", appID, width, height, fps)

	// Create HTTPS client with client certificate
	httpsClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				Certificates:       []tls.Certificate{*s.client.clientCert},
			},
		},
		Timeout: 30 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := httpsClient.Do(req)
	if err != nil {
		return fmt.Errorf("launch request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	// Parse response
	var launchResp struct {
		SessionURL  string `xml:"sessionUrl0"`
		GameSession string `xml:"gamesession"`
		StatusCode  string `xml:"status_code,attr"`
		StatusMsg   string `xml:"status_message,attr"`
	}
	if err := xml.Unmarshal(body, &launchResp); err != nil {
		log.Printf("Launch response parse error: %v, body: %s", err, string(body))
		return fmt.Errorf("parse launch response: %w", err)
	}

	if launchResp.GameSession != "1" {
		return fmt.Errorf("launch failed: %s (status: %s)", launchResp.StatusMsg, launchResp.StatusCode)
	}

	log.Printf("Launch successful, RTSP URL: %s", launchResp.SessionURL)
	return nil
}

// startLimelightConnection starts the moonlight-common-c connection
func (s *LimelightStream) startLimelightConnection() error {
	serverInfo := &limelight.ServerInfo{
		Address:              s.client.host,
		RtspSessionUrl:       "", // Let moonlight-common-c use default
		ServerCodecModeSupport: 0x0001, // H.264 support
	}

	streamConfig := &limelight.StreamConfig{
		Width:                s.width,
		Height:               s.height,
		FPS:                  s.fps,
		Bitrate:              s.bitrate,
		PacketSize:           1024,
		StreamingRemotely:    limelight.StreamingAuto,
		AudioConfiguration:   limelight.AudioConfigStereo,
		SupportedVideoFormats: limelight.VideoFormatH264,
		RiKey:                s.riKey,
		RiKeyID:              int(s.riKeyID),
	}

	return limelight.StartConnection(serverInfo, streamConfig)
}

// VideoFrames returns the channel for receiving video frames
func (s *LimelightStream) VideoFrames() <-chan []byte {
	return s.videoFrames
}

// AudioSamples returns the channel for receiving audio samples
func (s *LimelightStream) AudioSamples() <-chan []byte {
	return s.audioFrames
}

// SendInput sends input to Sunshine via moonlight-common-c
func (s *LimelightStream) SendInput(input InputPacket) {
	switch input.Type {
	case InputTypeGamepad:
		s.sendGamepadInput(input)
	case InputTypeKeyboard:
		s.sendKeyboardInput(input)
	case InputTypeMouse:
		s.sendMouseInput(input)
	case InputTypeMouseRelative:
		s.sendMouseRelativeInput(input)
	}
}

func (s *LimelightStream) sendGamepadInput(input InputPacket) {
	if len(input.Data) < 14 {
		return
	}

	// Parse gamepad state from input.Data
	// Expected format: buttonFlags(2) + leftTrigger(1) + rightTrigger(1) +
	//                  leftStickX(2) + leftStickY(2) + rightStickX(2) + rightStickY(2)
	buttonFlags := int(input.Data[0]) | int(input.Data[1])<<8
	leftTrigger := input.Data[2]
	rightTrigger := input.Data[3]
	leftStickX := int16(input.Data[4]) | int16(input.Data[5])<<8
	leftStickY := int16(input.Data[6]) | int16(input.Data[7])<<8
	rightStickX := int16(input.Data[8]) | int16(input.Data[9])<<8
	rightStickY := int16(input.Data[10]) | int16(input.Data[11])<<8

	// Multi-controller support
	controllerNum := int16(input.PlayerSlot)
	activeGamepadMask := int16(1 << input.PlayerSlot)

	limelight.SendMultiControllerEvent(
		controllerNum,
		activeGamepadMask,
		buttonFlags,
		leftTrigger,
		rightTrigger,
		leftStickX, leftStickY,
		rightStickX, rightStickY,
	)
}

func (s *LimelightStream) sendKeyboardInput(input InputPacket) {
	if len(input.Data) < 3 {
		return
	}

	keyCode := int16(input.Data[0]) | int16(input.Data[1])<<8
	keyAction := int8(input.Data[2])
	modifiers := int8(0)
	if len(input.Data) > 3 {
		modifiers = int8(input.Data[3])
	}

	limelight.SendKeyboardEvent(keyCode, keyAction, modifiers)
}

func (s *LimelightStream) sendMouseInput(input InputPacket) {
	if len(input.Data) < 2 {
		return
	}

	action := int8(input.Data[0])
	button := int(input.Data[1])

	limelight.SendMouseButtonEvent(action, button)
}

func (s *LimelightStream) sendMouseRelativeInput(input InputPacket) {
	if len(input.Data) < 4 {
		return
	}

	deltaX := int16(input.Data[0]) | int16(input.Data[1])<<8
	deltaY := int16(input.Data[2]) | int16(input.Data[3])<<8

	limelight.SendMouseMoveEvent(deltaX, deltaY)
}

// RequestIDR requests an IDR frame (keyframe)
func (s *LimelightStream) RequestIDR() {
	limelight.RequestIDRFrame()
}

// Close terminates the stream
func (s *LimelightStream) Close() error {
	s.cancel()
	limelight.StopConnection()

	// Send quit command to Sunshine
	quitURL := fmt.Sprintf("http://%s:%d/cancel?uniqueid=%s",
		s.client.host, s.client.port, s.client.uniqueID)
	s.client.httpClient.Get(quitURL)

	// Close channels safely
	close(s.videoFrames)
	close(s.audioFrames)

	return nil
}

// IsConnected returns whether the stream is currently connected
func (s *LimelightStream) IsConnected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connected
}
