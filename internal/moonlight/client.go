package moonlight

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
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

	"github.com/google/uuid"
)

// Sunshine ports
const (
	PortHTTP  = 47989 // Moonlight protocol HTTP API (pairing, serverinfo, applist)
	PortHTTPS = 47984 // Moonlight protocol HTTPS (secure channel after pairing)
	PortWebUI = 47990 // Sunshine web UI (not used by Moonlight protocol)
)

// Client handles communication with Sunshine server
type Client struct {
	host        string
	port        int // HTTP API port (default 47989)
	httpClient  *http.Client
	uniqueID    string
	clientCert  *tls.Certificate
	certDER     []byte    // Raw certificate bytes for pairing
	certPEM     []byte    // PEM-encoded certificate for pairing request
	privateKey  *rsa.PrivateKey
	paired      bool
	pairingPIN  string
	pairingSalt []byte    // Salt used in current pairing session
	pairingUUID string    // UUID for current pairing session
	deviceName  string
}

// NewClient creates a new Moonlight client
func NewClient(host string, port int) *Client {
	// Use default Moonlight HTTP port if not specified or if web UI port was given
	if port == 0 || port == PortWebUI {
		port = PortHTTP
	}

	return &Client{
		host:       host,
		port:       port,
		deviceName: "Moonparty",
		httpClient: &http.Client{
			Timeout: 90 * time.Second, // Long timeout for pairing (matches moonlight-web-stream)
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
				ResponseHeaderTimeout: 90 * time.Second,
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

	// First, test basic connectivity to Sunshine
	log.Printf("Testing connectivity to Sunshine at %s:%d...", c.host, c.port)
	if err := c.testConnectivity(ctx); err != nil {
		return fmt.Errorf("connectivity test failed: %w", err)
	}
	log.Println("Connectivity OK")

	// Check if already paired
	paired, err := c.checkPaired(ctx)
	if err != nil {
		log.Printf("Pair check returned error (may need pairing): %v", err)
		paired = false
	}

	c.paired = paired
	if !paired {
		log.Println("Not paired with Sunshine.")

		// First, unpair to clear any stuck pairing state
		log.Println("Clearing any stuck pairing state...")
		if err := c.Unpair(ctx); err != nil {
			log.Printf("Unpair returned (this is normal): %v", err)
		}

		// Generate PIN FIRST and display it BEFORE making the pairing request
		// This is critical because Sunshine holds the HTTP response open
		// until the user enters the PIN in the web UI
		pinBytes := make([]byte, 4)
		rand.Read(pinBytes)
		pin := fmt.Sprintf("%04d", (int(pinBytes[0])<<8|int(pinBytes[1]))%10000)
		c.pairingPIN = pin

		log.Println("")
		log.Println("============================================")
		log.Printf("  PAIRING PIN: %s", pin)
		log.Println("============================================")
		log.Println("")
		log.Println("Enter this PIN in Sunshine's web UI NOW:")
		log.Printf("  https://%s:47990 -> PIN Pairing", c.host)
		log.Println("")
		log.Println("The request below will wait until you enter the PIN...")
		log.Println("")

		// Now start pairing - this will block until user enters PIN in Sunshine
		if err := c.StartPairing(ctx); err != nil {
			return fmt.Errorf("pairing error: %w", err)
		}

		log.Println("Pairing successful!")
		c.paired = true
	} else {
		log.Println("Successfully connected to Sunshine (already paired)")
	}

	return nil
}

// testConnectivity checks if we can reach the Sunshine server
func (c *Client) testConnectivity(ctx context.Context) error {
	url := fmt.Sprintf("http://%s:%d/serverinfo", c.host, c.port)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach Sunshine: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Unpair clears the pairing state with Sunshine
func (c *Client) Unpair(ctx context.Context) error {
	url := fmt.Sprintf("http://%s:%d/unpair?uniqueid=%s", c.host, c.port, c.uniqueID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Unpair typically returns 200 OK regardless of previous state
	return nil
}

// StartPairing initiates the pairing process (PIN must be set before calling)
func (c *Client) StartPairing(ctx context.Context) error {
	if c.pairingPIN == "" {
		return fmt.Errorf("PIN must be set before starting pairing")
	}

	// Phase 1: Get server certificate (this blocks until user enters PIN in Sunshine!)
	serverCert, err := c.pairGetServerCert(ctx)
	if err != nil {
		return fmt.Errorf("getservercert failed: %w", err)
	}

	// Phase 2: Send challenge
	if err := c.pairChallenge(ctx, serverCert); err != nil {
		return fmt.Errorf("challenge failed: %w", err)
	}

	return nil
}

// pairGetServerCert initiates pairing and gets server certificate
func (c *Client) pairGetServerCert(ctx context.Context) ([]byte, error) {
	// Generate salt for this pairing session (16 random bytes)
	c.pairingSalt = make([]byte, 16)
	rand.Read(c.pairingSalt)

	// Generate UUID for this pairing session
	c.pairingUUID = uuid.New().String()

	// Hex-encode the salt (uppercase as per Moonlight protocol)
	saltHex := strings.ToUpper(hex.EncodeToString(c.pairingSalt))

	// Hex-encode the client certificate PEM (uppercase)
	certPEMHex := strings.ToUpper(hex.EncodeToString(c.certPEM))

	pairURL := fmt.Sprintf("http://%s:%d/pair?uniqueid=%s&uuid=%s&devicename=%s&updateState=1&phrase=getservercert&salt=%s&clientcert=%s",
		c.host, c.port, c.uniqueID, c.pairingUUID, c.deviceName, saltHex, certPEMHex)

	log.Printf("Sending getservercert request (URL length: %d bytes)...", len(pairURL))

	req, err := http.NewRequestWithContext(ctx, "GET", pairURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	log.Printf("Got response: status=%d", resp.StatusCode)

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

	log.Printf("Parsed response: paired=%s, status=%s, msg=%s, cert_len=%d",
		pairResp.Paired, pairResp.Status, pairResp.StatusMsg, len(pairResp.PlainCert))

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

// pairChallenge sends the client challenge (Phase 2)
func (c *Client) pairChallenge(ctx context.Context, serverCertPEM []byte) error {
	// Use the salt from Phase 1 to derive AES key
	aesKey := c.generateAESKey(c.pairingSalt)

	// Generate client challenge (16 random bytes)
	clientChallenge := make([]byte, 16)
	rand.Read(clientChallenge)

	// Encrypt challenge with AES key
	encryptedChallenge, err := c.aesEncrypt(aesKey, clientChallenge)
	if err != nil {
		return err
	}

	// Send challenge (Phase 2)
	challengeHex := strings.ToUpper(hex.EncodeToString(encryptedChallenge))
	pairURL := fmt.Sprintf("http://%s:%d/pair?uniqueid=%s&uuid=%s&devicename=%s&updateState=1&clientchallenge=%s",
		c.host, c.port, c.uniqueID, c.pairingUUID, c.deviceName, challengeHex)

	log.Printf("Sending clientchallenge (Phase 2)...")

	req, err := http.NewRequestWithContext(ctx, "GET", pairURL, nil)
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
		Paired        string `xml:"paired"`
		ChallengeResp string `xml:"challengeresponse"`
	}
	if err := xml.Unmarshal(body, &challengeResp); err != nil {
		return fmt.Errorf("parse challenge response: %w (body: %s)", err, string(body))
	}

	log.Printf("Phase 2 response: paired=%s, challengeresponse_len=%d", challengeResp.Paired, len(challengeResp.ChallengeResp))

	if challengeResp.Paired != "1" {
		return fmt.Errorf("challenge rejected")
	}

	// Decrypt server's response to get: hash (32 bytes) + server_challenge (16 bytes)
	encryptedResponse, err := hex.DecodeString(challengeResp.ChallengeResp)
	if err != nil {
		return fmt.Errorf("decode challenge response: %w", err)
	}

	decryptedResponse, err := c.aesDecrypt(aesKey, encryptedResponse)
	if err != nil {
		return fmt.Errorf("decrypt challenge response: %w", err)
	}

	// Response format: hash (SHA256 = 32 bytes) + server_challenge (16 bytes)
	if len(decryptedResponse) < 48 {
		return fmt.Errorf("challenge response too short: %d", len(decryptedResponse))
	}

	serverResponseHash := decryptedResponse[:32]
	serverChallenge := decryptedResponse[32:48]

	log.Printf("Decrypted Phase 2: hash_len=%d, server_challenge_len=%d", len(serverResponseHash), len(serverChallenge))

	// Continue to Phase 3
	return c.pairServerChallengeResponse(ctx, aesKey, serverCertPEM, clientChallenge, serverChallenge, serverResponseHash)
}

// pairServerChallengeResponse sends our response to server's challenge (Phase 3)
func (c *Client) pairServerChallengeResponse(ctx context.Context, aesKey, serverCertPEM, clientChallenge, serverChallenge, serverResponseHash []byte) error {
	// Generate client secret (16 random bytes) - we'll need this for Phase 4
	clientSecret := make([]byte, 16)
	rand.Read(clientSecret)

	// Get client certificate signature (from the cert itself)
	cert, err := x509.ParseCertificate(c.certDER)
	if err != nil {
		return fmt.Errorf("parse client cert: %w", err)
	}
	clientCertSignature := cert.Signature

	// Compute challenge response hash: SHA256(server_challenge + client_cert_signature + client_secret)
	h := sha256.New()
	h.Write(serverChallenge)
	h.Write(clientCertSignature)
	h.Write(clientSecret)
	challengeResponseHash := h.Sum(nil)

	// Encrypt the hash
	encryptedHash, err := c.aesEncrypt(aesKey, challengeResponseHash)
	if err != nil {
		return err
	}

	// Send Phase 3 request
	hashHex := strings.ToUpper(hex.EncodeToString(encryptedHash))
	pairURL := fmt.Sprintf("http://%s:%d/pair?uniqueid=%s&uuid=%s&devicename=%s&updateState=1&serverchallengeresp=%s",
		c.host, c.port, c.uniqueID, c.pairingUUID, c.deviceName, hashHex)

	log.Printf("Sending serverchallengeresp (Phase 3)...")

	req, err := http.NewRequestWithContext(ctx, "GET", pairURL, nil)
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
		return fmt.Errorf("parse server challenge response: %w (body: %s)", err, string(body))
	}

	log.Printf("Phase 3 response: paired=%s, pairingsecret_len=%d", scResp.Paired, len(scResp.PairingSecret))

	if scResp.Paired != "1" {
		return fmt.Errorf("server challenge response failed")
	}

	// Decode server pairing secret: server_secret (16 bytes) + signature
	serverPairingSecret, err := hex.DecodeString(scResp.PairingSecret)
	if err != nil {
		return fmt.Errorf("decode server pairing secret: %w", err)
	}

	if len(serverPairingSecret) < 16 {
		return fmt.Errorf("server pairing secret too short")
	}

	// Verify server signature (optional but recommended)
	// For now, continue to Phase 4

	// Send client pairing secret (Phase 4)
	return c.pairClientSecret(ctx, aesKey, clientSecret)
}

// pairClientSecret sends the client's pairing secret (Phase 4)
func (c *Client) pairClientSecret(ctx context.Context, aesKey, clientSecret []byte) error {
	// Sign the client secret with our private key using SHA256
	h := sha256.New()
	h.Write(clientSecret)
	secretHash := h.Sum(nil)

	signature, err := rsa.SignPKCS1v15(rand.Reader, c.privateKey, crypto.SHA256, secretHash)
	if err != nil {
		return fmt.Errorf("sign client secret: %w", err)
	}

	// Client pairing secret = client_secret (16 bytes) + signature
	pairingSecret := append(clientSecret, signature...)

	// Send unencrypted (Sunshine expects raw hex, not AES encrypted)
	secretHex := strings.ToUpper(hex.EncodeToString(pairingSecret))
	pairURL := fmt.Sprintf("http://%s:%d/pair?uniqueid=%s&uuid=%s&devicename=%s&updateState=1&clientpairingsecret=%s",
		c.host, c.port, c.uniqueID, c.pairingUUID, c.deviceName, secretHex)

	req, err := http.NewRequestWithContext(ctx, "GET", pairURL, nil)
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
		return fmt.Errorf("parse secret response: %w (body: %s)", err, string(body))
	}

	log.Printf("Phase 4 response: paired=%s", secretResp.Paired)

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
	// Key = SHA256(salt + PIN as ASCII bytes)[:16]
	// Note: Sunshine (server version 7+) uses SHA256, older servers use SHA1
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(c.pairingPIN))
	hash := h.Sum(nil)

	// Take first 16 bytes for AES-128
	return hash[:16]
}

// aesEncrypt encrypts data with AES-128-ECB (no padding, data must be block-aligned)
func (c *Client) aesEncrypt(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	// Data must be a multiple of block size (no padding per Moonlight protocol)
	if len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("data length %d is not a multiple of block size %d", len(data), aes.BlockSize)
	}

	// ECB mode encryption (no padding)
	encrypted := make([]byte, len(data))
	for i := 0; i < len(data); i += aes.BlockSize {
		block.Encrypt(encrypted[i:], data[i:])
	}

	return encrypted, nil
}

// aesDecrypt decrypts AES-128-ECB data (no padding)
func (c *Client) aesDecrypt(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	// ECB mode decryption (no padding removal)
	decrypted := make([]byte, len(data))
	for i := 0; i < len(data); i += aes.BlockSize {
		block.Decrypt(decrypted[i:], data[i:])
	}

	return decrypted, nil
}

// DeleteIdentity removes the stored client identity files
func (c *Client) DeleteIdentity() error {
	homeDir, _ := os.UserHomeDir()
	certDir := filepath.Join(homeDir, ".moonparty")

	certPath := filepath.Join(certDir, "client.crt")
	keyPath := filepath.Join(certDir, "client.key")
	idPath := filepath.Join(certDir, "unique_id")

	os.Remove(certPath)
	os.Remove(keyPath)
	os.Remove(idPath)

	log.Println("Deleted existing client identity")
	return nil
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

		// Load cert PEM and DER
		certPEMBytes, err := os.ReadFile(certPath)
		if err != nil {
			return err
		}
		c.certPEM = certPEMBytes
		certBlock, _ := pem.Decode(certPEMBytes)
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
	certPEMBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	c.certPEM = certPEMBytes
	if err := os.WriteFile(certPath, certPEMBytes, 0600); err != nil {
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
	url := fmt.Sprintf("http://%s:%d/serverinfo?uniqueid=%s", c.host, c.port, c.uniqueID)

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

// Streaming ports (relative to base port 47989)
const (
	PortVideoOffset   = 9  // 47998
	PortControlOffset = 10 // 47999
	PortAudioOffset   = 11 // 48000
	PortRTSPOffset    = 21 // 48010
)

// Stream represents an active game stream
type Stream struct {
	client      *Client
	videoFrames chan []byte
	audioFrames chan []byte
	inputChan   chan InputPacket
	ctx         context.Context
	cancel      context.CancelFunc
	riKey       []byte  // AES key for stream encryption
	riKeyID     uint32  // Key ID

	// Ports from RTSP SETUP
	videoPort   int
	audioPort   int
	controlPort int
	rtspPort    int

	// UDP connections
	videoConn   *net.UDPConn
	audioConn   *net.UDPConn
	controlConn net.Conn

	// RTSP state
	rtspConn    net.Conn
	rtspSeqNum  int
	sessionID   string
	pingPayload string

	// Stream configuration
	width   int
	height  int
	fps     int
	bitrate int
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
		width:       width,
		height:      height,
		fps:         fps,
		bitrate:     bitrate,
		rtspPort:    c.port + PortRTSPOffset,
		videoPort:   c.port + PortVideoOffset,
		audioPort:   c.port + PortAudioOffset,
		controlPort: c.port + PortControlOffset,
	}

	// Launch the desktop app (app ID 0 is typically Desktop)
	if err := s.launchApp(ctx, 0, width, height, fps, bitrate); err != nil {
		cancel()
		return nil, err
	}

	// Perform RTSP handshake
	if err := s.performRTSPHandshake(ctx); err != nil {
		cancel()
		return nil, fmt.Errorf("RTSP handshake failed: %w", err)
	}

	// Open UDP sockets for video/audio
	if err := s.openMediaSockets(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to open media sockets: %w", err)
	}

	// Start receiving video/audio
	go s.receiveVideoLoop()
	go s.receiveAudioLoop()

	return s, nil
}

// launchApp starts an application on Sunshine
func (s *Stream) launchApp(ctx context.Context, appID, width, height, fps, bitrate int) error {
	// Generate random AES key for stream encryption
	s.riKey = make([]byte, 16)
	rand.Read(s.riKey)
	s.riKeyID = uint32(time.Now().UnixNano() & 0xFFFFFFFF)

	// Build launch URL with parameters (must use HTTPS port 47984)
	riKeyHex := strings.ToUpper(hex.EncodeToString(s.riKey))
	params := fmt.Sprintf("uniqueid=%s&appid=%d&mode=%dx%dx%d&additionalStates=1&sops=0&rikey=%s&rikeyid=%d&localAudioPlayMode=0&gcmap=0&gcpersist=0",
		s.client.uniqueID, appID, width, height, fps, riKeyHex, s.riKeyID)

	// Use HTTPS port 47984 for launch
	url := fmt.Sprintf("https://%s:47984/launch?%s", s.client.host, params)

	log.Printf("Launching app %d at %dx%d@%dfps...", appID, width, height, fps)

	// Create HTTPS client with client certificate
	httpsClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				Certificates:       []tls.Certificate{*s.client.clientCert},
			},
		},
		Timeout: 30 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := httpsClient.Do(req)
	if err != nil {
		return fmt.Errorf("launch request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	// Parse response to get stream ports
	var launchResp struct {
		SessionURL  string `xml:"sessionUrl0"`
		GameSession string `xml:"gamesession"`
		StatusCode  string `xml:"status_code,attr"`
		StatusMsg   string `xml:"status_message,attr"`
	}
	if err := xml.Unmarshal(body, &launchResp); err != nil {
		log.Printf("Launch response parse error: %v, body: %s", err, string(body))
		return fmt.Errorf("parse launch response: %w", err)
	}

	if launchResp.GameSession != "1" {
		return fmt.Errorf("launch failed: %s (status: %s)", launchResp.StatusMsg, launchResp.StatusCode)
	}

	log.Printf("Launch successful, RTSP URL: %s", launchResp.SessionURL)
	return nil
}

// performRTSPHandshake performs the RTSP handshake with Sunshine
// Note: Sunshine closes the TCP connection after each RTSP message,
// so we need to open a new connection for each request.
func (s *Stream) performRTSPHandshake(ctx context.Context) error {
	s.rtspSeqNum = 1
	log.Printf("Starting RTSP handshake with %s:%d", s.client.host, s.rtspPort)

	// 1. OPTIONS
	if err := s.rtspOptions(); err != nil {
		return fmt.Errorf("OPTIONS failed: %w", err)
	}

	// 2. DESCRIBE
	if err := s.rtspDescribe(); err != nil {
		return fmt.Errorf("DESCRIBE failed: %w", err)
	}

	// 3. SETUP audio
	if err := s.rtspSetup("streamid=audio/0/0"); err != nil {
		return fmt.Errorf("SETUP audio failed: %w", err)
	}

	// 4. SETUP video
	if err := s.rtspSetup("streamid=video/0/0"); err != nil {
		return fmt.Errorf("SETUP video failed: %w", err)
	}

	// 5. SETUP control
	if err := s.rtspSetup("streamid=control/13/0"); err != nil {
		return fmt.Errorf("SETUP control failed: %w", err)
	}

	// 6. ANNOUNCE
	if err := s.rtspAnnounce(); err != nil {
		return fmt.Errorf("ANNOUNCE failed: %w", err)
	}

	// 7. PLAY
	if err := s.rtspPlay(); err != nil {
		return fmt.Errorf("PLAY failed: %w", err)
	}

	log.Println("RTSP handshake complete")
	return nil
}

// rtspSendRequest sends an RTSP request and returns the response
// Each request opens a new TCP connection because Sunshine closes after each response
func (s *Stream) rtspSendRequest(method, target, body string) (map[string]string, string, error) {
	// Open a new connection for this request
	addr := fmt.Sprintf("%s:%d", s.client.host, s.rtspPort)
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, "", fmt.Errorf("failed to connect to RTSP: %w", err)
	}
	defer conn.Close()

	// Build request
	var req strings.Builder
	req.WriteString(fmt.Sprintf("%s %s RTSP/1.0\r\n", method, target))
	req.WriteString(fmt.Sprintf("CSeq: %d\r\n", s.rtspSeqNum))
	req.WriteString("X-GS-ClientVersion: 14\r\n")
	req.WriteString(fmt.Sprintf("Host: %s\r\n", s.client.host))
	if s.sessionID != "" {
		req.WriteString(fmt.Sprintf("Session: %s\r\n", s.sessionID))
	}
	if body != "" {
		req.WriteString(fmt.Sprintf("Content-Length: %d\r\n", len(body)))
		req.WriteString("Content-Type: application/sdp\r\n")
	}
	req.WriteString("\r\n")
	if body != "" {
		req.WriteString(body)
	}

	s.rtspSeqNum++

	// Send request
	if _, err := conn.Write([]byte(req.String())); err != nil {
		return nil, "", err
	}

	// Read response
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	buf := make([]byte, 8192)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, "", err
	}

	response := string(buf[:n])

	// Parse response
	headers := make(map[string]string)
	lines := strings.Split(response, "\r\n")

	if len(lines) < 1 || !strings.Contains(lines[0], "200") {
		return nil, "", fmt.Errorf("RTSP error: %s", lines[0])
	}

	var payload string
	inPayload := false
	for _, line := range lines[1:] {
		if line == "" {
			inPayload = true
			continue
		}
		if inPayload {
			payload += line + "\n"
		} else {
			parts := strings.SplitN(line, ": ", 2)
			if len(parts) == 2 {
				headers[parts[0]] = parts[1]
			}
		}
	}

	return headers, payload, nil
}

