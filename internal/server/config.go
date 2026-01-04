package server

// Config holds the server configuration
type Config struct {
	// ListenAddr is the address to listen on (e.g., ":8080")
	ListenAddr string `json:"listen_addr"`

	// SunshineHost is the hostname/IP of the Sunshine server
	SunshineHost string `json:"sunshine_host"`

	// SunshinePort is the Moonlight API port of Sunshine (default 47989)
	// Note: 47990 is the web UI port and will be auto-corrected to 47989
	SunshinePort int `json:"sunshine_port"`

	// ConfigPath is the path to the config file
	ConfigPath string `json:"config_path"`

	// ForceNewIdentity forces regeneration of the client identity
	ForceNewIdentity bool `json:"-"`

	// ICEServers is a list of STUN/TURN server URLs
	ICEServers []string `json:"ice_servers"`

	// TURNUsername for TURN authentication (optional)
	TURNUsername string `json:"turn_username,omitempty"`

	// TURNCredential for TURN authentication (optional)
	TURNCredential string `json:"turn_credential,omitempty"`

	// MaxPlayers is the maximum number of active players (default 4)
	MaxPlayers int `json:"max_players"`

	// StreamSettings holds default streaming quality settings
	StreamSettings StreamSettings `json:"stream_settings"`
}

// StreamSettings holds video/audio streaming configuration
type StreamSettings struct {
	// Width of the video stream
	Width int `json:"width"`

	// Height of the video stream
	Height int `json:"height"`

	// FPS is the target frame rate
	FPS int `json:"fps"`

	// Bitrate in kbps
	Bitrate int `json:"bitrate"`

	// Codec preference: "h264", "h265", "av1"
	Codec string `json:"codec"`

	// AudioChannels: 2 for stereo, 6 for 5.1
	AudioChannels int `json:"audio_channels"`
}

// DefaultConfig returns a configuration with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		ListenAddr:   ":8080",
		SunshineHost: "localhost",
		SunshinePort: 47989,
		MaxPlayers:   4,
		ICEServers: []string{
			"stun:stun.l.google.com:19302",
		},
		StreamSettings: StreamSettings{
			Width:         1920,
			Height:        1080,
			FPS:           60,
			Bitrate:       20000,
			Codec:         "h264",
			AudioChannels: 2,
		},
	}
}
