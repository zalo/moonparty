package limelight

/*
#cgo CFLAGS: -I${SRCDIR}/../../../moonlight-common-c/src -I${SRCDIR}/../../../moonlight-common-c/enet/include -I${SRCDIR}/../../../moonlight-common-c/reedsolomon
#cgo LDFLAGS: -L${SRCDIR}/../../../build -L${SRCDIR}/../../../build/enet -lmoonlight-common-c -lenet -lcrypto -lm -lpthread -lrt

#include <stdlib.h>
#include <string.h>
#include "Limelight.h"

// Forward declarations for Go callbacks
extern int goDecoderSetup(int videoFormat, int width, int height, int redrawRate, void* context, int drFlags);
extern void goDecoderStart(void);
extern void goDecoderStop(void);
extern void goDecoderCleanup(void);
extern int goDecoderSubmitDecodeUnit(PDECODE_UNIT decodeUnit);

extern int goAudioInit(int audioConfiguration, POPUS_MULTISTREAM_CONFIGURATION opusConfig, void* context, int arFlags);
extern void goAudioStart(void);
extern void goAudioStop(void);
extern void goAudioCleanup(void);
extern void goAudioDecodeAndPlaySample(char* sampleData, int sampleLength);

extern void goConnectionStageStarting(int stage);
extern void goConnectionStageComplete(int stage);
extern void goConnectionStageFailed(int stage, int errorCode);
extern void goConnectionStarted(void);
extern void goConnectionTerminated(int errorCode);
extern void goConnectionLogMessage(char* format);
extern void goConnectionRumble(unsigned short controllerNumber, unsigned short lowFreqMotor, unsigned short highFreqMotor);

// C wrapper functions that call Go
static int cDecoderSetup(int videoFormat, int width, int height, int redrawRate, void* context, int drFlags) {
    return goDecoderSetup(videoFormat, width, height, redrawRate, context, drFlags);
}

static void cDecoderStart(void) {
    goDecoderStart();
}

static void cDecoderStop(void) {
    goDecoderStop();
}

static void cDecoderCleanup(void) {
    goDecoderCleanup();
}

static int cDecoderSubmitDecodeUnit(PDECODE_UNIT decodeUnit) {
    return goDecoderSubmitDecodeUnit(decodeUnit);
}

static int cAudioInit(int audioConfiguration, POPUS_MULTISTREAM_CONFIGURATION opusConfig, void* context, int arFlags) {
    return goAudioInit(audioConfiguration, opusConfig, context, arFlags);
}

static void cAudioStart(void) {
    goAudioStart();
}

static void cAudioStop(void) {
    goAudioStop();
}

static void cAudioCleanup(void) {
    goAudioCleanup();
}

static void cAudioDecodeAndPlaySample(char* sampleData, int sampleLength) {
    goAudioDecodeAndPlaySample(sampleData, sampleLength);
}

static void cConnectionStageStarting(int stage) {
    goConnectionStageStarting(stage);
}

static void cConnectionStageComplete(int stage) {
    goConnectionStageComplete(stage);
}

static void cConnectionStageFailed(int stage, int errorCode) {
    goConnectionStageFailed(stage, errorCode);
}

static void cConnectionStarted(void) {
    goConnectionStarted();
}

static void cConnectionTerminated(int errorCode) {
    goConnectionTerminated(errorCode);
}

static void cConnectionLogMessage(const char* format, ...) {
    // Simple passthrough of format string (args ignored for now)
    goConnectionLogMessage((char*)format);
}

static void cConnectionRumble(unsigned short controllerNumber, unsigned short lowFreqMotor, unsigned short highFreqMotor) {
    goConnectionRumble(controllerNumber, lowFreqMotor, highFreqMotor);
}

// Initialize callbacks structures
static DECODER_RENDERER_CALLBACKS makeDecoderCallbacks() {
    DECODER_RENDERER_CALLBACKS cbs;
    memset(&cbs, 0, sizeof(cbs));
    cbs.setup = cDecoderSetup;
    cbs.start = cDecoderStart;
    cbs.stop = cDecoderStop;
    cbs.cleanup = cDecoderCleanup;
    cbs.submitDecodeUnit = cDecoderSubmitDecodeUnit;
    return cbs;
}

static AUDIO_RENDERER_CALLBACKS makeAudioCallbacks() {
    AUDIO_RENDERER_CALLBACKS cbs;
    memset(&cbs, 0, sizeof(cbs));
    cbs.init = cAudioInit;
    cbs.start = cAudioStart;
    cbs.stop = cAudioStop;
    cbs.cleanup = cAudioCleanup;
    cbs.decodeAndPlaySample = cAudioDecodeAndPlaySample;
    return cbs;
}

static CONNECTION_LISTENER_CALLBACKS makeConnectionCallbacks() {
    CONNECTION_LISTENER_CALLBACKS cbs;
    memset(&cbs, 0, sizeof(cbs));
    cbs.stageStarting = cConnectionStageStarting;
    cbs.stageComplete = cConnectionStageComplete;
    cbs.stageFailed = cConnectionStageFailed;
    cbs.connectionStarted = cConnectionStarted;
    cbs.connectionTerminated = cConnectionTerminated;
    cbs.logMessage = cConnectionLogMessage;
    cbs.rumble = cConnectionRumble;
    return cbs;
}

// Helper to start connection with Go callbacks
static int startConnectionWithGoCallbacks(
    char* address,
    char* rtspSessionUrl,
    int serverCodecModeSupport,
    char* serverInfoAppVersion,
    char* serverInfoGfeVersion,
    int width, int height, int fps, int bitrate,
    int packetSize, int streamingRemotely,
    int audioConfiguration,
    int supportedVideoFormats,
    unsigned char* riKey, int riKeyLen,
    int riKeyId,
    void* renderContext, int drFlags,
    void* audioContext, int arFlags
) {
    SERVER_INFORMATION serverInfo;
    STREAM_CONFIGURATION streamConfig;

    LiInitializeServerInformation(&serverInfo);
    serverInfo.address = address;
    serverInfo.rtspSessionUrl = rtspSessionUrl;
    serverInfo.serverCodecModeSupport = serverCodecModeSupport;

    LiInitializeStreamConfiguration(&streamConfig);
    streamConfig.width = width;
    streamConfig.height = height;
    streamConfig.fps = fps;
    streamConfig.bitrate = bitrate;
    streamConfig.packetSize = packetSize;
    streamConfig.streamingRemotely = streamingRemotely;
    streamConfig.audioConfiguration = audioConfiguration;
    streamConfig.supportedVideoFormats = supportedVideoFormats;

    if (riKey && riKeyLen == 16) {
        memcpy(streamConfig.remoteInputAesKey, riKey, 16);
    }
    streamConfig.remoteInputAesIv[0] = (riKeyId >> 24) & 0xFF;
    streamConfig.remoteInputAesIv[1] = (riKeyId >> 16) & 0xFF;
    streamConfig.remoteInputAesIv[2] = (riKeyId >> 8) & 0xFF;
    streamConfig.remoteInputAesIv[3] = riKeyId & 0xFF;

    DECODER_RENDERER_CALLBACKS drCallbacks = makeDecoderCallbacks();
    AUDIO_RENDERER_CALLBACKS arCallbacks = makeAudioCallbacks();
    CONNECTION_LISTENER_CALLBACKS clCallbacks = makeConnectionCallbacks();

    return LiStartConnection(
        &serverInfo,
        &streamConfig,
        &clCallbacks,
        &drCallbacks,
        &arCallbacks,
        renderContext, drFlags,
        audioContext, arFlags
    );
}
*/
import "C"
import (
	"fmt"
	"log"
	"sync"
	"unsafe"
)

