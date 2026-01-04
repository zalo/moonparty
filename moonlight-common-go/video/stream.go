// Package video handles video stream reception and decoding for the Moonlight streaming protocol.
package video

import (
	"context"
	"encoding/binary"
	"net"
	"sync"
	"time"

	"github.com/zalo/moonparty/moonlight-common-go/crypto"
	"github.com/zalo/moonparty/moonlight-common-go/fec"
	"github.com/zalo/moonparty/moonlight-common-go/protocol"
	"github.com/zalo/moonparty/moonlight-common-go/types"
)

const (
	// RTPQueueDelay is the delay before considering packets lost
	RTPQueueDelay = 10 * time.Millisecond
	// RTPRecvPacketsBuffered is the desired socket buffer size in packets
	RTPRecvPacketsBuffered = 2048
	// FirstFrameTimeoutSec is the timeout for receiving the first frame
	FirstFrameTimeoutSec = 10
	// UDPRecvPollTimeout is the receive timeout
	UDPRecvPollTimeout = 100 * time.Millisecond
)

// Stream manages video RTP reception
type Stream struct {
	mu sync.Mutex

	// Configuration
	config    types.StreamConfiguration
	callbacks types.DecoderCallbacks

	// Networking
	conn       *net.UDPConn
	remoteAddr *net.UDPAddr
	localAddr  *net.UDPAddr

	// RTP state
	queue       *RTPQueue
	depacketizer *Depacketizer

	// FEC
	fecCodec *fec.ReedSolomon

	// Decryption
	encrypted bool
	aesKey    []byte

	// Threads
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup

	// State
	receivedData      bool
	receivedFullFrame bool
	firstDataTime     time.Time

	// Stats
	stats types.RTPVideoStats
}

// RTPQueue manages the RTP packet reordering queue
type RTPQueue struct {
	mu sync.Mutex

	currentFrameNumber uint32
	packets            map[uint16]*RTPPacket
	lastSeq            uint16

	stats types.RTPVideoStats
}

// RTPPacket represents a received RTP packet
type RTPPacket struct {
	Header     protocol.RTPHeader
	Payload    []byte
	RecvTime   time.Time
	FrameIndex uint32
	Flags      uint8
}

// Depacketizer reassembles video frames from RTP packets
type Depacketizer struct {
	mu sync.Mutex

	currentFrame     *FrameAssembly
	frameQueue       chan *types.DecodeUnit
	packetSize       int

	nextFrameNumber  uint32
	waitingForIDR    bool
}

// FrameAssembly tracks the assembly of a video frame
type FrameAssembly struct {
	FrameNumber     uint32
	FrameType       types.FrameType
	TotalPackets    int
	ReceivedPackets int
	Packets         []*RTPPacket
	DataSize        int
	StartTime       time.Time
}

// NewStream creates a new video stream handler
func NewStream(config types.StreamConfiguration, callbacks types.DecoderCallbacks) *Stream {
	return &Stream{
		config:    config,
		callbacks: callbacks,
		encrypted: (config.EncryptionFlags & types.EncVideo) != 0,
		aesKey:    config.RemoteInputAesKey,
	}
}

// Start begins video stream reception
func (s *Stream) Start(ctx context.Context, remoteAddr, localAddr *net.UDPAddr, videoPort int) error {
	s.ctx, s.cancel = context.WithCancel(ctx)

	// Setup UDP socket
	s.remoteAddr = &net.UDPAddr{
		IP:   remoteAddr.IP,
		Port: videoPort,
	}
	s.localAddr = localAddr

	conn, err := net.ListenUDP("udp", localAddr)
	if err != nil {
		return err
	}
	s.conn = conn

	// Set receive buffer size
	bufferSize := RTPRecvPacketsBuffered * (s.config.PacketSize + protocol.MaxRTPHeaderSize)
	_ = conn.SetReadBuffer(bufferSize)

	// Initialize components
	s.queue = &RTPQueue{
		packets: make(map[uint16]*RTPPacket),
	}
	s.queue.stats.MeasurementStartTime = time.Now()

	s.depacketizer = &Depacketizer{
		packetSize:    s.config.PacketSize,
		frameQueue:    make(chan *types.DecodeUnit, 16),
		waitingForIDR: true,
	}

	// Initialize video decoder
	if err := s.callbacks.Setup(s.config.SupportedVideoFormats, s.config.Width, s.config.Height, s.config.FPS, nil, 0); err != nil {
		conn.Close()
		return err
	}
	s.callbacks.Start()

	// Start threads
	s.wg.Add(2)
	go s.receiveLoop()
	go s.pingLoop()

	// Start decoder thread if not direct submit
	if s.callbacks.Capabilities()&(types.CapabilityDirectSubmit|types.CapabilityPullRenderer) == 0 {
		s.wg.Add(1)
		go s.decoderLoop()
	}

	return nil
}

