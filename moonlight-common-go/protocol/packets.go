// Package protocol defines the wire protocol structures for Moonlight streaming.
package protocol

import (
	"encoding/binary"
	"math"
)

// Byte order for protocol messages
var ByteOrder = binary.BigEndian
var LittleEndian = binary.LittleEndian

// RTP packet header
type RTPHeader struct {
	Header         uint8  // Version, padding, extension, CSRC count
	PacketType     uint8  // Marker + payload type
	SequenceNumber uint16
	Timestamp      uint32
	SSRC           uint32
}

const (
	RTPHeaderSize    = 12
	MaxRTPHeaderSize = 16
)

// NV input packet header
type NVInputHeader struct {
	Size  uint32 // Big-endian
	Magic uint32 // Little-endian
}

// Keyboard packet
type KeyboardPacket struct {
	Header    NVInputHeader
	Flags     uint8
	KeyCode   uint16
	Modifiers uint8
	Zero      uint8
}

// Relative mouse move packet
type RelMouseMovePacket struct {
	Header NVInputHeader
	DeltaX int16 // Big-endian
	DeltaY int16 // Big-endian
}

// Absolute mouse move packet
type AbsMouseMovePacket struct {
	Header NVInputHeader
	X      uint16 // Big-endian
	Y      uint16 // Big-endian
	Unused uint16
	Width  uint16 // Big-endian
	Height uint16 // Big-endian
}

// Mouse button packet
type MouseButtonPacket struct {
	Header NVInputHeader
	Button uint8
}

// Scroll packet
type ScrollPacket struct {
	Header     NVInputHeader
	ScrollAmt1 int16 // Big-endian
	ScrollAmt2 int16 // Big-endian
	Zero       uint16
}

// Horizontal scroll packet (Sunshine extension)
type HScrollPacket struct {
	Header       NVInputHeader
	ScrollAmount int16 // Big-endian
}

// Controller packet (legacy)
type ControllerPacket struct {
	Header       NVInputHeader
	HeaderB      uint16
	ButtonFlags  uint16
	LeftTrigger  uint8
	RightTrigger uint8
	LeftStickX   int16
	LeftStickY   int16
	RightStickX  int16
	RightStickY  int16
	TailA        uint32
	TailB        uint16
}

// Multi-controller packet
type MultiControllerPacket struct {
	Header           NVInputHeader
	HeaderB          uint16
	ControllerNumber uint16
	ActiveGamepadMask uint16
	MidB             uint16
	ButtonFlags      uint16
	LeftTrigger      uint8
	RightTrigger     uint8
	LeftStickX       int16
	LeftStickY       int16
	RightStickX      int16
	RightStickY      int16
	TailA            uint16
	ButtonFlags2     uint16
	TailB            uint16
}

// Haptics packet (enable rumble)
type HapticsPacket struct {
	Header NVInputHeader
	Enable uint16
}

// Touch packet (Sunshine extension)
type TouchPacket struct {
	Header           NVInputHeader
	EventType        uint8
	Zero1            [3]byte
	PointerID        uint32
	X                [4]byte // netfloat (little-endian float)
	Y                [4]byte
	PressureOrDist   [4]byte
	ContactAreaMajor [4]byte
	ContactAreaMinor [4]byte
	Rotation         uint16
	Zero2            [2]byte
}

// Pen packet (Sunshine extension)
type PenPacket struct {
	Header           NVInputHeader
	EventType        uint8
	ToolType         uint8
	PenButtons       uint8
	Zero1            byte
	X                [4]byte // netfloat
	Y                [4]byte
	PressureOrDist   [4]byte
	Rotation         uint16
	Tilt             uint8
	Zero2            byte
	ContactAreaMajor [4]byte
	ContactAreaMinor [4]byte
}

// Controller arrival packet (Sunshine extension)
type ControllerArrivalPacket struct {
	Header               NVInputHeader
	ControllerNumber     uint8
	Type                 uint8
	Capabilities         uint16
	SupportedButtonFlags uint32
}

// Controller touch packet (Sunshine extension)
type ControllerTouchPacket struct {
	Header           NVInputHeader
	ControllerNumber uint8
	EventType        uint8
	Zero             [2]byte
	PointerID        uint32
	X                [4]byte // netfloat
	Y                [4]byte
	Pressure         [4]byte
}

// Controller motion packet (Sunshine extension)
type ControllerMotionPacket struct {
	Header           NVInputHeader
	ControllerNumber uint8
	MotionType       uint8
	Zero             [2]byte
	X                [4]byte // netfloat
	Y                [4]byte
	Z                [4]byte
}

// Controller battery packet (Sunshine extension)
type ControllerBatteryPacket struct {
	Header            NVInputHeader
	ControllerNumber  uint8
	BatteryState      uint8
	BatteryPercentage uint8
	Zero              byte
}