// Video format constants
const (
	VideoFormatH264      = C.VIDEO_FORMAT_H264
	VideoFormatH265      = C.VIDEO_FORMAT_H265
	VideoFormatH265Main10 = C.VIDEO_FORMAT_H265_MAIN10
	VideoFormatAV1Main8  = C.VIDEO_FORMAT_AV1_MAIN8
	VideoFormatAV1Main10 = C.VIDEO_FORMAT_AV1_MAIN10
)

// Audio configuration constants
const (
	AudioConfigStereo = C.AUDIO_CONFIGURATION_STEREO
	AudioConfig51     = C.AUDIO_CONFIGURATION_51_SURROUND
	AudioConfig71     = C.AUDIO_CONFIGURATION_71_SURROUND
)

// Streaming location constants
const (
	StreamingLocal  = C.STREAM_CFG_LOCAL
	StreamingRemote = C.STREAM_CFG_REMOTE
	StreamingAuto   = C.STREAM_CFG_AUTO
)

// Decoder return codes
const (
	DrOk      = C.DR_OK
	DrNeedIDR = C.DR_NEED_IDR
)

// Button flags for controller input
const (
	ButtonA       = C.A_FLAG
	ButtonB       = C.B_FLAG
	ButtonX       = C.X_FLAG
	ButtonY       = C.Y_FLAG
	ButtonUp      = C.UP_FLAG
	ButtonDown    = C.DOWN_FLAG
	ButtonLeft    = C.LEFT_FLAG
	ButtonRight   = C.RIGHT_FLAG
	ButtonLB      = C.LB_FLAG
	ButtonRB      = C.RB_FLAG
	ButtonPlay    = C.PLAY_FLAG
	ButtonBack    = C.BACK_FLAG
	ButtonLSClick = C.LS_CLK_FLAG
	ButtonRSClick = C.RS_CLK_FLAG
	ButtonSpecial = C.SPECIAL_FLAG
)

