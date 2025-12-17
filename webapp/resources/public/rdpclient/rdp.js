import { Observable } from './observable.js';
import {scanCode} from './scancode.js';
import * as wasmModule from "./pkg/ironrdp_web_bg";

// Based on https://github.com/Devolutions/IronRDP/tree/master/web-client/iron-remote-desktop

const ModifierKey = {
    'ControlLeft': 'ControlLeft',
    'ShiftLeft': 'ShiftLeft',
    'ShiftRight': 'ShiftRight',
    'AltLeft': 'AltLeft',
    'ControlRight': 'ControlRight',
    'AltRight': 'AltRight',
}

const LockKey = {
    'CapsLock': 'CapsLock',
    'ScrollLock': 'ScrollLock',
    'NumLock': 'NumLock',
    'KanaMode': 'KanaMode',
}

const RotationUnit = {
    Pixel: 0,
    Line: 1,
    Page: 2,
};

export class Config {
    username;
    password;
    proxyAddress;

    constructor(
        username,
        password,
        proxyAddress,
    ) {
        this.username = username;
        this.password = password;
        this.proxyAddress = proxyAddress;
    }
}

export class RemoteDesktopService {

    sessionStartedObservable = new Observable();

    resizeObservable = new Observable();

    session = null;
    modifierKeyPressed = [];

    mousePositionObservable = new Observable();
    changeVisibilityObservable = new Observable();

    dynamicResizeObservable = new Observable();
    focused = false;
    
    constructor(rdpCanvas, mod) {
        this.module = mod;
        this.canvas = rdpCanvas;
        this.canvas.style.border = '1px solid black';
        // Paint canvas black
        this.clearScreenAndWriteText('Loading...');
        
        if (this.module === undefined || this.module === null) {
            this.clearScreenAndWriteText('Error: WebAssembly module is not loaded.');
            throw new Error('Module is undefined or null');
        }

        this.enableClipboard = true;

        this.canvas.onmousemove = (event) => this.getMousePos(event);
        this.canvas.onmousedown = (event) => this.setMouseButtonState(event, true);
        this.canvas.onmouseup = (event) => this.setMouseButtonState(event, false);
        this.canvas.onmouseleave = (event) => {
            this.setMouseButtonState(event, false);
            this.setMouseOut(event);
        };
        this.canvas.onmouseenter = (event) => {
            this.setMouseIn(event);
        };
        this.canvas.oncontextmenu = (event) => event.preventDefault();
        this.canvas.onwheel = (event) => this.mouseWheel(event);
        this.canvas.onselectstart = (event) => {
            event.preventDefault();
        };
        console.log('Web bridge initialized.');
    }

    reportError(err) {
        if (err instanceof IronError) {
            console.log(`RDP Connection error: ${err.kind()}: ${err.backtrace()}`)
            this.clearScreenAndWriteText(`Connection Error:\n${err.kind()}\n${err.backtrace()}`);
        } else {
            console.error('RDP Connection Error:', err);
            this.clearScreenAndWriteText("Connection Error:\n" + err.message);
        }
    }
    
    clearScreenAndWriteText(text) {
        // Write: 'Connecting...' text, centered
        this.ctx = this.canvas.getContext('2d');
        this.ctx.fillStyle = 'black';
        this.ctx.fillRect(0, 0, this.canvas.width, this.canvas.height);

        this.ctx.fillStyle = 'white';
        this.ctx.font = '20px Arial';
        this.ctx.textAlign = 'center';
        this.ctx.textBaseline = 'middle';
        this.ctx.fillText(text, this.canvas.width / 2, this.canvas.height / 2);
    }

    focusEventHandler(ev) {
        console.log(ev)
        this.focused = true;
    }

    initListeners() {
        this.resizeObservable.subscribe((evt) => {
            console.log(`Resize canvas to: ${evt.desktopSize.width}x${evt.desktopSize.height}`);
            this.canvas.width = evt.desktopSize.width;
            this.canvas.height = evt.desktopSize.height;
        });

        this.dynamicResizeObservable.subscribe((evt) => {
            console.log(`Dynamic resize!, width: ${evt.width}, height: ${evt.height}`);
        });

        const captureKeys = (evt) => {
            if (this.focused) {
                this.keyboardEvent(evt);
            }
        }

        window.addEventListener('keydown', captureKeys, false);
        window.addEventListener('keyup', captureKeys, false);
        window.addEventListener('focus', this.focusEventHandler);
    }