// UTF-8 text packet
type UTF8TextPacket struct {
	Header NVInputHeader
	Text   []byte
}

// Magic numbers for input packets
const (
	KeyboardMagicDown = 0x03
	KeyboardMagicUp   = 0x04

	MouseMoveRelMagic     = 0x06
	MouseMoveRelMagicGen5 = 0x07
	MouseMoveAbsMagic     = 0x05
	MouseButtonDownMagic  = 0x07
	MouseButtonUpMagic    = 0x08
	MouseButtonDownGen5   = 0x08
	MouseButtonUpGen5     = 0x09

	ScrollMagic     = 0x09
	ScrollMagicGen5 = 0x0A

	ControllerMagic          = 0x0d
	MultiControllerMagic     = 0x0e
	MultiControllerMagicGen5 = 0x1e

	EnableHapticsMagic = 0x55
	UTF8TextEventMagic = 0x56

	// Sunshine extensions
	SSHScrollMagic            = 0x57
	SSTouchMagic              = 0x58
	SSPenMagic                = 0x59
	SSControllerArrivalMagic  = 0x5a
	SSControllerTouchMagic    = 0x5b
	SSControllerMotionMagic   = 0x5c
	SSControllerBatteryMagic  = 0x5d
)

// Controller packet constants
const (
	ControllerHeaderB = 0x1400
	ControllerTailA   = 0x00140000
	ControllerTailB   = 0x0014

	MultiControllerHeaderB = 0x001c
	MultiControllerMidB    = 0x0014
	MultiControllerTailA   = 0x0000
	MultiControllerTailB   = 0x0014
)

// ENet packet flags
const (
	ENetPacketFlagReliable   = 1 << 0
	ENetPacketFlagUnsequenced = 1 << 1
	ENetPacketFlagNoAllocate = 1 << 2
)

// Control stream channel IDs
const (
	CtrlChannelGeneric    = 0
	CtrlChannelUrgent     = 1
	CtrlChannelKeyboard   = 2
	CtrlChannelMouse      = 3
	CtrlChannelGamepadBase = 4 // Channels 4-19 for gamepads
	CtrlChannelSensorBase = 20 // Channels 20-35 for motion sensors
	CtrlChannelTouch      = 36
	CtrlChannelPen        = 37
	CtrlChannelUTF8       = 38
	CtrlChannelCount      = 39
)

// Control stream packet types (Gen 7 encrypted)
var PacketTypesGen7Enc = map[string]uint16{
	"RequestIDR":         0x0302,
	"StartB":             0x0307,
	"InvalidateRefFrames": 0x0301,
	"LossStats":          0x0201,
	"FrameStats":         0x0204,
	"InputData":          0x0206,
	"RumbleData":         0x010b,
	"Termination":        0x0109,
	"HDRMode":            0x010e,
	"RumbleTriggers":     0x5500,
	"SetMotionEvent":     0x5501,
	"SetRGBLED":          0x5502,
	"SetAdaptiveTriggers": 0x5503,
}

// Video encryption header
type EncVideoHeader struct {
	IV          [12]byte
	Tag         [16]byte
	FrameNumber uint32
}

// Control stream TCP packet header
type NVCtrlTCPHeader struct {
	Type          uint16
	PayloadLength uint16
}

// Control stream ENet packet header (V1)
type NVCtrlENetHeaderV1 struct {
	Type uint16
}

// Control stream ENet packet header (V2)
type NVCtrlENetHeaderV2 struct {
	Type          uint16
	PayloadLength uint16
}

// Control stream encrypted packet header
type NVCtrlEncryptedHeader struct {
	EncryptedHeaderType uint16 // Always 0x0001
	Length              uint16 // sizeof(seq) + 16 byte tag + secondary header and data
	Seq                 uint32 // Monotonically increasing sequence number
}

// Frame FEC status (Sunshine extension)
type FrameFECStatus struct {
	FrameIndex      uint32
	HighestRecvIdx  uint32
	NextContiguousIdx uint32
	FirstShardIdx   uint32
	NumShards       uint8
	NumParity       uint8
	NumRecv         uint8
	NumRecovery     uint8
	TotalDataErrors uint8
	TotalParityErrors uint8
	FullyRecv       uint8
	FECPercentage   uint8
	RecvTimeMs      uint16
	RecvFirstMs     uint16
}

// Wheel delta matches Windows WHEEL_DELTA
const WheelDelta = 120

// AES-GCM constants
const AESGCMTagLength = 16

// FloatToNetfloat converts a float32 to little-endian bytes
func FloatToNetfloat(f float32) [4]byte {
	var b [4]byte
	bits := math.Float32bits(f)
	LittleEndian.PutUint32(b[:], bits)
	return b
}

// NetfloatToFloat converts little-endian bytes to float32
func NetfloatToFloat(b [4]byte) float32 {
	bits := LittleEndian.Uint32(b[:])
	return math.Float32frombits(bits)
}