// Mouse button constants
const (
	MouseButtonLeft   = C.BUTTON_LEFT
	MouseButtonMiddle = C.BUTTON_MIDDLE
	MouseButtonRight  = C.BUTTON_RIGHT
	MouseButtonX1     = C.BUTTON_X1
	MouseButtonX2     = C.BUTTON_X2

	ButtonActionPress   = C.BUTTON_ACTION_PRESS
	ButtonActionRelease = C.BUTTON_ACTION_RELEASE
)

// Key action constants
const (
	KeyActionDown = C.KEY_ACTION_DOWN
	KeyActionUp   = C.KEY_ACTION_UP
)

// Key modifier constants
const (
	ModifierShift = C.MODIFIER_SHIFT
	ModifierCtrl  = C.MODIFIER_CTRL
	ModifierAlt   = C.MODIFIER_ALT
	ModifierMeta  = C.MODIFIER_META
)

// Connection stages
const (
	StageNone              = C.STAGE_NONE
	StagePlatformInit      = C.STAGE_PLATFORM_INIT
	StageNameResolution    = C.STAGE_NAME_RESOLUTION
	StageAudioStreamInit   = C.STAGE_AUDIO_STREAM_INIT
	StageRTSPHandshake     = C.STAGE_RTSP_HANDSHAKE
	StageControlStreamInit = C.STAGE_CONTROL_STREAM_INIT
	StageVideoStreamInit   = C.STAGE_VIDEO_STREAM_INIT
	StageInputStreamInit   = C.STAGE_INPUT_STREAM_INIT
	StageControlStreamStart = C.STAGE_CONTROL_STREAM_START
	StageVideoStreamStart   = C.STAGE_VIDEO_STREAM_START
	StageAudioStreamStart   = C.STAGE_AUDIO_STREAM_START
	StageInputStreamStart   = C.STAGE_INPUT_STREAM_START
)

// DecodeUnit represents a video frame to decode
type DecodeUnit struct {
	FrameNumber     int
	FrameType       int
	Data            []byte
	ReceiveTimeUs   int64
	EnqueueTimeUs   int64
	PresentationTimeUs int64
}

// OpusConfig represents Opus audio configuration
type OpusConfig struct {
	SampleRate      int
	ChannelCount    int
	Streams         int
	CoupledStreams  int
	SamplesPerFrame int
	Mapping         [8]byte
}

