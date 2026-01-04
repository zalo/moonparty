// Package control handles the control stream for the Moonlight streaming protocol.
package control

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/zalo/moonparty/moonlight-common-go/protocol"
	"github.com/zalo/moonparty/moonlight-common-go/types"
)

const (
	// ControlStreamTimeoutSec is the connection timeout
	ControlStreamTimeoutSec = 10
	// LossReportIntervalMs is the interval for loss stats reporting
	LossReportIntervalMs = 50
	// PeriodicPingIntervalMs is the interval for periodic pings
	PeriodicPingIntervalMs = 100
)

// Stream manages the control stream connection
type Stream struct {
	mu sync.Mutex

	// Configuration
	config        types.StreamConfiguration
	callbacks     types.ConnectionCallbacks
	appVersion    [4]int
	isSunshine    bool

	// Networking
	conn       net.Conn
	remoteAddr net.Addr

	// Encryption
	encrypted       bool
	aesKey          []byte
	currentSeq      uint32
	encryptionCtx   []byte
	decryptionCtx   []byte

	// State
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	stopping      bool

	// Frame tracking
	lastGoodFrame   uint32
	lastSeenFrame   uint32
	idrRequested    bool

	// Connection status
	intervalGoodCount  int
	intervalTotalCount int
	intervalStartTime  time.Time
	lastLossPercent    int
	lastConnStatus     types.ConnectionStatus

	// HDR state
	hdrEnabled    bool
	hdrMetadata   types.HDRMetadata

	// Packet type tables
	packetTypes map[string]uint16
}

// NewStream creates a new control stream handler
func NewStream(config types.StreamConfiguration, callbacks types.ConnectionCallbacks, appVersion [4]int, isSunshine bool) *Stream {
	s := &Stream{
		config:     config,
		callbacks:  callbacks,
		appVersion: appVersion,
		isSunshine: isSunshine,
		aesKey:     config.RemoteInputAesKey,
	}

	s.encrypted = appVersionAtLeast(appVersion, 7, 1, 431)

	// Select packet types based on version
	if s.encrypted {
		s.packetTypes = protocol.PacketTypesGen7Enc
	}

	return s
}

// Start begins control stream operation
func (s *Stream) Start(ctx context.Context, remoteAddr net.Addr, controlPort int) error {
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.remoteAddr = remoteAddr

	// Connect to control port
	// For Gen5+, this would use ENet over UDP
	// For older versions, TCP
	if s.appVersion[0] >= 5 {
		// ENet connection would go here
		// For this port, we'll use a placeholder
		udpAddr := &net.UDPAddr{
			IP:   remoteAddr.(*net.UDPAddr).IP,
			Port: controlPort,
		}
		conn, err := net.DialUDP("udp", nil, udpAddr)
		if err != nil {
			return err
		}
		s.conn = conn
	} else {
		// TCP connection for older versions
		tcpAddr := &net.TCPAddr{
			IP:   remoteAddr.(*net.TCPAddr).IP,
			Port: 47995,
		}
		conn, err := net.DialTimeout("tcp", tcpAddr.String(), ControlStreamTimeoutSec*time.Second)
		if err != nil {
			return err
		}
		s.conn = conn
	}

	// Send startup messages
	if err := s.sendStartA(); err != nil {
		s.conn.Close()
		return err
	}

	if err := s.sendStartB(); err != nil {
		s.conn.Close()
		return err
	}

	// Start threads
	s.wg.Add(2)
	go s.receiveLoop()
	go s.lossStatsLoop()

	return nil
}

// Stop halts control stream operation
func (s *Stream) Stop() {
	s.mu.Lock()
	s.stopping = true
	s.mu.Unlock()

	if s.cancel != nil {
		s.cancel()
	}

	if s.conn != nil {
		s.conn.Close()
	}

	s.wg.Wait()
}

