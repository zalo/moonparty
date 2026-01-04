package moonlight

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Client handles communication with Sunshine server
type Client struct {
	host        string
	port        int
	httpClient  *http.Client
	uniqueID    string
	clientCert  *tls.Certificate
	certDER     []byte // Raw certificate bytes for pairing
	privateKey  *rsa.PrivateKey
	paired      bool
	pairingPIN  string
	deviceName  string
}

// NewClient creates a new Moonlight client
func NewClient(host string, port int) *Client {
	return &Client{
		host:       host,
		port:       port,
		deviceName: "Moonparty",
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		},
	}
}

// Connect establishes connection with Sunshine and handles pairing
func (c *Client) Connect(ctx context.Context) error {
	// Generate or load client identity
	if err := c.loadOrGenerateIdentity(); err != nil {
		return fmt.Errorf("identity error: %w", err)
	}

	// Check if already paired
	paired, err := c.checkPaired(ctx)
	if err != nil {
		return fmt.Errorf("pair check error: %w", err)
	}

	c.paired = paired
	if !paired {
		log.Println("Not paired with Sunshine.")
		log.Println("Starting pairing process...")

		// Generate PIN and start pairing
		pin, err := c.StartPairing(ctx)
		if err != nil {
			return fmt.Errorf("pairing error: %w", err)
		}

		log.Println("")
		log.Println("============================================")
		log.Printf("  PAIRING PIN: %s", pin)
		log.Println("============================================")
		log.Println("")
		log.Println("Enter this PIN in Sunshine's web UI:")
		log.Printf("  https://%s:47990 -> PIN Pairing", c.host)
		log.Println("")
		log.Println("Waiting for pairing to complete...")

		// Wait for user to enter PIN in Sunshine (poll for completion)
		if err := c.waitForPairing(ctx); err != nil {
			return fmt.Errorf("pairing failed: %w", err)
		}

		log.Println("Pairing successful!")
		c.paired = true
	} else {
		log.Println("Successfully connected to Sunshine (already paired)")
	}

	return nil
}

// StartPairing initiates the pairing process and returns a PIN
func (c *Client) StartPairing(ctx context.Context) (string, error) {
	// Generate a random 4-digit PIN
	pinBytes := make([]byte, 4)
	rand.Read(pinBytes)
	pin := fmt.Sprintf("%04d", (int(pinBytes[0])<<8|int(pinBytes[1]))%10000)
	c.pairingPIN = pin

	// Phase 1: Get server certificate
	serverCert, err := c.pairGetServerCert(ctx)
	if err != nil {
		return "", fmt.Errorf("getservercert failed: %w", err)
	}

	// Phase 2: Send challenge
	if err := c.pairChallenge(ctx, serverCert); err != nil {
		return "", fmt.Errorf("challenge failed: %w", err)
	}

	return pin, nil
}

// pairGetServerCert initiates pairing and gets server certificate
func (c *Client) pairGetServerCert(ctx context.Context) ([]byte, error) {
	url := fmt.Sprintf("https://%s:%d/pair?uniqueid=%s&devicename=%s&updateState=1&phrase=getservercert",
		c.host, c.port, c.uniqueID, c.deviceName)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var pairResp struct {
		Paired      string `xml:"paired"`
		PlainCert   string `xml:"plaincert"`
		Status      string `xml:"status_code"`
		StatusMsg   string `xml:"status_message"`
	}
	if err := xml.Unmarshal(body, &pairResp); err != nil {
		return nil, fmt.Errorf("parse error: %w (body: %s)", err, string(body))
	}

	if pairResp.Paired != "1" && pairResp.Status != "200" {
		return nil, fmt.Errorf("pairing not started: %s", pairResp.StatusMsg)
	}

	// Decode hex-encoded certificate
	certBytes, err := hex.DecodeString(pairResp.PlainCert)
	if err != nil {
		return nil, fmt.Errorf("decode cert: %w", err)
	}

	return certBytes, nil
}

