// Package limelight provides a pure Go implementation of the Moonlight streaming protocol.
// This replaces the previous CGO bindings to moonlight-common-c with moonlight-common-go.
package limelight

import (
	"context"
	"fmt"
	"log"
	"sync"

	common "github.com/zalo/moonparty/moonlight-common-go/limelight"
)

// Video format constants
const (
	VideoFormatH264       = int(common.VideoFormatH264)
	VideoFormatH265       = int(common.VideoFormatH265)
	VideoFormatH265Main10 = int(common.VideoFormatH265)
	VideoFormatAV1Main8   = int(common.VideoFormatAV1)
	VideoFormatAV1Main10  = int(common.VideoFormatAV1)
)

// Audio configuration constants
const (
	AudioConfigStereo = int(common.AudioConfigStereo)
	AudioConfig51     = int(common.AudioConfigSurround51)
	AudioConfig71     = int(common.AudioConfigSurround71)
)

// Streaming location constants
const (
	StreamingLocal  = 0
	StreamingRemote = 1
	StreamingAuto   = 2
)

// Decoder return codes
const (
	DrOk      = 0
	DrNeedIDR = -1
)

// Button flags for controller input
const (
	ButtonA       = common.ButtonA
	ButtonB       = common.ButtonB
	ButtonX       = common.ButtonX
	ButtonY       = common.ButtonY
	ButtonUp      = common.ButtonUp
	ButtonDown    = common.ButtonDown
	ButtonLeft    = common.ButtonLeft
	ButtonRight   = common.ButtonRight
	ButtonLB      = common.ButtonLeftBumper
	ButtonRB      = common.ButtonRightBumper
	ButtonPlay    = common.ButtonStart
	ButtonBack    = common.ButtonBack
	ButtonLSClick = common.ButtonLeftStick
	ButtonRSClick = common.ButtonRightStick
	ButtonSpecial = common.ButtonHome
)

// Mouse button constants
const (
	MouseButtonLeft   = common.MouseButtonLeft
	MouseButtonMiddle = common.MouseButtonMiddle
	MouseButtonRight  = common.MouseButtonRight
	MouseButtonX1     = common.MouseButtonX1
	MouseButtonX2     = common.MouseButtonX2

	ButtonActionPress   = common.MouseActionPress
	ButtonActionRelease = common.MouseActionRelease
)

// Key action constants
const (
	KeyActionDown = common.KeyActionDown
	KeyActionUp   = common.KeyActionUp
)

// Key modifier constants
const (
	ModifierShift = common.ModifierShift
	ModifierCtrl  = common.ModifierCtrl
	ModifierAlt   = common.ModifierAlt
	ModifierMeta  = common.ModifierMeta
)

// Connection stages
const (
	StageNone               = int(common.StageNone)
	StagePlatformInit       = int(common.StagePlatformInit)
	StageNameResolution     = int(common.StagePlatformInit) // Mapped to platform init
	StageAudioStreamInit    = int(common.StageAudioStreamInit)
	StageRTSPHandshake      = int(common.StageRTSPHandshake)
	StageControlStreamInit  = int(common.StageControlStreamInit)
	StageVideoStreamInit    = int(common.StageVideoStreamInit)
	StageInputStreamInit    = int(common.StageInputStreamInit)
	StageControlStreamStart = int(common.StageControlStreamStart)
	StageVideoStreamStart   = int(common.StageVideoStreamStart)
	StageAudioStreamStart   = int(common.StageAudioStreamStart)
	StageInputStreamStart   = int(common.StageInputStreamStart)
)

// DecodeUnit represents a video frame to decode
type DecodeUnit struct {
	FrameNumber        int
	FrameType          int
	Data               []byte
	ReceiveTimeUs      int64
	EnqueueTimeUs      int64
	PresentationTimeUs int64
}

// OpusConfig represents Opus audio configuration
type OpusConfig struct {
	SampleRate      int
	ChannelCount    int
	Streams         int
	CoupledStreams  int
	SamplesPerFrame int
	Mapping         [8]byte
}

// Callbacks holds the Go callback functions
type Callbacks struct {
	// Video decoder callbacks
	OnDecoderSetup   func(videoFormat, width, height, redrawRate int)
	OnDecoderStart   func()
	OnDecoderStop    func()
	OnDecoderCleanup func()
	OnDecodeUnit     func(unit *DecodeUnit) int

	// Audio renderer callbacks
	OnAudioInit    func(audioConfig int, opusConfig *OpusConfig) int
	OnAudioStart   func()
	OnAudioStop    func()
	OnAudioCleanup func()
	OnAudioSample  func(data []byte)

	// Connection callbacks
	OnStageStarting        func(stage int)
	OnStageComplete        func(stage int)
	OnStageFailed          func(stage, errorCode int)
	OnConnectionStarted    func()
	OnConnectionTerminated func(errorCode int)
	OnLogMessage           func(msg string)
	OnRumble               func(controllerNumber, lowFreq, highFreq uint16)
}

