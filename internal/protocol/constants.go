// Package protocol implements the Moonlight streaming protocol
package protocol

// Stream configuration constants
const (
	StreamCfgLocal  = 0
	StreamCfgRemote = 1
	StreamCfgAuto   = 2
)

// Color space constants
const (
	ColorspaceRec601  = 0
	ColorspaceRec709  = 1
	ColorspaceRec2020 = 2
)

// Color range constants
const (
	ColorRangeLimited = 0
	ColorRangeFull    = 1
)

// Encryption flags
const (
	EncFlagNone  = 0x00000000
	EncFlagAudio = 0x00000001
	EncFlagVideo = 0x00000002
	EncFlagAll   = 0xFFFFFFFF
)

// Video format constants (codec support flags)
const (
	VideoFormatH264           = 0x0001 // H.264 High Profile
	VideoFormatH264High8_444  = 0x0004 // H.264 High 4:4:4 8-bit
	VideoFormatH265           = 0x0100 // HEVC Main Profile
	VideoFormatH265Main10     = 0x0200 // HEVC Main10 Profile
	VideoFormatH265Rext8_444  = 0x0400 // HEVC RExt 4:4:4 8-bit
	VideoFormatH265Rext10_444 = 0x0800 // HEVC RExt 4:4:4 10-bit
	VideoFormatAV1Main8       = 0x1000 // AV1 Main 8-bit
	VideoFormatAV1Main10      = 0x2000 // AV1 Main 10-bit
	VideoFormatAV1High8_444   = 0x4000 // AV1 High 4:4:4 8-bit
	VideoFormatAV1High10_444  = 0x8000 // AV1 High 4:4:4 10-bit

	// Codec masks
	VideoFormatMaskH264   = 0x000F
	VideoFormatMaskH265   = 0x0F00
	VideoFormatMaskAV1    = 0xF000
	VideoFormatMask10Bit  = 0xAA00
	VideoFormatMaskYUV444 = 0xCC04
)

// Server codec mode support flags
const (
	SCM_H264           = 0x00000001
	SCM_HEVC           = 0x00000100
	SCM_HEVC_Main10    = 0x00000200
	SCM_AV1_Main8      = 0x00010000 // Sunshine extension
	SCM_AV1_Main10     = 0x00020000 // Sunshine extension
	SCM_H264_High8_444 = 0x00040000 // Sunshine extension
	SCM_HEVC_Rext8_444 = 0x00080000 // Sunshine extension
	SCM_HEVC_Rext10_444 = 0x00100000 // Sunshine extension
	SCM_AV1_High8_444  = 0x00200000 // Sunshine extension
	SCM_AV1_High10_444 = 0x00400000 // Sunshine extension
)

// Audio configuration helpers
const (
	AudioConfigStereo     = 0x000302CA // 2 channels, mask 0x3
	AudioConfig51Surround = 0x003F06CA // 6 channels, mask 0x3F
	AudioConfig71Surround = 0x063F08CA // 8 channels, mask 0x63F
	AudioMaxChannels      = 8
)

// MakeAudioConfiguration creates an audio configuration value
func MakeAudioConfiguration(channelCount int, channelMask int) int {
	return (channelMask << 16) | (channelCount << 8) | 0xCA
}

// ChannelCountFromConfig extracts channel count from audio config
func ChannelCountFromConfig(config int) int {
	return (config >> 8) & 0xFF
}

// ChannelMaskFromConfig extracts channel mask from audio config
func ChannelMaskFromConfig(config int) int {
	return (config >> 16) & 0xFFFF
}

// Connection stages
const (
	StageNone              = 0
	StagePlatformInit      = 1
	StageNameResolution    = 2
	StageAudioStreamInit   = 3
	StageRTSPHandshake     = 4
	StageControlStreamInit = 5
	StageVideoStreamInit   = 6
	StageInputStreamInit   = 7
	StageControlStreamStart = 8
	StageVideoStreamStart  = 9
	StageAudioStreamStart  = 10
	StageInputStreamStart  = 11
	StageMax               = 12
)

// StageName returns a human-readable name for a connection stage
func StageName(stage int) string {
	names := []string{
		"none",
		"platform initialization",
		"name resolution",
		"audio stream initialization",
		"RTSP handshake",
		"control stream initialization",
		"video stream initialization",
		"input stream initialization",
		"control stream start",
		"video stream start",
		"audio stream start",
		"input stream start",
	}
	if stage >= 0 && stage < len(names) {
		return names[stage]
	}
	return "unknown"
}

// Error codes
const (
	ErrGracefulTermination       = 0
	ErrNoVideoTraffic            = -100
	ErrNoVideoFrame              = -101
	ErrUnexpectedEarlyTermination = -102
	ErrProtectedContent          = -103
	ErrFrameConversion           = -104
	ErrUnsupported               = -5501
)

// Decoder return codes
const (
	DrOK      = 0
	DrNeedIDR = -1
)

// Frame types
const (
	FrameTypePFrame = 0x00 // P-frame (references IDR and previous P-frames)
	FrameTypeIDR    = 0x01 // Key frame (IDR)
)

// Buffer types (for H.264/HEVC NAL unit identification)
const (
	BufferTypePicData = 0x00
	BufferTypeSPS     = 0x01
	BufferTypePPS     = 0x02
	BufferTypeVPS     = 0x03
)

// Network ports (relative to base port 47989)
const (
	PortHTTP        = 47989 // Moonlight HTTP API
	PortHTTPS       = 47984 // Moonlight HTTPS
	PortWebUI       = 47990 // Sunshine Web UI
	PortVideo       = 47998 // RTP Video
	PortControl     = 47999 // Control/ENet
	PortAudio       = 48000 // RTP Audio
	PortRTSP        = 48010 // RTSP (21553 is also common)
)

// RTP constants
const (
	RTPHeaderSize    = 12
	RTPMaxHeaderSize = 16 // With extension
	RTPFlagExtension = 0x10
)

// FEC constants
const (
	AudioFECDataShards   = 4
	AudioFECParityShards = 2
	AudioFECTotalShards  = 6
)

// Feature flags (Sunshine extensions)
const (
	FeatureFlagFECStatus    = 0x01
	FeatureFlagSessionIDV1  = 0x02
	FeatureFlagPenTouch     = 0x01
	FeatureFlagControllerTouch = 0x02
)

// ENet control channels
const (
	CtrlChannelGeneric     = 0x00
	CtrlChannelUrgent      = 0x01
	CtrlChannelKeyboard    = 0x02
	CtrlChannelMouse       = 0x03
	CtrlChannelPen         = 0x04 // Sunshine only
	CtrlChannelTouch       = 0x05 // Sunshine only
	CtrlChannelUTF8        = 0x06
	CtrlChannelGamepadBase = 0x10 // 0x10-0x1F for controllers 0-15
	CtrlChannelSensorBase  = 0x20 // 0x20-0x2F for sensors 0-15
	CtrlChannelCount       = 0x30
)