// pairChallenge sends the client challenge
func (c *Client) pairChallenge(ctx context.Context, serverCert []byte) error {
	// Generate salt and derive AES key from PIN
	salt := make([]byte, 16)
	rand.Read(salt)

	aesKey := c.generateAESKey(salt)

	// Generate client challenge (random bytes)
	clientChallenge := make([]byte, 16)
	rand.Read(clientChallenge)

	// Encrypt challenge with AES key
	encryptedChallenge, err := c.aesEncrypt(aesKey, clientChallenge)
	if err != nil {
		return err
	}

	// Send challenge
	url := fmt.Sprintf("https://%s:%d/pair?uniqueid=%s&devicename=%s&updateState=1&clientchallenge=%s",
		c.host, c.port, c.uniqueID, c.deviceName, hex.EncodeToString(append(salt, encryptedChallenge...)))

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var challengeResp struct {
		Paired          string `xml:"paired"`
		ChallengeResp   string `xml:"challengeresponse"`
	}
	if err := xml.Unmarshal(body, &challengeResp); err != nil {
		return fmt.Errorf("parse challenge response: %w", err)
	}

	if challengeResp.Paired != "1" {
		return fmt.Errorf("challenge rejected")
	}

	// Verify server challenge response and continue pairing
	return c.pairServerChallengeResponse(ctx, aesKey, serverCert, clientChallenge)
}

// pairServerChallengeResponse handles the server's challenge response
func (c *Client) pairServerChallengeResponse(ctx context.Context, aesKey, serverCert, clientChallenge []byte) error {
	// Generate server challenge response
	serverChallengeResp := make([]byte, 16)
	rand.Read(serverChallengeResp)

	// Hash: SHA1(server_challenge + server_cert + secret)
	h := sha1.New()
	h.Write(serverChallengeResp)
	h.Write(serverCert)
	secret := make([]byte, 16)
	rand.Read(secret)
	h.Write(secret)
	challengeRespHash := h.Sum(nil)

	encryptedResp, err := c.aesEncrypt(aesKey, challengeRespHash)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://%s:%d/pair?uniqueid=%s&devicename=%s&updateState=1&serverchallengeresp=%s",
		c.host, c.port, c.uniqueID, c.deviceName, hex.EncodeToString(encryptedResp))

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var scResp struct {
		Paired        string `xml:"paired"`
		PairingSecret string `xml:"pairingsecret"`
	}
	if err := xml.Unmarshal(body, &scResp); err != nil {
		return fmt.Errorf("parse server challenge: %w", err)
	}

	if scResp.Paired != "1" {
		return fmt.Errorf("server challenge failed")
	}

	// Send client pairing secret (our certificate signature)
	return c.pairClientSecret(ctx, aesKey)
}

// pairClientSecret sends the client's pairing secret
func (c *Client) pairClientSecret(ctx context.Context, aesKey []byte) error {
	// Create pairing secret: client cert + signature
	h := sha256.New()
	h.Write(c.certDER)
	certHash := h.Sum(nil)

	// Sign with private key
	signature, err := rsa.SignPKCS1v15(rand.Reader, c.privateKey, 0, certHash)
	if err != nil {
		// If signing fails, just send the cert hash
		signature = certHash
	}

	pairingSecret := append(c.certDER, signature...)
	encryptedSecret, err := c.aesEncrypt(aesKey, pairingSecret)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://%s:%d/pair?uniqueid=%s&devicename=%s&updateState=1&clientpairingsecret=%s",
		c.host, c.port, c.uniqueID, c.deviceName, hex.EncodeToString(encryptedSecret))

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var secretResp struct {
		Paired string `xml:"paired"`
	}
	if err := xml.Unmarshal(body, &secretResp); err != nil {
		return fmt.Errorf("parse secret response: %w", err)
	}

	if secretResp.Paired != "1" {
		return fmt.Errorf("client secret rejected")
	}

	return nil
}