var (
	globalCallbacks *Callbacks
	callbackMutex   sync.RWMutex
	activeClient    *common.Client
	clientMutex     sync.Mutex
	clientCtx       context.Context
	clientCancel    context.CancelFunc
)

// SetCallbacks sets the global callbacks for limelight events
func SetCallbacks(cbs *Callbacks) {
	callbackMutex.Lock()
	defer callbackMutex.Unlock()
	globalCallbacks = cbs
}

// StreamConfig holds streaming configuration
type StreamConfig struct {
	Width                 int
	Height                int
	FPS                   int
	Bitrate               int
	PacketSize            int
	StreamingRemotely     int
	AudioConfiguration    int
	SupportedVideoFormats int
	RiKey                 []byte
	RiKeyID               int
}

// ServerInfo holds server information
type ServerInfo struct {
	Address                string
	RtspSessionUrl         string
	ServerCodecModeSupport int
	AppVersion             string
	GfeVersion             string
}

// callbackAdapter implements the common.ConnectionCallbacks interface
type callbackAdapter struct{}

func (a *callbackAdapter) StageStarting(stage common.Stage) {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnStageStarting != nil {
		cbs.OnStageStarting(int(stage))
	}
	log.Printf("Connection stage starting: %s", GetStageName(int(stage)))
}

func (a *callbackAdapter) StageComplete(stage common.Stage) {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnStageComplete != nil {
		cbs.OnStageComplete(int(stage))
	}
	log.Printf("Connection stage complete: %s", GetStageName(int(stage)))
}

func (a *callbackAdapter) StageFailed(stage common.Stage, err error) {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	errorCode := -1
	if cbs != nil && cbs.OnStageFailed != nil {
		cbs.OnStageFailed(int(stage), errorCode)
	}
	log.Printf("Connection stage failed: %s (error: %v)", GetStageName(int(stage)), err)
}

func (a *callbackAdapter) ConnectionStarted() {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnConnectionStarted != nil {
		cbs.OnConnectionStarted()
	}
	log.Println("Connection started")
}

func (a *callbackAdapter) ConnectionTerminated(errorCode int) {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnConnectionTerminated != nil {
		cbs.OnConnectionTerminated(errorCode)
	}
	log.Printf("Connection terminated (error %d)", errorCode)
}

func (a *callbackAdapter) ConnectionStatusUpdate(status common.ConnectionStatus) {
	// Log status updates
	log.Printf("Connection status: %v", status)
}

func (a *callbackAdapter) SetHDRMode(enabled bool) {
	log.Printf("HDR mode: %v", enabled)
}

func (a *callbackAdapter) Rumble(controllerNumber, lowFreq, highFreq uint16) {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnRumble != nil {
		cbs.OnRumble(controllerNumber, lowFreq, highFreq)
	}
}

func (a *callbackAdapter) RumbleTriggers(controllerNumber, leftTrigger, rightTrigger uint16) {
	// Trigger rumble not exposed in old API
}

func (a *callbackAdapter) SetMotionEventState(controllerNumber uint16, motionType common.MotionType, reportRateHz uint16) {
	// Motion events not exposed in old API
}

func (a *callbackAdapter) SetControllerLED(controllerNumber uint16, r, g, b uint8) {
	// LED control not exposed in old API
}

// decoderAdapter implements the common.DecoderCallbacks interface
type decoderAdapter struct{}

func (d *decoderAdapter) Setup(format common.VideoFormat, width, height, fps int, context interface{}, flags int) error {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnDecoderSetup != nil {
		cbs.OnDecoderSetup(int(format), width, height, fps)
	}
	return nil
}

func (d *decoderAdapter) Start() {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnDecoderStart != nil {
		cbs.OnDecoderStart()
	}
}

func (d *decoderAdapter) Stop() {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnDecoderStop != nil {
		cbs.OnDecoderStop()
	}
}

func (d *decoderAdapter) Cleanup() {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnDecoderCleanup != nil {
		cbs.OnDecoderCleanup()
	}
}

