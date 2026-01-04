// Package input handles game input sending for the Moonlight streaming protocol.
package input

import (
	"encoding/binary"
	"sync"

	"github.com/moonparty/moonlight-common-go/protocol"
)

// MaxGamepads is the maximum number of controllers supported
const MaxGamepads = 16

// MaxMotionEvents is the number of motion sensor types
const MaxMotionEvents = 2

// MaxInputPacketSize is the maximum size of an input packet
const MaxInputPacketSize = 128

// MaxQueuedInputPackets is the maximum number of queued input packets
const MaxQueuedInputPackets = 150

// MouseBatchingIntervalMs is the batching interval for mouse events
const MouseBatchingIntervalMs = 1

// Stream manages input packet sending
type Stream struct {
	mu sync.Mutex

	// Configuration
	appVersion     [4]int
	isSunshine     bool
	encryptedCtrl  bool

	// Encryption
	aesKey []byte
	aesIV  []byte

	// Packet sending
	sendFunc func(channelID uint8, flags uint32, data []byte, moreData bool) error

	// Batched state
	currentRelMouseState relativeMouseState
	currentAbsMouseState absoluteMouseState
	currentGamepadState  [MaxGamepads]*gamepadState
	gamepadSensorState   [MaxGamepads][MaxMotionEvents]sensorState

	// Virtual mouse position
	absCurrentPosX float32
	absCurrentPosY float32

	// Pen state
	currentPenButtonState uint8

	// Batched scroll
	needsBatchedScroll bool
	batchedScrollDelta int

	initialized bool
}

type relativeMouseState struct {
	deltaX int
	deltaY int
	dirty  bool
}

type absoluteMouseState struct {
	x, y          int
	width, height int
	dirty         bool
}

type gamepadState struct {
	buttonFlags  uint32
	leftTrigger  uint8
	rightTrigger uint8
	leftStickX   int16
	leftStickY   int16
	rightStickX  int16
	rightStickY  int16
}

type sensorState struct {
	x, y, z float32
	dirty   bool
}

// NewStream creates a new input stream
func NewStream(appVersion [4]int, isSunshine bool, aesKey, aesIV []byte,
	sendFunc func(channelID uint8, flags uint32, data []byte, moreData bool) error) *Stream {

	s := &Stream{
		appVersion:   appVersion,
		isSunshine:   isSunshine,
		aesKey:       aesKey,
		aesIV:        aesIV,
		sendFunc:     sendFunc,
		absCurrentPosX: 0.5,
		absCurrentPosY: 0.5,
	}

	s.encryptedCtrl = appVersionAtLeast(appVersion, 7, 1, 431)
	s.needsBatchedScroll = appVersionAtLeast(appVersion, 7, 1, 409) && !isSunshine
	s.initialized = true

	return s
}

// Close shuts down the input stream
func (s *Stream) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initialized = false
}

// SendMouseMove sends a relative mouse movement event
func (s *Stream) SendMouseMove(deltaX, deltaY int16) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.initialized {
		return ErrNotInitialized
	}

	if deltaX == 0 && deltaY == 0 {
		return nil
	}

	s.currentRelMouseState.deltaX += int(deltaX)
	s.currentRelMouseState.deltaY += int(deltaY)

	if !s.currentRelMouseState.dirty {
		s.currentRelMouseState.dirty = true

		packet := s.buildRelMouseMovePacket(deltaX, deltaY)
		return s.sendFunc(protocol.CtrlChannelMouse, protocol.ENetPacketFlagReliable, packet, false)
	}

	return nil
}

// SendMousePosition sends an absolute mouse position event
func (s *Stream) SendMousePosition(x, y, refWidth, refHeight int16) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.initialized {
		return ErrNotInitialized
	}

	s.currentAbsMouseState.x = int(x)
	s.currentAbsMouseState.y = int(y)
	s.currentAbsMouseState.width = int(refWidth)
	s.currentAbsMouseState.height = int(refHeight)

	if !s.currentAbsMouseState.dirty {
		s.currentAbsMouseState.dirty = true

		packet := s.buildAbsMouseMovePacket(x, y, refWidth, refHeight)
		return s.sendFunc(protocol.CtrlChannelMouse, protocol.ENetPacketFlagReliable, packet, false)
	}

	// Update virtual mouse position
	s.absCurrentPosX = clampFloat(float32(x)/float32(refWidth-1), 0, 1)
	s.absCurrentPosY = clampFloat(float32(y)/float32(refHeight-1), 0, 1)

	return nil
}