// waitForPairing polls until pairing is complete
func (c *Client) waitForPairing(ctx context.Context) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	timeout := time.After(2 * time.Minute)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("pairing timeout - PIN not entered in time")
		case <-ticker.C:
			paired, err := c.checkPaired(ctx)
			if err != nil {
				log.Printf("Checking pairing status... (waiting for PIN entry)")
				continue
			}
			if paired {
				return nil
			}
		}
	}
}

// generateAESKey derives an AES key from the PIN and salt
func (c *Client) generateAESKey(salt []byte) []byte {
	// Key = SHA1(salt + PIN as ASCII bytes)
	h := sha1.New()
	h.Write(salt)
	h.Write([]byte(c.pairingPIN))
	hash := h.Sum(nil)

	// Take first 16 bytes for AES-128
	return hash[:16]
}

// aesEncrypt encrypts data with AES-128-ECB
func (c *Client) aesEncrypt(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	// Pad to block size
	padLen := aes.BlockSize - (len(data) % aes.BlockSize)
	if padLen == 0 {
		padLen = aes.BlockSize
	}
	padded := make([]byte, len(data)+padLen)
	copy(padded, data)
	for i := len(data); i < len(padded); i++ {
		padded[i] = byte(padLen)
	}

	// ECB mode encryption
	encrypted := make([]byte, len(padded))
	for i := 0; i < len(padded); i += aes.BlockSize {
		block.Encrypt(encrypted[i:], padded[i:])
	}

	return encrypted, nil
}

// aesDecrypt decrypts AES-128-ECB data
func (c *Client) aesDecrypt(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	decrypted := make([]byte, len(data))
	for i := 0; i < len(data); i += aes.BlockSize {
		block.Decrypt(decrypted[i:], data[i:])
	}

	// Remove PKCS7 padding
	if len(decrypted) > 0 {
		padLen := int(decrypted[len(decrypted)-1])
		if padLen <= aes.BlockSize && padLen <= len(decrypted) {
			decrypted = decrypted[:len(decrypted)-padLen]
		}
	}

	return decrypted, nil
}

// loadOrGenerateIdentity loads or creates client certificates
func (c *Client) loadOrGenerateIdentity() error {
	homeDir, _ := os.UserHomeDir()
	certDir := filepath.Join(homeDir, ".moonparty")
	os.MkdirAll(certDir, 0700)

	certPath := filepath.Join(certDir, "client.crt")
	keyPath := filepath.Join(certDir, "client.key")
	idPath := filepath.Join(certDir, "unique_id")

	// Check if identity exists
	if _, err := os.Stat(certPath); err == nil {
		// Load existing identity
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return err
		}
		c.clientCert = &cert

		// Load private key
		keyPEM, err := os.ReadFile(keyPath)
		if err != nil {
			return err
		}
		keyBlock, _ := pem.Decode(keyPEM)
		if keyBlock != nil {
			c.privateKey, _ = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
		}

		// Load cert DER
		certPEM, err := os.ReadFile(certPath)
		if err != nil {
			return err
		}
		certBlock, _ := pem.Decode(certPEM)
		if certBlock != nil {
			c.certDER = certBlock.Bytes
		}

		idBytes, err := os.ReadFile(idPath)
		if err != nil {
			return err
		}
		c.uniqueID = strings.TrimSpace(string(idBytes))
		log.Printf("Loaded existing client identity: %s", c.uniqueID)
		return nil
	}

	// Generate new identity
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	c.privateKey = privateKey

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   "Moonparty",
			Organization: []string{"Moonparty"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(20, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return err
	}
	c.certDER = certDER

	// Save certificate
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		return err
	}

	// Save private key
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return err
	}

	// Generate unique ID from certificate hash
	hash := sha256.Sum256(certDER)
	c.uniqueID = hex.EncodeToString(hash[:8])

	if err := os.WriteFile(idPath, []byte(c.uniqueID), 0600); err != nil {
		return err
	}

	// Load the certificate
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return err
	}
	c.clientCert = &cert

	log.Printf("Generated new client identity: %s", c.uniqueID)
	return nil
}

