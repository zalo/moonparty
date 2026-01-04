// Moonparty Client Application

class MoonpartyClient {
    constructor() {
        this.ws = null;
        this.pc = null;
        this.dataChannels = {};
        this.sessionInfo = null;
        this.gamepadLoop = null;
        this.gamepads = {};

        this.initElements();
        this.initEventListeners();
        this.connect();
    }

    initElements() {
        // Video
        this.video = document.getElementById('video');
        this.canvas = document.getElementById('canvas');
        this.loading = document.getElementById('loading');
        this.stats = document.getElementById('stats');

        // Panel
        this.panel = document.getElementById('panel');
        this.panelToggle = document.getElementById('panel-toggle');

        // Session
        this.statusDot = document.querySelector('.status-dot');
        this.statusText = document.getElementById('status-text');
        this.sessionSection = document.getElementById('session-info');
        this.sessionId = document.getElementById('session-id');
        this.roleText = document.getElementById('role');
        this.slotText = document.getElementById('slot');

        // Players
        this.playersSection = document.getElementById('players-section');
        this.playerList = document.getElementById('player-list');
        this.joinGameBtn = document.getElementById('join-game-btn');

        // Host Controls
        this.hostControls = document.getElementById('host-controls');
        this.guestKeyboardToggle = document.getElementById('guest-keyboard-toggle');
        this.playerKeyboardToggles = document.getElementById('player-keyboard-toggles');

        // Quality
        this.resolutionSelect = document.getElementById('resolution');
        this.fpsSelect = document.getElementById('fps');
        this.bitrateSlider = document.getElementById('bitrate');
        this.bitrateValue = document.getElementById('bitrate-value');

        // Input
        this.captureKeyboard = document.getElementById('capture-keyboard');
        this.captureMouse = document.getElementById('capture-mouse');
        this.gamepadStatus = document.getElementById('gamepad-status');
        this.gamepadText = document.getElementById('gamepad-text');

        // Actions
        this.fullscreenBtn = document.getElementById('fullscreen-btn');
        this.disconnectBtn = document.getElementById('disconnect-btn');

        // Touch
        this.touchControls = document.getElementById('touch-controls');
    }

    initEventListeners() {
        // Panel toggle
        this.panelToggle.addEventListener('click', () => {
            this.panel.classList.toggle('collapsed');
        });

        // Quality controls
        this.bitrateSlider.addEventListener('input', () => {
            this.bitrateValue.textContent = this.bitrateSlider.value;
        });

        // Join game button
        this.joinGameBtn.addEventListener('click', () => this.joinAsPlayer());

        // Fullscreen
        this.fullscreenBtn.addEventListener('click', () => this.toggleFullscreen());

        // Disconnect
        this.disconnectBtn.addEventListener('click', () => this.disconnect());

        // Input capture
        this.captureKeyboard.addEventListener('change', () => {
            if (this.captureKeyboard.checked) {
                this.startKeyboardCapture();
            } else {
                this.stopKeyboardCapture();
            }
        });

        // Gamepad events
        window.addEventListener('gamepadconnected', (e) => this.onGamepadConnected(e));
        window.addEventListener('gamepaddisconnected', (e) => this.onGamepadDisconnected(e));

        // Video container click for focus
        document.getElementById('video-container').addEventListener('click', () => {
            if (this.captureMouse.checked) {
                this.video.requestPointerLock?.();
            }
        });

        // Mouse events
        document.addEventListener('mousemove', (e) => this.onMouseMove(e));
        document.addEventListener('mousedown', (e) => this.onMouseButton(e, true));
        document.addEventListener('mouseup', (e) => this.onMouseButton(e, false));
        document.addEventListener('wheel', (e) => this.onMouseWheel(e));

        // Keyboard events
        document.addEventListener('keydown', (e) => this.onKeyDown(e));
        document.addEventListener('keyup', (e) => this.onKeyUp(e));

        // Check for touch device
        if ('ontouchstart' in window) {
            this.touchControls.classList.remove('hidden');
            this.initTouchControls();
        }
    }

    setStatus(status, text) {
        this.statusDot.className = 'status-dot ' + status;
        this.statusText.textContent = text;
    }

    async connect() {
        this.setStatus('connecting', 'Connecting...');

        const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = `${protocol}//${location.host}/ws`;

        try {
            this.ws = new WebSocket(wsUrl);
            this.ws.onopen = () => this.onWebSocketOpen();
            this.ws.onmessage = (e) => this.onWebSocketMessage(e);
            this.ws.onclose = () => this.onWebSocketClose();
            this.ws.onerror = (e) => this.onWebSocketError(e);
        } catch (err) {
            console.error('WebSocket connection failed:', err);
            this.setStatus('offline', 'Connection failed');
        }
    }

