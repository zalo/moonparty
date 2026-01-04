// Package types provides shared types for the Moonlight streaming protocol.
package types

import (
	"net"
	"time"
)

// Version information
const (
	Version = "1.0.0"
)

// Connection stages
type Stage int

const (
	StageNone Stage = iota
	StagePlatformInit
	StageRTSPHandshake
	StageControlStreamInit
	StageVideoStreamInit
	StageAudioStreamInit
	StageInputStreamInit
	StageControlStreamStart
	StageVideoStreamStart
	StageAudioStreamStart
	StageInputStreamStart
	StageComplete
)

// Connection status
type ConnectionStatus int

const (
	ConnStatusOkay ConnectionStatus = iota
	ConnStatusPoor
)

// Error codes
const (
	ErrUnsupported             = -5501
	ErrGracefulTermination     = 0
	ErrNoVideoTraffic          = -100
	ErrNoVideoFrame            = -101
	ErrUnexpectedTermination   = -102
	ErrProtectedContent        = -103
	ErrFrameConversion         = -104
)

// Video formats
type VideoFormat int

const (
	VideoFormatH264 VideoFormat = 0x0001
	VideoFormatH265 VideoFormat = 0x0100
	VideoFormatAV1  VideoFormat = 0x0200

	VideoFormatMaskH264 = 0x000F
	VideoFormatMaskH265 = 0x0F00
	VideoFormatMaskAV1  = 0xF000
)

// Audio configuration
type AudioConfiguration int

const (
	AudioConfigStereo              AudioConfiguration = 0
	AudioConfigSurround51          AudioConfiguration = 1
	AudioConfigSurround71          AudioConfiguration = 2
	AudioConfigSurround51Highaudio AudioConfiguration = 3
	AudioConfigSurround71Highaudio AudioConfiguration = 4
)

// Controller types
type ControllerType uint8

const (
	ControllerTypeUnknown ControllerType = iota
	ControllerTypeXbox
	ControllerTypePS
	ControllerTypeNintendo
)

// Controller capabilities
type ControllerCapabilities uint16

const (
	CapAnalogTriggers   ControllerCapabilities = 0x01
	CapRumble           ControllerCapabilities = 0x02
	CapTriggerRumble    ControllerCapabilities = 0x04
	CapTouchpad         ControllerCapabilities = 0x08
	CapAccelerometer    ControllerCapabilities = 0x10
	CapGyro             ControllerCapabilities = 0x20
	CapBattery          ControllerCapabilities = 0x40
	CapRGB              ControllerCapabilities = 0x80
)

// Button flags
const (
	ButtonUp          = 0x0001
	ButtonDown        = 0x0002
	ButtonLeft        = 0x0004
	ButtonRight       = 0x0008
	ButtonStart       = 0x0010
	ButtonBack        = 0x0020
	ButtonLeftStick   = 0x0040
	ButtonRightStick  = 0x0080
	ButtonLeftBumper  = 0x0100
	ButtonRightBumper = 0x0200
	ButtonHome        = 0x0400 // Special/Guide button
	ButtonA           = 0x1000
	ButtonB           = 0x2000
	ButtonX           = 0x4000
	ButtonY           = 0x8000

	// Extended button flags (Sunshine only)
	ButtonMisc     = 0x010000
	ButtonPaddle1  = 0x020000
	ButtonPaddle2  = 0x040000
	ButtonPaddle3  = 0x080000
	ButtonPaddle4  = 0x100000
	ButtonTouchpad = 0x200000
)

// Key actions
const (
	KeyActionDown = 0x03
	KeyActionUp   = 0x04
)

// Key modifiers
const (
	ModifierShift = 0x01
	ModifierCtrl  = 0x02
	ModifierAlt   = 0x04
	ModifierMeta  = 0x08 // Win key
)

// Mouse buttons
const (
	MouseButtonLeft   = 0x01
	MouseButtonMiddle = 0x02
	MouseButtonRight  = 0x03
	MouseButtonX1     = 0x04
	MouseButtonX2     = 0x05
)

