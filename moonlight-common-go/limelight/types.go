// Package limelight provides the core types and interfaces for the Moonlight streaming protocol.
// This is a Go port of moonlight-common-c.
package limelight

import (
	"github.com/zalo/moonparty/moonlight-common-go/types"
)

// Re-export all types from the types package for backwards compatibility
type (
	Stage                  = types.Stage
	ConnectionStatus       = types.ConnectionStatus
	VideoFormat            = types.VideoFormat
	AudioConfiguration     = types.AudioConfiguration
	ControllerType         = types.ControllerType
	ControllerCapabilities = types.ControllerCapabilities
	TouchEventType         = types.TouchEventType
	PenToolType            = types.PenToolType
	MotionType             = types.MotionType
	BatteryState           = types.BatteryState
	FrameType              = types.FrameType
	StreamConfiguration    = types.StreamConfiguration
	ServerInformation      = types.ServerInformation
	HDRMetadata            = types.HDRMetadata
	Chromaticity           = types.Chromaticity
	OpusConfig             = types.OpusConfig
	DecodeUnit             = types.DecodeUnit
	BufferDescriptor       = types.BufferDescriptor
	RTPVideoStats          = types.RTPVideoStats
	RTPAudioStats          = types.RTPAudioStats
	RTTInfo                = types.RTTInfo
	Connection             = types.Connection
	DecoderCallbacks       = types.DecoderCallbacks
	AudioCallbacks         = types.AudioCallbacks
	ConnectionCallbacks    = types.ConnectionCallbacks
)