    getMousePos(evt) {
        const rect = this.canvas?.getBoundingClientRect(),
            scaleX = this.canvas?.width / rect.width,
            scaleY = this.canvas?.height / rect.height;

        const coord = {
            x: Math.round((evt.clientX - rect.left) * scaleX),
            y: Math.round((evt.clientY - rect.top) * scaleY),
        };

        RDP.updateMousePosition(coord);
    }

    setMouseButtonState(state, isDown) {
        this.mouseButtonState(state, isDown, true);
    }

    rotation_unit_from_wheel_event(event) {
        switch (event.deltaMode) {
            case event.DOM_DELTA_PIXEL:
                return RotationUnit.Pixel;
            case event.DOM_DELTA_LINE:
                return RotationUnit.Line;
            case event.DOM_DELTA_PAGE:
                return RotationUnit.Page;
            default:
                return RotationUnit.Pixel;
        }
    }
    
    mouseWheel(event) {
        const vertical = event.deltaY !== 0;
        const rotation = vertical ? event.deltaY : event.deltaX;
        const rotation_unit = this.rotation_unit_from_wheel_event(event);

        this.doTransactionFromDeviceEvents([
            this.module.DeviceEvent.wheelRotations(vertical, -rotation, rotation_unit),
        ]);
    }
    
    setMouseIn(evt) {
        this.canvas.focus({ preventScroll: true });
        this.mouseIn(evt);
    }

    setMouseOut(evt) {
        this.mouseOut(evt);
    }

    keyboardEvent(evt) {
        this.sendKeyboardEvent(evt);

        // Propagate further
        return true;
    }

    getWindowSize() {
        const win = window;
        const doc = document;
        const docElem = doc.documentElement;
        const body = doc.getElementsByTagName('body')[0];
        const x = win.innerWidth ?? docElem.clientWidth ?? body.clientWidth;
        const y = win.innerHeight ?? docElem.clientHeight ?? body.clientHeight;
        return { x, y };
    }

    get autoClipboard() {
        return this._autoClipboard;
    }

    // If set to false, the clipboard will not be enabled and the callbacks will not be registered to the Rust side
    setEnableClipboard(enable) {
        this.enableClipboard = enable;
    }

    // If set to true, automatic clipboard synchronization with the server is enabled.
    //
    // If set to false, then the client must invoke `PublicAPI.saveRemoteClipboardData` and
    // `PublicAPI.sendClipboardData` to write to clipboard and to send clipboard data to the server.
    setEnableAutoClipboard(enable) {
        this._autoClipboard = enable;
    }

    /// Callback to set the local clipboard content to data received from the remote.
    setOnRemoteClipboardChanged(callback) {
        this.onRemoteClipboardChanged = callback;
    }

    /// Callback which is called when the remote requests a forced clipboard update (e.g. on
    /// clipboard initialization sequence)
    setOnForceClipboardUpdate(callback) {
        this.onForceClipboardUpdate = callback;
    }


    /// Callback which is called when the warning event is emitted.
    setOnWarningCallback(callback) {
        this.onWarningCallback = callback;
    }

    /// Callback which is called when the clipboard remote update event is emitted.
    setOnClipboardRemoteUpdate(callback) {
        this.onClipboardRemoteUpdate = callback;
    }

    mouseIn(event) {
        this.syncModifier(event);
    }

    mouseOut(_event) {
        this.releaseAllInputs();
    }

    sendKeyboardEvent(evt) {
        this.sendKeyboard(evt);
    }

    shutdown() {
        this.session?.shutdown();
    }

    mouseButtonState(event, isDown, preventDefault) {
        if (preventDefault) {
            event.preventDefault(); // prevent default behavior (context menu, etc)
        }
        const mouseFnc = isDown
            ? this.module.DeviceEvent.mouseButtonPressed
            : this.module.DeviceEvent.mouseButtonReleased;
        this.doTransactionFromDeviceEvents([mouseFnc(event.button)]);
    }

    updateMousePosition(position) {
        this.doTransactionFromDeviceEvents([this.module.DeviceEvent.mouseMove(position.x, position.y)]);
        this.mousePositionObservable.publish(position);
    }