func (s *Stream) rtspOptions() error {
	target := fmt.Sprintf("rtsp://%s:%d", s.client.host, s.rtspPort)
	_, _, err := s.rtspSendRequest("OPTIONS", target, "")
	return err
}

func (s *Stream) rtspDescribe() error {
	target := fmt.Sprintf("rtsp://%s:%d", s.client.host, s.rtspPort)
	_, _, err := s.rtspSendRequest("DESCRIBE", target, "")
	return err
}

func (s *Stream) rtspSetup(streamID string) error {
	target := fmt.Sprintf("rtsp://%s:%d/%s", s.client.host, s.rtspPort, streamID)
	headers, _, err := s.rtspSendRequest("SETUP", target, "")
	if err != nil {
		return err
	}

	// Parse session ID from response
	if session, ok := headers["Session"]; ok && s.sessionID == "" {
		// Session format: "DEADBEEFCAFE;timeout = 90"
		s.sessionID = strings.Split(session, ";")[0]
		log.Printf("Got session ID: %s", s.sessionID)
	}

	// Parse X-SS-Ping-Payload for Sunshine ping protocol
	if ping, ok := headers["X-SS-Ping-Payload"]; ok {
		s.pingPayload = ping
		log.Printf("Got ping payload from %s: %s", streamID, ping)
	}

	// Parse Transport header for server port
	if transport, ok := headers["Transport"]; ok {
		// Format: "server_port=47998"
		for _, part := range strings.Split(transport, ";") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "server_port=") {
				portStr := strings.TrimPrefix(part, "server_port=")
				port, _ := strconv.Atoi(portStr)
				if strings.Contains(streamID, "video") {
					s.videoPort = port
					log.Printf("Video port: %d", port)
				} else if strings.Contains(streamID, "audio") {
					s.audioPort = port
					log.Printf("Audio port: %d", port)
				} else if strings.Contains(streamID, "control") {
					s.controlPort = port
					log.Printf("Control port: %d", port)
				}
			}
		}
	}

	return nil
}

