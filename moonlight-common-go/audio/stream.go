// Package audio handles audio stream reception and decoding for the Moonlight streaming protocol.
package audio

import (
	"context"
	"encoding/binary"
	"net"
	"sync"
	"time"

	"github.com/moonparty/moonlight-common-go/crypto"
	"github.com/moonparty/moonlight-common-go/limelight"
	"github.com/moonparty/moonlight-common-go/protocol"
)

const (
	// MaxPacketSize is the maximum audio packet size
	MaxPacketSize = 1400
	// UDPRecvPollTimeout is the receive timeout
	UDPRecvPollTimeout = 100 * time.Millisecond
	// InitialDropMs is the initial audio to drop to catch up
	InitialDropMs = 500
)

// Stream manages audio RTP reception
type Stream struct {
	mu sync.Mutex

	// Configuration
	config      limelight.StreamConfiguration
	callbacks   limelight.AudioCallbacks
	opusConfig  *limelight.OpusConfig
	packetDuration int // In milliseconds

	// Networking
	conn       *net.UDPConn
	remoteAddr *net.UDPAddr
	localAddr  *net.UDPAddr

	// Decryption
	encrypted bool
	aesKey    []byte
	aesIV     []byte
	riKeyID   uint32

	// Threads
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// State
	receivedData   bool
	lastSeq        uint16
	packetsToDrop  int

	// Queue for non-direct submit
	packetQueue chan *audioPacket

	// Stats
	stats limelight.RTPAudioStats
}

type audioPacket struct {
	data []byte
	size int
}

// NewStream creates a new audio stream handler
func NewStream(config limelight.StreamConfiguration, callbacks limelight.AudioCallbacks) *Stream {
	encrypted := config.AudioEncryptionEnabled

	// Calculate RI key ID from IV
	var riKeyID uint32
	if len(config.RemoteInputAesIV) >= 4 {
		riKeyID = binary.BigEndian.Uint32(config.RemoteInputAesIV[:4])
	}

	return &Stream{
		config:     config,
		callbacks:  callbacks,
		encrypted:  encrypted,
		aesKey:     config.RemoteInputAesKey,
		aesIV:      config.RemoteInputAesIV,
		riKeyID:    riKeyID,
	}
}

// Start begins audio stream reception
func (s *Stream) Start(ctx context.Context, remoteAddr, localAddr *net.UDPAddr, audioPort int, opusConfig *limelight.OpusConfig, packetDuration int) error {
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.opusConfig = opusConfig
	s.packetDuration = packetDuration

	// Setup UDP socket
	s.remoteAddr = &net.UDPAddr{
		IP:   remoteAddr.IP,
		Port: audioPort,
	}
	s.localAddr = localAddr

	conn, err := net.ListenUDP("udp", localAddr)
	if err != nil {
		return err
	}
	s.conn = conn

	// Initialize packet queue for non-direct submit
	if s.callbacks.Capabilities()&limelight.CapabilityDirectSubmit == 0 {
		s.packetQueue = make(chan *audioPacket, 30)
	}

	// Calculate packets to drop
	s.packetsToDrop = InitialDropMs / packetDuration

	// Initialize stats
	s.stats.MeasurementStartTime = time.Now()

	// Initialize audio decoder
	if err := s.callbacks.Init(s.config.AudioConfiguration, opusConfig, nil, 0); err != nil {
		conn.Close()
		return err
	}
	s.callbacks.Start()

	// Start threads
	s.wg.Add(2)
	go s.receiveLoop()
	go s.pingLoop()

	// Start decoder thread if not direct submit
	if s.callbacks.Capabilities()&limelight.CapabilityDirectSubmit == 0 {
		s.wg.Add(1)
		go s.decoderLoop()
	}

	return nil
}

// Stop halts audio stream reception
func (s *Stream) Stop() {
	if s.cancel != nil {
		s.cancel()
	}

	s.callbacks.Stop()

	if s.conn != nil {
		s.conn.Close()
	}

	if s.packetQueue != nil {
		close(s.packetQueue)
	}

	s.wg.Wait()

	s.callbacks.Cleanup()
}

// GetStats returns current audio statistics
func (s *Stream) GetStats() limelight.RTPAudioStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// GetPendingFrames returns the number of pending audio frames
func (s *Stream) GetPendingFrames() int {
	if s.packetQueue == nil {
		return 0
	}
	return len(s.packetQueue)
}

