package moonlight

// Streamer is the interface for a video/audio stream from Sunshine
type Streamer interface {
	// VideoFrames returns a channel for receiving video frame data
	VideoFrames() <-chan []byte

	// AudioSamples returns a channel for receiving audio sample data
	AudioSamples() <-chan []byte

	// SendInput sends an input packet to Sunshine
	SendInput(input InputPacket)

	// Close terminates the stream
	Close() error
}

// Verify that both implementations satisfy the interface
var _ Streamer = (*Stream)(nil)
var _ Streamer = (*LimelightStream)(nil)