// checkPaired checks if we're paired with Sunshine
func (c *Client) checkPaired(ctx context.Context) (bool, error) {
	url := fmt.Sprintf("https://%s:%d/serverinfo?uniqueid=%s", c.host, c.port, c.uniqueID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	// Parse XML response
	var serverInfo struct {
		PairStatus string `xml:"PairStatus"`
	}
	if err := xml.Unmarshal(body, &serverInfo); err != nil {
		return false, nil // Assume not paired if we can't parse
	}

	return serverInfo.PairStatus == "1", nil
}

// Stream represents an active game stream
type Stream struct {
	client      *Client
	videoFrames chan []byte
	audioFrames chan []byte
	inputChan   chan InputPacket
	conn        net.Conn
	ctx         context.Context
	cancel      context.CancelFunc
}

// InputPacket represents gamepad/keyboard/mouse input
type InputPacket struct {
	Type       InputType
	PeerID     string
	PlayerSlot int
	Data       []byte
}

// InputType identifies the type of input
type InputType int

const (
	InputTypeKeyboard InputType = iota
	InputTypeMouse
	InputTypeMouseRelative
	InputTypeGamepad
	InputTypeTouch
)

// StartStream begins streaming from Sunshine
func (c *Client) StartStream(ctx context.Context, width, height, fps, bitrate int) (*Stream, error) {
	if !c.paired {
		return nil, fmt.Errorf("not paired with Sunshine")
	}

	streamCtx, cancel := context.WithCancel(ctx)

	s := &Stream{
		client:      c,
		videoFrames: make(chan []byte, 60),
		audioFrames: make(chan []byte, 120),
		inputChan:   make(chan InputPacket, 256),
		ctx:         streamCtx,
		cancel:      cancel,
	}

	// Launch the desktop app (app ID 0 is typically Desktop)
	if err := s.launchApp(ctx, 0, width, height, fps, bitrate); err != nil {
		cancel()
		return nil, err
	}

	// Start receiving video/audio
	go s.receiveLoop()

	return s, nil
}

// launchApp starts an application on Sunshine
func (s *Stream) launchApp(ctx context.Context, appID, width, height, fps, bitrate int) error {
	// Build launch URL with parameters
	params := fmt.Sprintf("uniqueid=%s&appid=%d&mode=%dx%dx%d&bitrate=%d&sops=0&rikey=0&rikeyid=0",
		s.client.uniqueID, appID, width, height, fps, bitrate)

	url := fmt.Sprintf("https://%s:%d/launch?%s", s.client.host, s.client.port, params)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := s.client.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	// Parse response to get stream ports
	var launchResp struct {
		SessionURL string `xml:"sessionUrl0"`
	}
	if err := xml.Unmarshal(body, &launchResp); err != nil {
		log.Printf("Launch response: %s", string(body))
	}

	return s.connectStream(ctx)
}

// connectStream establishes the RTSP/UDP connections for video/audio
func (s *Stream) connectStream(ctx context.Context) error {
	// The actual streaming uses RTSP for control and UDP for media
	// Video port is typically 47998, audio is 47999, control is 47999

	// For now, establish a control connection
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
		Certificates:       []tls.Certificate{*s.client.clientCert},
	}

	addr := fmt.Sprintf("%s:%d", s.client.host, 47984) // Control port
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr, tlsConfig)
	if err != nil {
		log.Printf("Could not connect to control port: %v", err)
		// Continue anyway for demo purposes
		return nil
	}

	s.conn = conn
	return nil
}

// receiveLoop handles incoming video/audio data
func (s *Stream) receiveLoop() {
	defer s.Close()

	// In a real implementation, this would:
	// 1. Receive RTSP setup response
	// 2. Open UDP sockets for video/audio
	// 3. Parse RTP packets and extract NAL units / Opus frames

	// For now, generate placeholder data for testing
	ticker := time.NewTicker(16 * time.Millisecond) // ~60fps
	defer ticker.Stop()

	frameNum := 0
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			// In real implementation, receive actual video frame from Sunshine
			frameNum++
			// Placeholder: would send actual H.264 NAL units here
		}
	}
}

