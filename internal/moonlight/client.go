package moonlight

import (
	"bytes"
	"context"
	"crypto"
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

	log.Printf("Sending clientpairingsecret (Phase 4), secret_len=%d, sig_len=%d...", len(clientSecret), len(signature))

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

	url := fmt.Sprintf("http://%s:%d/launch?%s", s.client.host, s.client.port, params)

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
		quitURL := fmt.Sprintf("http://%s:%d/cancel?uniqueid=%s",
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