// RequestIDRFrame sends an IDR frame request
func (s *Stream) RequestIDRFrame() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.idrRequested = true

	if s.packetTypes != nil {
		ptype := s.packetTypes["RequestIDR"]
		return s.sendMessage(ptype, []byte{0, 0}, protocol.CtrlChannelUrgent, protocol.ENetPacketFlagReliable, false)
	}

	// Fallback: send invalidate reference frames
	return s.sendInvalidateRefFrames(0, s.lastSeenFrame)
}

// SendInputPacket sends an input packet on the control stream
func (s *Stream) SendInputPacket(channelID uint8, flags uint32, data []byte, moreData bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.appVersion[0] < 5 {
		return errors.New("input on control stream not supported")
	}

	ptype := s.packetTypes["InputData"]
	return s.sendMessage(ptype, data, channelID, flags, moreData)
}

// UpdateFrameStats updates frame reception statistics
func (s *Stream) UpdateFrameStats(frameIndex uint32, isGood bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastSeenFrame = frameIndex
	s.intervalTotalCount++

	if isGood {
		s.lastGoodFrame = frameIndex
		s.intervalGoodCount++
	}
}

// GetRTTInfo returns estimated round-trip time information
func (s *Stream) GetRTTInfo() (types.RTTInfo, bool) {
	// This would query ENet peer RTT in a real implementation
	return types.RTTInfo{}, false
}

// IsHDREnabled returns whether HDR is currently enabled
func (s *Stream) IsHDREnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hdrEnabled
}

// GetHDRMetadata returns the current HDR metadata
func (s *Stream) GetHDRMetadata() (types.HDRMetadata, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hdrEnabled {
		return types.HDRMetadata{}, false
	}
	return s.hdrMetadata, true
}

// Internal methods

func (s *Stream) sendStartA() error {
	if s.packetTypes == nil {
		return nil
	}
	// Start A is usually just zeros
	return s.sendMessage(s.packetTypes["StartB"]-2, []byte{0, 0}, protocol.CtrlChannelGeneric, protocol.ENetPacketFlagReliable, false)
}

func (s *Stream) sendStartB() error {
	if s.packetTypes == nil {
		return nil
	}
	return s.sendMessage(s.packetTypes["StartB"], []byte{0}, protocol.CtrlChannelGeneric, protocol.ENetPacketFlagReliable, false)
}

func (s *Stream) sendInvalidateRefFrames(start, end uint32) error {
	payload := make([]byte, 24)
	binary.LittleEndian.PutUint64(payload[0:8], uint64(start))
	binary.LittleEndian.PutUint64(payload[8:16], uint64(end))
	// Last 8 bytes are zero

	ptype := s.packetTypes["InvalidateRefFrames"]
	return s.sendMessage(ptype, payload, protocol.CtrlChannelUrgent, protocol.ENetPacketFlagReliable, false)
}

func (s *Stream) sendMessage(ptype uint16, payload []byte, channelID uint8, flags uint32, moreData bool) error {
	if s.conn == nil {
		return errors.New("not connected")
	}

	var packet []byte

	if s.encrypted {
		// Build encrypted packet
		encPacket := s.buildEncryptedPacket(ptype, payload)
		packet = encPacket
	} else if s.appVersion[0] >= 5 {
		// ENet V1 header
		packet = make([]byte, 2+len(payload))
		binary.LittleEndian.PutUint16(packet[0:2], ptype)
		copy(packet[2:], payload)
	} else {
		// TCP header
		packet = make([]byte, 4+len(payload))
		binary.LittleEndian.PutUint16(packet[0:2], ptype)
		binary.LittleEndian.PutUint16(packet[2:4], uint16(len(payload)))
		copy(packet[4:], payload)
	}

	_, err := s.conn.Write(packet)
	return err
}