// SendMouseButton sends a mouse button event
func (s *Stream) SendMouseButton(action uint8, button int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.initialized {
		return ErrNotInitialized
	}

	packet := s.buildMouseButtonPacket(action, button)
	return s.sendFunc(protocol.CtrlChannelMouse, protocol.ENetPacketFlagReliable, packet, false)
}

// SendKeyboard sends a keyboard event
func (s *Stream) SendKeyboard(keyCode int16, keyAction uint8, modifiers uint8, flags uint8) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.initialized {
		return ErrNotInitialized
	}

	// Apply modifier fixups for GFE compatibility
	if !s.isSunshine {
		keyCode, modifiers = s.fixModifiers(keyCode, modifiers)
	}

	packet := s.buildKeyboardPacket(keyCode, keyAction, modifiers, flags)
	return s.sendFunc(protocol.CtrlChannelKeyboard, protocol.ENetPacketFlagReliable, packet, false)
}

// SendScroll sends a scroll event
func (s *Stream) SendScroll(amount int16) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.initialized {
		return ErrNotInitialized
	}

	if amount == 0 {
		return nil
	}

	if s.needsBatchedScroll {
		return s.sendBatchedScroll(amount)
	}

	packet := s.buildScrollPacket(amount)
	return s.sendFunc(protocol.CtrlChannelMouse, protocol.ENetPacketFlagReliable, packet, false)
}

// SendHighResScroll sends a high-resolution scroll event
func (s *Stream) SendHighResScroll(amount int16) error {
	return s.SendScroll(amount)
}

// SendHScroll sends a horizontal scroll event (Sunshine only)
func (s *Stream) SendHScroll(amount int16) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.initialized {
		return ErrNotInitialized
	}

	if !s.isSunshine {
		return ErrUnsupported
	}

	if amount == 0 {
		return nil
	}

	packet := s.buildHScrollPacket(amount)
	return s.sendFunc(protocol.CtrlChannelMouse, protocol.ENetPacketFlagReliable, packet, false)
}

// SendController sends a controller state event
func (s *Stream) SendController(buttonFlags int, leftTrigger, rightTrigger uint8,
	leftStickX, leftStickY, rightStickX, rightStickY int16) error {
	return s.SendMultiController(0, 1, buttonFlags, leftTrigger, rightTrigger,
		leftStickX, leftStickY, rightStickX, rightStickY)
}

// SendMultiController sends a multi-controller state event
func (s *Stream) SendMultiController(controllerNumber, activeGamepadMask int16, buttonFlags int,
	leftTrigger, rightTrigger uint8, leftStickX, leftStickY, rightStickX, rightStickY int16) error {

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.initialized {
		return ErrNotInitialized
	}

	// Fix sign extension bug from old clients
	if buttonFlags < 0 {
		buttonFlags &= 0xFFFF
	}

	// Limit controller numbers for GFE
	if !s.isSunshine {
		controllerNumber %= 4
		activeGamepadMask &= 0xF

		// Map MISC to SPECIAL for GFE
		if buttonFlags&protocol.ButtonMisc != 0 {
			buttonFlags |= protocol.ButtonHome
		}
	} else {
		controllerNumber %= MaxGamepads
	}

	packet := s.buildMultiControllerPacket(controllerNumber, activeGamepadMask, buttonFlags,
		leftTrigger, rightTrigger, leftStickX, leftStickY, rightStickX, rightStickY)

	channelID := uint8(protocol.CtrlChannelGamepadBase + controllerNumber)
	return s.sendFunc(channelID, protocol.ENetPacketFlagReliable, packet, false)
}