func (d *decoderAdapter) SubmitDecodeUnit(unit *common.DecodeUnit) int {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs == nil || cbs.OnDecodeUnit == nil {
		return DrOk
	}

	// Convert common.DecodeUnit to local DecodeUnit
	du := &DecodeUnit{
		FrameNumber:        int(unit.FrameNumber),
		FrameType:          int(unit.FrameType),
		PresentationTimeUs: int64(unit.PresentationTimeMs * 1000),
		EnqueueTimeUs:      int64(unit.EnqueueTimeMs * 1000),
	}

	// Collect all buffer data
	totalLen := 0
	for _, buf := range unit.BufferList {
		totalLen += buf.Length
	}
	du.Data = make([]byte, 0, totalLen)
	for _, buf := range unit.BufferList {
		du.Data = append(du.Data, buf.Data[buf.Offset:buf.Offset+buf.Length]...)
	}

	return cbs.OnDecodeUnit(du)
}

func (d *decoderAdapter) Capabilities() int {
	return 0
}

// audioAdapter implements the common.AudioCallbacks interface
type audioAdapter struct{}

func (a *audioAdapter) Init(audioConfig common.AudioConfiguration, opusConfig *common.OpusConfig, context interface{}, flags int) error {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs == nil || cbs.OnAudioInit == nil {
		return nil
	}

	cfg := &OpusConfig{
		SampleRate:      opusConfig.SampleRate,
		ChannelCount:    opusConfig.ChannelCount,
		Streams:         opusConfig.Streams,
		CoupledStreams:  opusConfig.CoupledStreams,
		SamplesPerFrame: opusConfig.SamplesPerFrame,
	}
	for i := 0; i < len(opusConfig.ChannelMapping) && i < 8; i++ {
		cfg.Mapping[i] = opusConfig.ChannelMapping[i]
	}

	result := cbs.OnAudioInit(int(audioConfig), cfg)
	if result != 0 {
		return fmt.Errorf("audio init failed: %d", result)
	}
	return nil
}

func (a *audioAdapter) Start() {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnAudioStart != nil {
		cbs.OnAudioStart()
	}
}

func (a *audioAdapter) Stop() {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnAudioStop != nil {
		cbs.OnAudioStop()
	}
}

func (a *audioAdapter) Cleanup() {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnAudioCleanup != nil {
		cbs.OnAudioCleanup()
	}
}

func (a *audioAdapter) DecodeAndPlaySample(data []byte) {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnAudioSample != nil {
		cbs.OnAudioSample(data)
	}
}

func (a *audioAdapter) Capabilities() int {
	return 0
}

// StartConnection starts a streaming connection
func StartConnection(serverInfo *ServerInfo, streamConfig *StreamConfig) error {
	clientMutex.Lock()
	defer clientMutex.Unlock()

	// Stop any existing connection
	if activeClient != nil {
		activeClient.Stop()
		activeClient = nil
	}

	// Build configuration
	config := common.StreamConfiguration{
		Width:                 streamConfig.Width,
		Height:                streamConfig.Height,
		FPS:                   streamConfig.FPS,
		Bitrate:               streamConfig.Bitrate,
		PacketSize:            streamConfig.PacketSize,
		StreamingRemotely:     streamConfig.StreamingRemotely,
		AudioConfiguration:    common.AudioConfiguration(streamConfig.AudioConfiguration),
		SupportedVideoFormats: common.VideoFormat(streamConfig.SupportedVideoFormats),
	}

	// Set encryption keys
	if len(streamConfig.RiKey) == 16 {
		config.RemoteInputAesKey = make([]byte, 16)
		copy(config.RemoteInputAesKey, streamConfig.RiKey)
	}

	// Set IV from key ID
	config.RemoteInputAesIV = make([]byte, 16)
	config.RemoteInputAesIV[0] = byte(streamConfig.RiKeyID >> 24)
	config.RemoteInputAesIV[1] = byte(streamConfig.RiKeyID >> 16)
	config.RemoteInputAesIV[2] = byte(streamConfig.RiKeyID >> 8)
	config.RemoteInputAesIV[3] = byte(streamConfig.RiKeyID)

	// Build server info
	srvInfo := common.ServerInformation{
		Address:                  serverInfo.Address,
		ServerCodecModeSupport:   uint32(serverInfo.ServerCodecModeSupport),
		ServerInfoAppVersion:     serverInfo.AppVersion,
	}

	// Create client with adapters
	activeClient = common.NewClient(
		config,
		srvInfo,
		&decoderAdapter{},
		&audioAdapter{},
		&callbackAdapter{},
	)

	// Start connection
	clientCtx, clientCancel = context.WithCancel(context.Background())
	if err := activeClient.Start(clientCtx); err != nil {
		activeClient = nil
		clientCancel()
		return fmt.Errorf("connection failed: %w", err)
	}

	return nil
}