// Callbacks holds the Go callback functions
type Callbacks struct {
	// Video decoder callbacks
	OnDecoderSetup   func(videoFormat, width, height, redrawRate int)
	OnDecoderStart   func()
	OnDecoderStop    func()
	OnDecoderCleanup func()
	OnDecodeUnit     func(unit *DecodeUnit) int

	// Audio renderer callbacks
	OnAudioInit    func(audioConfig int, opusConfig *OpusConfig) int
	OnAudioStart   func()
	OnAudioStop    func()
	OnAudioCleanup func()
	OnAudioSample  func(data []byte)

	// Connection callbacks
	OnStageStarting func(stage int)
	OnStageComplete func(stage int)
	OnStageFailed   func(stage, errorCode int)
	OnConnectionStarted func()
	OnConnectionTerminated func(errorCode int)
	OnLogMessage func(msg string)
	OnRumble func(controllerNumber, lowFreq, highFreq uint16)
}

var (
	globalCallbacks *Callbacks
	callbackMutex   sync.RWMutex
)

// SetCallbacks sets the global callbacks for limelight events
func SetCallbacks(cbs *Callbacks) {
	callbackMutex.Lock()
	defer callbackMutex.Unlock()
	globalCallbacks = cbs
}

// StreamConfig holds streaming configuration
type StreamConfig struct {
	Width                int
	Height               int
	FPS                  int
	Bitrate              int
	PacketSize           int
	StreamingRemotely    int
	AudioConfiguration   int
	SupportedVideoFormats int
	RiKey                []byte
	RiKeyID              int
}

// ServerInfo holds server information
type ServerInfo struct {
	Address              string
	RtspSessionUrl       string
	ServerCodecModeSupport int
	AppVersion           string
	GfeVersion           string
}

// StartConnection starts a streaming connection
func StartConnection(serverInfo *ServerInfo, streamConfig *StreamConfig) error {
	cAddress := C.CString(serverInfo.Address)
	defer C.free(unsafe.Pointer(cAddress))

	var cRtspUrl *C.char
	if serverInfo.RtspSessionUrl != "" {
		cRtspUrl = C.CString(serverInfo.RtspSessionUrl)
		defer C.free(unsafe.Pointer(cRtspUrl))
	}

	var riKeyPtr *C.uchar
	if len(streamConfig.RiKey) == 16 {
		riKeyPtr = (*C.uchar)(unsafe.Pointer(&streamConfig.RiKey[0]))
	}

	result := C.startConnectionWithGoCallbacks(
		cAddress,
		cRtspUrl,
		C.int(serverInfo.ServerCodecModeSupport),
		nil, // serverInfoAppVersion
		nil, // serverInfoGfeVersion
		C.int(streamConfig.Width),
		C.int(streamConfig.Height),
		C.int(streamConfig.FPS),
		C.int(streamConfig.Bitrate),
		C.int(streamConfig.PacketSize),
		C.int(streamConfig.StreamingRemotely),
		C.int(streamConfig.AudioConfiguration),
		C.int(streamConfig.SupportedVideoFormats),
		riKeyPtr,
		C.int(len(streamConfig.RiKey)),
		C.int(streamConfig.RiKeyID),
		nil, 0, // renderContext, drFlags
		nil, 0, // audioContext, arFlags
	)

	if result != 0 {
		return fmt.Errorf("LiStartConnection failed with code %d", result)
	}
	return nil
}

// StopConnection stops the current streaming connection
func StopConnection() {
	C.LiStopConnection()
}

// InterruptConnection interrupts the current connection
func InterruptConnection() {
	C.LiInterruptConnection()
}

// GetStageName returns the human-readable name of a connection stage
func GetStageName(stage int) string {
	cName := C.LiGetStageName(C.int(stage))
	return C.GoString(cName)
}

// SendMouseMoveEvent sends a relative mouse move event
func SendMouseMoveEvent(deltaX, deltaY int16) error {
	result := C.LiSendMouseMoveEvent(C.short(deltaX), C.short(deltaY))
	if result != 0 {
		return fmt.Errorf("LiSendMouseMoveEvent failed: %d", result)
	}
	return nil
}