    async connect(config) {
        const sessionBuilder = new this.module.SessionBuilder();

        sessionBuilder.proxyAddress(config.proxyAddress);
        sessionBuilder.destination('');
        sessionBuilder.serverDomain('');
        sessionBuilder.password(config.password);
        sessionBuilder.authToken('');
        sessionBuilder.username(config.username);
        sessionBuilder.renderCanvas(this.canvas);
        sessionBuilder.setCursorStyleCallbackContext(this);
        sessionBuilder.setCursorStyleCallback(this.setCursorStyleCallback);

        if (this.onRemoteClipboardChanged != null && this.enableClipboard) {
            sessionBuilder.remoteClipboardChangedCallback(this.onRemoteClipboardChanged);
        }
        if (this.onForceClipboardUpdate != null && this.enableClipboard) {
            sessionBuilder.forceClipboardUpdateCallback(this.onForceClipboardUpdate);
        }

        if (config.desktopSize != null) {
            sessionBuilder.desktopSize(
                new this.module.DesktopSize(config.desktopSize.width, config.desktopSize.height),
            );
        }

        const session = await sessionBuilder.connect();
        this.initListeners();
        this.session = session;

        this.resizeObservable.publish({
            desktopSize: session.desktopSize(),
            sessionId: 0,
        });

        this.sessionStartedObservable.publish(null);

        const run = async () => {
            try {
                console.log('Starting the session.');
                return await session.run();
            } finally {
                this.setVisibility(false);
            }
        };

        return {
            initialDesktopSize: session.desktopSize(),
            run,
        };
    }

    setVisibility(state) {
        this.changeVisibilityObservable.publish(state);
    }

    resizeDynamic(width, height, scale) {
        this.dynamicResizeObservable.publish({ width, height });
        try {
            this.session?.resize(width, height, scale);
        } catch (e) {
            this.reportError(e)
        }
    }

    /// Triggered by the browser when local clipboard is updated. Clipboard backend should
    /// cache the content and send it to the server when it is requested.
    onClipboardChanged(data) {
        const onClipboardChangedPromise = async () => {
            try {
                await this.session?.onClipboardPaste(data);
            } catch (e) {
                this.reportError(e)
            }
        };
        return onClipboardChangedPromise();
    }

    onClipboardChangedEmpty() {
        const onClipboardChangedPromise = async () => {
            try {
                await this.session?.onClipboardPaste(new this.module.ClipboardData());
            } catch (e) {
                this.reportError(e)
            }
        };
        return onClipboardChangedPromise();
    }

    setKeyboardUnicodeMode(use_unicode) {
        this.keyboardUnicodeMode = use_unicode;
    }

    setCursorStyleOverride(style) {
        if (style == null) {
            this.canvas.style.cursor = this.lastCursorStyle;
            this.cursorHasOverride = false;
        } else {
            this.canvas.style.cursor = style;
            this.cursorHasOverride = true;
        }
    }

    invokeExtension(ext) {
        try {
            this.session?.invokeExtension(ext);
        } catch (e) {
            this.reportError(e)
        }
    }

    releaseAllInputs() {
        try {
            this.session?.releaseAllInputs();
        } catch (e) {
            this.reportError(e)
        }
    }

    supportsUnicodeKeyboardShortcuts() {
        // Use cached value to reduce FFI calls
        if (this.backendSupportsUnicodeKeyboardShortcuts !== undefined) {
            return this.backendSupportsUnicodeKeyboardShortcuts;
        }

        if (this.session?.supportsUnicodeKeyboardShortcuts) {
            try {
                this.backendSupportsUnicodeKeyboardShortcuts = this.session?.supportsUnicodeKeyboardShortcuts();
                return this.backendSupportsUnicodeKeyboardShortcuts;
            } catch (e) {
                this.reportError(e);
            }
        }

        // By default we use unicode keyboard shortcuts for backends
        return true;
    }