    onWebSocketOpen() {
        console.log('WebSocket connected');
        this.setStatus('connecting', 'Establishing stream...');
    }

    onWebSocketMessage(event) {
        const msg = JSON.parse(event.data);
        console.log('WS message:', msg.type, msg.payload);

        switch (msg.type) {
            case 'session_info':
                this.handleSessionInfo(msg.payload);
                break;
            case 'answer':
                this.handleAnswer(msg.payload);
                break;
            case 'ice_candidate':
                this.handleICECandidate(msg.payload);
                break;
            case 'player_slot':
                this.handlePlayerSlot(msg.payload);
                break;
            case 'peer_joined':
                this.handlePeerJoined(msg.payload);
                break;
            case 'peer_left':
                this.handlePeerLeft(msg.payload);
                break;
            case 'error':
                this.handleError(msg.payload);
                break;
        }
    }

    onWebSocketClose() {
        console.log('WebSocket closed');
        this.setStatus('offline', 'Disconnected');
        this.loading.classList.remove('hidden');
        this.disconnectBtn.classList.add('hidden');
    }

    onWebSocketError(error) {
        console.error('WebSocket error:', error);
        this.setStatus('offline', 'Connection error');
    }

    handleSessionInfo(info) {
        this.sessionInfo = info;

        this.sessionSection.classList.remove('hidden');
        this.sessionId.textContent = info.session_id;
        this.roleText.textContent = info.role;
        this.slotText.textContent = info.slot >= 0 ? `Player ${info.slot + 1}` : 'Spectator';

        this.playersSection.classList.remove('hidden');
        this.updatePlayerList(info.players);

        if (info.is_host) {
            this.hostControls.classList.remove('hidden');
        }

        if (info.role === 'spectator') {
            this.joinGameBtn.classList.remove('hidden');
        }

        this.disconnectBtn.classList.remove('hidden');

        // Initialize WebRTC
        this.initWebRTC();
    }

    async initWebRTC() {
        // Get ICE servers
        const iceResponse = await fetch('/api/ice-servers');
        const iceServers = await iceResponse.json();

        this.pc = new RTCPeerConnection({ iceServers });

        // Handle incoming tracks
        this.pc.ontrack = (event) => {
            console.log('Track received:', event.track.kind);
            if (event.track.kind === 'video') {
                this.video.srcObject = event.streams[0];
                this.loading.classList.add('hidden');
                this.stats.classList.remove('hidden');
                this.setStatus('online', 'Connected');
            }
        };

        // Handle data channels
        this.pc.ondatachannel = (event) => {
            const channel = event.channel;
            console.log('Data channel received:', channel.label);
            this.dataChannels[channel.label] = channel;

            channel.onopen = () => console.log(`Channel ${channel.label} open`);
            channel.onclose = () => console.log(`Channel ${channel.label} closed`);
            channel.onmessage = (e) => this.onDataChannelMessage(channel.label, e.data);
        };

        // Handle ICE candidates
        this.pc.onicecandidate = (event) => {
            if (event.candidate) {
                this.sendMessage('candidate', {
                    candidate: JSON.stringify(event.candidate.toJSON())
                });
            }
        };

        // Create offer
        const offer = await this.pc.createOffer({
            offerToReceiveVideo: true,
            offerToReceiveAudio: true
        });
        await this.pc.setLocalDescription(offer);

        this.sendMessage('offer', { sdp: offer.sdp });
    }

    async handleAnswer(payload) {
        if (!this.pc) return;

        const answer = new RTCSessionDescription({
            type: 'answer',
            sdp: payload.sdp
        });
        await this.pc.setRemoteDescription(answer);
    }

    async handleICECandidate(payload) {
        if (!this.pc) return;

        try {
            const candidate = JSON.parse(payload.candidate);
            await this.pc.addIceCandidate(candidate);
        } catch (err) {
            console.error('Failed to add ICE candidate:', err);
        }
    }

    handlePlayerSlot(payload) {
        this.sessionInfo.slot = payload.slot;
        this.sessionInfo.role = 'player';
        this.slotText.textContent = `Player ${payload.slot + 1}`;
        this.roleText.textContent = 'player';
        this.joinGameBtn.classList.add('hidden');
    }

    handlePeerJoined(payload) {
        console.log('Peer joined:', payload);
        this.updatePlayerList(payload.players);
    }