func (s *Stream) rtspAnnounce() error {
	target := fmt.Sprintf("rtsp://%s:%d", s.client.host, s.rtspPort)

	// Build SDP body with stream parameters
	var sdp strings.Builder
	sdp.WriteString("v=0\r\n")
	sdp.WriteString("o=- 0 0 IN IP4 0.0.0.0\r\n")
	sdp.WriteString("s=NVIDIA Streaming Client\r\n")
	sdp.WriteString(fmt.Sprintf("a=x-nv-video[0].clientViewportWd:%d\r\n", s.width))
	sdp.WriteString(fmt.Sprintf("a=x-nv-video[0].clientViewportHt:%d\r\n", s.height))
	sdp.WriteString(fmt.Sprintf("a=x-nv-video[0].maxFPS:%d\r\n", s.fps))
	sdp.WriteString(fmt.Sprintf("a=x-nv-vqos[0].bw.maximumBitrateKbps:%d\r\n", s.bitrate))
	sdp.WriteString("a=x-nv-video[0].packetSize:1024\r\n")
	sdp.WriteString("a=x-nv-video[0].rateControlMode:4\r\n")
	sdp.WriteString("a=x-nv-video[0].timeoutLengthMs:7000\r\n")
	sdp.WriteString("a=x-nv-video[0].framesWithInvalidRefThreshold:0\r\n")
	sdp.WriteString("a=x-nv-vqos[0].bitStreamFormat:0\r\n") // 0=H264, 1=HEVC
	sdp.WriteString("a=x-nv-video[0].encoderCscMode:0\r\n")
	sdp.WriteString("a=x-nv-video[0].maxNumReferenceFrames:1\r\n")
	sdp.WriteString("a=x-nv-video[0].videoEncoderSlicesPerFrame:1\r\n")
	sdp.WriteString("a=x-nv-audio.surround.numChannels:2\r\n")
	sdp.WriteString("a=x-nv-audio.surround.channelMask:3\r\n")
	sdp.WriteString("a=x-nv-audio.surround.enable:0\r\n")
	sdp.WriteString("a=x-nv-audio.surround.AudioQuality:0\r\n")
	sdp.WriteString("a=x-nv-aqos.packetDuration:5\r\n")
	sdp.WriteString("a=x-nv-general.useReliableUdp:1\r\n")
	sdp.WriteString("a=x-nv-vqos[0].fec.minRequiredFecPackets:0\r\n")
	sdp.WriteString("a=x-nv-general.featureFlags:135\r\n")

	_, _, err := s.rtspSendRequest("ANNOUNCE", target, sdp.String())
	return err
}