// SendControllerArrival sends a controller arrival notification (Sunshine only)
func (s *Stream) SendControllerArrival(controllerNumber uint8, activeGamepadMask uint16,
	controllerType uint8, supportedButtons uint32, capabilities uint16) error {

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.initialized {
		return ErrNotInitialized
	}

	controllerNumber %= MaxGamepads

	if s.isSunshine {
		packet := s.buildControllerArrivalPacket(controllerNumber, controllerType, capabilities, supportedButtons)
		channelID := uint8(protocol.CtrlChannelGamepadBase + int(controllerNumber))
		if err := s.sendFunc(channelID, protocol.ENetPacketFlagReliable, packet, false); err != nil {
			return err
		}
	}

	// Also send MC event for compatibility
	return s.SendMultiController(int16(controllerNumber), int16(activeGamepadMask), 0, 0, 0, 0, 0, 0, 0)
}

// SendTouch sends a touch event (Sunshine only)
func (s *Stream) SendTouch(eventType uint8, pointerID uint32, x, y, pressure, contactMajor, contactMinor float32, rotation uint16) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.initialized {
		return ErrNotInitialized
	}

	if !s.isSunshine {
		return ErrUnsupported
	}

	packet := s.buildTouchPacket(eventType, pointerID, x, y, pressure, contactMajor, contactMinor, rotation)
	flags := uint32(protocol.ENetPacketFlagReliable)
	if eventType == uint8(TouchEventHover) || eventType == uint8(TouchEventMove) {
		flags = 0 // Allow dropping for hover/move events
	}
	return s.sendFunc(protocol.CtrlChannelTouch, flags, packet, false)
}

// SendPen sends a pen/stylus event (Sunshine only)
func (s *Stream) SendPen(eventType, toolType, penButtons uint8, x, y, pressure float32,
	contactMajor, contactMinor float32, rotation uint16, tilt uint8) error {

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.initialized {
		return ErrNotInitialized
	}

	if !s.isSunshine {
		return ErrUnsupported
	}

	packet := s.buildPenPacket(eventType, toolType, penButtons, x, y, pressure, contactMajor, contactMinor, rotation, tilt)
	flags := uint32(protocol.ENetPacketFlagReliable)
	if (eventType == uint8(TouchEventHover) || eventType == uint8(TouchEventMove)) &&
		penButtons == s.currentPenButtonState {
		flags = 0
	}
	s.currentPenButtonState = penButtons
	return s.sendFunc(protocol.CtrlChannelPen, flags, packet, false)
}

// SendControllerMotion sends motion sensor data (Sunshine only)
func (s *Stream) SendControllerMotion(controllerNumber, motionType uint8, x, y, z float32) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.initialized {
		return ErrNotInitialized
	}

	if !s.isSunshine {
		return ErrUnsupported
	}

	if motionType < 1 || motionType > MaxMotionEvents {
		return ErrInvalidParameter
	}

	controllerNumber %= MaxGamepads

	s.gamepadSensorState[controllerNumber][motionType-1].x = x
	s.gamepadSensorState[controllerNumber][motionType-1].y = y
	s.gamepadSensorState[controllerNumber][motionType-1].z = z

	if !s.gamepadSensorState[controllerNumber][motionType-1].dirty {
		s.gamepadSensorState[controllerNumber][motionType-1].dirty = true

		packet := s.buildControllerMotionPacket(controllerNumber, motionType, x, y, z)
		channelID := uint8(protocol.CtrlChannelSensorBase + int(controllerNumber))
		return s.sendFunc(channelID, protocol.ENetPacketFlagReliable, packet, false)
	}

	return nil
}

// SendControllerBattery sends battery status (Sunshine only)
func (s *Stream) SendControllerBattery(controllerNumber, batteryState, percentage uint8) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.initialized {
		return ErrNotInitialized
	}

	if !s.isSunshine {
		return ErrUnsupported
	}

	controllerNumber %= MaxGamepads
	packet := s.buildControllerBatteryPacket(controllerNumber, batteryState, percentage)
	channelID := uint8(protocol.CtrlChannelGamepadBase + int(controllerNumber))
	return s.sendFunc(channelID, protocol.ENetPacketFlagReliable, packet, false)
}