    handlePeerLeft(payload) {
        console.log('Peer left:', payload);
        this.updatePlayerList(payload.players);
    }

    handleError(payload) {
        console.error('Server error:', payload.error);
        alert('Error: ' + payload.error);
    }

    updatePlayerList(players) {
        this.playerList.innerHTML = '';

        if (!players) return;

        players.forEach(player => {
            const li = document.createElement('li');
            li.className = 'player-item';
            li.innerHTML = `
                <span class="player-slot">${player.player_slot + 1}</span>
                <span class="player-name">${player.name}</span>
                ${player.role === 'host' ? '<span class="player-host">Host</span>' : ''}
            `;
            this.playerList.appendChild(li);
        });

        // Update host controls if we're the host
        if (this.sessionInfo?.is_host) {
            this.updateKeyboardToggles(players);
        }
    }

    updateKeyboardToggles(players) {
        this.playerKeyboardToggles.innerHTML = '';

        players.filter(p => p.role !== 'host').forEach(player => {
            const div = document.createElement('div');
            div.className = 'keyboard-toggle';
            div.innerHTML = `
                <span>P${player.player_slot + 1}: ${player.name}</span>
                <input type="checkbox" ${player.keyboard_enabled ? 'checked' : ''}
                       data-peer-id="${player.id}">
            `;

            div.querySelector('input').addEventListener('change', (e) => {
                this.togglePlayerKeyboard(player.id, e.target.checked);
            });

            this.playerKeyboardToggles.appendChild(div);
        });
    }

    joinAsPlayer() {
        this.sendMessage('join_as_player', {});
    }