// SendMousePositionEvent sends an absolute mouse position event
func SendMousePositionEvent(x, y, refWidth, refHeight int16) error {
	result := C.LiSendMousePositionEvent(C.short(x), C.short(y), C.short(refWidth), C.short(refHeight))
	if result != 0 {
		return fmt.Errorf("LiSendMousePositionEvent failed: %d", result)
	}
	return nil
}

// SendMouseButtonEvent sends a mouse button press/release event
func SendMouseButtonEvent(action int8, button int) error {
	result := C.LiSendMouseButtonEvent(C.char(action), C.int(button))
	if result != 0 {
		return fmt.Errorf("LiSendMouseButtonEvent failed: %d", result)
	}
	return nil
}

// SendScrollEvent sends a mouse scroll event
func SendScrollEvent(scrollClicks int8) error {
	result := C.LiSendScrollEvent(C.schar(scrollClicks))
	if result != 0 {
		return fmt.Errorf("LiSendScrollEvent failed: %d", result)
	}
	return nil
}

// SendKeyboardEvent sends a keyboard key event
func SendKeyboardEvent(keyCode int16, keyAction int8, modifiers int8) error {
	result := C.LiSendKeyboardEvent(C.short(keyCode), C.char(keyAction), C.char(modifiers))
	if result != 0 {
		return fmt.Errorf("LiSendKeyboardEvent failed: %d", result)
	}
	return nil
}

// SendControllerEvent sends a single controller input event
func SendControllerEvent(buttonFlags int, leftTrigger, rightTrigger uint8, leftStickX, leftStickY, rightStickX, rightStickY int16) error {
	result := C.LiSendControllerEvent(
		C.int(buttonFlags),
		C.uchar(leftTrigger),
		C.uchar(rightTrigger),
		C.short(leftStickX),
		C.short(leftStickY),
		C.short(rightStickX),
		C.short(rightStickY),
	)
	if result != 0 {
		return fmt.Errorf("LiSendControllerEvent failed: %d", result)
	}
	return nil
}

// SendMultiControllerEvent sends input for a specific controller
func SendMultiControllerEvent(controllerNumber int16, activeGamepadMask int16, buttonFlags int, leftTrigger, rightTrigger uint8, leftStickX, leftStickY, rightStickX, rightStickY int16) error {
	result := C.LiSendMultiControllerEvent(
		C.short(controllerNumber),
		C.short(activeGamepadMask),
		C.int(buttonFlags),
		C.uchar(leftTrigger),
		C.uchar(rightTrigger),
		C.short(leftStickX),
		C.short(leftStickY),
		C.short(rightStickX),
		C.short(rightStickY),
	)
	if result != 0 {
		return fmt.Errorf("LiSendMultiControllerEvent failed: %d", result)
	}
	return nil
}

// RequestIDRFrame requests an IDR (keyframe) from the server
func RequestIDRFrame() {
	C.LiRequestIdrFrame()
}

//export goDecoderSetup
func goDecoderSetup(videoFormat, width, height, redrawRate C.int, context unsafe.Pointer, drFlags C.int) C.int {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnDecoderSetup != nil {
		cbs.OnDecoderSetup(int(videoFormat), int(width), int(height), int(redrawRate))
	}
	return 0
}

//export goDecoderStart
func goDecoderStart() {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnDecoderStart != nil {
		cbs.OnDecoderStart()
	}
}

//export goDecoderStop
func goDecoderStop() {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnDecoderStop != nil {
		cbs.OnDecoderStop()
	}
}

//export goDecoderCleanup
func goDecoderCleanup() {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnDecoderCleanup != nil {
		cbs.OnDecoderCleanup()
	}
}