// SendUTF8Text sends UTF-8 text input
func (s *Stream) SendUTF8Text(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.initialized {
		return ErrNotInitialized
	}

	packet := s.buildUTF8TextPacket(text)
	return s.sendFunc(protocol.CtrlChannelUTF8, protocol.ENetPacketFlagReliable, packet, false)
}

// Helper functions

func (s *Stream) sendBatchedScroll(amount int16) error {
	// Reset accumulated delta when direction changes
	if (s.batchedScrollDelta < 0 && amount > 0) || (s.batchedScrollDelta > 0 && amount < 0) {
		s.batchedScrollDelta = 0
	}

	s.batchedScrollDelta += int(amount)

	for abs(s.batchedScrollDelta) >= protocol.WheelDelta {
		sendAmount := int16(protocol.WheelDelta)
		if s.batchedScrollDelta < 0 {
			sendAmount = -sendAmount
		}

		packet := s.buildScrollPacket(sendAmount)
		if err := s.sendFunc(protocol.CtrlChannelMouse, protocol.ENetPacketFlagReliable, packet, false); err != nil {
			return err
		}

		s.batchedScrollDelta -= int(sendAmount)
	}

	return nil
}

func (s *Stream) fixModifiers(keyCode int16, modifiers uint8) (int16, uint8) {
	switch keyCode & 0xFF {
	case 0x5B, 0x5C: // VK_LWIN, VK_RWIN
		modifiers &^= ModifierMeta
	case 0xA0: // VK_LSHIFT
		modifiers |= ModifierShift
	case 0xA1: // VK_RSHIFT
		modifiers &^= ModifierShift
	case 0xA2: // VK_LCONTROL
		modifiers |= ModifierCtrl
	case 0xA3: // VK_RCONTROL
		modifiers &^= ModifierCtrl
	case 0xA4: // VK_LMENU
		modifiers |= ModifierAlt
	case 0xA5: // VK_RMENU
		modifiers &^= ModifierAlt
	}
	return keyCode, modifiers
}

// Packet building functions

func (s *Stream) buildRelMouseMovePacket(deltaX, deltaY int16) []byte {
	buf := make([]byte, 12)
	magic := uint32(protocol.MouseMoveRelMagic)
	if s.appVersion[0] >= 5 {
		magic = protocol.MouseMoveRelMagicGen5
	}

	binary.BigEndian.PutUint32(buf[0:4], 8) // Size
	binary.LittleEndian.PutUint32(buf[4:8], magic)
	binary.BigEndian.PutUint16(buf[8:10], uint16(deltaX))
	binary.BigEndian.PutUint16(buf[10:12], uint16(deltaY))

	return buf
}

func (s *Stream) buildAbsMouseMovePacket(x, y, width, height int16) []byte {
	buf := make([]byte, 18)
	binary.BigEndian.PutUint32(buf[0:4], 14) // Size
	binary.LittleEndian.PutUint32(buf[4:8], protocol.MouseMoveAbsMagic)
	binary.BigEndian.PutUint16(buf[8:10], uint16(x))
	binary.BigEndian.PutUint16(buf[10:12], uint16(y))
	binary.BigEndian.PutUint16(buf[12:14], 0) // Unused
	binary.BigEndian.PutUint16(buf[14:16], uint16(width-1))
	binary.BigEndian.PutUint16(buf[16:18], uint16(height-1))
	return buf
}

func (s *Stream) buildMouseButtonPacket(action uint8, button int) []byte {
	buf := make([]byte, 9)
	magic := uint32(action)
	if s.appVersion[0] >= 5 {
		magic++
	}

	binary.BigEndian.PutUint32(buf[0:4], 5) // Size
	binary.LittleEndian.PutUint32(buf[4:8], magic)
	buf[8] = uint8(button)
	return buf
}

func (s *Stream) buildKeyboardPacket(keyCode int16, action, modifiers, flags uint8) []byte {
	buf := make([]byte, 14)
	binary.BigEndian.PutUint32(buf[0:4], 10) // Size
	binary.LittleEndian.PutUint32(buf[4:8], uint32(action))

	if s.isSunshine {
		buf[8] = flags
	} else {
		buf[8] = 0
	}
	binary.LittleEndian.PutUint16(buf[9:11], uint16(keyCode))
	buf[11] = modifiers
	buf[12] = 0
	buf[13] = 0
	return buf
}