// Mouse actions
const (
	MouseActionPress   = 0x07
	MouseActionRelease = 0x08
)

// Touch events
type TouchEventType uint8

const (
	TouchEventHover TouchEventType = iota
	TouchEventDown
	TouchEventUp
	TouchEventMove
	TouchEventCancel
	TouchEventCancelAll
	TouchEventHoverLeave
	TouchEventButtonOnly
)

// Pen tool types
type PenToolType uint8

const (
	PenToolUnknown PenToolType = iota
	PenToolPen
	PenToolEraser
)

// Pen buttons
const (
	PenButtonPrimary   = 0x01
	PenButtonSecondary = 0x02
	PenButtonTertiary  = 0x04
)

// Motion types
type MotionType uint8

const (
	MotionTypeAccelerometer MotionType = 1
	MotionTypeGyro          MotionType = 2
)

// Battery states
type BatteryState uint8

const (
	BatteryStateUnknown     BatteryState = 0x00
	BatteryStateNotPresent  BatteryState = 0x01
	BatteryStateDischarging BatteryState = 0x02
	BatteryStateCharging    BatteryState = 0x03
	BatteryStateNotCharging BatteryState = 0x04 // Connected to power, not charging (full or temp)
	BatteryStateFull        BatteryState = 0x05
)

// Decoder renderer callbacks capabilities
const (
	CapabilityDirectSubmit = 0x01
	CapabilityPullRenderer = 0x02
)

// Encryption features (must match Sunshine SS_ENC_* values)
const (
	EncControlV2 = 0x01 // SS_ENC_CONTROL_V2
	EncVideo     = 0x02 // SS_ENC_VIDEO
	EncAudio     = 0x04 // SS_ENC_AUDIO
)

// Feature flags (Sunshine extensions)
const (
	FFPenTouchEvents        = 0x01
	FFControllerTouchEvents = 0x02
)

// StreamConfiguration holds the streaming configuration
type StreamConfiguration struct {
	// Video settings
	Width      int
	Height     int
	FPS        int
	Bitrate    int // In Kbps
	PacketSize int

	// Stream features
	StreamingRemotely     int
	AudioConfiguration    AudioConfiguration
	SupportedVideoFormats VideoFormat

	// Encryption keys (from pairing)
	RemoteInputAesKey []byte // 16 bytes
	RemoteInputAesIV  []byte // 16 bytes

	// Color settings
	ColorSpace int
	ColorRange int
	ColorDepth int
	HDREnabled bool

	// Client settings
	ClientRefreshRateCapHz int
	EncryptionFlags        uint32
	AudioEncryptionEnabled bool
}

// ServerInformation contains server details
type ServerInformation struct {
	Address              string
	ServerInfoAppVersion string

	// Server codec support
	ServerCodecModeSupport uint32
}

// HDRMetadata contains HDR display metadata
type HDRMetadata struct {
	DisplayPrimaries      [3]Chromaticity
	WhitePoint            Chromaticity
	MaxDisplayLuminance   uint16
	MinDisplayLuminance   uint16
	MaxContentLightLevel  uint16
	MaxFrameAvgLightLevel uint16
	MaxFullFrameLuminance uint16
}

// Chromaticity represents x,y chromaticity coordinates
type Chromaticity struct {
	X uint16
	Y uint16
}

// OpusConfig contains Opus decoder configuration
type OpusConfig struct {
	SampleRate      int
	ChannelCount    int
	Streams         int
	CoupledStreams  int
	SamplesPerFrame int
	ChannelMapping  []uint8
}

// DecodeUnit represents a video decode unit
type DecodeUnit struct {
	BufferList         []BufferDescriptor
	FrameNumber        uint32
	FrameType          FrameType
	PresentationTimeMs uint64
	EnqueueTimeMs      uint64
}

// BufferDescriptor describes a buffer in a decode unit
type BufferDescriptor struct {
	Data   []byte
	Offset int
	Length int
}