//export goDecoderSubmitDecodeUnit
func goDecoderSubmitDecodeUnit(decodeUnit *C.DECODE_UNIT) C.int {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs == nil || cbs.OnDecodeUnit == nil {
		return C.DR_OK
	}

	// Convert C DECODE_UNIT to Go DecodeUnit
	unit := &DecodeUnit{
		FrameNumber:        int(decodeUnit.frameNumber),
		FrameType:          int(decodeUnit.frameType),
		ReceiveTimeUs:      int64(decodeUnit.receiveTimeUs),
		EnqueueTimeUs:      int64(decodeUnit.enqueueTimeUs),
		PresentationTimeUs: int64(decodeUnit.presentationTimeUs),
	}

	// Collect all buffer data
	totalLen := int(decodeUnit.fullLength)
	unit.Data = make([]byte, 0, totalLen)

	entry := decodeUnit.bufferList
	for entry != nil {
		data := C.GoBytes(unsafe.Pointer(entry.data), C.int(entry.length))
		unit.Data = append(unit.Data, data...)
		entry = entry.next
	}

	result := cbs.OnDecodeUnit(unit)
	return C.int(result)
}

//export goAudioInit
func goAudioInit(audioConfiguration C.int, opusConfig *C.OPUS_MULTISTREAM_CONFIGURATION, context unsafe.Pointer, arFlags C.int) C.int {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs == nil || cbs.OnAudioInit == nil {
		return 0
	}

	cfg := &OpusConfig{
		SampleRate:      int(opusConfig.sampleRate),
		ChannelCount:    int(opusConfig.channelCount),
		Streams:         int(opusConfig.streams),
		CoupledStreams:  int(opusConfig.coupledStreams),
		SamplesPerFrame: int(opusConfig.samplesPerFrame),
	}
	for i := 0; i < 8; i++ {
		cfg.Mapping[i] = byte(opusConfig.mapping[i])
	}

	return C.int(cbs.OnAudioInit(int(audioConfiguration), cfg))
}

//export goAudioStart
func goAudioStart() {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnAudioStart != nil {
		cbs.OnAudioStart()
	}
}

//export goAudioStop
func goAudioStop() {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnAudioStop != nil {
		cbs.OnAudioStop()
	}
}

//export goAudioCleanup
func goAudioCleanup() {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnAudioCleanup != nil {
		cbs.OnAudioCleanup()
	}
}

//export goAudioDecodeAndPlaySample
func goAudioDecodeAndPlaySample(sampleData *C.char, sampleLength C.int) {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs == nil || cbs.OnAudioSample == nil {
		return
	}

	data := C.GoBytes(unsafe.Pointer(sampleData), sampleLength)
	cbs.OnAudioSample(data)
}

//export goConnectionStageStarting
func goConnectionStageStarting(stage C.int) {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnStageStarting != nil {
		cbs.OnStageStarting(int(stage))
	}
	log.Printf("Connection stage starting: %s", GetStageName(int(stage)))
}

//export goConnectionStageComplete
func goConnectionStageComplete(stage C.int) {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnStageComplete != nil {
		cbs.OnStageComplete(int(stage))
	}
	log.Printf("Connection stage complete: %s", GetStageName(int(stage)))
}

//export goConnectionStageFailed
func goConnectionStageFailed(stage, errorCode C.int) {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnStageFailed != nil {
		cbs.OnStageFailed(int(stage), int(errorCode))
	}
	log.Printf("Connection stage failed: %s (error %d)", GetStageName(int(stage)), errorCode)
}

//export goConnectionStarted
func goConnectionStarted() {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnConnectionStarted != nil {
		cbs.OnConnectionStarted()
	}
	log.Println("Connection started")
}

//export goConnectionTerminated
func goConnectionTerminated(errorCode C.int) {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnConnectionTerminated != nil {
		cbs.OnConnectionTerminated(int(errorCode))
	}
	log.Printf("Connection terminated (error %d)", errorCode)
}

//export goConnectionLogMessage
func goConnectionLogMessage(format *C.char) {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	msg := C.GoString(format)
	if cbs != nil && cbs.OnLogMessage != nil {
		cbs.OnLogMessage(msg)
	}
	log.Printf("[limelight] %s", msg)
}

//export goConnectionRumble
func goConnectionRumble(controllerNumber, lowFreqMotor, highFreqMotor C.ushort) {
	callbackMutex.RLock()
	cbs := globalCallbacks
	callbackMutex.RUnlock()

	if cbs != nil && cbs.OnRumble != nil {
		cbs.OnRumble(uint16(controllerNumber), uint16(lowFreqMotor), uint16(highFreqMotor))
	}
}