func (s *Stream) buildScrollPacket(amount int16) []byte {
	buf := make([]byte, 14)
	magic := uint32(protocol.ScrollMagic)
	if s.appVersion[0] >= 5 {
		magic = protocol.ScrollMagicGen5
	}

	binary.BigEndian.PutUint32(buf[0:4], 10) // Size
	binary.LittleEndian.PutUint32(buf[4:8], magic)
	binary.BigEndian.PutUint16(buf[8:10], uint16(amount))
	binary.BigEndian.PutUint16(buf[10:12], uint16(amount))
	binary.BigEndian.PutUint16(buf[12:14], 0)
	return buf
}

func (s *Stream) buildHScrollPacket(amount int16) []byte {
	buf := make([]byte, 10)
	binary.BigEndian.PutUint32(buf[0:4], 6) // Size
	binary.LittleEndian.PutUint32(buf[4:8], protocol.SSHScrollMagic)
	binary.BigEndian.PutUint16(buf[8:10], uint16(amount))
	return buf
}

func (s *Stream) buildMultiControllerPacket(controllerNumber, activeGamepadMask int16, buttonFlags int,
	leftTrigger, rightTrigger uint8, leftStickX, leftStickY, rightStickX, rightStickY int16) []byte {

	buf := make([]byte, 30)
	magic := uint32(protocol.MultiControllerMagic)
	if s.appVersion[0] >= 5 {
		magic = protocol.MultiControllerMagicGen5
	}

	binary.BigEndian.PutUint32(buf[0:4], 26) // Size
	binary.LittleEndian.PutUint32(buf[4:8], magic)
	binary.LittleEndian.PutUint16(buf[8:10], protocol.MultiControllerHeaderB)
	binary.LittleEndian.PutUint16(buf[10:12], uint16(controllerNumber))
	binary.LittleEndian.PutUint16(buf[12:14], uint16(activeGamepadMask))
	binary.LittleEndian.PutUint16(buf[14:16], protocol.MultiControllerMidB)
	binary.LittleEndian.PutUint16(buf[16:18], uint16(buttonFlags&0xFFFF))
	buf[18] = leftTrigger
	buf[19] = rightTrigger
	binary.LittleEndian.PutUint16(buf[20:22], uint16(leftStickX))
	binary.LittleEndian.PutUint16(buf[22:24], uint16(leftStickY))
	binary.LittleEndian.PutUint16(buf[24:26], uint16(rightStickX))
	binary.LittleEndian.PutUint16(buf[26:28], uint16(rightStickY))
	binary.LittleEndian.PutUint16(buf[28:30], protocol.MultiControllerTailA)

	if s.isSunshine {
		// Extended packet with buttonFlags2
		buf = append(buf, 0, 0, 0, 0)
		binary.LittleEndian.PutUint16(buf[30:32], uint16(buttonFlags>>16))
		binary.LittleEndian.PutUint16(buf[32:34], protocol.MultiControllerTailB)
		binary.BigEndian.PutUint32(buf[0:4], 30) // Update size
	}

	return buf
}

func (s *Stream) buildControllerArrivalPacket(controllerNumber, controllerType uint8, capabilities uint16, supportedButtons uint32) []byte {
	buf := make([]byte, 16)
	binary.BigEndian.PutUint32(buf[0:4], 12)
	binary.LittleEndian.PutUint32(buf[4:8], protocol.SSControllerArrivalMagic)
	buf[8] = controllerNumber
	buf[9] = controllerType
	binary.LittleEndian.PutUint16(buf[10:12], capabilities)
	binary.LittleEndian.PutUint32(buf[12:16], supportedButtons)
	return buf
}