// GetPendingDuration returns the pending audio duration in milliseconds
func (s *Stream) GetPendingDuration() int {
	return s.GetPendingFrames() * s.packetDuration
}

// receiveLoop handles incoming RTP packets
func (s *Stream) receiveLoop() {
	defer s.wg.Done()

	buffer := make([]byte, MaxPacketSize)

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		// Set read deadline
		s.conn.SetReadDeadline(time.Now().Add(UDPRecvPollTimeout))

		n, _, err := s.conn.ReadFromUDP(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				if s.receivedData {
					s.packetsToDrop = 0
				}
				continue
			}
			return
		}

		if n < protocol.RTPHeaderSize {
			continue // Runt packet
		}

		if !s.receivedData {
			s.receivedData = true
		}

		s.mu.Lock()
		s.stats.ReceivedPackets++
		s.mu.Unlock()

		// Parse RTP header
		packetType := buffer[1]

		// Drop initial packets to catch up
		if s.packetsToDrop > 0 {
			if packetType == 97 { // Only count actual audio, not FEC
				s.packetsToDrop--
			}
			continue
		}

		// Extract sequence number
		seqNum := binary.BigEndian.Uint16(buffer[2:4])

		// Check for packet loss
		if s.lastSeq != 0 && seqNum != s.lastSeq+1 {
			// Packet loss detected
			s.mu.Lock()
			s.stats.DroppedPackets += uint32(seqNum - s.lastSeq - 1)
			s.mu.Unlock()
		}
		s.lastSeq = seqNum

		// Decrypt if needed
		var audioData []byte
		if s.encrypted {
			decrypted, err := s.decryptPacket(buffer[:n], seqNum)
			if err != nil {
				continue
			}
			audioData = decrypted
		} else {
			audioData = make([]byte, n-protocol.RTPHeaderSize)
			copy(audioData, buffer[protocol.RTPHeaderSize:n])
		}

		// Process audio
		if s.callbacks.Capabilities()&limelight.CapabilityDirectSubmit != 0 {
			s.callbacks.DecodeAndPlaySample(audioData)
		} else {
			select {
			case s.packetQueue <- &audioPacket{data: audioData, size: len(audioData)}:
			default:
				// Queue full, drop oldest
				select {
				case <-s.packetQueue:
				default:
				}
				s.packetQueue <- &audioPacket{data: audioData, size: len(audioData)}
			}
		}
	}
}

// pingLoop sends periodic UDP pings
func (s *Stream) pingLoop() {
	defer s.wg.Done()

	pingData := []byte{0x50, 0x49, 0x4E, 0x47} // "PING"
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.conn.WriteToUDP(pingData, s.remoteAddr)
		}
	}
}

// decoderLoop processes queued audio packets
func (s *Stream) decoderLoop() {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return
		case pkt, ok := <-s.packetQueue:
			if !ok {
				return
			}
			if pkt.size == 0 {
				// Packet loss concealment
				s.callbacks.DecodeAndPlaySample(nil)
			} else {
				s.callbacks.DecodeAndPlaySample(pkt.data)
			}
		}
	}
}

// decryptPacket decrypts an audio packet using AES-CBC
func (s *Stream) decryptPacket(data []byte, seqNum uint16) ([]byte, error) {
	if len(data) <= protocol.RTPHeaderSize {
		return nil, ErrPacketTooSmall
	}

	audioData := data[protocol.RTPHeaderSize:]

	// Build IV: riKeyID + sequence number
	iv := make([]byte, 16)
	ivSeq := s.riKeyID + uint32(seqNum)
	binary.BigEndian.PutUint32(iv[:4], ivSeq)

	// Decrypt using AES-CBC
	decrypted, err := decryptAESCBC(s.aesKey, iv, audioData)
	if err != nil {
		return nil, err
	}

	return decrypted, nil
}

// decryptAESCBC decrypts data using AES-CBC
func decryptAESCBC(key, iv, ciphertext []byte) ([]byte, error) {
	ctx, err := crypto.NewContext(key)
	if err != nil {
		return nil, err
	}
	return ctx.DecryptCBC(ciphertext, iv)
}

// Errors
var (
	ErrPacketTooSmall = &audioError{"packet too small"}
	ErrDecryptFailed  = &audioError{"decryption failed"}
)

type audioError struct {
	msg string
}

func (e *audioError) Error() string {
	return e.msg
}