// Re-export constants
const (
	Version = types.Version

	// Stages
	StageNone               = types.StageNone
	StagePlatformInit       = types.StagePlatformInit
	StageRTSPHandshake      = types.StageRTSPHandshake
	StageControlStreamInit  = types.StageControlStreamInit
	StageVideoStreamInit    = types.StageVideoStreamInit
	StageAudioStreamInit    = types.StageAudioStreamInit
	StageInputStreamInit    = types.StageInputStreamInit
	StageControlStreamStart = types.StageControlStreamStart
	StageVideoStreamStart   = types.StageVideoStreamStart
	StageAudioStreamStart   = types.StageAudioStreamStart
	StageInputStreamStart   = types.StageInputStreamStart
	StageComplete           = types.StageComplete

	// Connection status
	ConnStatusOkay = types.ConnStatusOkay
	ConnStatusPoor = types.ConnStatusPoor

	// Error codes
	ErrUnsupported           = types.ErrUnsupported
	ErrGracefulTermination   = types.ErrGracefulTermination
	ErrNoVideoTraffic        = types.ErrNoVideoTraffic
	ErrNoVideoFrame          = types.ErrNoVideoFrame
	ErrUnexpectedTermination = types.ErrUnexpectedTermination
	ErrProtectedContent      = types.ErrProtectedContent
	ErrFrameConversion       = types.ErrFrameConversion

	// Video formats
	VideoFormatH264     = types.VideoFormatH264
	VideoFormatH265     = types.VideoFormatH265
	VideoFormatAV1      = types.VideoFormatAV1
	VideoFormatMaskH264 = types.VideoFormatMaskH264
	VideoFormatMaskH265 = types.VideoFormatMaskH265
	VideoFormatMaskAV1  = types.VideoFormatMaskAV1

	// Audio config
	AudioConfigStereo              = types.AudioConfigStereo
	AudioConfigSurround51          = types.AudioConfigSurround51
	AudioConfigSurround71          = types.AudioConfigSurround71
	AudioConfigSurround51Highaudio = types.AudioConfigSurround51Highaudio
	AudioConfigSurround71Highaudio = types.AudioConfigSurround71Highaudio

	// Controller types
	ControllerTypeUnknown  = types.ControllerTypeUnknown
	ControllerTypeXbox     = types.ControllerTypeXbox
	ControllerTypePS       = types.ControllerTypePS
	ControllerTypeNintendo = types.ControllerTypeNintendo

	// Controller capabilities
	CapAnalogTriggers = types.CapAnalogTriggers
	CapRumble         = types.CapRumble
	CapTriggerRumble  = types.CapTriggerRumble
	CapTouchpad       = types.CapTouchpad
	CapAccelerometer  = types.CapAccelerometer
	CapGyro           = types.CapGyro
	CapBattery        = types.CapBattery
	CapRGB            = types.CapRGB

	// Button flags
	ButtonUp          = types.ButtonUp
	ButtonDown        = types.ButtonDown
	ButtonLeft        = types.ButtonLeft
	ButtonRight       = types.ButtonRight
	ButtonStart       = types.ButtonStart
	ButtonBack        = types.ButtonBack
	ButtonLeftStick   = types.ButtonLeftStick
	ButtonRightStick  = types.ButtonRightStick
	ButtonLeftBumper  = types.ButtonLeftBumper
	ButtonRightBumper = types.ButtonRightBumper
	ButtonHome        = types.ButtonHome
	ButtonA           = types.ButtonA
	ButtonB           = types.ButtonB
	ButtonX           = types.ButtonX
	ButtonY           = types.ButtonY
	ButtonMisc        = types.ButtonMisc
	ButtonPaddle1     = types.ButtonPaddle1
	ButtonPaddle2     = types.ButtonPaddle2
	ButtonPaddle3     = types.ButtonPaddle3
	ButtonPaddle4     = types.ButtonPaddle4
	ButtonTouchpad    = types.ButtonTouchpad

	// Key actions
	KeyActionDown = types.KeyActionDown
	KeyActionUp   = types.KeyActionUp

	// Key modifiers
	ModifierShift = types.ModifierShift
	ModifierCtrl  = types.ModifierCtrl
	ModifierAlt   = types.ModifierAlt
	ModifierMeta  = types.ModifierMeta

	// Mouse buttons
	MouseButtonLeft   = types.MouseButtonLeft
	MouseButtonMiddle = types.MouseButtonMiddle
	MouseButtonRight  = types.MouseButtonRight
	MouseButtonX1     = types.MouseButtonX1
	MouseButtonX2     = types.MouseButtonX2

	// Mouse actions
	MouseActionPress   = types.MouseActionPress
	MouseActionRelease = types.MouseActionRelease

	// Touch events
	TouchEventHover      = types.TouchEventHover
	TouchEventDown       = types.TouchEventDown
	TouchEventUp         = types.TouchEventUp
	TouchEventMove       = types.TouchEventMove
	TouchEventCancel     = types.TouchEventCancel
	TouchEventCancelAll  = types.TouchEventCancelAll
	TouchEventHoverLeave = types.TouchEventHoverLeave
	TouchEventButtonOnly = types.TouchEventButtonOnly

	// Pen tool types
	PenToolUnknown = types.PenToolUnknown
	PenToolPen     = types.PenToolPen
	PenToolEraser  = types.PenToolEraser

	// Pen buttons
	PenButtonPrimary   = types.PenButtonPrimary
	PenButtonSecondary = types.PenButtonSecondary
	PenButtonTertiary  = types.PenButtonTertiary

	// Motion types
	MotionTypeAccelerometer = types.MotionTypeAccelerometer
	MotionTypeGyro          = types.MotionTypeGyro

	// Battery states
	BatteryStateUnknown     = types.BatteryStateUnknown
	BatteryStateNotPresent  = types.BatteryStateNotPresent
	BatteryStateDischarging = types.BatteryStateDischarging
	BatteryStateCharging    = types.BatteryStateCharging
	BatteryStateNotCharging = types.BatteryStateNotCharging
	BatteryStateFull        = types.BatteryStateFull

	// Decoder capabilities
	CapabilityDirectSubmit = types.CapabilityDirectSubmit
	CapabilityPullRenderer = types.CapabilityPullRenderer

	// Encryption
	EncVideo     = types.EncVideo
	EncAudio     = types.EncAudio
	EncControlV2 = types.EncControlV2

	// Feature flags
	FFPenTouchEvents        = types.FFPenTouchEvents
	FFControllerTouchEvents = types.FFControllerTouchEvents

	// Frame types
	FrameTypeUnknown = types.FrameTypeUnknown
	FrameTypeIDR     = types.FrameTypeIDR
	FrameTypePFrames = types.FrameTypePFrames
)