    togglePlayerKeyboard(peerId, enabled) {
        fetch('/api/player/keyboard', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ peer_id: peerId, enabled })
        });
    }

    sendMessage(type, payload) {
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
            this.ws.send(JSON.stringify({ type, payload }));
        }
    }

    sendInput(inputType, data) {
        // Prefer WebRTC data channel for low latency
        const channel = this.dataChannels['input'];
        if (channel && channel.readyState === 'open') {
            channel.send(new Uint8Array([
                inputType,
                ...data
            ]));
        } else {
            // Fallback to WebSocket
            this.sendMessage('input', {
                input_type: inputType,
                data: Array.from(data)
            });
        }
    }

    // Input Handling

    onKeyDown(event) {
        if (!this.captureKeyboard.checked) return;
        if (!this.canSendKeyboard()) return;

        event.preventDefault();
        this.sendInput('keyboard', this.encodeKeyEvent(event.keyCode, true, event));
    }

    onKeyUp(event) {
        if (!this.captureKeyboard.checked) return;
        if (!this.canSendKeyboard()) return;

        event.preventDefault();
        this.sendInput('keyboard', this.encodeKeyEvent(event.keyCode, false, event));
    }

    encodeKeyEvent(keyCode, down, event) {
        let modifiers = 0;
        if (event.shiftKey) modifiers |= 0x01;
        if (event.ctrlKey) modifiers |= 0x02;
        if (event.altKey) modifiers |= 0x04;
        if (event.metaKey) modifiers |= 0x08;

        return new Uint8Array([
            keyCode & 0xFF,
            (keyCode >> 8) & 0xFF,
            modifiers,
            down ? 1 : 0
        ]);
    }

    onMouseMove(event) {
        if (!this.captureMouse.checked) return;
        if (!document.pointerLockElement) return;
        if (!this.canSendMouse()) return;

        this.sendInput('mouse_rel', new Uint8Array([
            ...this.encodeInt16(event.movementX),
            ...this.encodeInt16(event.movementY)
        ]));
    }

    onMouseButton(event, down) {
        if (!this.captureMouse.checked) return;
        if (!document.pointerLockElement) return;
        if (!this.canSendMouse()) return;

        this.sendInput('mouse', new Uint8Array([
            0x01, // Button action
            event.button,
            down ? 1 : 0
        ]));
    }

    onMouseWheel(event) {
        if (!this.captureMouse.checked) return;
        if (!document.pointerLockElement) return;
        if (!this.canSendMouse()) return;

        this.sendInput('mouse', new Uint8Array([
            0x02, // Scroll action
            ...this.encodeInt16(Math.sign(event.deltaY) * -120)
        ]));
    }

    encodeInt16(value) {
        const arr = new Uint8Array(2);
        arr[0] = value & 0xFF;
        arr[1] = (value >> 8) & 0xFF;
        return arr;
    }

    canSendKeyboard() {
        if (!this.sessionInfo) return false;
        return this.sessionInfo.role === 'host' ||
               (this.sessionInfo.role === 'player' && this.sessionInfo.keyboard_enabled);
    }

    canSendMouse() {
        return this.canSendKeyboard();
    }

    canSendGamepad() {
        if (!this.sessionInfo) return false;
        return this.sessionInfo.role === 'host' || this.sessionInfo.role === 'player';
    }

    // Gamepad Handling

    onGamepadConnected(event) {
        console.log('Gamepad connected:', event.gamepad.id);
        this.gamepads[event.gamepad.index] = event.gamepad;
        this.gamepadStatus.classList.add('connected');
        this.gamepadText.textContent = event.gamepad.id.slice(0, 30);

        if (!this.gamepadLoop) {
            this.startGamepadLoop();
        }
    }

    onGamepadDisconnected(event) {
        console.log('Gamepad disconnected:', event.gamepad.id);
        delete this.gamepads[event.gamepad.index];

        if (Object.keys(this.gamepads).length === 0) {
            this.gamepadText.textContent = 'No gamepad detected';
            this.gamepadStatus.classList.remove('connected');
            this.stopGamepadLoop();
        }
    }

    startGamepadLoop() {
        let lastState = {};

        const poll = () => {
            if (!this.canSendGamepad()) {
                this.gamepadLoop = requestAnimationFrame(poll);
                return;
            }

            const gamepads = navigator.getGamepads();

            for (const gamepad of gamepads) {
                if (!gamepad) continue;

                const state = this.encodeGamepadState(gamepad);
                const stateKey = gamepad.index;

                // Only send if state changed
                if (lastState[stateKey] !== state.toString()) {
                    lastState[stateKey] = state.toString();
                    this.sendInput('gamepad', new Uint8Array([
                        gamepad.index,
                        ...state
                    ]));
                }
            }

            this.gamepadLoop = requestAnimationFrame(poll);
        };

        poll();
    }

    stopGamepadLoop() {
        if (this.gamepadLoop) {
            cancelAnimationFrame(this.gamepadLoop);
            this.gamepadLoop = null;
        }
    }

    encodeGamepadState(gamepad) {
        // Standard gamepad mapping to Moonlight format
        let buttons = 0;

        // Map standard gamepad buttons to Moonlight button flags
        const buttonMap = [
            0x1000, // A (0)
            0x2000, // B (1)
            0x4000, // X (2)
            0x8000, // Y (3)
            0x0100, // LB (4)
            0x0200, // RB (5)
            0x0000, // LT (6) - handled as axis
            0x0000, // RT (7) - handled as axis
            0x0020, // Back (8)
            0x0010, // Start (9)
            0x0040, // L3 (10)
            0x0080, // R3 (11)
            0x0001, // D-pad up (12)
            0x0002, // D-pad down (13)
            0x0004, // D-pad left (14)
            0x0008, // D-pad right (15)
            0x0400, // Guide (16)
        ];

        gamepad.buttons.forEach((btn, i) => {
            if (btn.pressed && buttonMap[i]) {
                buttons |= buttonMap[i];
            }
        });

        // Encode axes (left stick, right stick) as signed 16-bit
        const leftX = Math.round(gamepad.axes[0] * 32767);
        const leftY = Math.round(gamepad.axes[1] * 32767);
        const rightX = Math.round(gamepad.axes[2] * 32767);
        const rightY = Math.round(gamepad.axes[3] * 32767);

        // Triggers as 0-255
        const leftTrigger = Math.round((gamepad.buttons[6]?.value || 0) * 255);
        const rightTrigger = Math.round((gamepad.buttons[7]?.value || 0) * 255);

        const data = new Uint8Array(14);
        // Buttons (2 bytes)
        data[0] = buttons & 0xFF;
        data[1] = (buttons >> 8) & 0xFF;
        // Left trigger
        data[2] = leftTrigger;
        // Right trigger
        data[3] = rightTrigger;
        // Left stick X (2 bytes, signed)
        data[4] = leftX & 0xFF;
        data[5] = (leftX >> 8) & 0xFF;
        // Left stick Y (2 bytes, signed)
        data[6] = leftY & 0xFF;
        data[7] = (leftY >> 8) & 0xFF;
        // Right stick X (2 bytes, signed)
        data[8] = rightX & 0xFF;
        data[9] = (rightX >> 8) & 0xFF;
        // Right stick Y (2 bytes, signed)
        data[10] = rightY & 0xFF;
        data[11] = (rightY >> 8) & 0xFF;

        return data;
    }

    // Touch Controls

    initTouchControls() {
        const touchBtns = document.querySelectorAll('.touch-btn');
        touchBtns.forEach(btn => {
            const buttonName = btn.dataset.btn;

            btn.addEventListener('touchstart', (e) => {
                e.preventDefault();
                this.sendTouchButton(buttonName, true);
            });

            btn.addEventListener('touchend', (e) => {
                e.preventDefault();
                this.sendTouchButton(buttonName, false);
            });
        });

        // Virtual D-pad
        const dpad = document.getElementById('touch-dpad');
        let dpadState = { up: false, down: false, left: false, right: false };

        dpad.addEventListener('touchstart', (e) => this.handleDpadTouch(e, dpadState));
        dpad.addEventListener('touchmove', (e) => this.handleDpadTouch(e, dpadState));
        dpad.addEventListener('touchend', () => {
            dpadState = { up: false, down: false, left: false, right: false };
            this.sendDpadState(dpadState);
        });
    }

    handleDpadTouch(event, state) {
        event.preventDefault();
        const touch = event.touches[0];
        const rect = event.target.getBoundingClientRect();

        const x = (touch.clientX - rect.left - rect.width / 2) / (rect.width / 2);
        const y = (touch.clientY - rect.top - rect.height / 2) / (rect.height / 2);

        state.left = x < -0.3;
        state.right = x > 0.3;
        state.up = y < -0.3;
        state.down = y > 0.3;

        this.sendDpadState(state);
    }

    sendTouchButton(name, pressed) {
        // Map touch button to gamepad button
        const buttonMap = { a: 0, b: 1, x: 2, y: 3 };
        const buttonIndex = buttonMap[name];
        if (buttonIndex !== undefined) {
            // Send minimal gamepad state with just this button
            const buttons = pressed ? (1 << (12 + buttonIndex)) : 0;
            this.sendInput('gamepad', new Uint8Array([
                0, // Gamepad index
                buttons & 0xFF,
                (buttons >> 8) & 0xFF,
                0, 0, // Triggers
                0, 0, 0, 0, // Left stick
                0, 0, 0, 0  // Right stick
            ]));
        }
    }

    sendDpadState(state) {
        let buttons = 0;
        if (state.up) buttons |= 0x0001;
        if (state.down) buttons |= 0x0002;
        if (state.left) buttons |= 0x0004;
        if (state.right) buttons |= 0x0008;

        this.sendInput('gamepad', new Uint8Array([
            0, // Gamepad index
            buttons & 0xFF,
            (buttons >> 8) & 0xFF,
            0, 0, // Triggers
            0, 0, 0, 0, // Left stick
            0, 0, 0, 0  // Right stick
        ]));
    }

    // Utilities

    toggleFullscreen() {
        if (!document.fullscreenElement) {
            document.getElementById('video-container').requestFullscreen();
            this.fullscreenBtn.textContent = 'Exit Fullscreen';
        } else {
            document.exitFullscreen();
            this.fullscreenBtn.textContent = 'Fullscreen';
        }
    }

    disconnect() {
        this.sendMessage('leave', {});

        if (this.pc) {
            this.pc.close();
            this.pc = null;
        }

        if (this.ws) {
            this.ws.close();
            this.ws = null;
        }

        this.stopGamepadLoop();
        this.sessionInfo = null;

        // Reset UI
        this.sessionSection.classList.add('hidden');
        this.playersSection.classList.add('hidden');
        this.hostControls.classList.add('hidden');
        this.disconnectBtn.classList.add('hidden');
        this.joinGameBtn.classList.add('hidden');
        this.loading.classList.remove('hidden');
        this.stats.classList.add('hidden');
        this.setStatus('offline', 'Disconnected');

        // Reconnect after a delay
        setTimeout(() => this.connect(), 2000);
    }

    onDataChannelMessage(label, data) {
        // Handle incoming data channel messages (stats, etc.)
        if (label === 'control') {
            try {
                const msg = JSON.parse(data);
                console.log('Control message:', msg);
            } catch (e) {
                // Binary data
            }
        }
    }

    startKeyboardCapture() {
        // Request keyboard lock for fullscreen (if supported)
        if (document.fullscreenElement && navigator.keyboard?.lock) {
            navigator.keyboard.lock(['Escape', 'Tab', 'AltLeft', 'AltRight']);
        }
    }

    stopKeyboardCapture() {
        if (navigator.keyboard?.unlock) {
            navigator.keyboard.unlock();
        }
    }
}

// Initialize on load
document.addEventListener('DOMContentLoaded', () => {
    window.moonparty = new MoonpartyClient();
});