// Stop halts video stream reception
func (s *Stream) Stop() {
	if s.cancel != nil {
		s.cancel()
	}

	s.callbacks.Stop()

	if s.conn != nil {
		s.conn.Close()
	}

	s.wg.Wait()

	s.callbacks.Cleanup()
}

// GetStats returns current video statistics
func (s *Stream) GetStats() types.RTPVideoStats {
	s.queue.mu.Lock()
	defer s.queue.mu.Unlock()
	return s.queue.stats
}

// receiveLoop handles incoming RTP packets
func (s *Stream) receiveLoop() {
	defer s.wg.Done()

	bufferSize := s.config.PacketSize + protocol.MaxRTPHeaderSize
	if s.encrypted {
		bufferSize += 28 // EncVideoHeader size
	}

	buffer := make([]byte, bufferSize)
	waitingMs := 0

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
				if !s.receivedData {
					waitingMs += int(UDPRecvPollTimeout / time.Millisecond)
					if waitingMs >= FirstFrameTimeoutSec*1000 {
						// Timeout waiting for video
						return
					}
				}
				continue
			}
			return
		}

		if !s.receivedData {
			s.receivedData = true
			s.firstDataTime = time.Now()
		}

		// Check for full frame timeout
		if !s.receivedFullFrame {
			if time.Since(s.firstDataTime) > FirstFrameTimeoutSec*time.Second {
				return
			}
		}

		// Process packet
		packet, err := s.parseRTPPacket(buffer[:n])
		if err != nil {
			continue
		}

		s.queue.mu.Lock()
		s.queue.stats.ReceivedPackets++
		s.queue.mu.Unlock()

		// Add to queue/depacketizer
		s.processPacket(packet)
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

// decoderLoop processes completed frames
func (s *Stream) decoderLoop() {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return
		case unit := <-s.depacketizer.frameQueue:
			if unit == nil {
				return
			}
			s.callbacks.SubmitDecodeUnit(unit)
			s.queue.mu.Lock()
			s.queue.stats.SubmittedFrames++
			s.queue.mu.Unlock()
		}
	}
}

// parseRTPPacket parses an RTP packet from raw bytes
func (s *Stream) parseRTPPacket(data []byte) (*RTPPacket, error) {
	if len(data) < protocol.RTPHeaderSize {
		return nil, ErrPacketTooSmall
	}

	var payload []byte
	var header protocol.RTPHeader

	if s.encrypted {
		// Decrypt packet
		decrypted, err := s.decryptPacket(data)
		if err != nil {
			return nil, err
		}
		data = decrypted
	}

	// Parse RTP header
	header.Header = data[0]
	header.PacketType = data[1]
	header.SequenceNumber = binary.BigEndian.Uint16(data[2:4])
	header.Timestamp = binary.BigEndian.Uint32(data[4:8])
	header.SSRC = binary.BigEndian.Uint32(data[8:12])

	payload = data[protocol.RTPHeaderSize:]

	return &RTPPacket{
		Header:   header,
		Payload:  payload,
		RecvTime: time.Now(),
	}, nil
}

// decryptPacket decrypts an encrypted video packet
func (s *Stream) decryptPacket(data []byte) ([]byte, error) {
	if len(data) < 28+protocol.RTPHeaderSize { // EncVideoHeader + RTP
		return nil, ErrPacketTooSmall
	}

	// Parse encryption header
	iv := data[0:12]
	tag := data[12:28]
	ciphertext := data[28:]

	// Decrypt using AES-GCM
	plaintext, err := decryptAESGCM(s.aesKey, iv, tag, ciphertext)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}