    sendKeyboard(evt) {
        evt.preventDefault();

        let keyEvent;
        let unicodeEvent;

        if (evt.type === 'keydown') {
            keyEvent = this.module.DeviceEvent.keyPressed;
            unicodeEvent = this.module.DeviceEvent.unicodePressed;
        } else if (evt.type === 'keyup') {
            keyEvent = this.module.DeviceEvent.keyReleased;
            unicodeEvent = this.module.DeviceEvent.unicodeReleased;
        }

        let sendAsUnicode = true;

        if (!this.supportsUnicodeKeyboardShortcuts()) {
            for (const modifier of ['Alt', 'Control', 'Meta', 'AltGraph', 'OS']) {
                if (evt.getModifierState(modifier)) {
                    sendAsUnicode = false;
                    break;
                }
            }
        }

        const isModifierKey = evt.code in ModifierKey;
        const isLockKey = evt.code in LockKey;

        if (isModifierKey) {
            this.updateModifierKeyState(evt);
        }

        if (isLockKey) {
            this.syncModifier(evt);
        }

        if (!evt.repeat || (!isModifierKey && !isLockKey)) {
            const keyScanCode = scanCode(evt.code);
            const unknownScanCode = Number.isNaN(keyScanCode);

            if (!this.keyboardUnicodeMode && keyEvent && !unknownScanCode) {
                this.doTransactionFromDeviceEvents([keyEvent(keyScanCode)]);
                return;
            }

            if (this.keyboardUnicodeMode && unicodeEvent && keyEvent) {
                // `Dead` and `Unidentified` keys should be ignored
                if (['Dead', 'Unidentified'].indexOf(evt.key) != -1) {
                    return;
                }

                const keyCode = scanCode(evt.key);
                const isUnicodeCharacter = Number.isNaN(keyCode) && evt.key.length === 1 && !isModifierKey;

                if (isUnicodeCharacter && sendAsUnicode) {
                    this.doTransactionFromDeviceEvents([unicodeEvent(evt.key)]);
                } else if (!unknownScanCode) {
                    // Use scancode instead of key code for non-unicode character values
                    this.doTransactionFromDeviceEvents([keyEvent(keyScanCode)]);
                }
            }
        }
    }

    setCursorStyleCallback(
        style,
        data,
        hotspotX,
        hotspotY,
    ) {
        let cssStyle;

        switch (style) {
            case 'hidden': {
                cssStyle = 'none';
                break;
            }
            case 'default': {
                cssStyle = 'default';
                break;
            }
            case 'url': {
                if (data === undefined || hotspotX === undefined || hotspotY === undefined) {
                    console.error('Invalid custom cursor parameters.');
                    return;
                }

                // IMPORTANT: We need to make proxy `Image` object to actually load the image and
                // make it usable for CSS property. Without this proxy object, URL will be rejected.
                const image = new Image();
                image.src = data;

                const rounded_hotspot_x = Math.round(hotspotX);
                const rounded_hotspot_y = Math.round(hotspotY);

                cssStyle = `url(${data}) ${rounded_hotspot_x} ${rounded_hotspot_y}, default`;

                break;
            }
            default: {
                console.error(`Unsupported cursor style: ${style}.`);
                return;
            }
        }

        this.lastCursorStyle = cssStyle;

        if (!this.cursorHasOverride) {
            this.canvas.style.cursor = cssStyle;
        }
    }

    syncModifier(evt) {
        const syncCapsLockActive = evt.getModifierState(LockKey["CAPS_LOCK"]);
        const syncNumsLockActive = evt.getModifierState(LockKey["NUM_LOCK"]);
        const syncScrollLockActive = evt.getModifierState(LockKey["SCROLL_LOCK"]);
        const syncKanaModeActive = evt.getModifierState(LockKey["KANA_MODE"]);

        try {
            this.session?.synchronizeLockKeys(
                syncScrollLockActive,
                syncNumsLockActive,
                syncCapsLockActive,
                syncKanaModeActive,
            );
        } catch (e) {
            this.reportError(e);
        }
    }

    updateModifierKeyState(evt) {
        const modKey = ModifierKey[evt.code];

        if (this.modifierKeyPressed.indexOf(modKey) === -1) {
            this.modifierKeyPressed.push(modKey);
        } else if (evt.type === 'keyup') {
            this.modifierKeyPressed.splice(this.modifierKeyPressed.indexOf(modKey), 1);
        }
    }

    doTransactionFromDeviceEvents(deviceEvents) {
        const transaction = new this.module.InputTransaction();
        deviceEvents.forEach((event) => transaction.addEvent(event));
        try {
            this.session?.applyInputs(transaction);
        } catch (e) {
            this.reportError(e)
        }
    }

    ctrlAltDel() {
        const ctrl = parseInt('0x001D', 16);
        const alt = parseInt('0x0038', 16);
        const suppr = parseInt('0xE053', 16);

        this.doTransactionFromDeviceEvents([
            this.module.DeviceEvent.keyPressed(ctrl),
            this.module.DeviceEvent.keyPressed(alt),
            this.module.DeviceEvent.keyPressed(suppr),
            this.module.DeviceEvent.keyReleased(ctrl),
            this.module.DeviceEvent.keyReleased(alt),
            this.module.DeviceEvent.keyReleased(suppr),
        ]);
    }

    sendMeta() { // a.k.a. windows key
        const meta = parseInt('0xE05B', 16);

        this.doTransactionFromDeviceEvents([
            this.module.DeviceEvent.keyPressed(meta),
            this.module.DeviceEvent.keyReleased(meta),
        ]);
    }
}