func (s *Stream) rtspPlay() error {
	target := fmt.Sprintf("rtsp://%s:%d", s.client.host, s.rtspPort)
	_, _, err := s.rtspSendRequest("PLAY", target, "")
	return err
}

// openMediaSockets opens UDP sockets for video and audio
func (s *Stream) openMediaSockets() error {
	// For localhost, always use 127.0.0.1 (IPv4) since Sunshine binds to IPv4
	host := s.client.host
	if host == "localhost" {
		host = "127.0.0.1"
	}

	// Resolve the server address
	serverIP := net.ParseIP(host)
	if serverIP == nil {
		// Try to resolve hostname - prefer IPv4
		addrs, err := net.LookupIP(host)
		if err != nil || len(addrs) == 0 {
			return fmt.Errorf("failed to resolve host %s: %v", host, err)
		}
		// Prefer IPv4 address
		for _, addr := range addrs {
			if addr.To4() != nil {
				serverIP = addr
				break
			}
		}
		if serverIP == nil {
			serverIP = addrs[0]
		}
	}

	// Use IPv4 explicitly to match Sunshine (which typically uses IPv4)
	networkType := "udp4"
	if serverIP.To4() == nil {
		networkType = "udp6"
	}

	log.Printf("Using %s for media sockets, server IP: %s", networkType, serverIP)

	// Open UDP socket for video
	videoAddr := &net.UDPAddr{IP: net.IPv4zero, Port: 0}
	videoConn, err := net.ListenUDP(networkType, videoAddr)
	if err != nil {
		return fmt.Errorf("failed to open video socket: %w", err)
	}
	s.videoConn = videoConn
	log.Printf("Video UDP socket bound to %s", videoConn.LocalAddr())

	// Open UDP socket for audio
	audioAddr := &net.UDPAddr{IP: net.IPv4zero, Port: 0}
	audioConn, err := net.ListenUDP(networkType, audioAddr)
	if err != nil {
		videoConn.Close()
		return fmt.Errorf("failed to open audio socket: %w", err)
	}
	s.audioConn = audioConn
	log.Printf("Audio UDP socket bound to %s", audioConn.LocalAddr())

	// Server addresses for video and audio
	serverVideoAddr := &net.UDPAddr{IP: serverIP, Port: s.videoPort}
	serverAudioAddr := &net.UDPAddr{IP: serverIP, Port: s.audioPort}

	// Build ping packet: 16-byte payload + 4-byte big-endian sequence number
	// Sunshine expects this format (SS_PING struct from moonlight-common-c)
	var pingPayload [16]byte
	if s.pingPayload != "" && len(s.pingPayload) == 16 {
		copy(pingPayload[:], s.pingPayload)
		log.Printf("Using Sunshine ping payload: %s", s.pingPayload)
	} else {
		// Legacy "PING" format (padded to 16 bytes)
		copy(pingPayload[:], "PING")
		log.Printf("Using legacy PING payload")
	}

	// Moonlight sends ping attempts continuously every 500ms
	// The ping packet is 20 bytes: 16-byte payload + 4-byte sequence number (big-endian)
	log.Printf("Starting ping threads for video %s and audio %s", serverVideoAddr, serverAudioAddr)

	// Start video ping goroutine (runs until stream closes)
	go func() {
		var seqNum uint32 = 0
		pingPacket := make([]byte, 20)
		copy(pingPacket[:16], pingPayload[:])

		for {
			select {
			case <-s.ctx.Done():
				return
			default:
			}

			seqNum++
			// Sequence number in big-endian
			pingPacket[16] = byte(seqNum >> 24)
			pingPacket[17] = byte(seqNum >> 16)
			pingPacket[18] = byte(seqNum >> 8)
			pingPacket[19] = byte(seqNum)

			if _, err := videoConn.WriteToUDP(pingPacket, serverVideoAddr); err != nil {
				log.Printf("Warning: video ping failed: %v", err)
			}

			if seqNum == 1 {
				log.Printf("Sent first video ping (20 bytes)")
			}

			time.Sleep(500 * time.Millisecond)
		}
	}()

	// Start audio ping goroutine (runs until stream closes)
	go func() {
		var seqNum uint32 = 0
		pingPacket := make([]byte, 20)
		copy(pingPacket[:16], pingPayload[:])

		for {
			select {
			case <-s.ctx.Done():
				return
			default:
			}

			seqNum++
			// Sequence number in big-endian
			pingPacket[16] = byte(seqNum >> 24)
			pingPacket[17] = byte(seqNum >> 16)
			pingPacket[18] = byte(seqNum >> 8)
			pingPacket[19] = byte(seqNum)

			if _, err := audioConn.WriteToUDP(pingPacket, serverAudioAddr); err != nil {
				log.Printf("Warning: audio ping failed: %v", err)
			}

			if seqNum == 1 {
				log.Printf("Sent first audio ping (20 bytes)")
			}

			time.Sleep(500 * time.Millisecond)
		}
	}()

	return nil
}