// processPacket handles a received RTP packet
func (s *Stream) processPacket(packet *RTPPacket) {
	s.depacketizer.mu.Lock()
	defer s.depacketizer.mu.Unlock()

	// Parse NV video header from payload
	if len(packet.Payload) < 4 {
		return
	}

	// Extract frame information from NV header
	frameIndex := binary.LittleEndian.Uint32(packet.Payload[0:4])
	packet.FrameIndex = frameIndex

	// Check if this is an IDR frame
	isIDR := (packet.Header.PacketType & 0x80) != 0

	// If waiting for IDR, drop non-IDR frames
	if s.depacketizer.waitingForIDR && !isIDR {
		return
	}

	if isIDR {
		s.depacketizer.waitingForIDR = false
		s.receivedFullFrame = true

		s.queue.mu.Lock()
		s.queue.stats.ReceivedFrames++
		s.queue.mu.Unlock()
	}

	// Assemble frame
	if s.depacketizer.currentFrame == nil || s.depacketizer.currentFrame.FrameNumber != frameIndex {
		// Start new frame
		if s.depacketizer.currentFrame != nil {
			// Submit previous frame if complete
			s.submitFrame(s.depacketizer.currentFrame)
		}

		frameType := types.FrameTypePFrames
		if isIDR {
			frameType = types.FrameTypeIDR
		}

		s.depacketizer.currentFrame = &FrameAssembly{
			FrameNumber: frameIndex,
			FrameType:   frameType,
			Packets:     make([]*RTPPacket, 0),
			StartTime:   time.Now(),
		}
	}

	// Add packet to frame
	s.depacketizer.currentFrame.Packets = append(s.depacketizer.currentFrame.Packets, packet)
	s.depacketizer.currentFrame.ReceivedPackets++
	s.depacketizer.currentFrame.DataSize += len(packet.Payload)

	// Check if frame is complete (simplified - real impl checks packet markers)
	if (packet.Header.PacketType & 0x40) != 0 { // End of frame marker
		s.submitFrame(s.depacketizer.currentFrame)
		s.depacketizer.currentFrame = nil
	}
}

// submitFrame sends a completed frame to the decoder
func (s *Stream) submitFrame(frame *FrameAssembly) {
	if frame == nil || len(frame.Packets) == 0 {
		return
	}

	// Build decode unit
	unit := &types.DecodeUnit{
		FrameNumber:        frame.FrameNumber,
		FrameType:          frame.FrameType,
		EnqueueTimeMs:      uint64(time.Since(frame.StartTime).Milliseconds()),
		PresentationTimeMs: uint64(time.Now().UnixMilli()),
	}

	// Collect buffer descriptors
	for _, pkt := range frame.Packets {
		unit.BufferList = append(unit.BufferList, types.BufferDescriptor{
			Data:   pkt.Payload,
			Offset: 0,
			Length: len(pkt.Payload),
		})
	}

	// Direct submit or queue
	if s.callbacks.Capabilities()&types.CapabilityDirectSubmit != 0 {
		s.callbacks.SubmitDecodeUnit(unit)
		s.queue.mu.Lock()
		s.queue.stats.SubmittedFrames++
		s.queue.mu.Unlock()
	} else {
		select {
		case s.depacketizer.frameQueue <- unit:
		default:
			// Queue full, drop frame
			s.queue.mu.Lock()
			s.queue.stats.DroppedFrames++
			s.queue.mu.Unlock()
		}
	}
}

// WaitForNextFrame waits for and returns the next video frame
func (s *Stream) WaitForNextFrame() (*types.DecodeUnit, bool) {
	select {
	case <-s.ctx.Done():
		return nil, false
	case unit := <-s.depacketizer.frameQueue:
		return unit, unit != nil
	}
}

// RequestIDRFrame requests a keyframe from the server
func (s *Stream) RequestIDRFrame() {
	s.depacketizer.mu.Lock()
	s.depacketizer.waitingForIDR = true
	s.depacketizer.currentFrame = nil
	s.depacketizer.mu.Unlock()

	s.queue.mu.Lock()
	s.queue.stats.RequestedIDRFrames++
	s.queue.mu.Unlock()
}

// GetCurrentFrameNumber returns the current frame being processed
func (s *Stream) GetCurrentFrameNumber() uint32 {
	s.queue.mu.Lock()
	defer s.queue.mu.Unlock()
	return s.queue.currentFrameNumber
}

// Errors
var (
	ErrPacketTooSmall = &videoError{"packet too small"}
	ErrDecryptFailed  = &videoError{"decryption failed"}
)

type videoError struct {
	msg string
}

func (e *videoError) Error() string {
	return e.msg
}

// decryptAESGCM decrypts data using AES-GCM
func decryptAESGCM(key, iv, tag, ciphertext []byte) ([]byte, error) {
	ctx, err := crypto.NewContext(key)
	if err != nil {
		return nil, err
	}
	return ctx.DecryptGCM(ciphertext, iv, tag, nil)
}