// StopConnection stops the current streaming connection
func StopConnection() {
	clientMutex.Lock()
	defer clientMutex.Unlock()

	if clientCancel != nil {
		clientCancel()
	}

	if activeClient != nil {
		activeClient.Stop()
		activeClient = nil
	}
}

// InterruptConnection interrupts the current connection
func InterruptConnection() {
	StopConnection()
}

// GetStageName returns the human-readable name of a connection stage
func GetStageName(stage int) string {
	switch common.Stage(stage) {
	case common.StageNone:
		return "None"
	case common.StagePlatformInit:
		return "Platform initialization"
	case common.StageRTSPHandshake:
		return "RTSP handshake"
	case common.StageControlStreamInit:
		return "Control stream initialization"
	case common.StageVideoStreamInit:
		return "Video stream initialization"
	case common.StageAudioStreamInit:
		return "Audio stream initialization"
	case common.StageInputStreamInit:
		return "Input stream initialization"
	case common.StageControlStreamStart:
		return "Control stream start"
	case common.StageVideoStreamStart:
		return "Video stream start"
	case common.StageAudioStreamStart:
		return "Audio stream start"
	case common.StageInputStreamStart:
		return "Input stream start"
	case common.StageComplete:
		return "Complete"
	default:
		return fmt.Sprintf("Unknown stage %d", stage)
	}
}

// SendMouseMoveEvent sends a relative mouse move event
func SendMouseMoveEvent(deltaX, deltaY int16) error {
	clientMutex.Lock()
	client := activeClient
	clientMutex.Unlock()

	if client == nil {
		return fmt.Errorf("not connected")
	}
	return client.SendMouseMove(deltaX, deltaY)
}

// SendMousePositionEvent sends an absolute mouse position event
func SendMousePositionEvent(x, y, refWidth, refHeight int16) error {
	clientMutex.Lock()
	client := activeClient
	clientMutex.Unlock()

	if client == nil {
		return fmt.Errorf("not connected")
	}
	return client.SendMousePosition(x, y, refWidth, refHeight)
}

// SendMouseButtonEvent sends a mouse button press/release event
func SendMouseButtonEvent(action int8, button int) error {
	clientMutex.Lock()
	client := activeClient
	clientMutex.Unlock()

	if client == nil {
		return fmt.Errorf("not connected")
	}
	return client.SendMouseButton(uint8(action), button)
}

// SendScrollEvent sends a mouse scroll event
func SendScrollEvent(scrollClicks int8) error {
	clientMutex.Lock()
	client := activeClient
	clientMutex.Unlock()

	if client == nil {
		return fmt.Errorf("not connected")
	}
	return client.SendScroll(int16(scrollClicks) * 120) // Convert to wheel delta
}

// SendKeyboardEvent sends a keyboard key event
func SendKeyboardEvent(keyCode int16, keyAction int8, modifiers int8) error {
	clientMutex.Lock()
	client := activeClient
	clientMutex.Unlock()

	if client == nil {
		return fmt.Errorf("not connected")
	}
	return client.SendKeyboard(keyCode, uint8(keyAction), uint8(modifiers))
}

// SendControllerEvent sends a single controller input event
func SendControllerEvent(buttonFlags int, leftTrigger, rightTrigger uint8, leftStickX, leftStickY, rightStickX, rightStickY int16) error {
	clientMutex.Lock()
	client := activeClient
	clientMutex.Unlock()

	if client == nil {
		return fmt.Errorf("not connected")
	}
	return client.SendController(buttonFlags, leftTrigger, rightTrigger, leftStickX, leftStickY, rightStickX, rightStickY)
}

// SendMultiControllerEvent sends input for a specific controller
func SendMultiControllerEvent(controllerNumber int16, activeGamepadMask int16, buttonFlags int, leftTrigger, rightTrigger uint8, leftStickX, leftStickY, rightStickX, rightStickY int16) error {
	clientMutex.Lock()
	client := activeClient
	clientMutex.Unlock()

	if client == nil {
		return fmt.Errorf("not connected")
	}
	return client.SendMultiController(controllerNumber, activeGamepadMask, buttonFlags, leftTrigger, rightTrigger, leftStickX, leftStickY, rightStickX, rightStickY)
}

// RequestIDRFrame requests an IDR (keyframe) from the server
func RequestIDRFrame() {
	clientMutex.Lock()
	client := activeClient
	clientMutex.Unlock()

	if client != nil {
		client.RequestIDRFrame()
	}
}