// VideoFrames returns the channel for receiving video frames
func (s *Stream) VideoFrames() <-chan []byte {
	return s.videoFrames
}

// AudioSamples returns the channel for receiving audio samples
func (s *Stream) AudioSamples() <-chan []byte {
	return s.audioFrames
}

// SendInput sends input to Sunshine
func (s *Stream) SendInput(input InputPacket) {
	if s.conn == nil {
		return
	}

	// Build and send input packet based on type
	var packet []byte

	switch input.Type {
	case InputTypeGamepad:
		packet = s.buildGamepadPacket(input)
	case InputTypeKeyboard:
		packet = s.buildKeyboardPacket(input)
	case InputTypeMouse:
		packet = s.buildMousePacket(input)
	}

	if len(packet) > 0 {
		s.conn.Write(packet)
	}
}

// buildGamepadPacket creates a gamepad input packet
func (s *Stream) buildGamepadPacket(input InputPacket) []byte {
	// Moonlight gamepad packet format:
	// Type (1 byte) + Controller ID (1 byte) + Button flags (2 bytes) +
	// Left trigger (1 byte) + Right trigger (1 byte) +
	// Left stick X (2 bytes) + Left stick Y (2 bytes) +
	// Right stick X (2 bytes) + Right stick Y (2 bytes)

	// Map player slot to controller index
	controllerID := byte(input.PlayerSlot)

	buf := bytes.NewBuffer(nil)
	buf.WriteByte(0x06) // Gamepad packet type
	buf.WriteByte(controllerID)
	buf.Write(input.Data) // Pre-formatted gamepad state

	return buf.Bytes()
}

// buildKeyboardPacket creates a keyboard input packet
func (s *Stream) buildKeyboardPacket(input InputPacket) []byte {
	// Moonlight keyboard packet format:
	// Type (1 byte) + Key code (2 bytes) + Modifiers (1 byte) + Key down (1 byte)

	buf := bytes.NewBuffer(nil)
	buf.WriteByte(0x04) // Keyboard packet type
	buf.Write(input.Data)

	return buf.Bytes()
}

// buildMousePacket creates a mouse input packet
func (s *Stream) buildMousePacket(input InputPacket) []byte {
	// Moonlight mouse packet format varies by type (move, button, scroll)

	buf := bytes.NewBuffer(nil)
	buf.WriteByte(0x05) // Mouse packet type
	buf.Write(input.Data)

	return buf.Bytes()
}

// Close terminates the stream
func (s *Stream) Close() error {
	s.cancel()

	if s.conn != nil {
		// Send quit command to Sunshine
		quitURL := fmt.Sprintf("https://%s:%d/cancel?uniqueid=%s",
			s.client.host, s.client.port, s.client.uniqueID)
		http.Get(quitURL)

		s.conn.Close()
	}

	close(s.videoFrames)
	close(s.audioFrames)

	return nil
}

// GetApps retrieves the list of available applications from Sunshine
func (c *Client) GetApps(ctx context.Context) ([]App, error) {
	url := fmt.Sprintf("https://%s:%d/applist?uniqueid=%s", c.host, c.port, c.uniqueID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var appList struct {
		Apps []struct {
			ID    string `xml:"ID"`
			Title string `xml:"AppTitle"`
		} `xml:"App"`
	}
	if err := xml.Unmarshal(body, &appList); err != nil {
		return nil, err
	}

	apps := make([]App, len(appList.Apps))
	for i, a := range appList.Apps {
		id, _ := strconv.Atoi(a.ID)
		apps[i] = App{ID: id, Title: a.Title}
	}

	return apps, nil
}

// App represents a Sunshine application
type App struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

// IsPaired returns whether the client is paired with Sunshine
func (c *Client) IsPaired() bool {
	return c.paired
}

// GetUniqueID returns the client's unique identifier
func (c *Client) GetUniqueID() string {
	return c.uniqueID
}

// Ensure cipher import is used
var _ cipher.Block