// receiveVideoLoop receives video RTP packets from Sunshine
func (s *Stream) receiveVideoLoop() {
	defer s.videoConn.Close()

	log.Printf("Video receive loop started, waiting for packets...")

	buf := make([]byte, 65536) // Large buffer for video packets
	packetsReceived := 0
	lastLogTime := time.Now()

	for {
		select {
		case <-s.ctx.Done():
			log.Printf("Video receive loop stopped, received %d packets total", packetsReceived)
			return
		default:
		}

		s.videoConn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, addr, err := s.videoConn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Log every 5 seconds while waiting
				if time.Since(lastLogTime) > 5*time.Second {
					log.Printf("Video: still waiting for packets (received %d so far)...", packetsReceived)
					lastLogTime = time.Now()
				}
				continue
			}
			log.Printf("Video receive error: %v", err)
			continue
		}

		if n < 12 {
			continue // Too short for RTP header
		}

		packetsReceived++
		if packetsReceived == 1 {
			log.Printf("Receiving video packets from Sunshine (first from %s, %d bytes)", addr, n)
		} else if packetsReceived%1000 == 0 {
			log.Printf("Video: received %d packets", packetsReceived)
		}

		// Send the complete RTP packet to the channel
		// Pion's TrackLocalStaticRTP expects full RTP packets
		select {
		case s.videoFrames <- append([]byte{}, buf[:n]...):
		default:
			// Channel full, drop packet
		}
	}
}