func (s *Stream) buildEncryptedPacket(ptype uint16, payload []byte) []byte {
	// Build V2 header
	innerHeader := make([]byte, 4+len(payload))
	binary.LittleEndian.PutUint16(innerHeader[0:2], ptype)
	binary.LittleEndian.PutUint16(innerHeader[2:4], uint16(len(payload)))
	copy(innerHeader[4:], payload)

	// Encrypt
	s.currentSeq++
	seq := s.currentSeq

	// Build IV
	iv := make([]byte, 12)
	binary.LittleEndian.PutUint32(iv[0:4], seq)
	iv[10] = 'C' // Client originated
	iv[11] = 'C' // Control stream

	// Encrypt using AES-GCM (placeholder - real impl needed)
	ciphertext := innerHeader // Would be encrypted
	tag := make([]byte, 16)   // Would be GCM tag

	// Build outer encrypted header
	outerLen := 4 + 16 + len(ciphertext) // seq + tag + ciphertext
	packet := make([]byte, 4+outerLen)
	binary.LittleEndian.PutUint16(packet[0:2], 0x0001) // Encrypted header type
	binary.LittleEndian.PutUint16(packet[2:4], uint16(outerLen))
	binary.LittleEndian.PutUint32(packet[4:8], seq)
	copy(packet[8:24], tag)
	copy(packet[24:], ciphertext)

	return packet
}

func (s *Stream) receiveLoop() {
	defer s.wg.Done()

	buffer := make([]byte, 2048)

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		// Set read deadline
		if tcpConn, ok := s.conn.(*net.TCPConn); ok {
			tcpConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		}

		n, err := s.conn.Read(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			// Connection error
			s.callbacks.ConnectionTerminated(-1)
			return
		}

		if n < 2 {
			continue
		}

		// Process received message
		s.processMessage(buffer[:n])
	}
}

func (s *Stream) processMessage(data []byte) {
	if len(data) < 2 {
		return
	}

	var ptype uint16
	var payload []byte

	if s.encrypted {
		// Check for encrypted packet
		headerType := binary.LittleEndian.Uint16(data[0:2])
		if headerType == 0x0001 {
			// Decrypt and process
			decrypted, err := s.decryptMessage(data)
			if err != nil {
				return
			}
			if len(decrypted) < 2 {
				return
			}
			ptype = binary.LittleEndian.Uint16(decrypted[0:2])
			if len(decrypted) >= 4 {
				payloadLen := binary.LittleEndian.Uint16(decrypted[2:4])
				if len(decrypted) >= 4+int(payloadLen) {
					payload = decrypted[4 : 4+payloadLen]
				}
			}
		} else {
			return // Expected encrypted but got plaintext
		}
	} else {
		ptype = binary.LittleEndian.Uint16(data[0:2])
		payload = data[2:]
	}

	// Handle specific packet types
	s.handlePacket(ptype, payload)
}

func (s *Stream) decryptMessage(data []byte) ([]byte, error) {
	if len(data) < 8 {
		return nil, errors.New("packet too small")
	}

	// Parse encrypted header
	// headerType := binary.LittleEndian.Uint16(data[0:2])
	length := binary.LittleEndian.Uint16(data[2:4])
	seq := binary.LittleEndian.Uint32(data[4:8])

	if len(data) < 4+int(length) {
		return nil, errors.New("incomplete packet")
	}

	// Build IV
	iv := make([]byte, 12)
	binary.LittleEndian.PutUint32(iv[0:4], seq)
	iv[10] = 'H' // Host originated
	iv[11] = 'C' // Control stream

	// Tag is after header
	// tag := data[8:24]

	// Ciphertext is after tag
	ciphertext := data[24 : 4+int(length)]

	// Decrypt using AES-GCM (placeholder - real impl needed)
	plaintext := ciphertext // Would be decrypted

	return plaintext, nil
}