// FrameType indicates the type of video frame
type FrameType int

const (
	FrameTypeUnknown FrameType = iota
	FrameTypeIDR               // Keyframe
	FrameTypePFrames           // P-frames only
)

// RTPVideoStats contains video stream statistics
type RTPVideoStats struct {
	ReceivedPackets      uint32
	DroppedPackets       uint32
	RecoveredPackets     uint32
	TotalFrames          uint32
	ReceivedFrames       uint32
	DroppedFrames        uint32
	RequestedIDRFrames   uint32

	SubmittedFrames      uint32
	NetworkDroppedFrames uint32
	TotalReassemblyTime  uint32

	MeasurementStartTime time.Time
}

// RTPAudioStats contains audio stream statistics
type RTPAudioStats struct {
	ReceivedPackets  uint32
	DroppedPackets   uint32
	RecoveredPackets uint32

	MeasurementStartTime time.Time
}

// RTTInfo contains round-trip time estimates
type RTTInfo struct {
	EstimatedRTT         uint32
	EstimatedRTTVariance uint32
}

// Connection represents an active streaming connection
type Connection struct {
	Config     StreamConfiguration
	ServerInfo ServerInformation
	RemoteAddr net.Addr
	LocalAddr  net.Addr

	// Callbacks
	Decoder  DecoderCallbacks
	Audio    AudioCallbacks
	Listener ConnectionCallbacks

	// Internal state
	Interrupted bool
	VideoFormat VideoFormat
}

// DecoderCallbacks interface for video decoder operations
type DecoderCallbacks interface {
	// Setup initializes the decoder with format and dimensions
	Setup(format VideoFormat, width, height, fps int, context interface{}, flags int) error

	// Start begins decoder operation
	Start()

	// Stop halts decoder operation
	Stop()

	// Cleanup releases decoder resources
	Cleanup()

	// SubmitDecodeUnit submits a decode unit for processing
	// Returns 0 on success
	SubmitDecodeUnit(unit *DecodeUnit) int

	// Capabilities returns decoder capability flags
	Capabilities() int
}

// AudioCallbacks interface for audio decoder operations
type AudioCallbacks interface {
	// Init initializes the audio decoder
	Init(audioConfig AudioConfiguration, opusConfig *OpusConfig, context interface{}, flags int) error

	// Start begins audio playback
	Start()

	// Stop halts audio playback
	Stop()

	// Cleanup releases audio resources
	Cleanup()

	// DecodeAndPlaySample decodes and plays an audio sample
	// data is nil for packet loss concealment
	DecodeAndPlaySample(data []byte)

	// Capabilities returns audio callback capability flags
	Capabilities() int
}

// ConnectionCallbacks interface for connection event handling
type ConnectionCallbacks interface {
	// StageStarting is called when a connection stage begins
	StageStarting(stage Stage)

	// StageComplete is called when a connection stage completes
	StageComplete(stage Stage)

	// StageFailed is called when a connection stage fails
	StageFailed(stage Stage, err error)

	// ConnectionStarted is called when streaming begins
	ConnectionStarted()

	// ConnectionTerminated is called when the connection ends
	ConnectionTerminated(errorCode int)

	// ConnectionStatusUpdate reports connection quality changes
	ConnectionStatusUpdate(status ConnectionStatus)

	// SetHDRMode is called when HDR mode changes
	SetHDRMode(enabled bool)

	// Rumble triggers controller rumble
	Rumble(controllerNumber, lowFreq, highFreq uint16)

	// RumbleTriggers triggers controller trigger rumble (Xbox style)
	RumbleTriggers(controllerNumber, leftTrigger, rightTrigger uint16)

	// SetMotionEventState enables/disables motion sensor reporting
	SetMotionEventState(controllerNumber uint16, motionType MotionType, reportRateHz uint16)

	// SetControllerLED sets controller LED color
	SetControllerLED(controllerNumber uint16, r, g, b uint8)
}