// receiveAudioLoop receives audio RTP packets from Sunshine
func (s *Stream) receiveAudioLoop() {
	defer s.audioConn.Close()

	log.Printf("Audio receive loop started, waiting for packets...")

	buf := make([]byte, 4096)
	packetsReceived := 0
	lastLogTime := time.Now()

	for {
		select {
		case <-s.ctx.Done():
			log.Printf("Audio receive loop stopped, received %d packets total", packetsReceived)
			return
		default:
		}

		s.audioConn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, addr, err := s.audioConn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Log every 5 seconds while waiting
				if time.Since(lastLogTime) > 5*time.Second {
					log.Printf("Audio: still waiting for packets (received %d so far)...", packetsReceived)
					lastLogTime = time.Now()
				}
				continue
			}
			log.Printf("Audio receive error: %v", err)
			continue
		}

		if n < 12 {
			continue // Too short for RTP header
		}

		packetsReceived++
		if packetsReceived == 1 {
			log.Printf("Receiving audio packets from Sunshine (first from %s, %d bytes)", addr, n)
		}

		// Send the complete RTP packet to the channel
		// Pion's TrackLocalStaticRTP expects full RTP packets
		select {
		case s.audioFrames <- append([]byte{}, buf[:n]...):
		default:
			// Channel full, drop packet
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
	// TODO: Input should be sent over the control channel (ENet/reliable UDP)
	// For now, this is a placeholder until control channel is implemented
	if s.controlConn == nil {
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
		s.controlConn.Write(packet)
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

	// Send quit command to Sunshine
	quitURL := fmt.Sprintf("http://%s:%d/cancel?uniqueid=%s",
		s.client.host, s.client.port, s.client.uniqueID)
	http.Get(quitURL)

	// Close all connections
	if s.rtspConn != nil {
		s.rtspConn.Close()
	}
	if s.videoConn != nil {
		s.videoConn.Close()
	}
	if s.audioConn != nil {
		s.audioConn.Close()
	}
	if s.controlConn != nil {
		s.controlConn.Close()
	}

	// Close channels safely
	select {
	case <-s.videoFrames:
	default:
		close(s.videoFrames)
	}
	select {
	case <-s.audioFrames:
	default:
		close(s.audioFrames)
	}

	return nil
}

// GetApps retrieves the list of available applications from Sunshine
func (c *Client) GetApps(ctx context.Context) ([]App, error) {
	url := fmt.Sprintf("http://%s:%d/applist?uniqueid=%s", c.host, c.port, c.uniqueID)

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