func (s *Stream) handlePacket(ptype uint16, payload []byte) {
	// Handle HDR info
	if s.packetTypes != nil && ptype == s.packetTypes["HDRMode"] && len(payload) >= 1 {
		s.mu.Lock()
		s.hdrEnabled = payload[0] != 0

		// Parse HDR metadata if from Sunshine
		if s.isSunshine && len(payload) >= 21 {
			// Parse display primaries, white point, luminance values
			offset := 1
			for i := 0; i < 3; i++ {
				s.hdrMetadata.DisplayPrimaries[i].X = binary.LittleEndian.Uint16(payload[offset:])
				offset += 2
				s.hdrMetadata.DisplayPrimaries[i].Y = binary.LittleEndian.Uint16(payload[offset:])
				offset += 2
			}
			s.hdrMetadata.WhitePoint.X = binary.LittleEndian.Uint16(payload[offset:])
			offset += 2
			s.hdrMetadata.WhitePoint.Y = binary.LittleEndian.Uint16(payload[offset:])
			offset += 2
			s.hdrMetadata.MaxDisplayLuminance = binary.LittleEndian.Uint16(payload[offset:])
			offset += 2
			s.hdrMetadata.MinDisplayLuminance = binary.LittleEndian.Uint16(payload[offset:])
		}
		s.mu.Unlock()

		s.callbacks.SetHDRMode(s.hdrEnabled)
	}

	// Handle rumble
	if s.packetTypes != nil && ptype == s.packetTypes["RumbleData"] && len(payload) >= 10 {
		controllerNum := binary.LittleEndian.Uint16(payload[4:6])
		lowFreq := binary.LittleEndian.Uint16(payload[6:8])
		highFreq := binary.LittleEndian.Uint16(payload[8:10])
		s.callbacks.Rumble(controllerNum, lowFreq, highFreq)
	}

	// Handle rumble triggers
	if s.packetTypes != nil && ptype == s.packetTypes["RumbleTriggers"] && len(payload) >= 6 {
		controllerNum := binary.LittleEndian.Uint16(payload[0:2])
		leftTrigger := binary.LittleEndian.Uint16(payload[2:4])
		rightTrigger := binary.LittleEndian.Uint16(payload[4:6])
		s.callbacks.RumbleTriggers(controllerNum, leftTrigger, rightTrigger)
	}

	// Handle termination
	if s.packetTypes != nil && ptype == s.packetTypes["Termination"] {
		var errorCode int
		if len(payload) >= 4 {
			errorCode = int(binary.BigEndian.Uint32(payload[0:4]))
		} else if len(payload) >= 2 {
			errorCode = int(binary.LittleEndian.Uint16(payload[0:2]))
		}
		s.callbacks.ConnectionTerminated(errorCode)
	}
}

func (s *Stream) lossStatsLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(PeriodicPingIntervalMs * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.sendPeriodicPing()
			s.checkConnectionStatus()
		}
	}
}

func (s *Stream) sendPeriodicPing() {
	payload := make([]byte, 8)
	binary.LittleEndian.PutUint16(payload[0:2], 4) // Length
	// Timestamp would go in remaining bytes

	s.mu.Lock()
	defer s.mu.Unlock()

	s.sendMessage(0x0200, payload, protocol.CtrlChannelGeneric, protocol.ENetPacketFlagReliable, false)
}

func (s *Stream) checkConnectionStatus() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if s.intervalStartTime.IsZero() || now.Sub(s.intervalStartTime) >= 3*time.Second {
		if s.intervalTotalCount > 0 {
			lossPercent := 100 - (s.intervalGoodCount * 100 / s.intervalTotalCount)

			// Check for status change
			if s.lastConnStatus != types.ConnStatusPoor && lossPercent >= 30 {
				s.lastConnStatus = types.ConnStatusPoor
				s.callbacks.ConnectionStatusUpdate(types.ConnStatusPoor)
			} else if lossPercent <= 5 && s.lastConnStatus != types.ConnStatusOkay {
				s.lastConnStatus = types.ConnStatusOkay
				s.callbacks.ConnectionStatusUpdate(types.ConnStatusOkay)
			}

			s.lastLossPercent = lossPercent
		}

		s.intervalStartTime = now
		s.intervalGoodCount = 0
		s.intervalTotalCount = 0
	}
}

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