func (s *Stream) buildTouchPacket(eventType uint8, pointerID uint32, x, y, pressure, contactMajor, contactMinor float32, rotation uint16) []byte {
	buf := make([]byte, 40)
	binary.BigEndian.PutUint32(buf[0:4], 36)
	binary.LittleEndian.PutUint32(buf[4:8], protocol.SSTouchMagic)
	buf[8] = eventType
	// 3 bytes zero
	binary.LittleEndian.PutUint32(buf[12:16], pointerID)
	copy(buf[16:20], protocol.FloatToNetfloat(x)[:])
	copy(buf[20:24], protocol.FloatToNetfloat(y)[:])
	copy(buf[24:28], protocol.FloatToNetfloat(pressure)[:])
	copy(buf[28:32], protocol.FloatToNetfloat(contactMajor)[:])
	copy(buf[32:36], protocol.FloatToNetfloat(contactMinor)[:])
	binary.LittleEndian.PutUint16(buf[36:38], rotation)
	return buf
}

func (s *Stream) buildPenPacket(eventType, toolType, penButtons uint8, x, y, pressure, contactMajor, contactMinor float32, rotation uint16, tilt uint8) []byte {
	buf := make([]byte, 44)
	binary.BigEndian.PutUint32(buf[0:4], 40)
	binary.LittleEndian.PutUint32(buf[4:8], protocol.SSPenMagic)
	buf[8] = eventType
	buf[9] = toolType
	buf[10] = penButtons
	// 1 byte zero
	copy(buf[12:16], protocol.FloatToNetfloat(x)[:])
	copy(buf[16:20], protocol.FloatToNetfloat(y)[:])
	copy(buf[20:24], protocol.FloatToNetfloat(pressure)[:])
	binary.LittleEndian.PutUint16(buf[24:26], rotation)
	buf[26] = tilt
	// 1 byte zero
	copy(buf[28:32], protocol.FloatToNetfloat(contactMajor)[:])
	copy(buf[32:36], protocol.FloatToNetfloat(contactMinor)[:])
	return buf
}

func (s *Stream) buildControllerMotionPacket(controllerNumber, motionType uint8, x, y, z float32) []byte {
	buf := make([]byte, 24)
	binary.BigEndian.PutUint32(buf[0:4], 20)
	binary.LittleEndian.PutUint32(buf[4:8], protocol.SSControllerMotionMagic)
	buf[8] = controllerNumber
	buf[9] = motionType
	// 2 bytes zero
	copy(buf[12:16], protocol.FloatToNetfloat(x)[:])
	copy(buf[16:20], protocol.FloatToNetfloat(y)[:])
	copy(buf[20:24], protocol.FloatToNetfloat(z)[:])
	return buf
}

func (s *Stream) buildControllerBatteryPacket(controllerNumber, batteryState, percentage uint8) []byte {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint32(buf[0:4], 8)
	binary.LittleEndian.PutUint32(buf[4:8], protocol.SSControllerBatteryMagic)
	buf[8] = controllerNumber
	buf[9] = batteryState
	buf[10] = percentage
	// 1 byte zero
	return buf
}

func (s *Stream) buildUTF8TextPacket(text string) []byte {
	textBytes := []byte(text)
	buf := make([]byte, 8+len(textBytes))
	binary.BigEndian.PutUint32(buf[0:4], uint32(4+len(textBytes)))
	binary.LittleEndian.PutUint32(buf[4:8], protocol.UTF8TextEventMagic)
	copy(buf[8:], textBytes)
	return buf
}

// Utility functions

func appVersionAtLeast(v [4]int, major, minor, build int) bool {
	if v[0] > major {
		return true
	}
	if v[0] < major {
		return false
	}
	if v[1] > minor {
		return true
	}
	if v[1] < minor {
		return false
	}
	return v[2] >= build
}

func clampFloat(val, min, max float32) float32 {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// Touch event types (exported)
const (
	TouchEventHover  = 0
	TouchEventDown   = 1
	TouchEventUp     = 2
	TouchEventMove   = 3
	TouchEventCancel = 4
)

// Modifier constants (exported)
const (
	ModifierShift = 0x01
	ModifierCtrl  = 0x02
	ModifierAlt   = 0x04
	ModifierMeta  = 0x08
)

// Errors
var (
	ErrNotInitialized   = &inputError{"input stream not initialized"}
	ErrUnsupported      = &inputError{"feature not supported"}
	ErrInvalidParameter = &inputError{"invalid parameter"}
)

type inputError struct {
	msg string
}

func (e *inputError) Error() string {
	return e.msg
}
